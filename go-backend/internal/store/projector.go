// Package store projects durable trading events into PostgreSQL.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"go-backend/internal/trading"
)

type Projector struct{ db *sql.DB }

func NewProjector(db *sql.DB) *Projector { return &Projector{db: db} }

func (p *Projector) Migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, email TEXT UNIQUE NOT NULL, username TEXT UNIQUE NOT NULL, password_hash BYTEA NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS markets (id TEXT PRIMARY KEY, symbol TEXT UNIQUE NOT NULL, owner_id TEXT, status TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`ALTER TABLE markets ADD COLUMN IF NOT EXISTS owner_id TEXT`,
		`CREATE TABLE IF NOT EXISTS balances (user_id TEXT PRIMARY KEY, cents BIGINT NOT NULL CHECK(cents >= 0))`,
		`CREATE TABLE IF NOT EXISTS orders (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, market_id TEXT NOT NULL REFERENCES markets(id), side TEXT NOT NULL, engine_order_id BIGINT, engine_price SMALLINT NOT NULL, price SMALLINT NOT NULL CHECK(price BETWEEN 1 AND 99), quantity INT NOT NULL CHECK(quantity > 0), filled_quantity INT NOT NULL DEFAULT 0, status TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS positions (user_id TEXT NOT NULL, market_id TEXT NOT NULL REFERENCES markets(id), yes_shares BIGINT NOT NULL DEFAULT 0, no_shares BIGINT NOT NULL DEFAULT 0, PRIMARY KEY(user_id, market_id))`,
		`CREATE TABLE IF NOT EXISTS trades (event_id TEXT PRIMARY KEY, sequence BIGINT NOT NULL, market_id TEXT NOT NULL REFERENCES markets(id), buy_order_id TEXT NOT NULL, sell_order_id TEXT NOT NULL, price SMALLINT NOT NULL, quantity INT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS order_events (event_id TEXT PRIMARY KEY, sequence BIGINT NOT NULL, event JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`ALTER TABLE order_events ADD COLUMN IF NOT EXISTS event_id TEXT`,
		`UPDATE order_events SET event_id=md5(sequence::text || event::text) WHERE event_id IS NULL`,
		`ALTER TABLE order_events ALTER COLUMN event_id SET NOT NULL`,
		`DO $$ BEGIN IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='order_events'::regclass AND contype='p' AND pg_get_constraintdef(oid) NOT LIKE '%event_id%') THEN ALTER TABLE order_events DROP CONSTRAINT order_events_pkey; END IF; IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='order_events'::regclass AND contype='p') THEN ALTER TABLE order_events ADD CONSTRAINT order_events_pkey PRIMARY KEY(event_id); END IF; END $$`,
		`ALTER TABLE trades ADD COLUMN IF NOT EXISTS event_id TEXT`,
		`UPDATE trades SET event_id=md5(sequence::text || market_id || buy_order_id || sell_order_id) WHERE event_id IS NULL`,
		`ALTER TABLE trades ALTER COLUMN event_id SET NOT NULL`,
		`DO $$ BEGIN IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='trades'::regclass AND contype='p' AND pg_get_constraintdef(oid) NOT LIKE '%event_id%') THEN ALTER TABLE trades DROP CONSTRAINT trades_pkey; END IF; IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='trades'::regclass AND contype='p') THEN ALTER TABLE trades ADD CONSTRAINT trades_pkey PRIMARY KEY(event_id); END IF; END $$`,
		`CREATE INDEX IF NOT EXISTS orders_market_status ON orders(market_id,status)`,
	}
	for _, statement := range statements {
		if _, err := p.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// Apply is idempotent by event sequence. The event row and materialized views
// are committed in one transaction.
func (p *Projector) Apply(ctx context.Context, e trading.Event) error {
	return p.ApplyBatch(ctx, []trading.Event{e})
}

// ApplyBatch amortizes transaction and fsync overhead while preserving the
// same atomic, sequence-idempotent projection semantics as Apply.
func (p *Projector) ApplyBatch(ctx context.Context, events []trading.Event) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range events {
		if err := projectEvent(ctx, tx, e); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func projectEvent(ctx context.Context, tx *sql.Tx, e trading.Event) error {
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO order_events(event_id,sequence,event) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, e.ID, e.Sequence, payload)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil
	}
	switch e.Type {
	case trading.EventMarket:
		_, err = tx.ExecContext(ctx, `INSERT INTO markets(id,symbol,owner_id,status) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET symbol=EXCLUDED.symbol,owner_id=EXCLUDED.owner_id,status=EXCLUDED.status`, e.Market.ID, e.Market.Symbol, nullable(e.Market.OwnerID), e.Market.Status)
	case trading.EventBalance:
		err = changeBalance(ctx, tx, e.UserID, e.Amount)
	case trading.EventOrder:
		o := e.Order
		_, err = tx.ExecContext(ctx, `INSERT INTO orders(id,user_id,market_id,side,engine_order_id,engine_price,price,quantity,filled_quantity,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(id) DO UPDATE SET engine_order_id=EXCLUDED.engine_order_id,filled_quantity=EXCLUDED.filled_quantity,status=EXCLUDED.status`, o.ID, o.UserID, o.MarketID, o.Side, o.EngineID, o.EnginePrice, o.Price, o.Quantity, o.FilledQuantity, o.Status)
		if err == nil && e.Amount != 0 {
			err = changeBalance(ctx, tx, e.UserID, e.Amount)
		}
	case trading.EventPosition:
		_, err = tx.ExecContext(ctx, `INSERT INTO positions(user_id,market_id,yes_shares,no_shares) VALUES($1,$2,$3,$4) ON CONFLICT(user_id,market_id) DO UPDATE SET yes_shares=positions.yes_shares+EXCLUDED.yes_shares,no_shares=positions.no_shares+EXCLUDED.no_shares`, e.UserID, e.MarketID, e.YesDelta, e.NoDelta)
		if err == nil && e.Amount != 0 {
			err = changeBalance(ctx, tx, e.UserID, e.Amount)
		}
	case trading.EventTrade:
		t := e.Trade
		_, err = tx.ExecContext(ctx, `INSERT INTO trades(event_id,sequence,market_id,buy_order_id,sell_order_id,price,quantity) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, e.ID, e.Sequence, t.MarketID, t.BuyOrderID, t.SellOrderID, t.Price, t.Quantity)
	}
	if err != nil {
		return err
	}
	return nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func changeBalance(ctx context.Context, tx *sql.Tx, user string, delta int64) error {
	if delta >= 0 {
		_, err := tx.ExecContext(ctx, `INSERT INTO balances(user_id,cents) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET cents=balances.cents+$2`, user, delta)
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE balances SET cents=cents+$2 WHERE user_id=$1 AND cents+$2>=0`, user, delta)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return fmt.Errorf("insufficient projected balance for %q", user)
	}
	return nil
}
