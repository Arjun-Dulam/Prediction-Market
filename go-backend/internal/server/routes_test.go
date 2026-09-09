package server

import (
	"bytes"
	"context"
	"encoding/json"
	"go-backend/internal/trading"
	"go-backend/internal/ws"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	enginepb "go-backend/internal/engine/pb"
)

type testEngine struct{ nextID atomic.Uint32 }

func (e *testEngine) AddOrder(_ context.Context, _ string, _ int32, _ uint32, _ enginepb.Side) (uint32, error) {
	return e.nextID.Add(1), nil
}
func (e *testEngine) BestBid(context.Context, string) (int32, error) { return 61, nil }
func (e *testEngine) BestAsk(context.Context, string) (int32, error) { return 63, nil }
func (e *testEngine) Close() error                                   { return nil }

func TestHandler(t *testing.T) {
	s := &Server{}
	server := httptest.NewServer(http.HandlerFunc(s.HelloWorldHandler))
	defer server.Close()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("error making request to server. Err: %v", err)
	}
	defer resp.Body.Close()
	// Assertions
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status OK; got %v", resp.Status)
	}
	expected := "{\"message\":\"Prediction Market Exchange\"}\n"
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("error reading response body. Err: %v", err)
	}
	if expected != string(body) {
		t.Errorf("expected response body to be %v; got %v", expected, string(body))
	}
}

func TestPlaceOrder(t *testing.T) {
	server := httptest.NewServer(NewHandler(&testEngine{}))
	defer server.Close()

	response, err := http.Post(server.URL+"/api/v1/orders", "application/json", bytes.NewBufferString(`{"symbol":"ELECTION_2028","price":65,"quantity":10,"side":"buy"}`))
	if err != nil {
		t.Fatalf("submit order: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %s", response.Status)
	}

	var body orderResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.OrderID != 1 {
		t.Fatalf("expected order ID 1, got %d", body.OrderID)
	}
}

func TestPlaceOrderRejectsInvalidPayload(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewBufferString(`{"symbol":"","price":100,"quantity":0,"side":"hold"}`))
	NewHandler(&testEngine{}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
}

// BenchmarkOrderSubmission measures HTTP validation, JSON serialization, and
// concurrent request handling. It intentionally uses a stub engine; benchmark
// against a running ENGINE_ADDR before citing a full gateway-to-engine metric.
func BenchmarkOrderSubmission(b *testing.B) {
	server := httptest.NewServer(NewHandler(&testEngine{}))
	defer server.Close()

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        128,
			MaxIdleConnsPerHost: 128,
		},
	}
	defer client.CloseIdleConnections()
	body := []byte(`{"symbol":"ELECTION_2028","price":65,"quantity":10,"side":"buy"}`)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/orders", bytes.NewReader(body))
			if err != nil {
				b.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := client.Do(request)
			if err != nil {
				b.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusCreated {
				b.Fatalf("expected 201, got %s", response.Status)
			}
		}
	})
	b.StopTimer()
}

// The HTTP deadline may end after durable reservation but before the group's
// independent engine operation finishes. That is retryable, not invalid input.
type delayedTradingEngine struct {
	entered chan struct{}
	release chan struct{}
}

func (e *delayedTradingEngine) AddBook(context.Context, string) error { return nil }
func (e *delayedTradingEngine) RemoveOrder(context.Context, string, uint32) (bool, error) {
	return true, nil
}
func (e *delayedTradingEngine) AddOrder(ctx context.Context, _ string, _ int32, _ uint32, _ string) (trading.EngineResult, error) {
	e.entered <- struct{}{}
	select {
	case <-e.release:
		return trading.EngineResult{OrderID: 1}, nil
	case <-ctx.Done():
		return trading.EngineResult{}, ctx.Err()
	}
}
func TestAdmittedOrderHTTPTimeoutIsRetryableAndRetainsReservation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancelled", true: "deadline"}[deadline], func(t *testing.T) {
			e := &delayedTradingEngine{entered: make(chan struct{}, 1), release: make(chan struct{})}
			service, err := trading.NewWithConfig(trading.Config{WALPath: filepath.Join(t.TempDir(), "wal"), Sync: true, Engine: e, OrderBatchSize: 32})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { service.Close() })
			if err = service.CreateMarket("m", "M"); err != nil {
				t.Fatal(err)
			}
			if err = service.Deposit("u", 100); err != nil {
				t.Fatal(err)
			}
			var ctx context.Context
			var cancel context.CancelFunc
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			request := httptest.NewRequest("POST", "/api/v1/trading/orders", strings.NewReader(`{"id":"o","user_id":"u","market_id":"m","side":"buy_yes","price":50,"quantity":1}`)).WithContext(ctx)
			recorder := httptest.NewRecorder()
			done := make(chan struct{})
			server := &Server{trading: service, hub: ws.NewHub()}
			go func() { server.durableOrderHandler(recorder, request); close(done) }()
			select {
			case <-e.entered:
			case <-time.After(time.Second):
				close(e.release)
				t.Fatal("order was not admitted")
			}
			if !deadline {
				cancel()
			}
			<-done
			if recorder.Code != 503 {
				close(e.release)
				t.Fatalf("HTTP %d: %s", recorder.Code, recorder.Body.String())
			}
			close(e.release)
			service.Close() // Wait for admitted work to finish despite the HTTP timeout.
			o, ok := service.Order("o")
			if !ok || o.Status != trading.Open || service.Balance("u") != 50 {
				t.Fatalf("admitted order did not finish exactly once: %+v", o)
			}
		})
	}
}
