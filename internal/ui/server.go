package ui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"agentflash/internal/event"
	"agentflash/internal/treewatch"
)

type Config struct {
	Dir         string
	Addr        string // e.g. "127.0.0.1:7777"
	WebFS       embed.FS
	WebPrefix   string // subdir inside WebFS, e.g. "web"
	RingSize    int
	RawDumpFile string // optional, forwarded to the tap as --raw-dump
	Debug       bool   // when true, emit verbose diagnostics
}

// Run starts the UI server: spawns the privileged tap subprocess via
// sudo, ingests its NDJSON output into a hub, and serves the web UI +
// WebSocket on cfg.Addr. Blocks until the tap exits or ctx is canceled.
func Run(ctx context.Context, cfg Config) error {
	dlog := newDebugLogger(cfg.Debug)

	dlog.Printf("startup: building hub")
	hub := NewHub(cfg.RingSize, cfg.Debug)

	dlog.Printf("startup: spawning tap (sudo password may be prompted)")
	tapStdout, stopTap, err := SpawnTap(cfg.Dir, cfg.RawDumpFile, cfg.Debug)
	if err != nil {
		return fmt.Errorf("spawn tap: %w", err)
	}
	defer stopTap()

	go func() {
		if err := hub.Ingest(tapStdout); err != nil {
			log.Printf("tap ingest stopped: %v", err)
		}
	}()

	// FSEvents watcher runs in the background. Failure to start it
	// MUST NOT block the HTTP server — tree refresh just falls back
	// to the fs_usage event path.
	dlog.Printf("startup: starting FSEvents watcher")
	go runFSEventsWatcher(cfg.Dir, hub, dlog)

	dlog.Printf("startup: configuring HTTP routes")
	mux := http.NewServeMux()

	sub, err := fs.Sub(cfg.WebFS, cfg.WebPrefix)
	if err != nil {
		return fmt.Errorf("web sub fs: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/tree", treeHandler(cfg.Dir))
	mux.HandleFunc("/api/info", infoHandler(cfg.Dir))
	mux.HandleFunc("/api/claude/event", claudeHookHandler(cfg.Dir, hub))
	mux.HandleFunc("/ws", wsHandler(hub, dlog))

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on http://%s (watching %s)", cfg.Addr, cfg.Dir)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runFSEventsWatcher(root string, hub *Hub, dlog *debugLogger) {
	w, err := treewatch.New(root)
	if err != nil {
		log.Printf("tree watcher disabled: %v", err)
		return
	}
	dlog.Printf("startup: FSEvents watcher ready")

	// Coalesce repeated events for the same (path, kind) within a
	// short window. Keying on kind (not just path) is critical: a
	// quick create-then-delete on the same path must not have the
	// delete suppressed, otherwise the tree won't refresh on remove.
	const dedupeWindow = 200 * time.Millisecond
	type dedupeKey struct {
		path string
		kind treewatch.EventKind
	}
	type lastSeen struct{ ts time.Time }
	recent := make(map[dedupeKey]lastSeen)

	for ev := range w.Events() {
		now := time.Now()
		key := dedupeKey{path: ev.Path, kind: ev.Kind}
		if ls, ok := recent[key]; ok && now.Sub(ls.ts) < dedupeWindow {
			continue
		}
		recent[key] = lastSeen{ts: now}
		if len(recent) > 1024 {
			for k, v := range recent {
				if now.Sub(v.ts) > 5*time.Second {
					delete(recent, k)
				}
			}
		}

		dlog.Printf("[fsevents] kind=%d path=%s", ev.Kind, ev.Path)
		hub.BroadcastTransient(event.Event{
			TS:      now,
			Op:      "dirchange",
			Path:    parentDir(ev.Path),
			Process: "fsevents",
		})
		if op, ok := timelineOp(ev.Kind); ok {
			hub.broadcast(event.Event{
				TS:      now,
				Op:      op,
				Path:    ev.Path,
				Process: "fsevents",
			})
		}
	}
}

func parentDir(p string) string {
	idx := -1
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return p
	}
	return p[:idx]
}

func timelineOp(k treewatch.EventKind) (string, bool) {
	switch k {
	case treewatch.KindCreate, treewatch.KindWrite, treewatch.KindRename:
		return "write", true
	}
	return "", false // KindRemove: tree disappearance is the visual signal
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Localhost-only server; allow any origin.
		return true
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
}

func wsHandler(hub *Hub, dlog *debugLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			dlog.Printf("ws upgrade error: %v", err)
			return
		}
		dlog.Printf("ws connected from %s", r.RemoteAddr)
		c := &client{send: make(chan []byte, 1024)}
		hub.register(c)
		defer func() {
			hub.unregister(c)
			conn.Close()
			dlog.Printf("ws disconnected from %s", r.RemoteAddr)
		}()

		// Reader goroutine: discard incoming messages, detect close.
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if _, _, err := conn.NextReader(); err != nil {
					return
				}
			}
		}()

		// Replay snapshot in a goroutine — does NOT block the writer
		// loop, so new broadcasts arriving during replay are still
		// served promptly.
		go func() {
			for _, ev := range hub.snapshot() {
				data, err := json.Marshal(ev)
				if err != nil {
					continue
				}
				select {
				case c.send <- data:
				case <-done:
					return
				}
			}
		}()

		// Writer loop — single drain of c.send for both replay and live.
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case msg, ok := <-c.send:
				if !ok {
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					dlog.Printf("ws write error: %v", err)
					return
				}
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}
}
