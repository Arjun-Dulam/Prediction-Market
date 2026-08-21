package engine

import (
	"context"
	"fmt"
	enginepb "go-backend/internal/engine/pb"
	"go-backend/internal/trading"
	"hash/fnv"
	"strings"
	"sync"
)

// Router assigns an entire symbol to one of at most two engines. Endpoint order
// is configuration, not discovery; a failed owner is never replaced on the fly.
// Restart ALL engines with the API before recovery or changing this assignment.
type Router struct{ clients []*Client }

func NewRouter(addresses string) (*Router, error) {
	parts := strings.Split(addresses, ",")
	if len(parts) > 2 {
		return nil, fmt.Errorf("support one or two engine addresses")
	}
	r := &Router{}
	seen := map[string]bool{}
	for _, address := range parts {
		address = strings.TrimSpace(address)
		if address == "" || seen[address] {
			_ = r.Close()
			return nil, fmt.Errorf("engine addresses must be nonempty and distinct")
		}
		seen[address] = true
		c, err := New(address)
		if err != nil {
			_ = r.Close()
			return nil, err
		}
		r.clients = append(r.clients, c)
	}
	return r, nil
}
func partition(symbol string, count int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(symbol))
	return int(h.Sum32() % uint32(count))
}
func (r *Router) pick(symbol string) *Client { return r.clients[partition(symbol, len(r.clients))] }
func (r *Router) AddOrder(ctx context.Context, s string, p int32, q uint32, side enginepb.Side) (uint32, error) {
	return r.pick(s).AddOrder(ctx, s, p, q, side)
}
func (r *Router) BestBid(ctx context.Context, s string) (int32, error) {
	return r.pick(s).BestBid(ctx, s)
}
func (r *Router) BestAsk(ctx context.Context, s string) (int32, error) {
	return r.pick(s).BestAsk(ctx, s)
}
func (r *Router) Ready(ctx context.Context) error {
	for _, c := range r.clients {
		if err := c.Ready(ctx); err != nil {
			return err
		}
	}
	return nil
}
func (r *Router) Close() error {
	var first error
	for _, c := range r.clients {
		if err := c.Close(); first == nil {
			first = err
		}
	}
	return first
}
func (r *Router) Trading() trading.Engine { return routedTrading{r} }

type routedTrading struct{ router *Router }

func (a routedTrading) AddBook(ctx context.Context, s string) error {
	return a.router.pick(s).AddBook(ctx, s)
}
func (a routedTrading) AddOrder(ctx context.Context, s string, p int32, q uint32, side string) (trading.EngineResult, error) {
	return (TradingAdapter{a.router.pick(s)}).AddOrder(ctx, s, p, q, side)
}
func (a routedTrading) RemoveOrder(ctx context.Context, s string, id uint32) (bool, error) {
	return a.router.pick(s).RemoveOrderText(ctx, s, id)
}
func (a routedTrading) Quote(ctx context.Context, s string) (trading.EngineQuote, error) {
	return (TradingAdapter{a.router.pick(s)}).Quote(ctx, s)
}
func (a routedTrading) AddOrders(ctx context.Context, orders []trading.EngineOrder) (trading.EngineBatch, error) {
	if len(orders) == 0 || len(orders) > 32 {
		return trading.EngineBatch{}, fmt.Errorf("batch size must be 1-32")
	}
	if len(a.router.clients) == 1 {
		return (TradingAdapter{a.router.clients[0]}).AddOrders(ctx, orders)
	}
	groups := make([][]trading.EngineOrder, len(a.router.clients))
	indices := make([][]int, len(groups))
	for i, o := range orders {
		p := partition(o.Symbol, len(groups))
		groups[p] = append(groups[p], o)
		indices[p] = append(indices[p], i)
	}
	batches := make([]trading.EngineBatch, len(groups))
	errors := make([]error, len(groups))
	var wait sync.WaitGroup
	for p, group := range groups {
		if len(group) == 0 {
			continue
		}
		wait.Add(1)
		go func(p int, group []trading.EngineOrder) {
			defer wait.Done()
			batches[p], errors[p] = (TradingAdapter{a.router.clients[p]}).AddOrders(ctx, group)
		}(p, group)
	}
	wait.Wait() // Both owners may have mutated, even if either RPC failed.
	result := trading.EngineBatch{Results: make([]trading.EngineResult, len(orders))}
	for p, batch := range batches {
		if errors[p] != nil {
			return trading.EngineBatch{}, fmt.Errorf("engine partition %d: %w", p, errors[p])
		}
		for i, r := range batch.Results {
			result.Results[indices[p][i]] = r
		}
		result.Quotes = append(result.Quotes, batch.Quotes...)
	}
	return result, nil
}
