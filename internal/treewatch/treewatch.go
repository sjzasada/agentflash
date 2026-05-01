// Package treewatch wraps the kernel's filesystem-change API to emit
// notifications when directory contents under a root change. It is
// independent of the fs_usage / fanotify tap: it doesn't need sudo
// and exists specifically to drive dynamic updates of the file tree
// in the UI.
//
// On macOS, fs_usage often cannot observe syscalls from
// hardened/SIP-protected processes such as /bin/zsh. FSEvents has no
// such restriction, so this package is also the source of truth for
// "a file was modified" timeline events. On Linux, inotify (via the
// same rjeczalik/notify wrapper) plays the same role.
package treewatch

import (
	"github.com/rjeczalik/notify"
)

// EventKind classifies a filesystem change.
type EventKind int

const (
	KindCreate EventKind = iota
	KindWrite
	KindRemove
	KindRename
)

// Event is a single FSEvents notification, normalized.
type Event struct {
	Kind EventKind
	Path string // file or dir affected
}

// Watcher emits FSEvents notifications under a root.
type Watcher struct {
	c      chan notify.EventInfo
	out    chan Event
	stop   chan struct{}
	closed chan struct{}
}

// New starts a recursive FSEvents watch under root. Always Close() to
// release the kernel watch.
func New(root string) (*Watcher, error) {
	c := make(chan notify.EventInfo, 256)
	if err := notify.Watch(root+"/...", c, notify.Create|notify.Remove|notify.Rename|notify.Write); err != nil {
		return nil, err
	}
	w := &Watcher{
		c:      c,
		out:    make(chan Event, 256),
		stop:   make(chan struct{}),
		closed: make(chan struct{}),
	}
	go w.loop()
	return w, nil
}

// Events returns the channel of normalized events.
func (w *Watcher) Events() <-chan Event { return w.out }

func (w *Watcher) loop() {
	defer close(w.closed)
	for {
		select {
		case ei := <-w.c:
			ev := Event{Path: normalizePath(ei.Path()), Kind: classify(ei.Event())}
			select {
			case w.out <- ev:
			default:
			}
		case <-w.stop:
			notify.Stop(w.c)
			close(w.out)
			return
		}
	}
}

func classify(e notify.Event) EventKind {
	switch {
	case e&notify.Create != 0:
		return KindCreate
	case e&notify.Remove != 0:
		return KindRemove
	case e&notify.Rename != 0:
		return KindRename
	default:
		return KindWrite
	}
}

// Close stops the watch and waits for the loop to exit.
func (w *Watcher) Close() {
	close(w.stop)
	<-w.closed
}
