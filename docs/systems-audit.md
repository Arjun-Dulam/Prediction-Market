# Backend and recovery audit — October 3, 2026

**Follow-up:** [Durable order-path optimization](performance-optimization.md)
records the later measured performance improvements. The tables below preserve
the earlier audit results.

This is an undergraduate exchange systems project. The improvements emphasize
observable correctness and honest measurement; they do not establish production
readiness or real-world capacity.

## Architecture and audit scope

The Chi HTTP API authenticates JWTs and derives account ownership from claims.
Registration/login use bcrypt and PostgreSQL. One Go trading service owns markets,
orders, available cash and YES/NO inventory. A global mutex serializes admission,
reservation, matching and accounting across **all** markets. Order mutations write
and fsync a local WAL, synchronously project events into PostgreSQL, then update
memory. Matching is a gRPC call to C++; a mutex protects each order book and
a shared mutex protects symbol routing. The engine stores sorted price levels and FIFO order vectors with cancellation lookup
and lazy compaction. Complementary YES/NO prices map into one YES book.

The API refreshes quotes with two more gRPC calls, writes Redis, publishes bounded
WebSocket hub messages and responds. Snapshots checkpoint Go state every 30 seconds;
startup loads the snapshot/WAL and rebuilds active engine orders by admission
sequence. PostgreSQL projections deduplicate by unique event ID.

This favors a comprehensible serialized state machine. Several clients can overlap
HTTP and quote I/O, but the trading critical section contains two WAL commits,
PostgreSQL transactions and matching. Concurrency sweeps show the latency cost of
queueing; they do not isolate which of disk, SQL or gRPC dominates. The C++
in-process benchmark excludes all these costs.

The existing implementation was uncommitted at audit start. Local commit
`c7f5e66` preserves it as the baseline. `d46a4f1` contains the measured improvements.
No code was pushed. Results and reproduction tools are checked in separately.

## Two focused improvements

1. **Credible authenticated load validation.** The Go client now runs repeated
   concurrency sweeps or a duration-based soak, writes JSONL, validates order
   acknowledgements, reports successful/attempted throughput, nearest-rank
   p50/p95/p99, error rates/statuses and ten-second windows. It checks the two users'
   exact balances and YES/NO positions after every zero-error run. Runtime metrics
   provide heap/goroutine observations during a soak. No tokens are written to
   result files. Unit tests cover failure accounting, percentiles and windows.
2. **Recover complete business batches safely.** New WAL records are versioned
   JSON envelopes containing a complete event batch, terminated by one newline.
   Recovery discards and durably truncates an unterminated tail before accepting
   more writes, even if the tail happens to be valid JSON. Complete malformed
   records cause startup to fail. PostgreSQL replays each new batch in one
   transaction. Legacy single-event lines remain readable. Persistence/projection
   errors latch an unavailable state: further writes and snapshots fail, readiness
   returns 503 and order placement returns 503. Restart the API **and** engine to
   recover. Snapshot writers are mutually exclusive. Rebuilding the engine clears
   stale active ID mappings and preserves partial-fill status. Expired requests
   are rejected after admission-lock acquisition, before writing the WAL.

The change retains fsync and synchronous projection. It adds no services, queues,
distributed locks or asynchronous durability. WAL batch records have a 64 MiB size
limit; an oversized transaction fails closed.

## Correctness evidence

`results/baseline-recovery-regressions.txt` records the new tests run against
`c7f5e66`. The baseline failed torn-tail append/restart, valid-JSON-without-newline,
transaction framing, checkpoint-after-projection-failure, stale engine-ID mapping
and expired-request tests. These tests pass on the improved implementation.

Additional tests kill a helper process after acknowledged orders, with and without
a snapshot, then compare order/account state on two successive replays. They also
submit 64 simultaneous identical retries before and after a checkpoint, reject a
conflicting retry, verify whole-batch projection on replay and preserve engine
admission order across snapshot + WAL recovery. Concurrent checkpoint writers
and deposits also recover the exact final balance. Existing C++ FIFO/better-price,
partial-fill, compaction and cancellation tests remain in place.

A separate Docker crash check performs 32 concurrent authenticated retries, sends
SIGKILL to both real services, compares recovered API state, verifies better-price/FIFO fills, cancels a
recovered resting order and independently reads PostgreSQL balances/positions.

