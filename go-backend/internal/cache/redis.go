package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisQuotes struct {
	client redis.UniversalClient
	ttl    time.Duration
}

func NewRedisQuotes(addr string) *RedisQuotes {
	return &RedisQuotes{client: redis.NewClient(&redis.Options{Addr: addr}), ttl: time.Minute}
}
func (r *RedisQuotes) Set(ctx context.Context, market string, q Quote) error {
	b, err := json.Marshal(q)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, "market:"+market+":quote", b, r.ttl).Err()
}
func (r *RedisQuotes) Get(ctx context.Context, market string) (Quote, error) {
	b, err := r.client.Get(ctx, "market:"+market+":quote").Bytes()
	if err != nil {
		return Quote{}, err
	}
	var q Quote
	if err = json.Unmarshal(b, &q); err != nil {
		return Quote{}, fmt.Errorf("decode quote: %w", err)
	}
	return q, nil
}
func (r *RedisQuotes) Close() error                   { return r.client.Close() }
func (r *RedisQuotes) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }
