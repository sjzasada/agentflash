package ui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// SpawnTap re-execs ourselves under sudo as `<self> __tap --dir <dir>`.
// Returns the tap's stdout reader and a cleanup func that kills the
// subprocess (and therefore fs_usage). Stderr is inherited so the sudo
// password prompt and any fs_usage errors land on the parent terminal.
// The UI's PID is passed via --exclude-pid so the tap drops events
// caused by our own directory listings.
func SpawnTap(dir string) (io.ReadCloser, func() error, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve self: %w", err)
	}
	cmd := exec.Command(
		"sudo", "--", self, "__tap",
		"--dir", dir,
		"--exclude-pid", strconv.Itoa(os.Getpid()),
		"--exclude-name", filepath.Base(self),
	)
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin // sudo needs a tty for the password prompt
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start tap: %w", err)
	}
	stop := func() error {
		// Killing sudo doesn't always reap the child; send SIGTERM and wait.
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		return nil
	}
	return stdout, stop, nil
}