Final verification commands:

```bash
cd go-backend
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
cd ..
cmake -S . -B build-audit -DCMAKE_BUILD_TYPE=Release
cmake --build build-audit --target OrderBookTests OrderBookBenchmark -j4
ctest --test-dir build-audit/engine --output-on-failure
```

Go integration tests require Docker; they fail when Docker is unavailable rather
than silently skipping. C++ tests compile with ASan/UBSan even in the Release
build. On this host a stale Homebrew cache prevented the original build directory from configuring; the fresh
`build-audit` directory worked. CMake needs a C++20 compiler, OpenSSL, gRPC,
Protobuf and abseil; Google Test/Benchmark are fetched at pinned tags. See the
existing CI workflow for Ubuntu packages.

## Reproduce authenticated measurements

Use a **disposable** stack. The following environment describes local test data.
Use the project name consistently; `down -v` deletes only that project's test
volumes and must not be used against data you wish to keep.

```bash
export BLUEPRINT_DB_DATABASE=exchange BLUEPRINT_DB_USERNAME=exchange
export BLUEPRINT_DB_PASSWORD=audit-local BLUEPRINT_DB_PORT=15432
export JWT_SECRET=audit-local-secret-for-testing
cd go-backend
docker compose -p pme-audit up -d --build --wait
go build -o /tmp/pme-loadtest ./cmd/loadtest
/tmp/pme-loadtest -label improved -orders 5000 -levels 1,8,32,64 -repeats 3 \
  -environment 'describe CPU/RAM, Docker resources, Go/C++ build and durability' \
  > /tmp/improved-load.jsonl
/tmp/pme-loadtest -label improved-soak -duration 2m -concurrency 32 \
  -environment 'same configuration; retained state from sweep' > /tmp/soak.jsonl
cd ..
python3 scripts/summarize-load.py /tmp/improved-load.jsonl /tmp/soak.jsonl
python3 scripts/check-stack-recovery.py --project pme-audit
```

The crash check assumes database/user `exchange`, and deliberately SIGKILLs the
specified Compose project's API and engine. Run it only on the disposable stack.
Registration, funding, market creation and final accounting checks are outside the
load interval. No order warmup is performed. Each run places 5,000 distinct,
alternating `buy_yes`/`buy_no` orders at 50 cents for one share in a new market.
Registration returns a real JWT used for every order. Quotes, Redis writes and
WebSocket hub publication are included; these tests have **no connected WebSocket
subscribers**. They do not benchmark delivered fan-out or login throughput.

The client is closed-loop: each worker submits its next order only when its prior
response finishes. Latency includes HTTP I/O, body consumption and acknowledgement
validation, starting after payload construction. Percentiles include failed
attempts. Successful throughput excludes errors; attempted throughput counts all
attempts. Duration mode submits complete YES/NO pairs until the target interval
expires, then drains workers. Its count/duration may slightly exceed the target.
Zero HTTP errors with incorrect accounting still produces a failing exit code.
Ten-second windows count requests by completion time. Metrics polling every ten
seconds occurs only in duration mode. Samples and
latencies are retained in the client, so very long/high-volume runs consume client
memory. Use this for minute-scale experiments, not unbounded stress testing.

To reproduce the baseline with the **same final client**, build the client once,
export the baseline source, and build its API image separately. Stop the measured
stack before replacing its services; reset only disposable test volumes between
sweeps. Keep service configuration, client counts and repeat ordering identical.

```bash
mkdir -p /tmp/pme-baseline
git archive c7f5e66 | tar -x -C /tmp/pme-baseline
docker build -t pme-audit-api:baseline /tmp/pme-baseline/go-backend
# Write an override in /tmp; run from go-backend with the environment above.
cat > /tmp/pme-baseline-compose.yml <<'YAML'
services:
  api:
    image: pme-audit-api:baseline
YAML
cd go-backend
docker compose -p pme-audit down -v
docker compose -p pme-audit -f docker-compose.yml \
  -f /tmp/pme-baseline-compose.yml up -d --no-build --wait
/tmp/pme-loadtest -label baseline-c7f5e66 -orders 5000 -levels 1,8,32,64 \
  -repeats 3 -environment 'same hardware/configuration' > /tmp/baseline-load.jsonl
```

