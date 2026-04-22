# Go Backend - Detailed Implementation Plan

## Current State (as of April 2026)

The `go-backend/` directory has a go-blueprint scaffold:
- chi router wired up, CORS configured
- Postgres connection via pgx, health check endpoint
- Graceful shutdown in `main.go`
- docker-compose with Postgres only
- No migrations, no models, no store layer, no auth, no handlers

The C++ gRPC server is complete. Proto is at `proto/exchange.proto`.

**Known proto gap:** `AddOrder` returns only `OrderID`. It needs to return trades as well (required for Phase 5). Fix this before implementing order placement.

---

## Phase 3A: Project Structure Refactor

Reorganize `internal/` to match the planned layout. The blueprint put things in `internal/server/` — move them out.

**Target structure:**
```
go-backend/
  cmd/api/main.go               # entry point (already exists, keep as-is)
  internal/
    api/
      router.go                 # chi router + middleware wiring (replaces server/routes.go)
      server.go                 # http.Server setup (replaces server/server.go)
      middleware/
        auth.go                 # JWT extraction, inject user_id into context
      handlers/
        users.go
        markets.go
        orders.go
        accounts.go
    models/                     # plain Go structs, no DB logic
      user.go
      market.go
      order.go
      trade.go
      position.go
      balance.go
    store/                      # Postgres CRUD, one file per entity
      db.go                     # shared DBTX interface + connection setup
      user_store.go
      market_store.go
      order_store.go
      trade_store.go
      position_store.go
      balance_store.go
    engine/
      client.go                 # gRPC client wrapping generated code
    service/
      trading.go                # core order placement business logic
      recovery.go               # engine state rebuild on startup
    cache/
      redis.go                  # connection + generic helpers
      orderbook_cache.go        # set/get best bid, ask, last price, depth
    ws/
      hub.go                    # connection manager, fan-out broadcaster
      client.go                 # per-connection read/write pumps
  migrations/
    001_create_users.up.sql / .down.sql
    002_create_markets.up.sql / .down.sql
    003_create_orders.up.sql / .down.sql
    004_create_trades.up.sql / .down.sql
    005_create_positions.up.sql / .down.sql
    006_create_balances.up.sql / .down.sql
```

**Steps:**
1. Create `internal/api/router.go` — copy route registration from `internal/server/routes.go`
2. Create `internal/api/server.go` — copy Server struct and NewServer from `internal/server/server.go`
3. Update `cmd/api/main.go` import path from `go-backend/internal/server` to `go-backend/internal/api`
4. Delete `internal/server/`

---

## Phase 3B: Database Migrations

**Install tooling:**
```bash
go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```

**Add to Makefile:**
```makefile
migrate-up:
    migrate -path migrations/ -database "postgres://..." up

migrate-down:
    migrate -path migrations/ -database "postgres://..." down 1
```

**Schema — write these SQL files:**

`001_create_users.up.sql`
```sql
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email         TEXT NOT NULL UNIQUE,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

`002_create_markets.up.sql`
```sql
CREATE TYPE market_status AS ENUM ('open', 'halted', 'resolved_yes', 'resolved_no');

