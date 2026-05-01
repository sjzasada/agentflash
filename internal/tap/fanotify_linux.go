//go:build linux

package tap

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// fanotifyMetadataSize is the size of struct fanotify_event_metadata
// (kernel ABI). It's 24 bytes on all supported architectures.
const fanotifyMetadataSize = 24

// fanotifyEvent is a normalized parsed event. The FD field is an open
// file descriptor that the caller MUST close after resolving the path,
// or the kernel will leak fds inside our process.
type fanotifyEvent struct {
	PID  int
	Mask uint64
	FD   int
}

// parseFanotifyEvents decodes the metadata-only event stream returned
// by read() on a fanotify fd opened in FAN_CLASS_NOTIF mode without
// FAN_REPORT_DFID_NAME. Each event reserves event_len bytes; we use
// metadata_len to skip past any extra info records the kernel might
// append in the future. Returns events plus any leftover (incomplete
// trailing) bytes the caller should keep for the next read.
func parseFanotifyEvents(buf []byte) ([]fanotifyEvent, []byte, error) {
	var out []fanotifyEvent
	rest := buf
	for len(rest) >= fanotifyMetadataSize {
		eventLen := binary.LittleEndian.Uint32(rest[0:4])
		if eventLen < fanotifyMetadataSize {
			return out, nil, fmt.Errorf("fanotify event_len=%d shorter than metadata header", eventLen)
		}
		if int(eventLen) > len(rest) {
			break // truncated; caller must read more
		}
		mask := binary.LittleEndian.Uint64(rest[8:16])
		fd := int32(binary.LittleEndian.Uint32(rest[16:20]))
		pid := int32(binary.LittleEndian.Uint32(rest[20:24]))
		out = append(out, fanotifyEvent{
			PID:  int(pid),
			Mask: mask,
			FD:   int(fd),
		})
		rest = rest[eventLen:]
	}
	return out, rest, nil
}

// fanotifyDevice wraps an open fanotify fd.
type fanotifyDevice struct {
	fd int
}

func newFanotifyDevice() (*fanotifyDevice, error) {
	fd, err := unix.FanotifyInit(
		unix.FAN_CLASS_NOTIF|unix.FAN_CLOEXEC,
		unix.O_RDONLY|unix.O_LARGEFILE|unix.O_CLOEXEC,
	)
	if err != nil {
		return nil, fmt.Errorf("fanotify_init: %w", err)
	}
	return &fanotifyDevice{fd: fd}, nil
}

// Mark adds a watch on path. It tries FAN_MARK_FILESYSTEM first
// (kernel 5.1+) and falls back to FAN_MARK_MOUNT on older kernels.
// On the broader-than-needed fallback, the per-event path filter in
// the read loop still ensures we only emit events under cfg.WatchDir.
func (f *fanotifyDevice) Mark(path string, mask uint64) error {
	err := unix.FanotifyMark(
		f.fd,
		unix.FAN_MARK_ADD|unix.FAN_MARK_FILESYSTEM,
		mask,
		unix.AT_FDCWD,
		path,
	)
	if err == nil {
		return nil
	}
	if !errors.Is(err, unix.EINVAL) {
		return fmt.Errorf("fanotify_mark filesystem: %w", err)
	}
	if err2 := unix.FanotifyMark(
		f.fd,
		unix.FAN_MARK_ADD|unix.FAN_MARK_MOUNT,
		mask,
		unix.AT_FDCWD,
		path,
	); err2 != nil {
		return fmt.Errorf("fanotify_mark mount fallback: %w (filesystem err: %v)", err2, err)
	}
	return nil
}

// Read drains the fanotify fd into buf. Wraps unix.Read so the
// caller can substitute a faked reader in tests via parseFanotifyEvents.
func (f *fanotifyDevice) Read(buf []byte) (int, error) {
	return unix.Read(f.fd, buf)
}

func (f *fanotifyDevice) Close() error { return unix.Close(f.fd) }

// pathForFD resolves an open file descriptor to its underlying path
// via /proc/self/fd. Returns the path or "" if the fd is no longer
// valid (e.g. file was deleted between event and lookup).
func pathForFD(fd int) string {
	link, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(fd))
	if err != nil {
		return ""
	}
	return link
}
