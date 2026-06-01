package store

import (
	"context"
	"database/sql"
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
}
