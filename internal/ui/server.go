package ui

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

type Config struct {
	Dir        string
	Addr       string // e.g. "127.0.0.1:7777"
	WebFS      embed.FS
	WebPrefix  string // subdir inside WebFS, e.g. "web"
	RingSize   int
}

// Run starts the UI server: spawns the privileged tap subprocess via
// sudo, ingests its NDJSON output into a hub, and serves the web UI +
// WebSocket on cfg.Addr. Blocks until the tap exits or ctx is canceled.
func Run(ctx context.Context, cfg Config) error {
	hub := NewHub(cfg.RingSize)

	tapStdout, stopTap, err := SpawnTap(cfg.Dir)
	if err != nil {
		return fmt.Errorf("spawn tap: %w", err)
	}
	defer stopTap()

	go func() {
		if err := hub.Ingest(tapStdout); err != nil {
			log.Printf("tap ingest stopped: %v", err)
		}
	}()

	mux := http.NewServeMux()

	sub, err := fs.Sub(cfg.WebFS, cfg.WebPrefix)
	if err != nil {
		return fmt.Errorf("web sub fs: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/tree", treeHandler(cfg.Dir))
	mux.HandleFunc("/api/info", infoHandler(cfg.Dir))
	mux.HandleFunc("/ws", wsHandler(hub))

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

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Localhost-only server; allow any origin.
		return true
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
}

func wsHandler(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := &client{send: make(chan []byte, 256)}
		hub.register(c)
		defer func() {
			hub.unregister(c)
			conn.Close()
		}()

		// Replay ring buffer on connect.
		for _, ev := range hub.snapshot() {
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		}

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

		// Writer loop.
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
