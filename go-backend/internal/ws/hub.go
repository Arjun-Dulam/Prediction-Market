// Package ws provides bounded, per-market fan-out. The HTTP/WebSocket adapter
// can attach any connection implementation to this concurrency-safe core.
package ws

import "sync"

type Message struct {
	Channel string `json:"channel"`
	Type    string `json:"type"`
	Data    any    `json:"data"`
}
type Hub struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan Message]struct{}
}

func NewHub() *Hub { return &Hub{subscribers: map[string]map[chan Message]struct{}{}} }
func (h *Hub) Subscribe(channel string, capacity int) (<-chan Message, func()) {
	if capacity < 1 {
		capacity = 1
	}
	ch := make(chan Message, capacity)
	h.mu.Lock()
	if h.subscribers[channel] == nil {
		h.subscribers[channel] = map[chan Message]struct{}{}
	}
	h.subscribers[channel][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if set := h.subscribers[channel]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(h.subscribers, channel)
			}
		}
		h.mu.Unlock()
		close(ch)
	}
}

// Publish never blocks trade processing; slow consumers lose stale updates and
// are expected to fetch a fresh quote from Redis/the quote endpoint.
func (h *Hub) Publish(m Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers[m.Channel] {
		select {
		case ch <- m:
		default:
		}
	}
}
