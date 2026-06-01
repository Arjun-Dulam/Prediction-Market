# Prediction Market Exchange

A runnable binary-outcome exchange composed of an authenticated Go REST/WebSocket service, a
C++ price-time-priority matching engine over gRPC, PostgreSQL materialized
state, Redis quote caching, and a durable local write-ahead log.

## Architecture

```text
HTTP / WebSocket clients
          |
      Go API (Chi) ---- Redis quote cache
       |      |
       |      +-------- PostgreSQL projections
       |
       +-- gRPC --> C++ matching engine
       |
      +---------- fsynced WAL + periodic snapshots
```

Users register/login with bcrypt-hashed passwords and receive HMAC-SHA256 JWTs.
Protected handlers derive account ownership from the token rather than trusting
client-provided user IDs, and only a market's creator can settle it. The API also
exposes dependency-aware readiness and Prometheus-compatible service counters.

Each market has one shared YES book. The API translates `buy_no @ 40` into an
engine-side sell at `60`, so complementary YES/NO bids match without duplicating
liquidity. Cash and share inventory are reserved before submission. Fills use
the actual execution price, including price-improvement refunds, and settlement
pays the winning outcome at 100 cents per share. Cancellation and settlement
events are committed in atomic WAL/database batches to avoid per-position fsyncs.

Accepted state changes are appended and fsynced before acknowledgment. The API
periodically snapshots materialized state and deterministically replays open
markets/orders into a restarted engine. PostgreSQL projection batches use unique
event IDs, making WAL replay idempotent even when local sequence numbers restart.

## Run the complete stack

Create `go-backend/.env` with the database variables shown below, then start all
four containers:

```dotenv
BLUEPRINT_DB_HOST=localhost
BLUEPRINT_DB_PORT=5432
BLUEPRINT_DB_DATABASE=exchange
BLUEPRINT_DB_USERNAME=exchange
BLUEPRINT_DB_PASSWORD=exchange
BLUEPRINT_DB_SCHEMA=public
JWT_SECRET=replace-with-at-least-16-random-characters
```

```bash
docker compose up -d --build
curl http://localhost:8080/health
```

Docker Compose waits for PostgreSQL and Redis health checks, starts the C++
engine on port 50051, and exposes the Go API on port 8080. Durable files live in
the `api_data` volume. `SNAPSHOT_INTERVAL` defaults to `30s`.

## Trading API

- `POST /api/v1/users/register` and `POST /api/v1/users/login`
- `POST /api/v1/markets` with `{"id":"election","symbol":"ELECTION"}`
- `GET /api/v1/markets` and `GET /api/v1/markets/{id}`
- `POST /api/v1/balances/deposit` with `{"user_id":"alice","cents":10000}`
- `GET /api/v1/balances/{userID}`
- `POST /api/v1/trading/orders` with an optional client-supplied idempotency ID
- `GET /api/v1/trading/orders/{id}`
- `DELETE /api/v1/trading/orders/{id}?user_id={userID}`
- `GET /api/v1/positions/{userID}/{marketID}`
- `GET /api/v1/markets/{symbol}/quote`
- `POST /api/v1/markets/{id}/resolve` with `{"outcome":"yes"}` or `{"outcome":"no"}`
- `GET /ws?channel=market:{id}` for bounded, non-blocking quote/order fan-out
- `GET /metrics` for Prometheus-format request, error, order, latency, and snapshot metrics

Order sides are `buy_yes`, `buy_no`, `sell_yes`, and `sell_no`; prices are whole
cents from 1 through 99. Reusing an order ID with identical parameters is an
idempotent retry. Reusing it with different parameters is rejected.

Example:

```json
{
  "id": "alice-order-1",
  "user_id": "alice",
  "market_id": "election",
  "side": "buy_yes",
  "price": 60,
  "quantity": 10
}
```

## Verification and performance

```bash
go test ./...
make loadtest ORDERS=5000 CONCURRENCY=32
```

The committed full-stack load generator registers and authenticates two users,
creates a market, funds both accounts, and submits complementary orders through
HTTP, JWT middleware, WAL/fsync, PostgreSQL, gRPC, C++, Redis, and WebSocket
publication. On an Apple Silicon development machine via Docker Desktop, the
three-run median for 5,000 orders at concurrency 32 was **595 orders/sec** with
**53 ms p50, 58 ms p95, and 68 ms p99**, with zero failed requests. Treat these
as local reference results and rerun them on the target host before quoting them.

The C++ benchmark uses a deterministic 15-million-order generated stream:

```bash
cmake -S .. -B ../build -DCMAKE_BUILD_TYPE=Release
cmake --build ../build --target OrderBookBenchmark
../build/engine/OrderBookBenchmark --benchmark_filter='BM_Matching(Performance|Latency)/0$'
```

The latest three-run median measured **3.73M orders/sec**, **125 ns p50**, and
**1.29 us p99** for in-process matching. Returning per-order fills only allocates
when a caller asks for them, and new books reserve a bounded initial lookup table
rather than allocating capacity for 15 million orders per market.

The checked-in [OpenAPI contract](openapi.yaml) documents the public surface,
and GitHub Actions runs Go race/static/integration checks, sanitizer-backed C++
tests, and production container builds.

This is an exchange systems project, not a real-money service. External payment
custody, administrator roles, outcome-oracle verification, and regulatory controls
are deliberately outside its scope; do not expose the demo funding endpoint publicly.
