# Prediction Market Exchange - Implementation Plan

## Context
Build a prediction market exchange (simplified Polymarket) around the existing C++ matching engine. The system lets users create binary outcome markets, trade YES/NO shares priced $0.01-$0.99, and stream prices in real time. Targeting FAANG backend/infra resume - technology choices prioritize what's recognizable and used at these companies.

The C++ OrderBook (single-threaded, single-symbol matching) is solid and well understood. The Exchange class (multi-symbol, thread-safe wrapper) was vibecoded and needs to be rewritten from scratch with full understanding. All C++ code lives directly in this repo under `engine/` (not a submodule).

**Resume sentence:** "I built a prediction market exchange with a C++ matching engine, Go API layer with gRPC service communication, PostgreSQL, Redis caching, and WebSocket streaming, deployed on AWS with Terraform."

---

## Architecture Overview

```
 [Trading UI]
      |
   [ALB] -- REST + WebSocket
      |
 [Go API Service]  -- gRPC --  [C++ Matching Engine Service]
      |       |
  [Postgres] [Redis]
```

- **C++ Engine (gRPC server):** The matching engine wrapped in a gRPC service. Receives order/cancel requests, returns trades. Runs as its own container.
- **Go API (gRPC client + HTTP server):** User-facing REST API + WebSocket. Handles auth, balance checks, market CRUD, order translation (YES/NO -> engine terms). Calls the C++ engine via gRPC.
- **Postgres:** Source of truth - users, markets, orders, trades, positions, balances.
- **Redis:** Hot cache - best bid/ask, depth, last trade price. WebSocket reads from here.
- **WebSocket:** Real-time price/trade streaming to connected clients.

### Key Design Decision: Single Orderbook Per Market
- Buying YES at 65c is economically identical to selling NO at 35c
- Each market has ONE orderbook trading "YES shares"
- The Go API translates user intent: `buy_no at 35c` -> `sell_yes at 65c` (100 - 35)
- C++ engine works unmodified - it just sees buy/sell orders

---

## Phase 1: C++ Engine - Copy, Clean, and Rewrite Exchange Layer
**Goal:** Bring the C++ code into this repo and rewrite the multi-symbol Exchange class from scratch.

### Step 1A: Copy the core code you wrote (keep as-is)
Copy from `/Users/adulam/Desktop/Orderbook/` into `engine/` in this repo:
- `include/order.hpp` - Order and Trade structs, Side enum
- `include/orderbook.hpp` - OrderBook class (single-symbol matching)
- `src/order.cpp` - side_to_string helper
- `src/orderbook.cpp` - matching logic, compaction, removal
- `tests/test.cpp` - OrderBook unit tests
- `benchmarks/orderbook_bench.cpp` - single-book benchmarks
- `benchmarks/order_generator.hpp` / `order_generator.cpp` - test helpers
- `CMakeLists.txt` - build config (will be modified)

### Step 1B: Delete the vibecoded Exchange code
Remove from the copied files:
- `include/exchange.hpp`
- `src/exchange.cpp`
- `tests/exchange_test.cpp`
- `benchmarks/exchange_bench.cpp`
- Remove `EXCHANGE_SOURCES` and exchange targets from `CMakeLists.txt`

### Step 1C: Rewrite Exchange from scratch (the learning part)
Write a new `Exchange` class that manages multiple OrderBooks with thread safety.

**Concepts you'll learn by building this yourself:**
1. **`std::shared_mutex`** - a reader-writer lock. Multiple threads can read simultaneously (shared lock), but writes require exclusive access. Why this matters: looking up which OrderBook to use is a read; adding a new symbol is a write. Reads are far more frequent, so shared_mutex avoids unnecessary serialization.
2. **`std::shared_lock` vs `std::unique_lock`** - shared_lock acquires the mutex in shared/read mode; unique_lock acquires it exclusively. RAII ensures the lock is released when the scope exits, even on exceptions.
3. **Per-book locking** - the Exchange mutex protects the `map<string, OrderBook>`. Each OrderBook has its own internal mutex. This means orders on different symbols run in parallel; orders on the same symbol are serialized. This is the correct granularity.
4. **Safe map access** - never use `map[key]` for lookup (it silently inserts a default if missing). Use `.find()` or `.at()`. The vibecoded version had this bug.
5. **Query methods** - add `get_best_bid()`, `get_best_ask()`, `get_depth()` to OrderBook. The Exchange delegates to the right book.

