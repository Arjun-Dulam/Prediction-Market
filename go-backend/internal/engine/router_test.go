package engine

import (
	"context"
	pb "go-backend/internal/engine/pb"
	"go-backend/internal/trading"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"sync"
	"testing"
	"time"
)

type batchPeer struct {
	pb.UnimplementedMatchingEngineServer
	mu      sync.Mutex
	symbols []string
	entered chan struct{}
	release <-chan struct{}
	fail    bool
}

func (p *batchPeer) AddOrders(ctx context.Context, r *pb.OrderBatch) (*pb.AddOrdersResponse, error) {
	if p.entered != nil {
		p.entered <- struct{}{}
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.fail {
		return nil, status.Error(codes.Unavailable, "lost acknowledgement")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := &pb.AddOrdersResponse{}
	for i, o := range r.Orders {
		p.symbols = append(p.symbols, o.Symbol)
		out.Results = append(out.Results, &pb.AddOrderResponse{OrderId: uint32(i + 1)})
		out.Quotes = append(out.Quotes, &pb.BookQuote{Symbol: o.Symbol, Bid: o.Order.Price, Ask: -1})
	}
	return out, nil
}
func testPeer(t *testing.T, p *batchPeer) *Client {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterMatchingEngineServer(server, p)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///test", grpc.WithInsecure(), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); server.Stop(); listener.Close() })
	return &Client{conn: conn, client: pb.NewMatchingEngineClient(conn)}
}
func TestPartitionedBatchRunsOwnersConcurrentlyAndRestoresInputOrder(t *testing.T) {
	released := make(chan struct{})
	defer close(released)
	a, b := &batchPeer{entered: make(chan struct{}, 1), release: released}, &batchPeer{entered: make(chan struct{}, 1), release: released}
	router := &Router{clients: []*Client{testPeer(t, a), testPeer(t, b)}}
	// FNV-1a maps A/B to distinct owners; each owner allocates IDs from one.
	if partition("A", 2) == partition("B", 2) {
		t.Fatal("fixture needs distinct owners")
	}
	done := make(chan trading.EngineBatch, 1)
	errs := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		result, err := (routedTrading{router}).AddOrders(ctx, []trading.EngineOrder{{Symbol: "A", Price: 40, Quantity: 1, Side: "buy"}, {Symbol: "B", Price: 50, Quantity: 1, Side: "buy"}, {Symbol: "A", Price: 41, Quantity: 1, Side: "buy"}})
		done <- result
		errs <- err
	}()
	for _, p := range []*batchPeer{a, b} {
		select {
		case <-p.entered:
		case <-ctx.Done():
			t.Fatal("owner RPCs were serialized")
		}
	}
	// Release without closing the channel twice through deferred cleanup.
	released <- struct{}{}
	released <- struct{}{}
	result := <-done
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 3 || result.Results[0].OrderID != 1 || result.Results[1].OrderID != 1 || result.Results[2].OrderID != 2 {
		t.Fatalf("input order lost: %+v", result)
	}
	p := a
	if partition("A", 2) == 1 {
		p = b
	}
	if len(p.symbols) != 2 || p.symbols[0] != "A" || p.symbols[1] != "A" {
		t.Fatal("same market split between owners")
	}
}
func TestPartitionFailureReturnsNoPartialSuccess(t *testing.T) {
	peers := []*batchPeer{{fail: true}, {}}
	r := &Router{clients: []*Client{testPeer(t, peers[0]), testPeer(t, peers[1])}}
	batch, err := (routedTrading{r}).AddOrders(context.Background(), []trading.EngineOrder{{Symbol: "A", Price: 40, Quantity: 1, Side: "buy"}, {Symbol: "B", Price: 50, Quantity: 1, Side: "buy"}})
	if err == nil || len(batch.Results) != 0 {
		t.Fatalf("partial success: %+v %v", batch, err)
	}
	if len(peers[1].symbols) != 1 {
		t.Fatal("successful peer should have executed: recovery must cover both")
	}
}
func TestInvalidRoutingConfiguration(t *testing.T) {
	for _, s := range []string{"", "a,", "a,a", "a,b,c"} {
		if r, err := NewRouter(s); err == nil {
			r.Close()
			t.Fatalf("accepted %q", s)
		}
	}
	if _, err := (routedTrading{&Router{}}).AddOrders(context.Background(), nil); err == nil {
		t.Fatal("empty batch")
	}
}
