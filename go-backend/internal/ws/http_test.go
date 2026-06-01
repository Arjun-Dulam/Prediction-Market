package ws

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketStreamsMarketUpdate(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(ServeHTTP(hub))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?channel=market:test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(10 * time.Millisecond)
	hub.Publish(Message{Channel: "market:test", Type: "quote", Data: map[string]int{"bid": 55}})
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var message Message
	if err = conn.ReadJSON(&message); err != nil {
		t.Fatal(err)
	}
	if message.Type != "quote" {
		t.Fatalf("message type=%q", message.Type)
	}
}
