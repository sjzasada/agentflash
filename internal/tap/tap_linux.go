//go:build linux

package tap

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"agentflash/internal/event"
)

// Run opens a fanotify fd, marks the watch dir, and forwards every
// open / read / write / close-on-write event under that dir as NDJSON
// to out. Blocks until the fanotify fd errors or out closes.
//
// Requires CAP_SYS_ADMIN (i.e. running under sudo). Marks at the
// filesystem level on kernel 5.1+, falls back to per-mount otherwise.
func Run(cfg Config, out io.Writer) error {
	abs, err := filepath.Abs(cfg.WatchDir)
	if err != nil {
		return fmt.Errorf("resolve --dir: %w", err)
	}
	prefix := abs
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	logger := log.New(os.Stderr, "[tap] ", log.LstdFlags)
	dlog := &debugLogger{on: cfg.Debug, l: logger}
	dlog.Printf("starting fanotify (watch prefix=%q)", prefix)

	dev, err := newFanotifyDevice()
	if err != nil {
		return err
	}
	defer dev.Close()

	const mask = unix.FAN_OPEN | unix.FAN_ACCESS | unix.FAN_MODIFY | unix.FAN_CLOSE_WRITE
	if err := dev.Mark(abs, mask); err != nil {
		return err
	}
	dlog.Printf("fanotify mark on %s", abs)

	excludePIDs := make(map[int]struct{})
	for _, p := range cfg.ExcludePID {
		excludePIDs[p] = struct{}{}
	}
	excludePIDs[os.Getpid()] = struct{}{} // tap itself
	if pp := os.Getppid(); pp > 1 {
		excludePIDs[pp] = struct{}{} // sudo (parent of tap)
	}
	excludeNames := make(map[string]struct{}, len(excludeProcesses)+len(cfg.ExcludeName))
	for k := range excludeProcesses {
		excludeNames[k] = struct{}{}
	}
	for _, n := range cfg.ExcludeName {
		excludeNames[n] = struct{}{}
	}
	dlog.Printf("excluding pids=%v names=%v", keysOf(excludePIDs), nameKeys(excludeNames))

	var rawDump *os.File
	if cfg.RawDumpFile != "" {
		f, err := os.Create(cfg.RawDumpFile)
		if err != nil {
			logger.Printf("raw-dump: cannot open %s: %v", cfg.RawDumpFile, err)
		} else {
			rawDump = f
			defer rawDump.Close()
			logger.Printf("raw-dump: writing to %s", cfg.RawDumpFile)
		}
	}

	pr := newProcReader("/proc")
	w := event.NewWriter(out)

	var (
		lines    atomic.Uint64
		parsed   atomic.Uint64
		kept     atomic.Uint64
		dropped  atomic.Uint64
		excluded atomic.Uint64
	)
	parsedSamples := 0
	filteredSamples := 0
	const maxSamples = 200

	if cfg.Debug {
		go statsTicker(logger, &lines, &parsed, &kept, &dropped, &excluded)
	}

	buf := make([]byte, 1<<16) // 64 KiB read window
	leftover := []byte{}
	for {
		// Concatenate any partial trailing event from the previous read.
		readBuf := buf
		if len(leftover) > 0 {
			readBuf = append(append([]byte{}, leftover...), buf...)
		}
		n, err := dev.Read(readBuf[len(leftover):])
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return fmt.Errorf("fanotify read: %w", err)
		}
		total := readBuf[:len(leftover)+n]

		events, rest, perr := parseFanotifyEvents(total)
		if perr != nil {
			logger.Printf("fanotify parse: %v", perr)
			leftover = nil
			continue
		}
		leftover = append(leftover[:0], rest...)

		for _, ev := range events {
			lines.Add(1)
			if rawDump != nil {
				fmt.Fprintf(rawDump, "mask=%x pid=%d fd=%d\n", ev.Mask, ev.PID, ev.FD)
			}
			path := pathForFD(ev.FD)
			// Always close the fd the kernel handed us — it's an open
			// file descriptor in our own process.
			_ = unix.Close(ev.FD)
			if path == "" {
				continue
			}
			parsed.Add(1)
			path = cleanPath(path)
			if !pathIn(path, abs, prefix) {
				dropped.Add(1)
				if cfg.Debug && filteredSamples < maxSamples {
					logger.Printf("[filtered] %s (pid=%d)", path, ev.PID)
					filteredSamples++
				}
				continue
			}
			if _, ok := excludePIDs[ev.PID]; ok {
				excluded.Add(1)
				continue
			}
			pname := pr.CommName(ev.PID)
			if _, ok := excludeNames[pname]; ok {
				excluded.Add(1)
				continue
			}
			op := opFromMask(ev.Mask)
			kept.Add(1)
			if cfg.Debug && parsedSamples < maxSamples {
				logger.Printf("[kept] %s %s %s.%d", op, path, pname, ev.PID)
				parsedSamples++
			}
			outEvt := event.Event{
				TS:      time.Now(),
				Op:      op,
				Path:    path,
				Process: pname,
				PID:     ev.PID,
			}
			if err := w.Write(outEvt); err != nil {
				return fmt.Errorf("write event: %w", err)
			}
		}
	}
}

// opFromMask maps a fanotify event mask to our op enum, mirroring the
// macOS tap's read/write categorisation.
func opFromMask(m uint64) string {
	if m&(unix.FAN_MODIFY|unix.FAN_CLOSE_WRITE) != 0 {
		return "write"
	}
	return "read"
}
