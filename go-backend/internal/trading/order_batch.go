package trading

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const maxQueuedOrders = 128

var ErrOrderQueueFull = errors.New("order admission queue is full")

type placement struct {
	ctx    context.Context
	order  Order
	queued time.Time
	done   chan error
}

// enqueue starts a worker only while work exists. The finite queue bounds memory
// and overload; no timer delays an isolated order to manufacture a bigger batch.
func (s *Service) enqueue(ctx context.Context, order Order) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	request := &placement{ctx: ctx, order: order, queued: time.Now(), done: make(chan error, 1)}
	s.pendingMu.Lock()
	if s.closed {
		s.pendingMu.Unlock()
		return errors.New("trading service is closed")
	}
	if len(s.pending) >= maxQueuedOrders {
		s.pendingMu.Unlock()
		return ErrOrderQueueFull
	}
	s.pending = append(s.pending, request)
	if !s.running {
		s.running = true
		s.workers.Add(1)
		go s.drainOrders()
	}
	s.pendingMu.Unlock()
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) drainOrders() {
	defer s.workers.Done()
	for {
		s.pendingMu.Lock()
		n := min(len(s.pending), s.cfg.OrderBatchSize)
		if n == 0 {
			s.running = false
			s.pendingMu.Unlock()
			return
		}
		requests := append([]*placement(nil), s.pending[:n]...)
		// Copy the remaining queue so completed requests/contexts are not retained.
		copy(s.pending, s.pending[n:])
		clear(s.pending[len(s.pending)-n:])
		s.pending = s.pending[:len(s.pending)-n]
		s.pendingMu.Unlock()
		outcomes := s.placeBatch(requests)
		for i, request := range requests {
			request.done <- outcomes[i]
		}
	}
}

// placeBatch has two durable barriers: reserve all admitted orders before any
// engine call, then commit their ordered completions before any success reply.
// All engine calls and staging occur under the existing state lock. The staged
// completion map is private, so readers and snapshots never see uncommitted fills.
func (s *Service) placeBatch(requests []*placement) []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	outcomes := make([]error, len(requests))
	for _, request := range requests {
		s.observe(StageLockWait, request.queued)
	}
	if s.failure != nil {
		for i := range outcomes {
			outcomes[i] = s.failure
		}
		return outcomes
	}
	var initial []Event
	var admitted []int
	byID := make(map[string]int)
	aliases := make(map[int]int)
	cash := make(map[string]int64)
	type accountMarket struct{ user, market string }
	shares := make(map[accountMarket]Position)
	for i, request := range requests {
		if err := request.ctx.Err(); err != nil {
			outcomes[i] = err
			continue
		}
		o := request.order
		if original, ok := byID[o.ID]; ok {
			previous := requests[original].order
			if previous.UserID == o.UserID && previous.MarketID == o.MarketID && previous.Side == o.Side && previous.Price == o.Price && previous.Quantity == o.Quantity {
				aliases[i] = original
			} else {
				outcomes[i] = errors.New("order id already exists with different parameters")
			}
			continue
		}
		events, err := s.prepareOrder(o)
		if err != nil || len(events) == 0 {
			outcomes[i] = err
			continue
		}
		reserved := cash[o.UserID] + events[0].Amount
		if s.balances[o.UserID]+reserved < 0 {
			outcomes[i] = errors.New("insufficient balance")
			continue
		}
		key := accountMarket{o.UserID, o.MarketID}
		reservedShares := shares[key]
		if !isBuy(o.Side) {
			reservedShares.Yes += events[1].YesDelta
			reservedShares.No += events[1].NoDelta
			current := s.positions[o.UserID][o.MarketID]
			if current.Yes+reservedShares.Yes < 0 || current.No+reservedShares.No < 0 {
				outcomes[i] = errors.New("insufficient shares")
				continue
			}
		}
		cash[o.UserID] = reserved
		shares[key] = reservedShares
		events[0].Order.Sequence = s.sequence + uint64(len(initial)) + 1
		initial = append(initial, events...)
		admitted = append(admitted, i)
		byID[o.ID] = i
	}
	if len(admitted) == 0 {
		return outcomes
	}
	// Cancellation after admission cannot abort other clients' durability. Each
	// batch has its own finite deadline; an uncertain engine/persistence result
	// latches failure, requiring API + engine recovery rather than guessing a refund.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.commitMany(ctx, initial)
	if err == nil {
		staged := make(map[string]Order)
		engineIDs := make(map[uint32]string)
		var completed []Event
		for _, i := range admitted {
			o := s.orders[requests[i].order.ID]
			o.Status = Open
			if s.cfg.Engine != nil {
				start := time.Now()
				var result EngineResult
				result, err = s.cfg.Engine.AddOrder(ctx, s.markets[o.MarketID].Symbol, int32(o.EnginePrice), uint32(o.Quantity), o.EngineSide)
				s.observe(StageEngine, start)
				if err != nil {
					err = s.fail(fmt.Errorf("matching engine batch: %w", err))
					break
				}
				o.EngineID = result.OrderID
				engineIDs[o.EngineID] = o.ID
				staged[o.ID] = o
				completed = append(completed, Event{Type: EventOrder, Order: o})
				completed = append(completed, s.buildTradeEvents(o.MarketID, result.Trades, engineIDs, staged)...)
			} else {
				completed = append(completed, Event{Type: EventOrder, Order: o})
			}
		}
		if err == nil {
			err = s.commitMany(ctx, completed)
		}
	}
	for _, i := range admitted {
		outcomes[i] = err
	}
	for alias, original := range aliases {
		outcomes[alias] = outcomes[original]
	}
	return outcomes
}
