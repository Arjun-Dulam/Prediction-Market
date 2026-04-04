# Prediction Market Exchange

A Polymarket-esque prediction market exchange.

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
cmake -S . -b build
```

```bash
cmake --build build
```
