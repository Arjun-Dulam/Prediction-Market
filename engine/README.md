# C++ matching engine

The engine matches orders by price, then insertion order within a price level.
Each prediction market uses one book; the Go API translates complementary YES/NO
orders into engine-side buys and sells.

## Book storage and locking

- `std::map<price, std::vector<Order>>` holds each side of the book. Matching
  walks highest bids or lowest asks first, then each level in FIFO order.
- `std::unordered_map<order_id, OrderLocation>` indexes cancellation by side,
  price, and vector position. Lookup is average O(1).
- Filled and cancelled orders are marked for deletion. Compaction removes them,
  repairs lookup positions, and drops empty price levels.
- A mutex protects each book. The exchange's shared mutex protects symbol
  lookup; batch submissions take its exclusive lock to prevent interleaving.

`AddOrders` accepts 1–32 commands. It validates the group before mutation and
returns per-order fills plus a final quote for each affected symbol. `GetQuote`
reads bid and ask from the same book state.

The engine holds books in memory. Recovery is coordinated by the Go service,
which rebuilds open orders from its WAL and snapshot. Restart all engine owners
with the API after an uncertain RPC result.

## Build and test

Requires a C++20 compiler, CMake 3.21+, gRPC, Protobuf, OpenSSL, and Abseil.
CMake fetches pinned Google Test and Google Benchmark versions. The
[Dockerfile](Dockerfile) lists Ubuntu build dependencies.

From the repository root:

```bash
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j4
ctest --test-dir build/engine --output-on-failure
```

The test target uses ASan and UBSan. Tests cover price-time priority, partial
fills, cancellation, compaction, multiple books, and batch validation.

## Benchmarks

```bash
build/engine/OrderBookBenchmark
```

The suite measures insertion, cancellation at different depths, and matching
throughput/latency. These run inside the C++ process and exclude HTTP, auth,
gRPC transport, WAL fsync, SQL, and quote publication. See the
[benchmark records](../docs/systems-audit.md) for measured results and hardware.

## Files

- `include/`, `src/`: book storage, exchange wrapper, and gRPC server.
- `tests/test.cpp`: Google Test cases.
- `benchmarks/`: Google Benchmark workloads and analysis scripts.
- [`../proto/exchange.proto`](../proto/exchange.proto): RPC definitions.

Contact: [adulam3@gatech.edu](mailto:adulam3@gatech.edu)
