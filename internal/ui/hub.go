package ui

import (
	"encoding/json"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"agentflash/internal/event"
)

// Hub fans out events from the tap to all connected WebSocket clients
// and keeps a ring buffer for replay on new connections.
type Hub struct {
	mu         sync.Mutex
	ring       []event.Event
	ringHead   int
	ringSize   int
	ringFilled bool
	clients    map[*client]struct{}
	debug      bool

	bcastCount   atomic.Uint64 // total broadcasts since start
	deliverCount atomic.Uint64 // queued frames to clients (post-drop)
	dropCount    atomic.Uint64 // frames dropped because client buffer full
}

type client struct {
	send chan []byte
}

func NewHub(ringSize int, debug bool) *Hub {
	h := &Hub{
		ring:     make([]event.Event, ringSize),
		ringSize: ringSize,
		clients:  make(map[*client]struct{}),
		debug:    debug,
	}
	if debug {
		go h.statsTicker()
	}
	return h
}

func (h *Hub) statsTicker() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var prev uint64
	for range t.C {
		b := h.bcastCount.Load()
		if b == prev {
			continue
		}
		prev = b
		h.mu.Lock()
		nClients := len(h.clients)
		h.mu.Unlock()
		log.Printf("[hub] bcast=%d delivered=%d dropped=%d clients=%d",
			b, h.deliverCount.Load(), h.dropCount.Load(), nClients)
	}
}

// Ingest reads NDJSON events from r and broadcasts them. Blocks until r
// closes or errors.
func (h *Hub) Ingest(r io.Reader) error {
	rd := event.NewReader(r)
	for {
		ev, ok, err := rd.Next()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		h.broadcast(ev)
	}
}

func (h *Hub) broadcast(ev event.Event) {
	h.send(ev, true)
}

// BroadcastTransient sends ev to all clients but does NOT add it to
// the replay ring buffer. Used for control messages like dirchange,
// which are only meaningful in real time.
func (h *Hub) BroadcastTransient(ev event.Event) {
	h.send(ev, false)
}

func (h *Hub) send(ev event.Event, store bool) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.bcastCount.Add(1)
	h.mu.Lock()
	if store {
		h.ring[h.ringHead] = ev
		h.ringHead = (h.ringHead + 1) % h.ringSize
		if h.ringHead == 0 {
			h.ringFilled = true
		}
	}
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		select {
		case c.send <- data:
			h.deliverCount.Add(1)
		default:
			h.dropCount.Add(1)
		}
	}
}

func (h *Hub) snapshot() []event.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.ringFilled {
		out := make([]event.Event, h.ringHead)
		copy(out, h.ring[:h.ringHead])
		return out
	}
	out := make([]event.Event, h.ringSize)
	copy(out, h.ring[h.ringHead:])
	copy(out[h.ringSize-h.ringHead:], h.ring[:h.ringHead])
	return out
}

func (h *Hub) register(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}
