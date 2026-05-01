//go:build linux

package tap

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// procReader resolves process names from /proc/<pid>/comm with a small
// LRU-ish cache. The root path is overridable so tests can populate a
// fake /proc tree under t.TempDir().
type procReader struct {
	root  string
	mu    sync.Mutex
	cache map[int]string
}

func newProcReader(root string) *procReader {
	if root == "" {
		root = "/proc"
	}
	return &procReader{root: root, cache: make(map[int]string)}
}

// CommName returns the process's "comm" (kernel-truncated to 15
// chars). Returns "" if the process has exited or comm is unreadable.
// Cached on first lookup; the kernel reuses pids so callers must
// invalidate via ForgetPID when they observe the process exit (we
// don't currently track that, accepting some staleness — fine for a
// short-lived debug UI).
func (p *procReader) CommName(pid int) string {
	p.mu.Lock()
	if name, ok := p.cache[pid]; ok {
		p.mu.Unlock()
		return name
	}
	p.mu.Unlock()

	path := filepath.Join(p.root, strconv.Itoa(pid), "comm")
	data, err := os.ReadFile(path)
	if err != nil {
		return "" // ENOENT (process exited) or EACCES — best-effort
	}
	name := string(bytes.TrimRight(data, "\n"))

	p.mu.Lock()
	// Keep the cache bounded. 4096 is plenty for any realistic
	// system; on overflow we just drop the whole map (cheap).
	if len(p.cache) > 4096 {
		p.cache = make(map[int]string)
	}
	p.cache[pid] = name
	p.mu.Unlock()
	return name
}