The C++ engine source is unchanged by this audit, so the final engine image is
also valid for the baseline API comparison. To restore the improved API, omit the
override and use `up -d --build --wait` after resetting disposable volumes. Tests
must finish before measurements; avoid simultaneous builds or other load tests.
Image tags/package repositories are mutable: `results/environment.txt` and
`results/final-images.txt` record this experiment's hardware, configuration and
image identifiers. Reproduce the methodology, not an assumed exact score.

## Measured results

| Build | Clients | Runs | Orders | Median orders/s (range) | p50 ms | p95 ms | p99 ms | Error rate | Median seconds | Verified |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| baseline-c7f5e66 | 1 | 3 | 15000 | 635.2 (603.2–641.4) | 1.63 | 1.95 | 2.50 | 0.000% | 7.87 | 3/3 |
| baseline-c7f5e66 | 8 | 3 | 15000 | 778.0 (744.0–794.3) | 9.88 | 12.01 | 16.41 | 0.000% | 6.43 | 3/3 |
| baseline-c7f5e66 | 32 | 3 | 15000 | 794.2 (786.4–796.3) | 39.44 | 44.98 | 50.86 | 0.000% | 6.30 | 3/3 |
| baseline-c7f5e66 | 64 | 3 | 15000 | 774.2 (698.5–791.3) | 80.32 | 91.68 | 125.24 | 0.000% | 6.46 | 3/3 |
| improved-d46a4f1 | 1 | 3 | 15000 | 604.1 (143.6–605.6) | 1.66 | 2.13 | 3.20 | 0.000% | 8.28 | 3/3 |
| improved-d46a4f1 | 8 | 3 | 15000 | 725.6 (210.2–802.3) | 10.76 | 13.32 | 17.47 | 0.000% | 6.89 | 3/3 |
| improved-d46a4f1 | 32 | 3 | 15000 | 652.3 (294.8–806.2) | 48.05 | 60.32 | 69.23 | 0.000% | 7.67 | 3/3 |
| improved-d46a4f1 | 64 | 3 | 15000 | 465.1 (358.6–770.7) | 139.77 | 174.14 | 206.92 | 0.000% | 10.75 | 3/3 |

Both final comparison sweeps completed 60,000 orders with zero failures and 12/12
accounting checks. The baseline's 32-client median was **794.2 orders/s** versus
**652.3** for the improved sweep; the improved sweep also had much wider ranges.
This does **not** establish a performance gain. These sequential desktop runs
cannot distinguish a code-induced regression from host/I/O variation. The
correctness fixes and reduced microbenchmark allocations are the demonstrated
improvements. Original historical scores are not substituted for the baseline.

A focused final confirmation used fresh volumes and only concurrency 32 for
three additional 5,000-order runs: **552.2 orders/s median** (532.7–592.0),
**51.30 ms p50 / 87.70 ms p95 / 140.05 ms p99**, zero errors and 3/3 accounting
checks. This is a separate experiment with only 15,000 retained orders, not a
replacement for the full sweep. It reinforces that a performance improvement has
**not** been demonstrated; the observed lower throughput relative to baseline
remains unresolved and should be profiled before further performance work.
See `results/final-confirmation.jsonl`.

These are synthetic development-machine results. Each table row reports medians
of **three per-run values**; percentile medians are not pooled percentiles. Error
rates pool the row's attempts. Ranges expose run variability. Each sweep starts
with empty test volumes but retains state between rounds, so snapshots and
PostgreSQL/order-map growth are included. The initial baseline sweep and original
microbenchmarks overlapped some audit work; retained exploratory files are labeled
and excluded from the final comparison.

The **120.08-second soak** followed the final 60,000-order sweep without a data
reset. It accepted **58,832 additional orders**, **489.9 orders/s**, **62.86 ms p50,
85.19 ms p95, 136.43 ms p99**, zero errors and exact cash/share accounting.
Full ten-second windows ranged from **420.3 to 615.4 orders/s**, with p99 from
**81.05 to 280.71 ms**. The first/last full windows were 615.4/539.9 orders/s;
there was variability rather than evidence of a steady trend.

