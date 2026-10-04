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
periodically copies checkpoint state under the admission lock, writes it outside
that lock, and rotates two crash-safe WAL segments. It deterministically replays open
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
the `api_data` volume. `SNAPSHOT_INTERVAL` defaults to `30s`. `ORDER_BATCH_SIZE` defaults to `32`
(1–32): one worker drains already waiting orders with no batching timer, while
retaining durable reservations before matching and completions before success.
The queue holds at most 128 waiting orders; overload returns 503. See the
[performance report](../docs/performance-optimization.md) for recovery semantics
and reproducible comparisons.

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
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
make loadtest ORDERS=5000 LEVELS=1,8,32,64 REPEATS=3
# Or build once and preserve JSONL:
go build -o /tmp/pme-loadtest ./cmd/loadtest
/tmp/pme-loadtest -orders 5000 -levels 1,8,32,64 -repeats 3 > /tmp/load.jsonl
/tmp/pme-loadtest -duration 2m -concurrency 32 > /tmp/soak.jsonl
```

Start the Docker stack before load testing. The synthetic, closed-loop generator
registers two users, authenticates orders with real JWTs, funds accounts and creates
a market. It reports successful/attempted throughput, p50/p95/p99 through response
validation, error rates, duration and order counts at each concurrency level.
Every zero-error run checks exact cash/share accounting. Setup and verification are
outside the measured interval. WebSocket publication is included without subscribers.
Use `-environment` to describe the host and server configuration and `-label` to
identify the build. Exit status is nonzero for order or accounting failures.

See [the performance report](../docs/performance-optimization.md) for the
verified **807 → 2,678 authenticated orders/s** improvement at 64 clients,
p50/p95/p99, raw data and reproduction commands. The
[systems audit](../docs/systems-audit.md) preserves earlier results and recovery work.
Historical claims of 595 HTTP orders/sec and 3.73M in-process C++ orders/sec
should be reproduced before use; they are not guaranteed current results.

New WAL writes frame complete business event batches in a single versioned record.
Replay discards and truncates an incomplete trailing record and rejects complete
corrupt records. After a WAL/projection or ambiguous engine RPC error, readiness and new orders fail until
both API and engine are restarted. Preserve `orders.wal.previous` together with
`orders.wal` and the snapshot during backup/recovery; its prefix is deleted only
after a durable checkpoint. This prevents retries or snapshots from
compounding uncertain state. Keep PostgreSQL and API volumes together: snapshots
alone cannot recreate a lost PostgreSQL database after WAL truncation.

The checked-in [OpenAPI contract](openapi.yaml) documents the public surface,
and GitHub Actions runs Go race/static/integration checks, sanitizer-backed C++
tests, and production container builds.

This is an exchange systems project, not a real-money service. External payment
custody, administrator roles, outcome-oracle verification, and regulatory controls
are deliberately outside its scope; do not expose the demo funding endpoint publicly.
