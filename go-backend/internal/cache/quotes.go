// Package cache contains the Redis-shaped quote cache boundary. A production
// Redis adapter can implement this interface without changing API consumers.
package cache

import (
	"sync"
	"time"
)

type Quote struct {
	Bid       int32     `json:"bid"`
	Ask       int32     `json:"ask"`
	Last      int32     `json:"last"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Quotes struct {
	mu     sync.RWMutex
	values map[string]Quote
}

func NewQuotes() *Quotes { return &Quotes{values: map[string]Quote{}} }
func (q *Quotes) Set(market string, v Quote) {
	q.mu.Lock()
	v.UpdatedAt = time.Now().UTC()
	q.values[market] = v
	q.mu.Unlock()
}
func (q *Quotes) Get(market string) (Quote, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	v, ok := q.values[market]
	return v, ok
}