Active goroutine samples stayed at **77–79**. Heap allocation samples ranged from
**38.1 to 217.2 MiB**, falling with GC; heap objects grew as history accumulated.
Before/after heap was 97.7/51.2 MiB. The final 46 goroutines were sampled before
closing the client's idle connections, so they are not an idle leak count.
This run neither proves bounded memory nor rules out leaks. The raw samples and
windows are in `results/final-soak.jsonl`. The real-stack SIGKILL + replay/FIFO/SQL
check passed afterward (`results/full-stack-recovery.json`).

Historical claims of 595 authenticated orders/s, 53 ms p50 / 68 ms p99 and 3.73M
in-process orders/s are reference claims. Use the verified results here, with
hardware/workload qualifiers. The C++ benchmark uses a generated/shuffled stream
and reports CPU-time throughput; its timer-instrumented latency test is a separate
experiment. It does not represent end-to-end exchange capacity. Run it with:

```bash
build-audit/engine/OrderBookBenchmark \
  --benchmark_filter='BM_Matching(Performance|Latency)/0$' \
  --benchmark_min_time=1s --benchmark_repetitions=3 \
  --benchmark_out=/tmp/cpp.json --benchmark_out_format=json
```

The unchanged standalone C++ code produced **3.84M orders/s median**, ranging
**2.67M–5.14M**, over three repetitions. The separate latency benchmark's medians
were **125 ns p50**, **1.83 µs p95** and **3.58 µs p99**. Its p99 ranged
1.71–8.17 µs; no C++ speedup is attributed to this audit.

The existing Go durable-placement microbenchmark (fsynced local file, **stub
engine, no PostgreSQL**) changed from a median **4,516 B/op, 17 allocs/op** to
**2,486 B/op, 15 allocs/op**, about 45% less allocation from serializing a whole
batch once. Median time was 5.96 ms vs 5.78 ms; the time difference is too small
relative to host variability to establish a speedup. The unchanged stub HTTP
benchmark varied from 40.17 µs to 13.36 µs median, illustrating why these exploratory
microbenchmark times should not be presented as causal improvements.

## Remaining limits and next experiments

- The global trading mutex and synchronous I/O bound throughput. Snapshot
  serialization/fsync holds it too; latency and deadline failures can grow with
  retained state. There is no fairness promise for HTTP arrival order: FIFO is
  engine admission order. Profile the real path before changing this design.
- Fail-closed behavior preserves evidence after persistence failure but sacrifices
  availability and requires operator restart. The Go lock waits themselves are
  not interruptible; expired waiters are checked once they acquire it.
- The WAL guarantees tested here concern process crashes and complete batches,
  not arbitrary disk corruption or sudden machine power loss. It has no checksum;
  syntactically valid bit corruption is not detected. Legacy multi-line transactions
  cannot retroactively gain atomic framing. Power-loss ordering at initial file
  creation and broad snapshot-failure injection remain unverified.
- WAL, PostgreSQL and the C++ process do not form one distributed transaction.
  An ambiguous gRPC failure after engine acceptance can still diverge engine and
  account state. A lost HTTP response may also leave a committed order; retry with
  the same ID and inspect its status. Engine/API must be restarted together for
  recovery; persistent engine command deduplication is future work.
- Snapshot files contain account state but do not rebuild an empty PostgreSQL
  instance after WAL truncation. Preserve PostgreSQL and API volumes together.
- Filled orders, positions and engine trade history are retained. Heap growth
  proportional to retained business history is expected. A two-minute soak cannot
  establish absence of leaks or long-term latency stability. Runtime heap metrics
  are not RSS and vary with GC; these are observations, not proof.
- Two users and one active market per run are narrow synthetic traffic. No external
  payment custody, administrator/oracle authorization, shared-host isolation or
  real-money operation is claimed. Numeric boundary hardening and broader
  cancellation/settlement fault injection are useful next correctness work.

## Verified resume bullet options

- Built a Go/C++ prediction exchange with JWT authentication, gRPC matching and
  fsynced WAL/PostgreSQL accounting; validated 60,000 orders across four client
  concurrency levels and a 58,832-order, two-minute soak at 490 orders/s with
  zero errors and verified cash/share balances on an Apple M3 via Docker.
- Strengthened exchange recovery with atomic WAL batch framing, torn-tail repair
  and fail-closed persistence handling; validated 64 concurrent idempotent retries
  and real API/engine SIGKILL recovery preserving cash, shares and price-time
  priority, plus Go race detection and 56 C++ ASan/UBSan tests.
