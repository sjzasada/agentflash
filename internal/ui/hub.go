package ui

import (
	"encoding/json"
	"io"
	"sync"

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
}

type client struct {
	send chan []byte
}

func NewHub(ringSize int) *Hub {
	return &Hub{
		ring:     make([]event.Event, ringSize),
		ringSize: ringSize,
		clients:  make(map[*client]struct{}),
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
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.mu.Lock()
	h.ring[h.ringHead] = ev
	h.ringHead = (h.ringHead + 1) % h.ringSize
	if h.ringHead == 0 {
		h.ringFilled = true
	}
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		select {
		case c.send <- data:
		default:
			// Drop frame for slow client; do not block the hub.
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