**Files to create:**
- `engine/include/exchange.hpp` - Exchange class declaration
- `engine/src/exchange.cpp` - Exchange implementation
- `engine/tests/exchange_test.cpp` - thread safety tests you write
- `engine/benchmarks/exchange_bench.cpp` - multi-threaded benchmarks

### Step 1D: Add OrderBook query methods
The engine currently has no way to inspect book state. Add:
- `OrderBook::get_best_bid() -> int32_t` - highest bid price, or -1 if empty
- `OrderBook::get_best_ask() -> int32_t` - lowest ask price, or -1 if empty
- `OrderBook::get_depth(int levels) -> vector<pair<int32_t, uint32_t>>` - top N price levels with aggregate qty (for the UI order book display)

---

## Phase 2: C++ gRPC Service
**Goal:** Wrap the Exchange in a gRPC server so Go can call it over the network.

### New files in `engine/`:
- `proto/exchange.proto` - gRPC service definition
- `src/grpc_server.hpp` / `src/grpc_server.cpp` - gRPC service implementation wrapping Exchange
- `src/main_server.cpp` - entry point, starts gRPC server on a port
- `Dockerfile` - builds the C++ gRPC server

### Proto definition (key RPCs):
```protobuf
service MatchingEngine {
  rpc AddSymbol(AddSymbolRequest) returns (AddSymbolResponse);
  rpc AddOrder(AddOrderRequest) returns (AddOrderResponse);  // returns trades + assigned order_id
  rpc RemoveOrder(RemoveOrderRequest) returns (RemoveOrderResponse);
  rpc GetBookState(BookStateRequest) returns (BookStateResponse);  // best bid/ask, depth
}
```

### Build changes:
- Add gRPC/protobuf to CMakeLists.txt via FetchContent or system install
- Add server executable target

### What you'll learn:
- Protocol Buffers and gRPC (used at every FAANG company)
- Wrapping an existing library as a network service
- C++ build system management with CMake

---

## Phase 3: Go Project Scaffold + Database
**Goal:** Set up the Go project with Postgres, database schema, and migration tooling.

### Project structure:
```
go.mod
Makefile
docker-compose.yml          # Postgres + Redis + C++ engine

engine/                     # C++ matching engine (copied into this repo)
  include/                  # order.hpp, orderbook.hpp, exchange.hpp
  src/                      # order.cpp, orderbook.cpp, exchange.cpp, grpc_server.cpp
  proto/                    # exchange.proto
  tests/                    # GTest tests
  benchmarks/               # Google Benchmark
  CMakeLists.txt
  Dockerfile

cmd/server/main.go          # Go API entry point

internal/
  models/                   # Data types: Market, Order, Trade, User, Position
  store/                    # Postgres operations per entity
  engine/                   # gRPC client to C++ engine

migrations/                 # SQL migration files (golang-migrate)
  001_create_users.up.sql
  002_create_markets.up.sql
  003_create_orders.up.sql
  004_create_trades.up.sql
  005_create_positions.up.sql
  006_create_balances.up.sql
```

### Database schema (key tables):
- **users** - id (UUID), email, username, password_hash
- **current_balances** - user_id (PK), balance (BIGINT cents), CHECK >= 0
- **balance_ledger** - append-only audit log of every balance change
- **markets** - id, title, symbol, status (open/halted/resolved_yes/resolved_no), close_at
- **orders** - id, user_id, market_id, engine_order_id, side (buy_yes/buy_no/sell_yes/sell_no), engine_side (buy/sell), price, quantity, filled_quantity, status
- **trades** - id, market_id, buy_order_id, sell_order_id, buyer_id, seller_id, price, quantity
- **positions** - user_id + market_id (unique), yes_shares, no_shares

