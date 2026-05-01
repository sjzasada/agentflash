//go:build linux

package tap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProcReader(t *testing.T) {
	root := t.TempDir()
	mk := func(pid int, comm string) {
		dir := filepath.Join(root, "12345")
		_ = pid
		dir = filepath.Join(root, dirName(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(42, "cat")
	mk(7, "systemd-journal")

	pr := newProcReader(root)
	if got := pr.CommName(42); got != "cat" {
		t.Errorf("CommName(42) = %q, want cat", got)
	}
	if got := pr.CommName(7); got != "systemd-journal" {
		t.Errorf("CommName(7) = %q, want systemd-journal", got)
	}
	if got := pr.CommName(99999); got != "" {
		t.Errorf("CommName(99999) = %q, want empty (no such pid)", got)
	}

	// Cache hit path — value should still be there after we delete the file.
	if err := os.RemoveAll(filepath.Join(root, "42")); err != nil {
		t.Fatal(err)
	}
	if got := pr.CommName(42); got != "cat" {
		t.Errorf("cached CommName(42) = %q, want cat", got)
	}
}

func dirName(pid int) string {
	// inlined to avoid pulling in strconv in test
	return itoa(pid)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
