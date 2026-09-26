# Prediction Market Exchange

A durable binary-outcome exchange with a Go REST/WebSocket backend, C++
price-time-priority matching engine over gRPC, PostgreSQL projections, Redis
quote caching, deterministic recovery, and a reproducible Docker stack.

See [go-backend/README.md](go-backend/README.md) for API examples, architecture,
deployment instructions, and measured performance.

## Systems audit and verified performance

See [the performance report](docs/performance-optimization.md) for the measured
807 → 2,678 authenticated orders/s improvement at 64 clients (3.3×), with fsync
and synchronous PostgreSQL accounting retained. It includes repeated concurrency
sweeps, raw results, setup, limitations and verified resume bullets.

See [docs/systems-audit.md](docs/systems-audit.md) for the architecture audit,
WAL crash-recovery fixes, authenticated concurrency sweeps, two-minute soak,
raw results, reproduction commands and verified resume bullet options.

The [local market-owner experiment](docs/local-market-partitions.md) adds batched
engine RPCs, versioned quotes and an optional two-engine Docker deployment.
The [interview study guide](docs/interview-study-guide.md) connects the code to
books, papers and hands-on exercises.

The load test checks accounting as well as HTTP success. Performance numbers
are synthetic local measurements with hardware and variability recorded.

## How to Compile?

From the repository root:

```bash
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
```

```bash
cmake --build build
```

Or start the complete product:

```bash
cd go-backend
docker compose up -d --build
```
