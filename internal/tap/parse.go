package tap

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"agentflash/internal/event"
)

// fs_usage -w -f filesys lines look roughly like:
//
//   HH:MM:SS.ffffff  CALL  [F=N]  [(MODE)]  /path/to/file   0.000123 W  process.pid
//
// The columns are space-separated but paths can contain spaces and arg
// formatting varies per syscall. We anchor parsing on the trailing
// `<duration> [W] <process>.<pid>` pattern and treat everything between
// the syscall name and the duration as args, then extract the first
// absolute path from there.

var lineRE = regexp.MustCompile(
	`^\s*(\d{2}:\d{2}:\d{2}\.\d+)\s+(\S+)\s+(.+?)\s+(\d+\.\d+)\s*(W)?\s+(\S+?)\.(\d+)\s*$`,
)

var openModeRE = regexp.MustCompile(`\(([A-Z_]+)\)`)

// ParseLine parses one fs_usage output line. Returns ok=false for header
// lines, blank lines, and unparseable continuations.
func ParseLine(line string, today time.Time) (event.Event, bool) {
	m := lineRE.FindStringSubmatch(line)
	if m == nil {
		return event.Event{}, false
	}
	tsStr, call, args, _, _, proc, pidStr := m[1], m[2], m[3], m[4], m[5], m[6], m[7]

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return event.Event{}, false
	}

	path := extractPath(args)
	if path == "" {
		return event.Event{}, false
	}

	op, keep := mapSyscall(call, args)
	if !keep {
		return event.Event{}, false
	}

	ts, err := parseClockOnDay(tsStr, today)
	if err != nil {
		return event.Event{}, false
	}

	return event.Event{
		TS:      ts,
		Op:      op,
		Path:    path,
		Process: proc,
		PID:     pid,
	}, true
}

// extractPath returns the substring from the first `/` to the end of
// args. fs_usage paths can contain spaces (e.g. "Application Support")
// and the line-level regex has already separated args from the
// trailing duration/process columns, so taking the rest of the string
// is correct for single-path syscalls (open/unlink/mkdir/rmdir/...).
//
// For multi-path syscalls (rename, link) this returns both paths
// joined; that's a known imprecision but still enough to identify
// activity inside the watched dir.
func extractPath(args string) string {
	idx := strings.Index(args, "/")
	if idx < 0 {
		return ""
	}
	return strings.TrimRight(args[idx:], " \t")
}

// mapSyscall returns (op, keep). keep=false means the syscall is one we
// drop entirely (close, fstat, raw read/write that have no path).
//
// Only syscalls that emit a pathname in fs_usage output are kept. The
// FD-only forms (read/write/close/fstat) emit just `F=N` and we cannot
// recover the path here without tracking FD-table state per process.
func mapSyscall(call, args string) (string, bool) {
	switch call {
	case "open", "openat", "open_nocancel", "openat_nocancel":
		mode := openMode(args)
		if strings.ContainsAny(mode, "WC") {
			return "write", true
		}
		return "read", true
	case "rename", "renameat", "renameatx_np":
		return "rename", true
	case "unlink", "unlinkat":
		return "unlink", true
	case "mkdir", "mkdirat":
		return "mkdir", true
	case "rmdir":
		return "rmdir", true
	case "link", "linkat", "symlink", "symlinkat":
		return "write", true
	case "truncate":
		return "write", true
	case "chmod", "fchmodat":
		return "write", true
	case "utimes", "utimensat", "futimes", "futimens":
		// `touch` on an existing file uses these to bump mtime/atime.
		return "write", true
	}
	return "", false
}

func openMode(args string) string {
	m := openModeRE.FindStringSubmatch(args)
	if m == nil {
		return ""
	}
	return m[1]
}

// parseClockOnDay turns "HH:MM:SS.ffffff" into a time.Time on the given
// day. fs_usage only emits the time-of-day; we attach today's date.
func parseClockOnDay(clock string, today time.Time) (time.Time, error) {
	t, err := time.Parse("15:04:05.000000", clock)
	if err != nil {
		// fs_usage uses 6 fractional digits; fall back to flexible parse.
		t, err = time.Parse("15:04:05.999999", clock)
		if err != nil {
			return time.Time{}, err
		}
	}
	y, mo, d := today.Date()
	return time.Date(y, mo, d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), today.Location()), nil
}