CREATE TABLE markets (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title      TEXT NOT NULL,
    symbol     TEXT NOT NULL UNIQUE,
    status     market_status NOT NULL DEFAULT 'open',
    close_at   TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

`003_create_orders.up.sql`
```sql
CREATE TYPE order_side AS ENUM ('buy_yes', 'buy_no', 'sell_yes', 'sell_no');
CREATE TYPE engine_side AS ENUM ('buy', 'sell');
CREATE TYPE order_status AS ENUM ('open', 'partial', 'filled', 'cancelled');

CREATE TABLE orders (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id          UUID NOT NULL REFERENCES users(id),
    market_id        UUID NOT NULL REFERENCES markets(id),
    engine_order_id  INT,
    side             order_side NOT NULL,
    engine_side      engine_side NOT NULL,
    price            INT NOT NULL CHECK (price BETWEEN 1 AND 99),
    quantity         INT NOT NULL CHECK (quantity > 0),
    filled_quantity  INT NOT NULL DEFAULT 0,
    status           order_status NOT NULL DEFAULT 'open',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX orders_market_status ON orders(market_id, status);
CREATE INDEX orders_user ON orders(user_id);
```

`004_create_trades.up.sql`
```sql
CREATE TABLE trades (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    market_id     UUID NOT NULL REFERENCES markets(id),
    buy_order_id  UUID NOT NULL REFERENCES orders(id),
    sell_order_id UUID NOT NULL REFERENCES orders(id),
    buyer_id      UUID NOT NULL REFERENCES users(id),
    seller_id     UUID NOT NULL REFERENCES users(id),
    price         INT NOT NULL,
    quantity      INT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX trades_market ON trades(market_id);
```

`005_create_positions.up.sql`
```sql
CREATE TABLE positions (
    user_id    UUID NOT NULL REFERENCES users(id),
    market_id  UUID NOT NULL REFERENCES markets(id),
    yes_shares INT NOT NULL DEFAULT 0 CHECK (yes_shares >= 0),
    no_shares  INT NOT NULL DEFAULT 0 CHECK (no_shares >= 0),
    PRIMARY KEY (user_id, market_id)
);
```

`006_create_balances.up.sql`
```sql
CREATE TABLE current_balances (
    user_id UUID PRIMARY KEY REFERENCES users(id),
    balance BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0)
);

CREATE TABLE balance_ledger (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id      UUID NOT NULL REFERENCES users(id),
    amount       BIGINT NOT NULL,   -- positive = credit, negative = debit
    reason       TEXT NOT NULL,     -- e.g. 'deposit', 'order_reserve', 'trade_proceeds', 'refund'
    reference_id UUID,              -- order_id or trade_id this relates to
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ledger_user ON balance_ledger(user_id);
```

Each `.down.sql` should `DROP TABLE` in reverse order, `DROP TYPE` where applicable.

---

## Phase 3C: Models

Create plain Go structs in `internal/models/`. No database logic here — just types.

Key design points:
- Use `uuid.UUID` (from `github.com/google/uuid`) for ID fields
- Use `time.Time` for timestamps
- Use string-backed custom types for enums (e.g., `type MarketStatus string`)

```go
// internal/models/market.go
type MarketStatus string
const (
    MarketOpen        MarketStatus = "open"
    MarketHalted      MarketStatus = "halted"
    MarketResolvedYes MarketStatus = "resolved_yes"
    MarketResolvedNo  MarketStatus = "resolved_no"
)

type Market struct {
    ID        uuid.UUID
    Title     string
    Symbol    string
    Status    MarketStatus
    CloseAt   *time.Time
    CreatedAt time.Time
}
```

Do the same pattern for User, Order, Trade, Position, Balance.

---

## Phase 3D: Store Layer

**Key design: the `DBTX` interface**

All store methods accept a `DBTX` so they work both standalone and inside a transaction:

```go
// internal/store/db.go
type DBTX interface {
    ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
    QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
    QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
```

Each store takes a `DBTX` parameter (not stored on the struct), so you can pass `*sql.DB` normally or `*sql.Tx` when inside a transaction.

**Store interfaces to implement:**

```go
// UserStore
Create(ctx, db DBTX, u *models.User) error
GetByID(ctx, db DBTX, id uuid.UUID) (*models.User, error)
GetByEmail(ctx, db DBTX, email string) (*models.User, error)

// MarketStore
Create(ctx, db DBTX, m *models.Market) error
GetByID(ctx, db DBTX, id uuid.UUID) (*models.Market, error)
List(ctx, db DBTX) ([]*models.Market, error)
UpdateStatus(ctx, db DBTX, id uuid.UUID, status models.MarketStatus) error

// OrderStore
Create(ctx, db DBTX, o *models.Order) error
GetByID(ctx, db DBTX, id uuid.UUID) (*models.Order, error)
UpdateEngineID(ctx, db DBTX, id uuid.UUID, engineID int) error
UpdateFilled(ctx, db DBTX, id uuid.UUID, filledQty int, status models.OrderStatus) error
UpdateStatus(ctx, db DBTX, id uuid.UUID, status models.OrderStatus) error
ListOpenByMarket(ctx, db DBTX, marketID uuid.UUID) ([]*models.Order, error)

// TradeStore
Create(ctx, db DBTX, t *models.Trade) error
ListByMarket(ctx, db DBTX, marketID uuid.UUID) ([]*models.Trade, error)

// PositionStore
GetOrCreate(ctx, db DBTX, userID, marketID uuid.UUID) (*models.Position, error)
AddShares(ctx, db DBTX, userID, marketID uuid.UUID, yesD, noD int) error

// BalanceStore
Get(ctx, db DBTX, userID uuid.UUID) (int64, error)
DebitForUpdate(ctx, db DBTX, userID uuid.UUID, amount int64) error  // assumes FOR UPDATE already held
Credit(ctx, db DBTX, userID uuid.UUID, amount int64) error
InsertLedger(ctx, db DBTX, userID uuid.UUID, amount int64, reason string, refID *uuid.UUID) error
```

---

## Phase 3E: gRPC Client

**Step 1 — Codegen setup.** Install protoc plugins:
```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

Add Makefile target:
```makefile
proto-gen:
    protoc --go_out=go-backend/internal/engine/pb \
           --go-grpc_out=go-backend/internal/engine/pb \
           -I proto proto/exchange.proto
```

**Step 2 — Fix the proto before generating.** Update `proto/exchange.proto` so `AddOrder` returns trades:

```protobuf
message TradeResult {
  uint32 buy_engine_order_id  = 1;
  uint32 sell_engine_order_id = 2;
  int32  price                = 3;
  uint32 quantity             = 4;
}

message AddOrderResponse {
  uint32              order_id = 1;
  repeated TradeResult trades  = 2;
}

service MatchingEngine {
  rpc AddBook(Symbol)            returns (BookVoid);
  rpc RemoveBook(Symbol)         returns (BookVoid);
  rpc AddOrder(OrderSubmission)  returns (AddOrderResponse);  // UPDATED
  rpc RemoveOrder(OrderDeletion) returns (Boolean);
  rpc GetBestBid(Symbol)         returns (Price);
  rpc GetBestAsk(Symbol)         returns (Price);
  rpc GetLastTradePrice(Symbol)  returns (Price);
}
```

Also update the C++ `grpc_server.cpp` to populate `trades` in the response from `Exchange::add_order`'s return value.

**Step 3 — `internal/engine/client.go`:**
```go
type Client struct {
    conn   *grpc.ClientConn
    engine pb.MatchingEngineClient
}

func New(addr string) (*Client, error)  // grpc.NewClient with timeout
func (c *Client) AddBook(ctx, symbol string) error
func (c *Client) AddOrder(ctx context.Context, symbol string, price int32, qty uint32, side pb.Side) (orderID uint32, trades []*pb.TradeResult, err error)
func (c *Client) RemoveOrder(ctx context.Context, symbol string, orderID uint32) (bool, error)
func (c *Client) GetBestBid(ctx context.Context, symbol string) (int32, error)
func (c *Client) GetBestAsk(ctx context.Context, symbol string) (int32, error)
```

---

## Phase 4: Users, Markets, Auth

**Dependencies to add:**
- `golang-jwt/jwt/v5` — JWT creation + validation
- `golang.org/x/crypto/bcrypt` — password hashing (already in stdlib-adjacent)

**`POST /api/v1/users/register`**
1. Decode JSON body: `{email, username, password}`
2. Validate: non-empty, email format, password length
3. `bcrypt.GenerateFromPassword(password, 12)`
4. `UserStore.Create` + `BalanceStore` insert initial 0-balance row (same tx)
5. Return `{user_id, username}`

**`POST /api/v1/users/login`**
1. `UserStore.GetByEmail`
2. `bcrypt.CompareHashAndPassword`
3. Sign JWT with `user_id` claim, 24h expiry
4. Return `{token}`

**JWT middleware (`internal/api/middleware/auth.go`)**
- Parse `Authorization: Bearer <token>` header
- Validate signature + expiry
- Inject `user_id` (UUID) into `context.Context`
- Return 401 if missing/invalid

**`POST /api/v1/markets`** (admin only — for now just check a hardcoded admin UUID or env var)
1. Create market row in Postgres
2. Call `engine.Client.AddBook(symbol)` via gRPC
3. Return market object

**`GET /api/v1/markets`** — query + return list

**`GET /api/v1/markets/:id`** — query market, call `GetBestBid` + `GetBestAsk` from engine, attach to response

**`POST /api/v1/deposit`** — credit balance + ledger entry (no real payment integration)

**`GET /api/v1/balance`** — return `current_balances.balance` for authed user

---

## Phase 5: Order Placement

This is the most complex part. Read it carefully before starting.

**The side translation rule:**
| User intent | Engine order |
|---|---|
| buy_yes @ 65 | buy @ 65 |
| sell_yes @ 65 | sell @ 65 |
| buy_no @ 35 | sell @ 65 (100 - 35) |
| sell_no @ 35 | buy @ 65 (100 - 35) |

**`POST /api/v1/orders` flow (in `internal/service/trading.go`):**

```
1. Validate inputs: market exists + open, price ∈ [1,99], qty > 0
2. Translate side to engine_side + engine_price
3. Compute cost = price * quantity  (cents)
4. BEGIN TX
5. SELECT balance FROM current_balances WHERE user_id = $1 FOR UPDATE
   -- the FOR UPDATE is critical: serializes concurrent orders from same user
6. If balance < cost: ROLLBACK, return 402 Insufficient Funds
7. Debit balance: UPDATE current_balances SET balance = balance - cost WHERE user_id = $1
8. INSERT into balance_ledger (reason='order_reserve')
9. INSERT into orders (status='open', engine_order_id=NULL initially)
10. COMMIT  -- release the balance lock before calling gRPC
            -- (don't hold a DB tx open across a network call)

11. Call engine.AddOrder(symbol, engine_price, qty, engine_side)
    -- returns (engine_order_id, []TradeResult)

12. BEGIN TX
13. UPDATE orders SET engine_order_id = $1 WHERE id = $2
14. For each trade in TradeResult:
    a. Resolve buy_order and sell_order UUIDs from engine IDs
       (query orders table by engine_order_id)
    b. INSERT into trades
    c. UPDATE orders.filled_quantity for both sides; set status='filled' or 'partial'
    d. Credit seller balance + ledger entry (reason='trade_proceeds')
    e. UPDATE positions for buyer (+yes_shares) and seller (-yes_shares)
       using PositionStore.GetOrCreate + AddShares
15. COMMIT
16. Return {order_id, status, fills: [...]}
```

**`DELETE /api/v1/orders/:id` (cancel order):**
1. Get order, verify it belongs to authed user
2. Call `engine.RemoveOrder(symbol, engine_order_id)`
3. If successful:
   - UPDATE order status = 'cancelled'
   - Refund reserved balance: `cost - (filled_quantity * price)`
   - INSERT ledger entry (reason='refund')
4. Return 200

**Key file: `internal/api/handlers/orders.go`** — thin, just parse/validate HTTP, call service, write response.

---

## Phase 6: Engine State Recovery

Create `internal/service/recovery.go`. Call this from `main.go` after DB connection, before `ListenAndServe`.

```go
func RebuildEngineState(ctx context.Context, db *sql.DB, engine *engine.Client) error {
    // 1. Get all open markets
    markets := marketStore.List(ctx, db)  // filter status = 'open'

    for _, m := range markets {
        engine.AddBook(ctx, m.Symbol)
    }

    // 2. Get all open/partial orders ordered by created_at ASC
    orders := orderStore.ListNonFinalByCreatedAt(ctx, db)

    for _, o := range orders {
        remaining := o.Quantity - o.FilledQuantity
        newEngineID, _, err := engine.AddOrder(ctx, market.Symbol, o.EnginePrice, remaining, o.EngineSide)
        // update engine_order_id in DB
        orderStore.UpdateEngineID(ctx, db, o.ID, newEngineID)
    }
}
```

Note: trades returned during replay should be discarded — you're just restoring state, not re-recording history.

---

## Phase 7: Redis + WebSocket

**Add to docker-compose.yml:**
```yaml
redis:
  image: redis:7-alpine
  ports:
    - "6379:6379"
```

**Add dependency:**
```bash
go get github.com/redis/go-redis/v9
```

**`internal/cache/redis.go`** — connect with `redis.NewClient`, expose `Set`, `Get`, `SetJSON`, `GetJSON` helpers.

**`internal/cache/orderbook_cache.go`** — key format: `market:{id}:best_bid`, etc.
```go
func SetBestBid(ctx, rdb, marketID, price)
func GetBestBid(ctx, rdb, marketID) (int32, error)
// same for best_ask, last_price
func SetDepth(ctx, rdb, marketID string, depth []DepthLevel)
```

**`internal/ws/hub.go`** — central goroutine pattern:
```go
type Hub struct {
    subscribe   chan subscription
    unsubscribe chan subscription
    broadcast   chan Message
    clients     map[string]map[*Client]bool  // channel -> clients
}

func (h *Hub) Run()  // goroutine: select on channels, fan out
```

**`internal/ws/client.go`** — per-connection:
- `readPump`: read subscribe/unsubscribe messages, send to hub
- `writePump`: drain send channel, write to WebSocket

**Wire into router:**
```go
r.Get("/ws", s.wsHandler)  // upgrades to WebSocket, creates Client, registers with Hub
```

**Post-trade async hook** — after Phase 5 step 15 COMMIT:
```go
go func() {
    bid, _ := engine.GetBestBid(ctx, symbol)
    ask, _ := engine.GetBestAsk(ctx, symbol)
    cache.SetBestBid(ctx, rdb, marketID, bid)
    cache.SetBestAsk(ctx, rdb, marketID, ask)
    hub.Broadcast(Message{Channel: "market:" + marketID, ...tradeData})
}()
```

---

## Suggested Order of Work

| # | Task | Prereqs |
|---|------|---------|
| 1 | Restructure `internal/` directories | — |
| 2 | Write + run migrations | docker-compose up |
| 3 | Models + Store layer | Migrations done |
| 4 | Fix proto + codegen gRPC client | C++ engine compiling |
| 5 | JWT middleware + user register/login | Store layer |
| 6 | Market CRUD endpoints | User auth + engine client |
| 7 | Account/balance endpoints | User auth + store |
| 8 | Order placement (Phase 5) | All of the above |
| 9 | Engine state recovery | Order store + engine client |
| 10 | Redis cache + WebSocket | Order placement working |

---

## Packages to Add

```bash
go get github.com/golang-jwt/jwt/v5
go get github.com/redis/go-redis/v9
go get github.com/google/uuid
go get golang.org/x/crypto
go get github.com/gorilla/websocket
go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

Note: `grpc` and `protobuf` are already in `go.mod` as indirect deps. Make them direct.
