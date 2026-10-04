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

// Compare decimal strings rather than Lua doubles, preserving all uint64 values.
var setQuote = redis.NewScript(`
local old = redis.call('GET', KEYS[1])
if old then
  local decoded = cjson.decode(old)
  local previous = decoded.sequence or '0'
  local next = ARGV[2]
  if string.len(previous) > string.len(next) or
     (string.len(previous) == string.len(next) and previous >= next) then return 0 end
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[3])
return 1
`)

func (r *RedisQuotes) Set(ctx context.Context, market string, q Quote) error {
	_, err := r.SetVersioned(ctx, market, q)
	return err
}
func (r *RedisQuotes) SetVersioned(ctx context.Context, market string, q Quote) (bool, error) {
	q.UpdatedAt = time.Now().UTC()
	b, err := json.Marshal(q)
	if err != nil {
		return false, err
	}
	accepted, err := setQuote.Run(ctx, r.client, []string{"market:" + market + ":quote"}, b, fmt.Sprint(q.Sequence), r.ttl.Milliseconds()).Int()
	return accepted == 1, err
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
