// Package tap reads kernel-level filesystem events and forwards them
// to the UI as NDJSON. The actual event source is OS-specific:
// fs_usage on macOS (see tap_darwin.go) and fanotify on Linux (see
// tap_linux.go). This file holds the OS-agnostic glue.
package tap

import (
	"log"
	"path"
	"strings"
	"sync/atomic"
	"time"
)

// Config controls what the tap reports.
type Config struct {
	WatchDir    string
	ExcludePID  []int    // event.PID matching any of these is dropped
	ExcludeName []string // process names (in addition to the OS noise list)
	RawDumpFile string   // if non-empty, every raw event line is appended (debug)
	Debug       bool     // verbose diagnostics
}

// statsTicker periodically logs running counters. Shared across OSes.
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

// pathIn reports whether p is inside dir (or equals it).
func pathIn(p, dir, prefix string) bool {
	return p == dir || strings.HasPrefix(p, prefix)
}

// cleanPath collapses doubled slashes (`//Users/...`) and resolves
// embedded `..` segments that some event sources emit
// (`/../../System/...`). Uses path.Clean (slash-only) since the event
// payload always uses forward slashes regardless of OS.
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
