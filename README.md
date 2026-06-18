# Prediction Market Exchange

A durable binary-outcome exchange with a Go REST/WebSocket backend, C++
price-time-priority matching engine over gRPC, PostgreSQL projections, Redis
quote caching, deterministic recovery, and a reproducible Docker stack.

See [go-backend/README.md](go-backend/README.md) for API examples, architecture,
deployment instructions, and measured performance.

## Systems audit and verified results

See [docs/systems-audit.md](docs/systems-audit.md) for the architecture audit,
WAL crash-recovery fixes, authenticated concurrency sweeps, two-minute soak,
raw results, reproduction commands and verified resume bullet options.

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
