package trading

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

type matchingEngine struct {
	mu   sync.Mutex
	next uint32
}

func (e *matchingEngine) AddBook(context.Context, string) error                     { return nil }
func (e *matchingEngine) RemoveOrder(context.Context, string, uint32) (bool, error) { return true, nil }
func (e *matchingEngine) AddOrder(_ context.Context, _ string, _ int32, _ uint32, side string) (EngineResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.next++
	result := EngineResult{OrderID: e.next}
	if e.next == 2 && side == "sell" {
		result.Trades = []EngineTrade{{BuyOrderID: 1, SellOrderID: 2, Price: 60, Quantity: 10}}
	}
	return result, nil
}

func TestComplementaryFillAndSettlement(t *testing.T) {
	engine := &matchingEngine{}
	dir := t.TempDir()
	s, err := NewWithConfig(Config{WALPath: filepath.Join(dir, "orders.wal"), SnapshotPath: filepath.Join(dir, "snapshot.json"), Engine: engine, Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateMarket("m", "ELECTION"); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"yes", "no"} {
		if err = s.Deposit(user, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Place(context.Background(), Order{ID: "yes-order", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 10}); err != nil {
		t.Fatal(err)
	}
	if err = s.Place(context.Background(), Order{ID: "no-order", UserID: "no", MarketID: "m", Side: BuyNo, Price: 40, Quantity: 10}); err != nil {
		t.Fatal(err)
	}
	if got := s.Position("yes", "m").Yes; got != 10 {
		t.Fatalf("YES position=%d", got)
	}
	if got := s.Position("no", "m").No; got != 10 {
		t.Fatalf("NO position=%d", got)
	}
	if err = s.Resolve(context.Background(), "m", true); err != nil {
		t.Fatal(err)
	}
	if got := s.Balance("yes"); got != 1400 {
		t.Fatalf("winner balance=%d", got)
	}
	if got := s.Balance("no"); got != 600 {
		t.Fatalf("loser balance=%d", got)
	}
	if err = s.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if info, statErr := os.Stat(filepath.Join(dir, "orders.wal")); statErr != nil || info.Size() != 0 {
		t.Fatalf("checkpoint did not truncate WAL: info=%v err=%v", info, statErr)
	}
	recovered, err := NewWithConfig(Config{WALPath: filepath.Join(dir, "orders.wal"), SnapshotPath: filepath.Join(dir, "snapshot.json"), Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := recovered.Balance("yes"); got != 1400 {
		t.Fatalf("snapshot recovery balance=%d", got)
	}
}

type improvedPriceEngine struct{ matchingEngine }

func (e *improvedPriceEngine) AddOrder(ctx context.Context, symbol string, price int32, quantity uint32, side string) (EngineResult, error) {
	result, err := e.matchingEngine.AddOrder(ctx, symbol, price, quantity, side)
	if len(result.Trades) != 0 {
		result.Trades[0].Price = 55
	}
	return result, err
}

func TestExecutionPriceRefundsAndConservesCash(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "orders.wal"), &improvedPriceEngine{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateMarket("m", "PRICE_IMPROVEMENT"); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"yes", "no"} {
		if err = s.Deposit(user, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Place(context.Background(), Order{ID: "yes", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 10}); err != nil {
		t.Fatal(err)
	}
	if err = s.Place(context.Background(), Order{ID: "no", UserID: "no", MarketID: "m", Side: BuyNo, Price: 50, Quantity: 10}); err != nil {
		t.Fatal(err)
	}
	if got := s.Balance("yes"); got != 450 {
		t.Fatalf("YES balance after 55-cent execution=%d", got)
	}
	if got := s.Balance("no"); got != 550 {
		t.Fatalf("NO balance after 45-cent execution=%d", got)
	}
	if total := s.Balance("yes") + s.Balance("no"); total != 1000 {
		t.Fatalf("cash committed to contracts=%d, want 1000", 2000-total)
	}
}

func TestEngineRecoveryReplaysAndPersistsFills(t *testing.T) {
	dir := t.TempDir()
	wal := filepath.Join(dir, "orders.wal")
	state, err := NewWithConfig(Config{WALPath: wal, Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = state.CreateMarket("m", "RECOVERY"); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"yes", "no"} {
		if err = state.Deposit(user, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if err = state.Place(context.Background(), Order{ID: "yes", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 10}); err != nil {
		t.Fatal(err)
	}
	if err = state.Place(context.Background(), Order{ID: "no", UserID: "no", MarketID: "m", Side: BuyNo, Price: 40, Quantity: 10}); err != nil {
		t.Fatal(err)
	}
	_ = state.Close()

	recovered, err := NewWithConfig(Config{WALPath: wal, Engine: &matchingEngine{}, Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = recovered.RecoverEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if order, _ := recovered.Order("yes"); order.Status != Filled {
		t.Fatalf("recovered YES status=%s", order.Status)
	}
	if got := recovered.Position("yes", "m").Yes; got != 10 {
		t.Fatalf("recovered YES position=%d", got)
	}
	_ = recovered.Close()

	replayed, err := NewWithConfig(Config{WALPath: wal, Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := replayed.Position("no", "m").No; got != 10 {
		t.Fatalf("persisted NO position=%d", got)
	}
}

type fakeEngine struct{}

func (fakeEngine) AddBook(context.Context, string) error { return nil }
func (fakeEngine) AddOrder(context.Context, string, int32, uint32, string) (EngineResult, error) {
	return EngineResult{OrderID: 1}, nil
}

type countingProjector struct{ batches atomic.Int64 }

func (p *countingProjector) Apply(context.Context, Event) error {
	p.batches.Add(1)
	return nil
}

func (p *countingProjector) ApplyBatch(context.Context, []Event) error {
	p.batches.Add(1)
	return nil
}

func FuzzOrderInvariants(f *testing.F) {
	f.Add(int64(10_000), 1, 1)
	f.Fuzz(func(t *testing.T, deposit int64, price, quantity int) {
		if deposit < 1 {
			deposit = 1
		}
		price = price%99 + 1
		if quantity < 1 {
			quantity = -quantity + 1
		}
		quantity = quantity%100 + 1
		wal := filepath.Join(t.TempDir(), "orders.wal")
		s, err := New(wal, fakeEngine{})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.CreateMarket("m", "TEST"); err != nil {
			t.Fatal(err)
		}
		if err = s.Deposit("u", deposit); err != nil {
			t.Fatal(err)
		}
		before := s.Balance("u")
		err = s.Place(context.Background(), Order{ID: "o", UserID: "u", MarketID: "m", Side: BuyYes, Price: price, Quantity: quantity})
		if err == nil && s.Balance("u") < 0 {
			t.Fatal("negative balance")
		}
		if err != nil && s.Balance("u") != before {
			t.Fatal("failed order changed balance")
		}
	})
}

func BenchmarkDurableOrderPlacement(b *testing.B) {
	wal := filepath.Join(b.TempDir(), "orders.wal")
	s, err := New(wal, fakeEngine{})
	if err != nil {
		b.Fatal(err)
	}
	if err := s.CreateMarket("m", "TEST"); err != nil {
		b.Fatal(err)
	}
	if err := s.Deposit("u", int64(b.N*100)); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := s.Place(context.Background(), Order{ID: string(rune(i + 1)), UserID: "u", MarketID: "m", Side: BuyYes, Price: 1, Quantity: 1})
		if err != nil {
			b.Fatal(err)
		}
	}
}
func (fakeEngine) RemoveOrder(context.Context, string, uint32) (bool, error) { return true, nil }
func TestWALRecoveryAndNoOverspend(t *testing.T) {
	wal := filepath.Join(t.TempDir(), "orders.wal")
	s, err := New(wal, fakeEngine{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateMarket("m", "ELECTION"); err != nil {
		t.Fatal(err)
	}
	if err = s.Deposit("u", 1000); err != nil {
		t.Fatal(err)
	}
	if err = s.Place(context.Background(), Order{ID: "o", UserID: "u", MarketID: "m", Side: BuyYes, Price: 65, Quantity: 10}); err != nil {
		t.Fatal(err)
	}
	if got := s.Balance("u"); got != 350 {
		t.Fatalf("balance %d", got)
	}
	recovered, err := New(wal, fakeEngine{})
	if err != nil {
		t.Fatal(err)
	}
	if got := recovered.Balance("u"); got != 350 {
		t.Fatalf("recovered balance %d", got)
	}
	if err := recovered.Place(context.Background(), Order{ID: "x", UserID: "u", MarketID: "m", Side: BuyYes, Price: 99, Quantity: 4}); err == nil {
		t.Fatal("expected insufficient funds")
	}
}

func TestIdempotentRetryDoesNotReserveTwice(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "orders.wal"), fakeEngine{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.CreateMarket("m", "IDEMPOTENCY")
	_ = s.Deposit("u", 1000)
	order := Order{ID: "retry-key", UserID: "u", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 10}
	if err = s.Place(context.Background(), order); err != nil {
		t.Fatal(err)
	}
	if err = s.Place(context.Background(), order); err != nil {
		t.Fatalf("identical retry failed: %v", err)
	}
	if got := s.Balance("u"); got != 400 {
		t.Fatalf("retry reserved funds twice: balance=%d", got)
	}
}

func TestConcurrentOrdersCannotOverspend(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "orders.wal"), fakeEngine{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.CreateMarket("m", "CONCURRENT")
	_ = s.Deposit("u", 100)
	var successes atomic.Int64
	var group sync.WaitGroup
	for i := 0; i < 100; i++ {
		group.Add(1)
		go func(id int) {
			defer group.Done()
			if s.Place(context.Background(), Order{ID: fmt.Sprintf("o-%d", id), UserID: "u", MarketID: "m", Side: BuyYes, Price: 10, Quantity: 1}) == nil {
				successes.Add(1)
			}
		}(i)
	}
	group.Wait()
	if successes.Load() != 10 || s.Balance("u") != 0 {
		t.Fatalf("successes=%d balance=%d", successes.Load(), s.Balance("u"))
	}
}

func TestOnlyMarketOwnerCanResolve(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "orders.wal"), fakeEngine{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateMarketFor("m", "OWNED", "creator"); err != nil {
		t.Fatal(err)
	}
	if err = s.ResolveFor(context.Background(), "m", true, "attacker"); err == nil {
		t.Fatal("non-owner resolved market")
	}
	if err = s.ResolveFor(context.Background(), "m", true, "creator"); err != nil {
		t.Fatal(err)
	}
}

func TestResolutionProjectsInOneAtomicBatch(t *testing.T) {
	projector := &countingProjector{}
	s, err := NewWithConfig(Config{WALPath: filepath.Join(t.TempDir(), "orders.wal"), Engine: fakeEngine{}, Projector: projector, Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateMarket("m", "BATCHED"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		user := fmt.Sprintf("u-%d", i)
		if err = s.Deposit(user, 100); err != nil {
			t.Fatal(err)
		}
		if err = s.Place(context.Background(), Order{ID: fmt.Sprintf("o-%d", i), UserID: user, MarketID: "m", Side: BuyYes, Price: 50, Quantity: 1}); err != nil {
			t.Fatal(err)
		}
	}
	projector.batches.Store(0)
	if err = s.Resolve(context.Background(), "m", true); err != nil {
		t.Fatal(err)
	}
	if got := projector.batches.Load(); got != 1 {
		t.Fatalf("resolution used %d projection transactions, want 1", got)
	}
}

func TestRecoveryIgnoresUnacknowledgedPartialTail(t *testing.T) {
	wal := filepath.Join(t.TempDir(), "orders.wal")
	s, err := New(wal, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Deposit("u", 1000); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(wal, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(`{"id":"torn","sequence":2`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	recovered, err := New(wal, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := recovered.Balance("u"); got != 1000 {
		t.Fatalf("balance after torn-tail recovery=%d", got)
	}
}
