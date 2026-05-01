package tap

import (
	"testing"
	"time"
)

func TestParseLine(t *testing.T) {
	today := time.Date(2026, 4, 30, 0, 0, 0, 0, time.Local)
	cases := []struct {
		name    string
		line    string
		wantOp  string
		wantPth string
		wantPrc string
		wantPID int
		wantOK  bool
	}{
		{
			name:    "open read",
			line:    "21:07:01.123456  open              F=12  (R_____)    /Users/stef/foo.txt    0.000123 W cat.12345",
			wantOp:  "read",
			wantPth: "/Users/stef/foo.txt",
			wantPrc: "cat",
			wantPID: 12345,
			wantOK:  true,
		},
		{
			name:    "open write",
			line:    "21:07:01.234567  open              F=13  (WC____)    /Users/stef/bar.txt    0.000098 W vim.12347",
			wantOp:  "write",
			wantPth: "/Users/stef/bar.txt",
			wantPrc: "vim",
			wantPID: 12347,
			wantOK:  true,
		},
		{
			name:   "write syscall (FD-only, no path) is dropped",
			line:   "21:07:01.345678  write             F=13   B=0x4                                    0.000067    sh.12346",
			wantOK: false,
		},
		{
			// Multi-path syscalls return both paths joined; documented limitation.
			name:    "rename",
			line:    "21:07:01.567890  rename                              /Users/stef/a.txt /Users/stef/b.txt    0.000045 W mv.99999",
			wantOp:  "rename",
			wantPth: "/Users/stef/a.txt /Users/stef/b.txt",
			wantPrc: "mv",
			wantPID: 99999,
			wantOK:  true,
		},
		{
			// unlink is intentionally dropped — the FSEvents-driven
			// tree refresh shows deletion via the file disappearing.
			name:   "unlink (dropped from timeline)",
			line:   "21:07:01.678901  unlink                              /Users/stef/junk.txt                  0.000023    rm.88888",
			wantOK: false,
		},
		{
			name:   "header line",
			line:   "Timestamp  Call (F=#)             [args]              FILENAME       Time(s) WAIT  PROCESS.PID",
			wantOK: false,
		},
		{
			name:   "blank",
			line:   "",
			wantOK: false,
		},
		{
			name:   "close (dropped)",
			line:   "21:07:01.456789  close             F=12                                       0.000010    cat.12345",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, ok := ParseLine(tc.line, today)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (ev=%+v)", ok, tc.wantOK, ev)
			}
			if !ok {
				return
			}
			if ev.Op != tc.wantOp {
				t.Errorf("op = %q, want %q", ev.Op, tc.wantOp)
			}
			if ev.Path != tc.wantPth {
				t.Errorf("path = %q, want %q", ev.Path, tc.wantPth)
			}
			if ev.Process != tc.wantPrc {
				t.Errorf("process = %q, want %q", ev.Process, tc.wantPrc)
			}
			if ev.PID != tc.wantPID {
				t.Errorf("pid = %d, want %d", ev.PID, tc.wantPID)
			}
		})
	}
}

func TestExtractPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"F=12 (R_____) /Users/stef/foo.txt", "/Users/stef/foo.txt"},
		{"F=12 (R) /Users/stef/Library/Application Support/foo", "/Users/stef/Library/Application Support/foo"},
		{"F=N (W) //Users/stef/x", "//Users/stef/x"},
		{"F=12 (R) /../../System/Volumes/Preboot/OS", "/../../System/Volumes/Preboot/OS"},
		{"F=N B=0x100", ""},
	}
	for _, c := range cases {
		got := extractPath(c.in)
		if got != c.want {
			t.Errorf("extractPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/stef/foo", "/Users/stef/foo"},
		{"/System/Volumes/Data/Users/stef/foo", "/Users/stef/foo"},
		{"/System/Volumes/Data", "/System/Volumes/Data"},
		{"/private/var/foo", "/private/var/foo"},
	}
	for _, c := range cases {
		got := normalizePath(c.in)
		if got != c.want {
			t.Errorf("normalizePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPathIn(t *testing.T) {
	dir := "/Users/stef/dev/agentflash"
	prefix := dir + "/"
	if !pathIn("/Users/stef/dev/agentflash/main.go", dir, prefix) {
		t.Error("subpath should match")
	}
	if !pathIn(dir, dir, prefix) {
		t.Error("exact dir should match")
	}
	if pathIn("/Users/stef/dev/other/main.go", dir, prefix) {
		t.Error("sibling should not match")
	}
	if pathIn("/Users/stef/dev/agentflash-other", dir, prefix) {
		t.Error("prefix-but-not-subpath should not match")
	}
}
