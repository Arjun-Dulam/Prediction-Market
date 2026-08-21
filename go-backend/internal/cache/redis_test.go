package cache

import (
	"context"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"math"
	"sync"
	"testing"
	"time"
)

func TestRedisQuotesRejectStaleDuplicateAndFullWidthVersions(t *testing.T) {
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{Image: "redis:7-alpine", ExposedPorts: []string{"6379/tcp"}, WaitingFor: wait.ForLog("Ready to accept connections")}, Started: true})
	if err != nil {
		t.Fatal(err)
	}
	defer container.Terminate(ctx)
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRedisQuotes(host + ":" + port.Port())
	defer r.Close()
	versions := []uint64{1, 2, 3, 1 << 53, 1<<53 + 1, math.MaxUint64 - 1, math.MaxUint64}
	for _, v := range versions {
		accepted, err := r.SetVersioned(ctx, "A", Quote{Sequence: v, Bid: 41, Ask: 60})
		if err != nil || !accepted {
			t.Fatalf("version %d: %v %v", v, accepted, err)
		}
		for _, old := range []uint64{v, v - 1} {
			accepted, err = r.SetVersioned(ctx, "A", Quote{Sequence: old, Bid: 99})
			if err != nil || accepted {
				t.Fatalf("accepted stale %d", old)
			}
		}
	}
	q, err := r.Get(ctx, "A")
	if err != nil || q.Sequence != math.MaxUint64 || q.Bid != 41 || q.UpdatedAt.IsZero() {
		t.Fatalf("%+v %v", q, err)
	}
	var wg sync.WaitGroup
	for i := uint64(1); i <= 64; i++ {
		wg.Add(1)
		go func(v uint64) {
			defer wg.Done()
			if _, err := r.SetVersioned(ctx, "B", Quote{Sequence: v, Bid: int32(v)}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	q, err = r.Get(ctx, "B")
	if err != nil || q.Sequence != 64 || q.Bid != 64 {
		t.Fatalf("concurrent regression %+v %v", q, err)
	}
	// An expired derived cache can be rebuilt; it is not the durable ledger.
	r.ttl = time.Millisecond
	_, err = r.SetVersioned(ctx, "C", Quote{Sequence: 100})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	accepted, err := r.SetVersioned(ctx, "C", Quote{Sequence: 1})
	if err != nil || !accepted {
		t.Fatalf("expiry %v %v", accepted, err)
	}
}
