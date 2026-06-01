package ws

import (
	"github.com/gorilla/websocket"
	"net/http"
	"net/url"
	"os"
)

var upgrader = websocket.Upgrader{CheckOrigin: checkOrigin}

func checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return parsed.Host == r.Host || origin == os.Getenv("WS_ALLOWED_ORIGIN")
}

// ServeHTTP upgrades a client and streams one market channel. Bounded Hub
// buffers protect matching/order processing from slow network consumers.
func ServeHTTP(h *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channel := r.URL.Query().Get("channel")
		if channel == "" {
			http.Error(w, "channel is required", 400)
			return
		}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		messages, cancel := h.Subscribe(channel, 64)
		defer cancel()
		for m := range messages {
			if err := c.WriteJSON(m); err != nil {
				return
			}
		}
	}
}
