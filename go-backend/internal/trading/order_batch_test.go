package trading

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

type scriptedBatchEngine struct {
	matchingEngine
	before func()
	calls  int
	fail   bool
}

func (e *scriptedBatchEngine) AddOrder(context.Context, string, int32, uint32, string) (EngineResult, error) {
	if e.before != nil {
		e.before()
	}
	e.calls++
	if e.fail {
		return EngineResult{}, errors.New("ambiguous RPC failure")
	}
	result := EngineResult{OrderID: uint32(e.calls)}
	if e.calls == 2 || e.calls == 3 {
		quantity := uint32(4)
		if e.calls == 3 {
			quantity = 6
		}
		result.Trades = []EngineTrade{{BuyOrderID: 1, SellOrderID: uint32(e.calls), Price: 55, Quantity: quantity}}
	}
	return result, nil
}
func batchService(t *testing.T, engine Engine) *Service {
	t.Helper()
	dir := t.TempDir()
	s, err := NewWithConfig(Config{WALPath: filepath.Join(dir, "wal"), SnapshotPath: filepath.Join(dir, "snapshot"), Sync: true, Engine: engine, OrderBatchSize: 32})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.CreateMarket("m", "BATCH"); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"yes", "no"} {
		if err = s.Deposit(user, 1000); err != nil {
			t.Fatal(err)
		}
	}
	return s
}
func placements(orders ...Order) []*placement {
	result := make([]*placement, len(orders))
	for i, o := range orders {
		result[i] = &placement{ctx: context.Background(), order: o, queued: time.Now(), done: make(chan error, 1)}
	}
	return result
}
func TestBatchPartialFillsAndReplayMatchSequentialOrders(t *testing.T) {
	orders := []Order{
		{ID: "yes", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 10},
		{ID: "no-first", UserID: "no", MarketID: "m", Side: BuyNo, Price: 45, Quantity: 4},
		{ID: "no-second", UserID: "no", MarketID: "m", Side: BuyNo, Price: 45, Quantity: 6},
		{ID: "resting", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 50, Quantity: 2},
	}
	s := batchService(t, &scriptedBatchEngine{})
	for _, err := range s.placeBatch(placements(orders...)) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.Balance("yes") != 350 || s.Balance("no") != 550 || s.Position("yes", "m").Yes != 10 || s.Position("no", "m").No != 10 {
		t.Fatal("incorrect multi-fill accounting")
	}
	sequential := batchService(t, &scriptedBatchEngine{})
	for _, o := range orders {
		if err := sequential.placeSingle(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range orders {
		got, _ := s.Order(o.ID)
		want, _ := sequential.Order(o.ID)
		got.Sequence = 0
		want.Sequence = 0 // groups assign admission sequences before completions
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("different execution: got %+v want %+v", got, want)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewWithConfig(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if !reflect.DeepEqual(s.balances, recovered.balances) || !reflect.DeepEqual(s.positions, recovered.positions) || !reflect.DeepEqual(s.orders, recovered.orders) {
		t.Fatal("group replay changed acknowledged state")
	}
	last := uint64(0)
	for _, o := range orders {
		got, _ := recovered.Order(o.ID)
		if got.Sequence <= last {
			t.Fatal("admission order lost")
		}
		last = got.Sequence
	}
}
func TestBatchAggregateReservationsAndDuplicateIDs(t *testing.T) {
	s := batchService(t, nil)
	o := Order{ID: "same", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 10}
	conflict := o
	conflict.Quantity = 1
	overdraw := o
	overdraw.ID = "overdraw"
	before, _ := os.ReadFile(s.cfg.WALPath)
	result := s.placeBatch(placements(o, o, conflict, overdraw))
	if result[0] != nil || result[1] != nil || result[2] == nil || result[3] == nil {
		t.Fatalf("outcomes: %v", result)
	}
	after, _ := os.ReadFile(s.cfg.WALPath)
	if bytes.Count(after[len(before):], []byte("\n")) != 2 || s.Balance("yes") != 400 {
		t.Fatal("duplicate/reservation not atomic")
	}
	if err := s.commit(context.Background(), Event{Type: EventPosition, UserID: "yes", MarketID: "m", YesDelta: 5}); err != nil {
		t.Fatal(err)
	}
	sell := Order{ID: "sell1", UserID: "yes", MarketID: "m", Side: SellYes, Price: 60, Quantity: 3}
	sell2 := sell
	sell2.ID = "sell2"
	result = s.placeBatch(placements(sell, sell2))
	if result[0] != nil || result[1] == nil || s.Position("yes", "m").Yes != 2 {
		t.Fatalf("share overdraw: %v", result)
	}
}

type barrierProjector struct {
	calls            int
	entered, release chan struct{}
	fail             bool
}

func (p *barrierProjector) Apply(ctx context.Context, e Event) error {
	return p.ApplyBatch(ctx, []Event{e})
}
func (p *barrierProjector) ApplyBatch(_ context.Context, _ []Event) error {
	p.calls++
	if p.calls == 2 {
		if p.entered != nil {
			close(p.entered)
			<-p.release
		}
		if p.fail {
			return errors.New("projection unavailable")
		}
	}
	return nil
}
func TestBatchDurabilityBarriersAndFailureRecovery(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			engine := &scriptedBatchEngine{}
			s := batchService(t, engine)
			p := &barrierProjector{entered: make(chan struct{}), release: make(chan struct{}), fail: fail}
			s.cfg.Projector = p
			engine.before = func() {
				if p.calls != 1 {
					t.Error("engine called before reservation projection")
				}
				wal, err := os.ReadFile(s.cfg.WALPath)
				if err != nil || !bytes.Contains(wal, []byte(`"status":"pending"`)) || wal[len(wal)-1] != '\n' {
					t.Error("engine called without complete durable reservation")
				}
			}
			o := Order{ID: "durable", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 10}
			done := make(chan []error, 1)
			go func() { done <- s.placeBatch(placements(o)) }()
			<-p.entered
			select {
			case <-done:
				t.Fatal("returned before completion projection")
			default:
			}
			close(p.release)
			result := <-done
			if fail {
				if !errors.Is(result[0], ErrRecoveryRequired) {
					t.Fatalf("failure not latched: %v", result)
				}
				if s.Snapshot() == nil {
					t.Fatal("checkpoint discarded durable completion")
				}
			} else if result[0] != nil {
				t.Fatal(result[0])
			}
			s.Close()
			cfg := s.cfg
			cfg.Projector = nil
			cfg.Engine = nil
			recovered, err := NewWithConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			got, ok := recovered.Order(o.ID)
			if !ok || got.Status != Open || recovered.Balance("yes") != 400 {
				t.Fatalf("lost durable completion: %+v", got)
			}
		})
	}
}
func TestBatchEngineUncertaintyFailsClosed(t *testing.T) {
	for _, size := range []int{1, 32} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			engine := &scriptedBatchEngine{fail: true}
			s := batchService(t, engine)
			s.cfg.OrderBatchSize = size
			o := Order{ID: "uncertain", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 10}
			result := []error{s.Place(context.Background(), o)}
			if !errors.Is(result[0], ErrRecoveryRequired) || s.HealthError() == nil || s.Snapshot() == nil {
				t.Fatal("engine uncertainty allowed more writes")
			}
			if engine.calls != 1 {
				t.Fatal("retried ambiguous engine command")
			}
			result = []error{s.Place(context.Background(), o)}
			if !errors.Is(result[0], ErrRecoveryRequired) || engine.calls != 1 {
				t.Fatal("continued after uncertainty")
			}
			s.Close()
			cfg := s.cfg
			cfg.Engine = &matchingEngine{}
			recovered, err := NewWithConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			if err = recovered.RecoverEngine(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := recovered.Order(o.ID)
			if got.Status != Open || recovered.Balance("yes") != 400 {
				t.Fatal("reservation lost or applied twice")
			}
		})
	}
}
func TestBatchAdmissionCancellationQueueBoundAndClose(t *testing.T) {
	s := batchService(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := Order{ID: "cancelled", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 1}
	if err := s.Place(ctx, o); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s.pendingMu.Lock()
	s.pending = make([]*placement, maxQueuedOrders)
	s.pendingMu.Unlock()
	if err := s.Place(context.Background(), o); !errors.Is(err, ErrOrderQueueFull) {
		t.Fatal(err)
	}
	s.pendingMu.Lock()
	s.pending = nil
	s.pendingMu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Place(context.Background(), o); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if s.Balance("yes") != 940 {
		t.Fatal("concurrent retry charged twice")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Place(context.Background(), o); err == nil {
		t.Fatal("placement after close succeeded")
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.running || len(s.pending) != 0 {
		t.Fatal("worker or request retained after close")
	}
}

func TestGroupReservationOnlyAndTornCompletionRecovery(t *testing.T) {
	s := batchService(t, &scriptedBatchEngine{})
	orders := []Order{
		{ID: "yes", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 10},
		{ID: "no", UserID: "no", MarketID: "m", Side: BuyNo, Price: 45, Quantity: 4},
	}
	before, _ := os.ReadFile(s.cfg.WALPath)
	for _, err := range s.placeBatch(placements(orders...)) {
		if err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	full, _ := os.ReadFile(s.cfg.WALPath)
	records := bytes.SplitAfter(full[len(before):], []byte("\n"))
	if len(records) != 3 {
		t.Fatalf("expected two barriers, got %d records", len(records)-1)
	}
	reservationEnd := len(before) + len(records[0])
	for _, cut := range []int{reservationEnd, reservationEnd + len(records[1])/2, len(full) - 1, len(full)} {
		t.Run(fmt.Sprint(cut), func(t *testing.T) {
			cfg := s.cfg
			cfg.WALPath = filepath.Join(t.TempDir(), "cut.wal")
			cfg.SnapshotPath = ""
			cfg.Engine = &scriptedBatchEngine{}
			if err := os.WriteFile(cfg.WALPath, full[:cut], 0600); err != nil {
				t.Fatal(err)
			}
			recovered, err := NewWithConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			if err = recovered.RecoverEngine(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(s.balances, recovered.balances) || !reflect.DeepEqual(s.positions, recovered.positions) {
				t.Fatalf("crash boundary changed accounts: balances=%v positions=%v", recovered.balances, recovered.positions)
			}
			for _, o := range orders {
				got, _ := recovered.Order(o.ID)
				want, _ := s.Order(o.ID)
				got.EngineID = 0
				want.EngineID = 0
				if got != want {
					t.Fatalf("rebuild mismatch: got %+v want %+v", got, want)
				}
			}
		})
	}
}

func TestCancellationAfterAdmissionDoesNotAbortOtherClients(t *testing.T) {
	s := batchService(t, nil)
	p := &barrierProjector{entered: make(chan struct{}), release: make(chan struct{})}
	s.cfg.Projector = p
	ctx, cancel := context.WithCancel(context.Background())
	o := Order{ID: "cancel-after", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 1}
	other := o
	other.ID = "other"
	other.UserID = "no"
	requests := placements(o, other)
	requests[0].ctx = ctx
	done := make(chan []error, 1)
	go func() { done <- s.placeBatch(requests) }()
	<-p.entered
	cancel()
	close(p.release)
	for _, err := range <-done {
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.Balance("yes") != 940 || s.Balance("no") != 940 {
		t.Fatal("cancellation aborted admitted durability")
	}
	if err := s.Place(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if s.Balance("yes") != 940 {
		t.Fatal("retry after cancellation debited twice")
	}
}
