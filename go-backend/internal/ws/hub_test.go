package ws

import (
	"testing"
	"time"
)

func TestHubFanout(t *testing.T) {
	h := NewHub()
	a, ca := h.Subscribe("market:m", 1)
	defer ca()
	b, cb := h.Subscribe("market:m", 1)
	defer cb()
	h.Publish(Message{Channel: "market:m", Type: "quote"})
	for _, ch := range []<-chan Message{a, b} {
		select {
		case m := <-ch:
			if m.Type != "quote" {
				t.Fatal(m)
			}
		case <-time.After(time.Second):
			t.Fatal("no update")
		}
	}
}