### What you'll learn:
- Go project layout conventions
- Database schema design for financial systems (ledger pattern)
- Migration tooling (golang-migrate)
- docker-compose for local development

---

## Phase 4: Core API - Users + Markets
**Goal:** REST endpoints for user registration/auth and market CRUD.

### Endpoints:
```
POST   /api/v1/users/register
POST   /api/v1/users/login        -> returns JWT
GET    /api/v1/markets             -> list markets
POST   /api/v1/markets             -> create market (admin)
GET    /api/v1/markets/:id         -> market detail
POST   /api/v1/deposit             -> add funds
GET    /api/v1/balance             -> check balance
```

### Key files:
- `internal/api/router.go` - chi router setup
- `internal/api/middleware.go` - JWT auth middleware
- `internal/api/handlers/users.go`
- `internal/api/handlers/markets.go`
- `internal/api/handlers/accounts.go`

### What you'll learn:
- chi router (idiomatic Go HTTP)
- JWT authentication
- Middleware patterns
- RESTful API design

---

## Phase 5: Order Placement - The Critical Path
**Goal:** The full order flow - validate, translate, call engine, persist, respond.

### Order flow:
```
1. POST /api/v1/orders { market_id, side: "buy_yes", price: 65, quantity: 10 }
2. Validate: auth, market open, price in [1,99], quantity > 0
3. Translate: "buy_no at 35" -> engine sell at 65 (100 - price)
4. Balance check: BEGIN TX, SELECT FOR UPDATE on current_balances
5. Reserve funds: debit balance by (price x quantity)
6. Call C++ engine via gRPC: AddOrder(symbol, price, qty, side)
7. Engine returns: [trades], assigned_order_id
8. Persist: INSERT order, INSERT trades, UPDATE filled quantities,
   UPDATE positions (buyer gets shares, seller loses shares),
   UPDATE balances (seller gets credited)
9. COMMIT TX
10. Return: { order_id, status, fills: [...] }
```

### Key files:
- `internal/api/handlers/orders.go`
- `internal/service/trading.go` - core business logic
- `internal/engine/client.go` - gRPC client wrapper
- `internal/store/order_store.go`, `trade_store.go`, `position_store.go`

### Critical concern: balance race conditions
Two concurrent orders from the same user could both pass the balance check. Solution: `SELECT ... FOR UPDATE` on `current_balances` row serializes per-user.

### What you'll learn:
- Transactional consistency in financial systems
- gRPC client usage in Go
- Service layer pattern (handlers are thin, business logic in services)

---

## Phase 6: Engine State Recovery
**Goal:** On Go service restart, rebuild C++ engine state from Postgres.

### Startup sequence:
1. Query all markets with status = 'open'
2. For each: call `AddSymbol` via gRPC
3. Query all orders with status IN ('open', 'partial'), ordered by created_at
4. Replay each with remaining unfilled quantity via `AddOrder`
5. Update `engine_order_id` mappings (engine assigns new IDs on replay)

### What you'll learn:
- State recovery patterns (event sourcing lite)
- System initialization and startup ordering

---

## Phase 7: Redis + WebSocket
**Goal:** Real-time price streaming.

### Redis cache keys:
```
market:{id}:best_bid    -> "65"
market:{id}:best_ask    -> "68"
market:{id}:last_price  -> "66"
market:{id}:depth       -> JSON of top 10 levels
```

### WebSocket:
- `ws://host/ws?token=JWT`
- Client subscribes: `{ "action": "subscribe", "channel": "market:abc123" }`
- Server pushes: trade events, book updates, price changes
- Hub pattern: central goroutine manages connections + subscriptions, fans out messages

