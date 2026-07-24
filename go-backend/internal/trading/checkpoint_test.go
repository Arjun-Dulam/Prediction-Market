package trading

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func checkpointWithWriter(s *Service, writer func(string, snapshot) error) error {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()
	return s.checkpoint(writer)
}

func TestSlowCheckpointAllowsOrdersAndPreservesFrozenState(t *testing.T) {
	s := batchService(t, nil)
	initial := Order{ID: "first", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 50, Quantity: 1}
	if err := s.Place(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	err := s.commit(context.Background(), Event{Type: EventPosition, UserID: "yes", MarketID: "m", YesDelta: 5})
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- checkpointWithWriter(s, func(path string, state snapshot) error {
			close(entered)
			<-release
			if state.Balances["yes"] != 950 || state.Positions["yes"]["m"].Yes != 5 || len(state.Orders) != 1 {
				return fmt.Errorf("snapshot mutated after freeze: %+v", state)
			}
			return writeSnapshot(path, state)
		})
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	next := Order{ID: "second", UserID: "yes", MarketID: "m", Side: BuyYes, Price: 40, Quantity: 1}
	// The order must finish while snapshot storage remains blocked.
	err = s.Place(ctx, next)
	if err != nil {
		close(release)
		<-done
		t.Fatalf("checkpoint blocked order: %v", err)
	}
	if err = s.Deposit("yes", 7); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	err = s.commit(context.Background(), Event{Type: EventPosition, UserID: "yes", MarketID: "m", YesDelta: 3})
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(s.cfg.WALPath + ".previous"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completed checkpoint retained previous segment")
	}
	s.Close()
	recovered, err := NewWithConfig(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recovered.Balance("yes") != 917 || recovered.Position("yes", "m").Yes != 8 || len(recovered.orders) != 2 {
		t.Fatal("post-freeze acknowledgements lost on replay")
	}
}

func TestFailedCheckpointRetainsBothSegmentsAndRetryIsSafe(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(fmt.Sprint(published), func(t *testing.T) {
			s := batchService(t, nil)
			if err := s.Snapshot(); err != nil {
				t.Fatal(err)
			}
			if err := s.Deposit("yes", 7); err != nil {
				t.Fatal(err)
			}
			err := checkpointWithWriter(s, func(path string, state snapshot) error {
				if err := s.Deposit("yes", 11); err != nil {
					return err
				}
				if published {
					if err := writeSnapshot(path, state); err != nil {
						return err
					}
				}
				return errors.New("checkpoint disk failure")
			})
			if err == nil {
				t.Fatal("checkpoint failure ignored")
			}
			if _, err = os.Stat(s.cfg.WALPath + ".previous"); err != nil {
				t.Fatal("unpublished prefix discarded")
			}
			s.Close()
			recovered, err := NewWithConfig(s.cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			if recovered.Balance("yes") != 1018 {
				t.Fatal("checkpoint failure lost a segment")
			}
			if err = recovered.Deposit("yes", 5); err != nil {
				t.Fatal(err)
			}
			if err = recovered.Snapshot(); err != nil {
				t.Fatal(err)
			} // retain current log for this retry
			if err = recovered.Snapshot(); err != nil {
				t.Fatal(err)
			} // next normal rotation
			recovered.Close()
			again, err := NewWithConfig(s.cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer again.Close()
			if again.Balance("yes") != 1023 {
				t.Fatal("retry lost or duplicated events")
			}
		})
	}
}

func TestCrashBetweenWALRenameAndCreationRecoversPrevious(t *testing.T) {
	s := batchService(t, nil)
	if err := s.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if err := s.Deposit("yes", 7); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := os.Rename(s.cfg.WALPath, s.cfg.WALPath+".previous"); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewWithConfig(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if recovered.Balance("yes") != 1007 {
		t.Fatal("missing-current rotation gap lost prefix")
	}
	if err = recovered.Deposit("yes", 11); err != nil {
		t.Fatal(err)
	}
	if err = recovered.Snapshot(); err != nil {
		t.Fatal(err)
	}
	recovered.Close()
	again, err := NewWithConfig(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Balance("yes") != 1018 {
		t.Fatal("post-gap append was not recoverable")
	}
}

func TestCheckpointCrashHelper(t *testing.T) {
	dir := os.Getenv("PME_CHECKPOINT_HELPER_DIR")
	if dir == "" {
		return
	}
	phase := os.Getenv("PME_CHECKPOINT_HELPER_PHASE")
	s, err := NewWithConfig(Config{WALPath: filepath.Join(dir, "wal"), SnapshotPath: filepath.Join(dir, "snapshot"), Sync: true, OrderBatchSize: 32})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateMarket("m", "CHECKPOINT"); err != nil {
		t.Fatal(err)
	}
	if err = s.Deposit("u", 1000); err != nil {
		t.Fatal(err)
	}
	first := Order{ID: "first", UserID: "u", MarketID: "m", Side: BuyYes, Price: 50, Quantity: 2}
	if err = s.Place(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err = s.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if err = s.Deposit("u", 7); err != nil {
		t.Fatal(err)
	}
	wait := func() {
		fmt.Println("READY")
		for {
			time.Sleep(time.Hour)
		}
	}
	err = checkpointWithWriter(s, func(path string, state snapshot) error {
		second := Order{ID: "second", UserID: "u", MarketID: "m", Side: BuyYes, Price: 40, Quantity: 3}
		if err := s.Place(context.Background(), second); err != nil {
			return err
		}
		if err := s.Deposit("u", 11); err != nil {
			return err
		}
		if phase == "rotated" {
			wait()
		}
		if err := writeSnapshot(path, state); err != nil {
			return err
		}
		if phase == "published" {
			wait()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if phase == "deleted" {
		wait()
	}
	t.Fatal("invalid checkpoint helper phase")
}

func TestKilledCheckpointRecoversBothSidesOfFreeze(t *testing.T) {
	for _, phase := range []string{"rotated", "published", "deleted"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestCheckpointCrashHelper$")
			cmd.Env = append(os.Environ(), "PME_CHECKPOINT_HELPER_DIR="+dir, "PME_CHECKPOINT_HELPER_PHASE="+phase)
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { cmd.Process.Kill(); cmd.Wait() }()
			ready := make(chan bool, 1)
			go func() {
				scan := bufio.NewScanner(output)
				for scan.Scan() {
					if scan.Text() == "READY" {
						ready <- true
						return
					}
				}
				ready <- false
			}()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("helper exited before crash boundary")
				}
			case <-time.After(15 * time.Second):
				t.Fatal("checkpoint helper timed out")
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			cmd.Wait()
			cfg := Config{WALPath: filepath.Join(dir, "wal"), SnapshotPath: filepath.Join(dir, "snapshot"), Sync: true, OrderBatchSize: 32}
			s, err := NewWithConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if s.Balance("u") != 798 || len(s.orders) != 2 {
				t.Fatal("lost acknowledged writes around checkpoint")
			}
			encoded, _ := json.Marshal(snapshot{Sequence: s.sequence, Markets: s.markets, Orders: s.orders, Balances: s.balances, Positions: s.positions})
			s.Close()
			again, err := NewWithConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer again.Close()
			encodedAgain, _ := json.Marshal(snapshot{Sequence: again.sequence, Markets: again.markets, Orders: again.orders, Balances: again.balances, Positions: again.positions})
			if !reflect.DeepEqual(encoded, encodedAgain) {
				t.Fatal("second checkpoint replay differs")
			}
			if err = again.Snapshot(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCloseWaitsForInFlightCheckpoint(t *testing.T) {
	s := batchService(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := checkpointWithWriter(s, func(path string, state snapshot) error { close(entered); <-release; return writeSnapshot(path, state) }); err != nil {
			t.Error(err)
		}
	}()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case <-closed:
		t.Fatal("close raced unfinished checkpoint")
	default:
	}
	close(release)
	wg.Wait()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}
