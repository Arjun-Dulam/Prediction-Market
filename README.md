# Prediction Market Exchange

Binary prediction-market exchange with a Go API and a C++ price-time-priority
matching engine. Orders cross a gRPC boundary; the API owns authentication,
cash/share reservations, accounting, and recovery.

## Run locally

```bash
cd go-backend
cp .env.example .env
# Set JWT_SECRET in .env before starting.
docker compose up -d --build --wait
curl http://localhost:8080/health
```

The stack includes the API on port 8080, the engine on 50051, PostgreSQL, and
Redis. The deposit endpoint creates test funds. Market creators choose the
settlement outcome; there is no payment integration or external outcome oracle.

[Backend setup and API](go-backend/README.md) · [C++ engine](engine/README.md)

## Order path

1. Authenticate the request and validate its idempotency key.
2. Reserve cash or shares, fsync the WAL, and commit the PostgreSQL projection.
3. Submit an ordered batch to the C++ engine.
4. Persist fills and accounting changes.
5. Publish versioned quotes to Redis/WebSocket subscribers and acknowledge the order.

One Go ledger serializes admission across markets. Snapshots and WAL replay
rebuild accounts and open books. An uncertain engine or persistence result stops
new writes; recovery requires restarting the API and all engine owners together.

## Measurements

Local authenticated HTTP workload on an Apple M3, with 64 concurrent clients,
three 5,000-order trials per build, WAL fsync, and synchronous SQL accounting:

| Build | Orders/sec | p50 | p95 | p99 | Errors |
|---|---:|---:|---:|---:|---:|
| Before RPC batching | 2,879.4 | 21.44 ms | 37.00 ms | 44.87 ms | 0 |
| After RPC batching | 3,816.5 | 16.38 ms | 18.11 ms | 25.27 ms | 0 |

Values are medians across runs. Each run checks final balances and positions.
The two-engine, eight-market soak completed 322,448 orders in 120 seconds with
zero errors. A second engine did not improve throughput in this workload;
SQL projection remains the largest measured serialized stage.

The client is closed-loop and runs on the same host as Docker. The measurements
exclude setup and include no connected WebSocket subscribers.

- [RPC batching and market ownership](docs/local-market-partitions.md): latest
  measurements, raw results, configuration, and reproduction commands.
- [Persistence and checkpoints](docs/performance-optimization.md): group commit,
  PostgreSQL batching, and WAL rotation.
- [Recovery audit](docs/systems-audit.md): earlier regressions and crash tests.
- [Reading notes](docs/reading-notes.md): references and exercises.

## Tests and load client

```bash
cd go-backend
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o /tmp/pme-loadtest ./cmd/loadtest
/tmp/pme-loadtest -orders 5000 -levels 1,8,32,64 -repeats 3
```

Go integration tests require Docker. Run native C++ tests from the repository
root; the test target includes ASan and UBSan:

```bash
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --target OrderBookTests -j4
ctest --test-dir build/engine --output-on-failure
```
