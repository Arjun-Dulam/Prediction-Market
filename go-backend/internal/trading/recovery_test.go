package trading

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTornTailCanBeFollowedByWritesAndAnotherRestart(t *testing.T) {
	for _, tail := range []string{`{"sequence":2`, `{"id":"uncommitted","sequence":2,"type":"balance","user_id":"u","amount":99}`} {
		t.Run(fmt.Sprint(len(tail)), func(t *testing.T) {
			wal := filepath.Join(t.TempDir(), "orders.wal")
			s, err := New(wal, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Deposit("u", 100); err != nil {
				t.Fatal(err)
			}
			_ = s.Close()
			f, err := os.OpenFile(wal, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString(tail)
			_ = f.Close()
			if err != nil {
				t.Fatal(err)
			}
			s, err = New(wal, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Balance("u"); got != 100 {
				t.Fatalf("uncommitted tail applied: %d", got)
			}
			if err = s.Deposit("u", 25); err != nil {
				t.Fatal(err)
			}
			_ = s.Close()
			s, err = New(wal, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if got := s.Balance("u"); got != 125 {
				t.Fatalf("second restart balance=%d", got)
			}
		})
	}
}
func TestWALBatchIsIndivisibleAtCrashBoundaries(t *testing.T) {
	wal := filepath.Join(t.TempDir(), "orders.wal")
	s, err := New(wal, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Deposit("u", 100); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(wal)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.commitMany(context.Background(), []Event{{Type: EventBalance, UserID: "u", Amount: 7}, {Type: EventBalance, UserID: "u", Amount: 11}}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	full, err := os.ReadFile(wal)
	if err != nil {
		t.Fatal(err)
	}
	batch := full[len(before):]
	if bytes.Count(batch, []byte("\n")) != 1 {
		t.Fatal("transaction spans multiple independently replayable WAL records")
	}
	// Cut within each event, after the first event, after valid JSON but before
	// its commit newline, and after the newline. Only the last is replayable.
	firstEnd := bytes.Index(batch, []byte("},{")) + 1
	for _, cut := range []int{1, len(batch) / 4, firstEnd, len(batch) / 2, len(batch) - 2, len(batch) - 1, len(batch)} {
		t.Run(fmt.Sprint(cut), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cut.wal")
			data := append(append([]byte{}, before...), batch[:cut]...)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			recovered, err := New(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			want := int64(100)
			if cut == len(batch) {
				want = 118
			}
			if got := recovered.Balance("u"); got != want {
				t.Fatalf("partial transaction replay: got %d want %d", got, want)
			}
		})
	}
}
func TestCompleteCorruptWALRecordIsRejected(t *testing.T) {
	wal := filepath.Join(t.TempDir(), "orders.wal")
	if err := os.WriteFile(wal, []byte("{invalid}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(wal, nil); err == nil {
		t.Fatal("silently discarded complete corruption")
	}
}

type failingProjector struct{}

func (failingProjector) Apply(context.Context, Event) error {
	return errors.New("database unavailable")
}
func TestProjectionFailureBlocksWritesAndCheckpointUntilRecovery(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{WALPath: filepath.Join(dir, "orders.wal"), SnapshotPath: filepath.Join(dir, "snapshot"), Projector: failingProjector{}, Sync: true}
	s, err := NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Deposit("u", 100); err == nil {
		t.Fatal("expected projection failure")
	}
	if err = s.Deposit("u", 100); err == nil {
		t.Fatal("allowed retry with durable state ahead of memory")
	}
	if err = s.Snapshot(); err == nil {
		t.Fatal("checkpoint discarded unprojected durable event")
	}
	_ = s.Close()
	cfg.Projector = nil
	s, err = NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.Balance("u"); got != 100 {
		t.Fatalf("retry doubled durable deposit: %d", got)
	}
}
func TestConcurrentIdempotentRetriesAcrossCheckpointAndReplay(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{WALPath: filepath.Join(dir, "orders.wal"), SnapshotPath: filepath.Join(dir, "snapshot"), Sync: true}
	s, err := NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateMarket("m", "RETRIES"); err != nil {
		t.Fatal(err)
	}
	if err = s.Deposit("u", 1000); err != nil {
		t.Fatal(err)
	}
	o := Order{ID: "same", UserID: "u", MarketID: "m", Side: BuyYes, Price: 60, Quantity: 10}
	retry := func() {
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
		if s.Balance("u") != 400 {
			t.Fatalf("balance=%d", s.Balance("u"))
		}
	}
	retry()
	if err = s.Snapshot(); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	retry()
	conflict := o
	conflict.Quantity++
	if err = s.Place(context.Background(), conflict); err == nil {
		t.Fatal("conflicting retry accepted")
	}
}

type recordingEngine struct {
	submitted []Order
	next      uint32
}

func (*recordingEngine) AddBook(context.Context, string) error                     { return nil }
func (*recordingEngine) RemoveOrder(context.Context, string, uint32) (bool, error) { return true, nil }
func (e *recordingEngine) AddOrder(_ context.Context, _ string, price int32, qty uint32, side string) (EngineResult, error) {
	e.next++
	e.submitted = append(e.submitted, Order{EnginePrice: int(price), Quantity: int(qty), EngineSide: side})
	return EngineResult{OrderID: e.next}, nil
}
func TestSnapshotAndWALReplayPreserveAdmissionOrder(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{WALPath: filepath.Join(dir, "wal"), SnapshotPath: filepath.Join(dir, "snapshot"), Sync: true}
	s, err := NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateMarket("m", "FIFO"); err != nil {
		t.Fatal(err)
	}
	if err = s.Deposit("u", 10000); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err = s.Place(context.Background(), Order{ID: fmt.Sprint(i), UserID: "u", MarketID: "m", Side: BuyYes, Price: 50, Quantity: i + 1}); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if err = s.Snapshot(); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Keep an old engine mapping whose ID can collide with a restarted engine.
	o := s.orders["0"]
	o.EngineID = 2
	if err = s.commit(context.Background(), Event{Type: EventOrder, Order: o}); err != nil {
		t.Fatal(err)
	}
	o = s.orders["1"]
	o.EngineID = 1
	if err = s.commit(context.Background(), Event{Type: EventOrder, Order: o}); err != nil {
		t.Fatal(err)
	}
	wantBalances, wantPositions := s.balances, s.positions
	_ = s.Close()
	e := &recordingEngine{}
	cfg.Engine = e
	s, err = NewWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.RecoverEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, o := range e.submitted {
		if o.Quantity != i+1 || o.EnginePrice != 50 || o.EngineSide != "buy" {
			t.Fatalf("replay order=%+v", e.submitted)
		}
	}
	if len(e.submitted) != 3 || !reflect.DeepEqual(wantBalances, s.balances) || !reflect.DeepEqual(wantPositions, s.positions) {
		t.Fatal("replay changed account state")
	}
	for _, o := range s.orders {
		if s.engineOrders[o.EngineID] != o.ID {
			t.Fatalf("stale engine ID collision: %+v", s.engineOrders)
		}
	}
}

func TestCrashHelper(t *testing.T) {
	dir := os.Getenv("PME_CRASH_HELPER_DIR")
	if dir == "" {
		return
	}
	s, err := NewWithConfig(Config{WALPath: filepath.Join(dir, "wal"), SnapshotPath: filepath.Join(dir, "snapshot"), Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateMarket("m", "CRASH"); err != nil {
		t.Fatal(err)
	}
	if err = s.Deposit("u", 1000); err != nil {
		t.Fatal(err)
	}
	if err = s.Place(context.Background(), Order{ID: "first", UserID: "u", MarketID: "m", Side: BuyYes, Price: 50, Quantity: 2}); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PME_CRASH_SNAPSHOT") == "yes" {
		if err = s.Snapshot(); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Place(context.Background(), Order{ID: "second", UserID: "u", MarketID: "m", Side: BuyYes, Price: 40, Quantity: 3}); err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	for {
		time.Sleep(time.Hour)
	}
}
func TestKilledProcessRecoversAcknowledgedState(t *testing.T) {
	for _, checkpoint := range []string{"no", "yes"} {
		t.Run(checkpoint, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
			cmd.Env = append(os.Environ(), "PME_CRASH_HELPER_DIR="+dir, "PME_CRASH_SNAPSHOT="+checkpoint)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			ready := make(chan bool, 1)
			go func() {
				scan := bufio.NewScanner(stdout)
				for scan.Scan() {
					if strings.TrimSpace(scan.Text()) == "READY" {
						ready <- true
						return
					}
				}
				ready <- false
			}()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("helper exited before durable acknowledgement")
				}
			case <-time.After(15 * time.Second):
				t.Fatal("helper timed out")
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			cfg := Config{WALPath: filepath.Join(dir, "wal"), SnapshotPath: filepath.Join(dir, "snapshot"), Sync: true}
			s, err := NewWithConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if s.Balance("u") != 780 {
				t.Fatalf("lost acknowledged reservation: %d", s.Balance("u"))
			}
			for _, id := range []string{"first", "second"} {
				o, ok := s.Order(id)
				if !ok || o.Status != Open {
					t.Fatalf("lost order %s: %+v", id, o)
				}
				if err = s.Place(context.Background(), o); err != nil {
					t.Fatal(err)
				}
			}
			state, _ := json.Marshal(snapshot{Sequence: s.sequence, Markets: s.markets, Orders: s.orders, Balances: s.balances, Positions: s.positions})
			_ = s.Close()
			again, err := NewWithConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer again.Close()
			replay, _ := json.Marshal(snapshot{Sequence: again.sequence, Markets: again.markets, Orders: again.orders, Balances: again.balances, Positions: again.positions})
			if !bytes.Equal(state, replay) {
				t.Fatal("second replay changed final account/book state")
			}
		})
	}
}

func TestReplayProjectsWholeBatch(t *testing.T) {
	wal := filepath.Join(t.TempDir(), "wal")
	s, err := New(wal, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.commitMany(context.Background(), []Event{{Type: EventBalance, UserID: "u", Amount: 10}, {Type: EventBalance, UserID: "u", Amount: 20}}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	p := &countingProjector{}
	s, err = NewWithConfig(Config{WALPath: wal, Projector: p, Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if p.batches.Load() != 1 || s.Balance("u") != 30 {
		t.Fatalf("replay split batch: transactions=%d balance=%d", p.batches.Load(), s.Balance("u"))
	}
}
func TestExpiredPlacementDoesNotWriteWAL(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "wal"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateMarket("m", "CANCELLED_CONTEXT"); err != nil {
		t.Fatal(err)
	}
	if err = s.Deposit("u", 100); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = s.Place(ctx, Order{ID: "o", UserID: "u", MarketID: "m", Side: BuyYes, Price: 50, Quantity: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if s.Balance("u") != 100 {
		t.Fatal("expired request reserved funds")
	}
	if _, ok := s.Order("o"); ok {
		t.Fatal("expired request created order")
	}
}