### Key files:
- `internal/cache/redis.go` - connection + helpers
- `internal/cache/orderbook_cache.go` - update/read book state
- `internal/ws/hub.go` - WebSocket connection manager
- `internal/ws/client.go` - per-connection handler

### Post-trade hook (added to Phase 5 flow):
After step 9 (COMMIT), async:
- Update Redis with new best bid/ask from engine (call `GetBookState` via gRPC)
- Publish trade event to WebSocket hub

### What you'll learn:
- Redis as a caching layer
- WebSocket server in Go (gorilla/websocket)
- Pub/sub fan-out pattern
- Async processing after transaction commit

---

## Phase 8: Market Resolution
**Goal:** Resolve markets, settle positions, pay out winners.

### Resolution flow:
1. `POST /api/v1/markets/:id/resolve { outcome: "yes" }`
2. Halt market: UPDATE status = 'halted'
3. Cancel all open orders: query Postgres, call RemoveOrder for each, refund reserved balances
4. Settle: YES shares pay 100c each, NO shares pay 0 (or vice versa)
5. Credit winners' balances
6. UPDATE status = 'resolved_yes'

---

## Phase 9: Docker + docker-compose
**Goal:** Full local development stack.

### Containers:
- `engine` - C++ gRPC server
- `api` - Go API server
- `postgres` - PostgreSQL 16
- `redis` - Redis 7

### Dockerfiles:
- C++ engine: Ubuntu base, install grpc + protobuf + cmake, build server
- Go API: golang:1.22, install protoc + Go gRPC plugins, build binary

---

## Phase 10: Terraform + AWS
**Goal:** Deploy to AWS, all infrastructure as code.

### Resources:
- **ECS Fargate** - two services (engine + api)
- **RDS Postgres** - db.t3.micro for demo
- **ElastiCache Redis** - cache.t3.micro
- **ALB** - routes HTTP/WebSocket to API service
- **VPC** - private subnets for RDS/Redis, public for ALB

### File structure:
```
deploy/terraform/
  main.tf, variables.tf, outputs.tf
  modules/vpc/, modules/ecs/, modules/rds/, modules/elasticache/, modules/alb/
```

---

## Phase 11: GitHub Actions CI/CD
**Goal:** Automated lint, test, build, deploy pipeline.

### Workflows:
- **ci.yml** (on push): lint Go (golangci-lint), run Go tests, build C++ tests, build Docker images
- **deploy.yml** (on merge to main): push images to ECR, update ECS services

---

## Phase 12: Trading UI
**Goal:** Simple demo frontend (vibecoded).

- Market browser with live prices
- Order form (buy YES / buy NO, price slider, quantity)
- Order book depth visualization
- Position/portfolio view
- WebSocket-powered real-time updates

---

## Verification Plan
1. **Unit tests:** Go service tests with mocked gRPC engine client
2. **Integration tests:** docker-compose up, run test suite against real Postgres/Redis/Engine
3. **Manual smoke test:** Create market -> deposit funds -> place buy YES -> place sell YES -> verify trade -> check positions -> resolve market -> verify payout
4. **Load test (optional):** Send 1000 orders/sec, verify matching correctness and WebSocket latency

---

## Technology Summary (FAANG-aligned)
| Component | Technology | Used at |
|-----------|-----------|---------|
| Matching engine | C++ | HFT, exchanges |
| API layer | Go + chi | Google, Uber, Cloudflare |
| Service communication | gRPC + Protobuf | Google, Meta, every FAANG |
| Database | PostgreSQL | Industry standard |
| Cache | Redis | Industry standard |
| Real-time | WebSocket | Industry standard |
| Containers | Docker | Industry standard |
| IaC | Terraform | Industry standard |
| CI/CD | GitHub Actions | Industry standard |
| Cloud | AWS (ECS, RDS, ElastiCache) | Industry standard |
