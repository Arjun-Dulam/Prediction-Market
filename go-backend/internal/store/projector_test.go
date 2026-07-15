package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"go-backend/internal/trading"
)

func TestPostgresProjectionIsAtomicAndIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	container, err := postgrescontainer.Run(ctx, "postgres:17-alpine",
		postgrescontainer.WithDatabase("exchange"),
		postgrescontainer.WithUsername("exchange"),
		postgrescontainer.WithPassword("exchange"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(20*time.Second)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Terminate(context.Background())
	connection, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	projector := NewProjector(db)
	if err = projector.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	events := []trading.Event{
		{ID: "e1", Sequence: 1, Type: trading.EventMarket, Market: trading.Market{ID: "m", Symbol: "TEST", OwnerID: "creator", Status: trading.Open}},
		{ID: "e2", Sequence: 2, Type: trading.EventBalance, UserID: "u", Amount: 1000},
		{ID: "e3", Sequence: 3, Type: trading.EventOrder, UserID: "u", Amount: -600, Order: trading.Order{ID: "o", UserID: "u", MarketID: "m", Side: trading.BuyYes, Price: 60, EnginePrice: 60, Quantity: 10, Status: trading.Pending}},
		{ID: "e4", Sequence: 4, Type: trading.EventOrder, Order: trading.Order{ID: "o", UserID: "u", MarketID: "m", Side: trading.BuyYes, Price: 60, EnginePrice: 60, EngineID: 7, Quantity: 10, FilledQuantity: 10, Status: trading.Filled}},
		{ID: "e5", Sequence: 5, Type: trading.EventPosition, UserID: "u", MarketID: "m", YesDelta: 10, Amount: 50},
		{ID: "e6", Sequence: 6, Type: trading.EventTrade, Trade: trading.Trade{MarketID: "m", BuyOrderID: "o", SellOrderID: "counterparty", Price: 55, Quantity: 10}},
	}
	if err = projector.ApplyBatch(ctx, events); err != nil {
		t.Fatal(err)
	}
	if err = projector.ApplyBatch(ctx, events); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	var balance int64
	var yes int
	var status string
	var ownerID string
	var trades, eventCount int
	if err = db.QueryRowContext(ctx, `SELECT cents FROM balances WHERE user_id='u'`).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT yes_shares FROM positions WHERE user_id='u' AND market_id='m'`).Scan(&yes); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT status FROM orders WHERE id='o'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT owner_id FROM markets WHERE id='m'`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM trades`).Scan(&trades)
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM order_events`).Scan(&eventCount)
	if balance != 450 || yes != 10 || status != string(trading.Filled) || ownerID != "creator" || trades != 1 || eventCount != len(events) {
		t.Fatalf("projection balance=%d yes=%d status=%s owner=%s trades=%d events=%d", balance, yes, status, ownerID, trades, eventCount)
	}
	t.Run("mixed replay and duplicate within batch", func(t *testing.T) {
		extra := []trading.Event{events[4], {ID: "mixed-credit", Sequence: 7, Type: trading.EventBalance, UserID: "u", Amount: 10}, {ID: "mixed-position", Sequence: 8, Type: trading.EventPosition, UserID: "u", MarketID: "m", YesDelta: 2}}
		extra = append(extra, extra[1], extra[2])
		if err := projector.ApplyBatch(ctx, extra); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT cents FROM balances WHERE user_id='u'`).Scan(&balance); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT yes_shares FROM positions WHERE user_id='u' AND market_id='m'`).Scan(&yes); err != nil {
			t.Fatal(err)
		}
		if balance != 460 || yes != 12 {
			t.Fatalf("repeated effects: balance=%d yes=%d", balance, yes)
		}
	})
	t.Run("debit checks each intermediate state", func(t *testing.T) {
		bad := []trading.Event{
			{ID: "rollback-credit", Sequence: 9, Type: trading.EventBalance, UserID: "u", Amount: 100},
			{ID: "rollback-debit", Sequence: 10, Type: trading.EventBalance, UserID: "u", Amount: -1000},
			{ID: "rollback-later-credit", Sequence: 11, Type: trading.EventBalance, UserID: "u", Amount: 1000},
		}
		if err := projector.ApplyBatch(ctx, bad); err == nil {
			t.Fatal("insufficient intermediate balance accepted")
		}
		if err := db.QueryRowContext(ctx, `SELECT cents FROM balances WHERE user_id='u'`).Scan(&balance); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM order_events WHERE event_id LIKE 'rollback-%'`).Scan(&eventCount); err != nil {
			t.Fatal(err)
		}
		if balance != 460 || eventCount != 0 {
			t.Fatalf("partial transaction persisted: balance=%d events=%d", balance, eventCount)
		}
	})
	t.Run("SQL failure rolls back admitted IDs and prior effects", func(t *testing.T) {
		badOrder := events[2]
		badOrder.ID = "constraint-order"
		badOrder.Order.ID = "invalid"
		badOrder.Order.Quantity = 0
		credit := trading.Event{ID: "constraint-credit", Sequence: 12, Type: trading.EventBalance, UserID: "u", Amount: 20}
		if err := projector.ApplyBatch(ctx, []trading.Event{credit, badOrder}); err == nil {
			t.Fatal("invalid order accepted")
		}
		if err := db.QueryRowContext(ctx, `SELECT cents FROM balances WHERE user_id='u'`).Scan(&balance); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM order_events WHERE event_id LIKE 'constraint-%'`).Scan(&eventCount); err != nil {
			t.Fatal(err)
		}
		if balance != 460 || eventCount != 0 {
			t.Fatalf("partial transaction persisted: balance=%d events=%d", balance, eventCount)
		}
	})
	t.Run("concurrent duplicate admission", func(t *testing.T) {
		credit := trading.Event{ID: "concurrent-credit", Sequence: 13, Type: trading.EventBalance, UserID: "u", Amount: 7}
		var workers sync.WaitGroup
		errors := make(chan error, 16)
		for i := 0; i < 16; i++ {
			workers.Add(1)
			go func() { defer workers.Done(); errors <- projector.Apply(ctx, credit) }()
		}
		workers.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := db.QueryRowContext(ctx, `SELECT cents FROM balances WHERE user_id='u'`).Scan(&balance); err != nil {
			t.Fatal(err)
		}
		if balance != 467 {
			t.Fatalf("duplicate credit: %d", balance)
		}
	})
	t.Run("cancellation inside native transaction rolls back and returns pool", func(t *testing.T) {
		db.SetMaxOpenConns(2)
		locker, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer locker.Rollback()
		if _, err = locker.ExecContext(ctx, `SELECT cents FROM balances WHERE user_id='u' FOR UPDATE`); err != nil {
			t.Fatal(err)
		}
		blocked, stop := context.WithTimeout(ctx, 50*time.Millisecond)
		defer stop()
		credit := trading.Event{ID: "blocked-credit", Sequence: 14, Type: trading.EventBalance, UserID: "u", Amount: 11}
		if err = projector.Apply(blocked, credit); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected blocked transaction deadline, got %v", err)
		}
		if err = locker.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err = projector.Apply(ctx, credit); err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRowContext(ctx, `SELECT cents FROM balances WHERE user_id='u'`).Scan(&balance); err != nil {
			t.Fatal(err)
		}
		if balance != 478 {
			t.Fatalf("timed out debit applied twice: %d", balance)
		}
	})

	t.Run("cancelled context leaves connection reusable", func(t *testing.T) {
		db.SetMaxOpenConns(1)
		cancelled, stop := context.WithCancel(ctx)
		stop()
		credit := trading.Event{ID: "cancelled-credit", Sequence: 15, Type: trading.EventBalance, UserID: "u", Amount: 11}
		if err := projector.Apply(cancelled, credit); err == nil {
			t.Fatal("cancelled operation succeeded")
		}
		if err := projector.Apply(ctx, credit); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT cents FROM balances WHERE user_id='u'`).Scan(&balance); err != nil {
			t.Fatal(err)
		}
		if balance != 489 {
			t.Fatalf("cancelled credit persisted: %d", balance)
		}
	})

}

// The native pgx batch must preserve SQL ordering, atomicity and deduplication
// while returning its borrowed connection to database/sql in a usable state.
