package tap

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"agentflash/internal/event"
)

// Config controls what the tap reports.
type Config struct {
	WatchDir    string
	ExcludePID  []int    // event.PID matching any of these is dropped
	ExcludeName []string // process names (in addition to the hardcoded macOS noise list)
	RawDumpFile string   // if non-empty, every fs_usage line is appended to this path
	Debug       bool     // verbose diagnostics: stats, [unparsed], [filtered], [kept], [interesting]
}

// Run spawns fs_usage, parses its output, filters events whose path is
// inside cfg.WatchDir, and writes them as NDJSON to out. Blocks until
// fs_usage exits. Diagnostic counters and unparsed-line samples are
// written to os.Stderr so the parent UI process can surface them.
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
	dlog.Printf("starting fs_usage (watch prefix=%q)", prefix)

	cmd := exec.Command("fs_usage", "-w", "-f", "filesys")
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start fs_usage: %w", err)
	}
	dlog.Printf("fs_usage pid=%d", cmd.Process.Pid)

	excludePIDs := make(map[int]struct{})
	for _, p := range cfg.ExcludePID {
		excludePIDs[p] = struct{}{}
	}
	excludePIDs[os.Getpid()] = struct{}{}     // tap itself
	excludePIDs[cmd.Process.Pid] = struct{}{} // fs_usage
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

	w := event.NewWriter(out)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)

	var (
		lines    atomic.Uint64
		parsed   atomic.Uint64
		kept     atomic.Uint64
		dropped  atomic.Uint64 // parsed but path didn't match
		excluded atomic.Uint64 // path matched but process is on the denylist
	)
	unparsedSamples := 0
	parsedSamples := 0
	filteredSamples := 0
	interestingSamples := 0
	const maxSamples = 200
	const maxInteresting = 1000

	if cfg.Debug {
		go statsTicker(logger, &lines, &parsed, &kept, &dropped, &excluded)
	}

	// We match raw lines on `/<watchBase>` (with leading slash) so we
	// catch path mentions but skip lines where `<watchBase>` only
	// appears as part of our own process name `agentflash.PID`.
	pathTrigger := "/" + filepath.Base(abs)
	dataPathTrigger := macDataVolume + pathTrigger

	for sc.Scan() {
		line := sc.Text()
		lines.Add(1)
		if rawDump != nil {
			rawDump.WriteString(line)
			rawDump.WriteString("\n")
		}
		// Diagnostic: any raw line that mentions the watch dir path,
		// even if it never parses or gets filtered out, gets logged so
		// we can see the ground truth for things like cat / touch.
		if cfg.Debug && interestingSamples < maxInteresting &&
			(strings.Contains(line, pathTrigger) || strings.Contains(line, dataPathTrigger)) {
			logger.Printf("[interesting] %s", line)
			interestingSamples++
		}
		now := time.Now()
		ev, ok := ParseLine(line, now)
		if !ok {
			if cfg.Debug && unparsedSamples < maxSamples && len(line) > 0 && !isNoiseLine(line) {
				logger.Printf("[unparsed] %s", line)
				unparsedSamples++
			}
			continue
		}
		parsed.Add(1)
		ev.Path = cleanPath(ev.Path)
		ev.Path = normalizePath(ev.Path)
		if !pathIn(ev.Path, abs, prefix) {
			dropped.Add(1)
			if cfg.Debug && filteredSamples < maxSamples {
				logger.Printf("[filtered] %s %s %s.%d", ev.Op, ev.Path, ev.Process, ev.PID)
				filteredSamples++
			}
			continue
		}
		if _, ok := excludePIDs[ev.PID]; ok {
			excluded.Add(1)
			continue
		}
		if _, ok := excludeNames[ev.Process]; ok {
			excluded.Add(1)
			continue
		}
		kept.Add(1)
		if cfg.Debug && parsedSamples < maxSamples {
			logger.Printf("[kept] %s %s %s.%d", ev.Op, ev.Path, ev.Process, ev.PID)
			parsedSamples++
		}
		if err := w.Write(ev); err != nil {
			_ = cmd.Process.Kill()
			return fmt.Errorf("write event: %w", err)
		}
	}
	if err := sc.Err(); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("scan fs_usage: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("fs_usage exited: %w", err)
	}
	return nil
}

func statsTicker(logger *log.Logger, lines, parsed, kept, dropped, excluded *atomic.Uint64) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	var prev uint64
	for range t.C {
		l := lines.Load()
		if l == prev {
			continue
		}
		prev = l
		logger.Printf("stats lines=%d parsed=%d kept=%d filtered_out=%d excluded=%d",
			l, parsed.Load(), kept.Load(), dropped.Load(), excluded.Load())
	}
}

// excludeProcesses is the set of macOS background daemons whose
// activity we want to drop by default. They re-read files constantly
// (Spotlight indexing, fsevent notification, telemetry agents) and
// produce noise that drowns out the user's own activity.
var excludeProcesses = map[string]struct{}{
	"mds":              {},
	"mds_stores":       {},
	"mdworker":         {},
	"mdworker_shared":  {},
	"fseventsd":        {},
	"BiomeAgent":       {},
	"corespotlightd":   {},
}

func keysOf(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func nameKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// isNoiseLine drops lines that are obviously not syscall events (e.g.
// the fs_usage header) so we don't spam the [unparsed] sampler.
func isNoiseLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return true
	}
	// Header line printed by fs_usage on start.
	if strings.HasPrefix(t, "Timestamp") || strings.Contains(t, "PROCESS.PID") {
		return true
	}
	return false
}

func pathIn(p, dir, prefix string) bool {
	return p == dir || strings.HasPrefix(p, prefix)
}

// cleanPath collapses doubled slashes (`//Users/...`) and resolves
// embedded `..` segments that fs_usage occasionally emits
// (`/../../System/...`). Uses path.Clean (slash-only) since fs_usage
// always uses forward slashes regardless of OS.
func cleanPath(p string) string {
	if p == "" {
		return p
	}
	c := path.Clean(p)
	if c == "." {
		return ""
	}
	return c
}

// normalizePath strips the macOS firmlink data-volume prefix so events
// emitted as `/System/Volumes/Data/Users/...` match a `/Users/...`
// watch dir. This is the historical Catalina-onward layout where user
// data lives on a separate APFS volume and `/Users` is a firmlink.
const macDataVolume = "/System/Volumes/Data"

func normalizePath(p string) string {
	if strings.HasPrefix(p, macDataVolume+"/") {
		return p[len(macDataVolume):]
	}
	return p
}
