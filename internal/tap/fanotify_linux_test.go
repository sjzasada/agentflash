//go:build linux

package tap

import (
	"encoding/binary"
	"testing"

	"golang.org/x/sys/unix"
)

// makeFanotifyEvent crafts a single fanotify_event_metadata record.
func makeFanotifyEvent(mask uint64, fd, pid int32) []byte {
	buf := make([]byte, fanotifyMetadataSize)
	binary.LittleEndian.PutUint32(buf[0:4], fanotifyMetadataSize) // event_len
	buf[4] = 3                                                    // vers (FANOTIFY_METADATA_VERSION)
	buf[5] = 0                                                    // reserved
	binary.LittleEndian.PutUint16(buf[6:8], fanotifyMetadataSize) // metadata_len
	binary.LittleEndian.PutUint64(buf[8:16], mask)
	binary.LittleEndian.PutUint32(buf[16:20], uint32(fd))
	binary.LittleEndian.PutUint32(buf[20:24], uint32(pid))
	return buf
}

func TestParseFanotifyEvents_Single(t *testing.T) {
	buf := makeFanotifyEvent(unix.FAN_OPEN|unix.FAN_ACCESS, 7, 4321)
	events, rest, err := parseFanotifyEvents(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Errorf("rest=%d, want 0", len(rest))
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].PID != 4321 {
		t.Errorf("pid=%d, want 4321", events[0].PID)
	}
	if events[0].FD != 7 {
		t.Errorf("fd=%d, want 7", events[0].FD)
	}
	if events[0].Mask&unix.FAN_OPEN == 0 {
		t.Errorf("mask missing FAN_OPEN: %x", events[0].Mask)
	}
}

func TestParseFanotifyEvents_Batch(t *testing.T) {
	buf := append(makeFanotifyEvent(unix.FAN_OPEN, 3, 100), makeFanotifyEvent(unix.FAN_MODIFY, 4, 200)...)
	events, rest, err := parseFanotifyEvents(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Errorf("rest=%d, want 0", len(rest))
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d", len(events))
	}
	if events[0].PID != 100 || events[1].PID != 200 {
		t.Errorf("pids = %d, %d, want 100, 200", events[0].PID, events[1].PID)
	}
}

func TestParseFanotifyEvents_Truncated(t *testing.T) {
	// One full event + 10 trailing bytes that aren't a complete header.
	full := makeFanotifyEvent(unix.FAN_OPEN, 5, 50)
	buf := append(full, make([]byte, 10)...)
	events, rest, err := parseFanotifyEvents(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if len(rest) != 10 {
		t.Errorf("rest=%d, want 10 (caller keeps for next read)", len(rest))
	}
}

func TestParseFanotifyEvents_BadEventLen(t *testing.T) {
	buf := makeFanotifyEvent(unix.FAN_OPEN, 1, 1)
	binary.LittleEndian.PutUint32(buf[0:4], 8) // event_len smaller than metadata
	_, _, err := parseFanotifyEvents(buf)
	if err == nil {
		t.Fatal("want error for event_len < metadata size")
	}
}
