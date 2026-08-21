package trading

import (
	"context"
	"errors"
	"testing"
)

type groupedEngine struct {
	scriptedBatchEngine
	batchCalls  int
	singleCalls int
	fail        bool
	short       bool
}

func (e *groupedEngine) AddOrder(ctx context.Context, s string, p int32, q uint32, side string) (EngineResult, error) {
	e.singleCalls++
	return e.scriptedBatchEngine.AddOrder(ctx, s, p, q, side)
}
func (e *groupedEngine) AddOrders(ctx context.Context, orders []EngineOrder) (EngineBatch, error) {
	e.batchCalls++
	if e.fail {
		return EngineBatch{}, errors.New("lost group acknowledgement")
	}
	b := EngineBatch{}
	for _, o := range orders {
		r, err := e.scriptedBatchEngine.AddOrder(ctx, o.Symbol, o.Price, o.Quantity, o.Side)
		if err != nil {
			return b, err
		}
		b.Results = append(b.Results, r)
	}
	if e.short {
		b.Results = b.Results[:len(b.Results)-1]
	}
	b.Quotes = []EngineQuote{{Symbol: "BATCH", Bid: 40, Ask: 60}}
	return b, nil
}
func TestEngineBatchUsesOneCallAndCommitsBeforeQuotePublication(t *testing.T) {
	e := &groupedEngine{}
	s := batchService(t, e)
	// Use partial fills crossing the sequential reference fixture.
	requests := placements(Order{ID: "a", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 10}, Order{ID: "b", UserID: "no", MarketID: "m", Side: BuyNo, Price: 45, Quantity: 4})
	outcomes, quotes := s.placeBatchWithQuotes(requests)
	for _, err := range outcomes {
		if err != nil {
			t.Fatal(err)
		}
	}
	if e.batchCalls != 1 || e.singleCalls != 0 || len(quotes) != 1 || quotes[0].Sequence != s.sequence {
		t.Fatalf("calls/quotes %+v %+v", e, quotes)
	}
	if o, _ := s.Order("a"); o.Status != Partial {
		t.Fatalf("missing committed fills %+v", o)
	}
	replay, err := NewWithConfig(Config{WALPath: s.cfg.WALPath, Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	if replay.Position("no", "m") != s.Position("no", "m") || replay.Balance("yes") != s.Balance("yes") {
		t.Fatal("batch replay differs")
	}
}
func TestEngineBatchFailureDoesNotPublishAndRequiresRecovery(t *testing.T) {
	for _, short := range []bool{false, true} {
		e := &groupedEngine{fail: !short, short: short}
		s := batchService(t, e)
		outcomes, quotes := s.placeBatchWithQuotes(placements(Order{ID: "a", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 50, Quantity: 1}))
		if !errors.Is(outcomes[0], ErrRecoveryRequired) || len(quotes) != 0 {
			t.Fatalf("%v %+v", outcomes, quotes)
		}
		if o, _ := s.Order("a"); o.Status != Pending {
			t.Fatal("uncertain reservation must survive")
		}
	}
}

// Each symbol models a separate engine starting its numeric IDs at one.
type localIDEngine struct {
	matchingEngine
	next map[string]uint32
}

func (e *localIDEngine) AddOrder(_ context.Context, s string, p int32, q uint32, side string) (EngineResult, error) {
	if e.next == nil {
		e.next = map[string]uint32{}
	}
	e.next[s]++
	r := EngineResult{OrderID: e.next[s]}
	if side == "sell" {
		r.Trades = []EngineTrade{{BuyOrderID: 1, SellOrderID: r.OrderID, Price: p, Quantity: q}}
	}
	return r, nil
}
func (e *localIDEngine) AddOrders(ctx context.Context, orders []EngineOrder) (EngineBatch, error) {
	b := EngineBatch{}
	for _, o := range orders {
		r, err := e.AddOrder(ctx, o.Symbol, o.Price, o.Quantity, o.Side)
		if err != nil {
			return b, err
		}
		b.Results = append(b.Results, r)
	}
	return b, nil
}
func TestOverlappingEngineIDsKeepMarketsAndAccountsSeparate(t *testing.T) {
	s := batchService(t, &localIDEngine{})
	if err := s.CreateMarket("b", "OTHER"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMarket("duplicate", "OTHER"); err == nil {
		t.Fatal("allowed two markets to share one engine book")
	}
	orders := []Order{
		{ID: "a-yes", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 2},
		{ID: "b-yes", UserID: "yes", MarketID: "b", Side: BuyYes, Price: 60, Quantity: 2},
		{ID: "a-no", UserID: "no", MarketID: "m", Side: BuyNo, Price: 40, Quantity: 1},
		{ID: "b-no", UserID: "no", MarketID: "b", Side: BuyNo, Price: 40, Quantity: 1},
	}
	for _, err := range s.placeBatch(placements(orders...)) {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []string{"m", "b"} {
		if s.Position("yes", m).Yes != 1 || s.Position("no", m).No != 1 {
			t.Fatalf("cross-market fill in %s", m)
		}
	}
	if s.Balance("yes") != 760 || s.Balance("no") != 920 {
		t.Fatal("incorrect shared wallet")
	}
	if err := s.Cancel(context.Background(), "a-yes", "yes"); err != nil {
		t.Fatal(err)
	}
	if s.Balance("yes") != 820 {
		t.Fatal("cancel refunded wrong market")
	}
	for repeat := 0; repeat < 2; repeat++ {
		recovered, err := NewWithConfig(Config{WALPath: s.cfg.WALPath, Sync: true, Engine: &localIDEngine{}})
		if err != nil {
			t.Fatal(err)
		}
		if err = recovered.RecoverEngine(context.Background()); err != nil {
			t.Fatal(err)
		}
		if recovered.Balance("yes") != 820 || recovered.Position("yes", "b").Yes != 1 {
			t.Fatal("recovery changed accounts")
		}
		o, ok := recovered.Order("b-yes")
		if !ok || recovered.engineOrders[engineOrderKey{"b", o.EngineID}] != o.ID {
			t.Fatal("rebuilt mapping belongs to wrong market")
		}
		recovered.Close()
	}
}
func TestSharedWalletReservationCannotOverdrawAcrossMarkets(t *testing.T) {
	s := batchService(t, &localIDEngine{})
	if err := s.CreateMarket("b", "OTHER"); err != nil {
		t.Fatal(err)
	}
	outcomes := s.placeBatch(placements(Order{ID: "a", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 10}, Order{ID: "b", UserID: "yes", MarketID: "b", Side: BuyYes, Price: 60, Quantity: 10}))
	if outcomes[0] != nil || outcomes[1] == nil || s.Balance("yes") != 400 {
		t.Fatalf("overdraw %v balance=%d", outcomes, s.Balance("yes"))
	}
}
