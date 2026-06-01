# Prediction Market Exchange

A durable binary-outcome exchange with a Go REST/WebSocket backend, C++
price-time-priority matching engine over gRPC, PostgreSQL projections, Redis
quote caching, deterministic recovery, and a reproducible Docker stack.

See [go-backend/README.md](go-backend/README.md) for API examples, architecture,
deployment instructions, and measured performance.

## Directory Structure

```text
Prediction-Market-Exchange/
├── CMakeLists.txt
├── README.md
├── CLAUDE.md
├── build
├── proto/
│   └── exchange.proto
├── docs/
│   ├── dev-log.md
│   ├── engine-notes.md
│   └── plan.md
└── engine/
    ├── CMakeLists.txt
    ├── README.md
    ├── include/
    │   ├── exchange.hpp
    │   ├── order.hpp
    │   ├── orderbook.hpp
    │   └── thread_queue.hpp
    ├── src/
    │   ├── exchange.cpp
    │   ├── grpc_server.cpp
    │   ├── main_server.cpp
    │   ├── order.cpp
    │   └── orderbook.cpp
    ├── benchmarks/
    │   ├── order_generator.cpp
    │   ├── order_generator.hpp
    │   ├── orderbook_bench.cpp
    │   └── scripts/
    │       ├── analyze_compaction_study.py
    │       └── compaction_ratios.sh
    └── tests/
        └── test.cpp
```

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
