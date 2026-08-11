# Durable order-path optimization

This follow-up to [the systems audit](systems-audit.md) improves authenticated
order placement through measurement and three focused persistence optimizations.
The C++ matcher is unchanged. No new services or deployment infrastructure were
added. These are synthetic local measurements, not production capacity claims.

## Architecture and measured bottleneck

A JWT-authenticated Go handler validates ownership and submits to the trading
service. One state lock protects accounts, positions, orders and admission order.
The service reserves funds/shares, fsyncs a WAL record and synchronously projects
it into PostgreSQL before calling the C++ engine over gRPC. It then durably commits
order/fill/accounting events. The handler refreshes quotes through two engine RPCs,
writes Redis and publishes bounded WebSocket hub messages before returning 201.
Snapshots run every 30 seconds. Recovery replays whole WAL batches and rebuilds
open engine orders by their original admission sequence. A checkpoint uses
an immutable previous WAL segment plus the current segment until publication.

The baseline issued PostgreSQL statements one at a time while holding the state
lock. It also performed two WAL flushes and two database transactions per order.
Adding clients saturated this one lane and increased waiting. At 32 clients,
median per-run average costs were 0.746 ms/order in projection, 0.454 ms/order in
WAL write/flush and 0.060 ms/order in matching RPCs, with 40.692 ms/order waiting
for the lock. Matching CPU was not the useful first optimization target.

## Measurement and three focused changes

Fixed-label counters/sums expose admission waiting, WAL, projection, engine,
quote and checkpoint pause time. A maximum-pause gauge records the longest
checkpoint admission pause since process start. The load client records metric
deltas and runtime samples; `scripts/summarize-stages.py` computes per-order
costs. These are elapsed-time measurements, not CPU profiles or server latency
percentiles. Concurrent stages overlap, so their sums are not an additive
end-to-end latency breakdown.

1. **Pipeline PostgreSQL projection.** A single `INSERT ... RETURNING` admits new
   event IDs; ordered materialized statements are sent using `pgx.Batch` on a
   borrowed connection from the existing `database/sql` pool. Everything stays in
   one synchronous transaction. Duplicate IDs, including duplicates within one
   batch, apply once. Each debit still checks its affected-row count, and any SQL,
   deadline or insufficient-balance error rolls back the entire transaction.
2. **Bounded group commit.** One temporary worker drains up to 32 already waiting
   orders, without a batching timer. Admission keeps a maximum of 128 queued
   orders and returns 503 when full. Aggregate reservations prevent cash/share
   overdrafts across a group. The group first fsyncs/projects all reservations,
   then calls the engine serially in admission order, stages completions privately
   and fsyncs/projects them together before any success reply. Partial fills can
   refer to earlier orders in the same group. Readers and checkpoint state copies
   use the state lock; checkpoint encoding/storage run outside it. A worker exits when its queue empties; Close
   drains existing work.

3. **Move checkpoint encoding off admission.** A repeated soak on large retained
   history exposed 29 two-second request timeouts with the old
   checkpoint lock. The service now copies state briefly under that lock, rotates
   the WAL and writes/fsyncs the snapshot outside it. The immutable
   `orders.wal.previous` is deleted only after the new snapshot and directory are
   durable. The backup name is directory-synced before the current pathname is
   reused; the replacement file and its directory are synced as well. Writers
   continue on `orders.wal`; recovery reads previous then current
   and skips events already covered by the snapshot. A failed checkpoint keeps
   both files; a retry covers the older segment without overwriting it. At most
   two WAL files are needed. First-file creation/rotation/removal sync directory
   entries. Checkpoint writers remain serialized; Close waits for publication.

`ORDER_BATCH_SIZE` defaults to 32; valid values are 1–32. Setting 1 retains a
single-order execution path useful for isolating batching effects. Neither mode
turns off fsync, synchronous PostgreSQL commit, reservation before matching, quote
updates or accounting checks. The existing WAL envelope already supports multiple
events, so event encoding did not change. Checkpoint rotation adds a previous
WAL file that must be retained during recovery.

Before admission, cancelled/expired requests are skipped. After admission, a group
uses its own two-second context: one disconnected client cannot abort durability
for other clients. A caller can consequently time out while its order completes;
retry with the same ID. Ambiguous engine order-submission transport errors now
latch failure in both modes, preserving the durable reservation. Restart the API **and** engine
before retrying; readiness and further writes fail until recovery. This avoids
refunding an order that the engine may already have matched. It is a deliberately
simple recovery rule, not a distributed atomic transaction.

## Results

| Build | Clients | Runs | Orders | Median orders/s (range) | p50 ms | p95 ms | p99 ms | Error rate | Median seconds | Verified |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| sync-sql-baseline | 1 | 3 | 15000 | 626.9 (554.3–641.4) | 1.62 | 1.97 | 2.60 | 0.000% | 7.98 | 3/3 |
| sync-sql-baseline | 8 | 3 | 15000 | 759.6 (686.0–804.5) | 9.83 | 12.65 | 16.96 | 0.000% | 6.58 | 3/3 |
| sync-sql-baseline | 32 | 3 | 15000 | 749.7 (702.5–808.2) | 40.82 | 47.47 | 57.22 | 0.000% | 6.67 | 3/3 |
| sync-sql-baseline | 64 | 3 | 15000 | 807.1 (793.5–808.1) | 78.32 | 87.34 | 92.97 | 0.000% | 6.20 | 3/3 |
| sql-pipeline-only | 1 | 3 | 15000 | 705.4 (695.3–708.2) | 1.41 | 1.65 | 1.96 | 0.000% | 7.09 | 3/3 |
| sql-pipeline-only | 8 | 3 | 15000 | 892.0 (839.1–906.3) | 8.72 | 9.91 | 12.93 | 0.000% | 5.61 | 3/3 |
| sql-pipeline-only | 32 | 3 | 15000 | 877.7 (862.7–882.5) | 34.79 | 41.03 | 57.14 | 0.000% | 5.70 | 3/3 |
| sql-pipeline-only | 64 | 3 | 15000 | 897.3 (773.1–904.6) | 70.35 | 79.71 | 82.57 | 0.000% | 5.57 | 3/3 |
| durable-group-commit | 1 | 3 | 15000 | 690.6 (684.5–693.2) | 1.43 | 1.74 | 2.28 | 0.000% | 7.24 | 3/3 |
| durable-group-commit | 8 | 3 | 15000 | 1561.7 (1515.3–1684.6) | 4.81 | 10.14 | 15.75 | 0.000% | 3.20 | 3/3 |
| durable-group-commit | 32 | 3 | 15000 | 2432.9 (2394.9–2526.4) | 12.56 | 18.83 | 25.62 | 0.000% | 2.06 | 3/3 |
| durable-group-commit | 64 | 3 | 15000 | 2874.2 (2825.0–2882.6) | 21.67 | 36.65 | 44.49 | 0.000% | 1.74 | 3/3 |
| final-durable-checkpoint | 1 | 3 | 15000 | 571.8 (568.2–624.5) | 1.72 | 2.09 | 2.92 | 0.000% | 8.74 | 3/3 |
| final-durable-checkpoint | 8 | 3 | 15000 | 1463.4 (1354.7–1522.3) | 5.23 | 8.66 | 16.12 | 0.000% | 3.42 | 3/3 |
| final-durable-checkpoint | 32 | 3 | 15000 | 2257.3 (2236.5–2391.8) | 13.58 | 24.85 | 30.17 | 0.000% | 2.22 | 3/3 |
| final-durable-checkpoint | 64 | 3 | 15000 | 2677.9 (2640.9–2815.2) | 23.36 | 40.36 | 47.60 | 0.000% | 1.87 | 3/3 |

Each primary sweep used three 5,000-order runs at each concurrency level: 60,000 orders
per build. Percentiles and throughput above are medians of run-level results;
ranges show all three throughputs. All primary sweep runs had zero HTTP errors and
exact final balance/YES/NO position checks. Baseline and final sweeps each started
with fresh volumes and retained state between runs. SQL-only results isolate the
first optimization; the small preliminary group check is retained separately and
is excluded from the final comparison.

At 64 clients, median throughput rose **3.32× (807.1 → 2677.9 orders/s)**,
while p99 fell **92.97 → 47.60 ms**. At 32 clients the throughput gain was
3.01×. The single-client median decreased from 626.9 to
571.8 orders/s, with overlapping run ranges; no single-client improvement
is claimed. Batching benefits contended work and adds some isolated-request
overhead. Host variability also remains. The checkpoint fix addresses long
admission pauses, rather than establishing another peak-throughput gain.
All measurements include JWT verification, PostgreSQL accounting, engine calls
and quote refreshes.

| Build | Clients | Wait ms/order | WAL ms/order | SQL ms/order | Engine ms/order | Quote ms/order | Orders/group |
|---|---:|---:|---:|---:|---:|---:|---:|
| final-durable-checkpoint | 1 | 0.002 | 0.673 | 0.581 | 0.062 | 0.153 | 1.000 |
| final-durable-checkpoint | 8 | 1.117 | 0.207 | 0.344 | 0.059 | 0.402 | 3.165 |
| final-durable-checkpoint | 32 | 2.768 | 0.107 | 0.267 | 0.059 | 0.881 | 10.989 |
| final-durable-checkpoint | 64 | 5.684 | 0.066 | 0.244 | 0.058 | 1.189 | 21.186 |
| sync-sql-baseline | 1 | 0.000 | 0.377 | 0.704 | 0.059 | 0.150 | 1.000 |
| sync-sql-baseline | 8 | 8.761 | 0.443 | 0.745 | 0.061 | 0.191 | 1.000 |
| sync-sql-baseline | 32 | 40.692 | 0.454 | 0.746 | 0.060 | 0.191 | 1.000 |
| sync-sql-baseline | 64 | 76.976 | 0.427 | 0.733 | 0.060 | 0.188 | 1.000 |

Stage entries are medians of per-run mean milliseconds/order. Orders/group is
inferred from the two WAL phases; it is smaller than 32 because the worker drains
available work rather than waiting for a full group. WAL costs include JSON
encoding, writing and fsync. PostgreSQL costs include admission, statements and
transaction commit. Waiting in the group path includes the bounded queue and
lock admission. Quote refreshes still happen outside the trading lock.

The duration-based runs expose the limitation that short sweeps alone miss:

| Build | Clients | Runs | Orders | Median orders/s (range) | p50 ms | p95 ms | p99 ms | Error rate | Median seconds | Verified |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| durable-group-soak | 32 | 1 | 207708 | 1725.6 (1725.6–1725.6) | 15.30 | 36.63 | 54.65 | 0.000% | 120.37 | 1/1 |
| durable-group-soak-repeat | 32 | 1 | 164192 | 1367.5 (1367.5–1367.5) | 16.87 | 51.43 | 101.57 | 0.018% | 120.04 | 0/1 |
| checkpoint-aged-soak | 32 | 1 | 145022 | 1208.3 (1208.3–1208.3) | 20.51 | 55.99 | 109.09 | 0.000% | 120.02 | 1/1 |
| checkpoint-aged-final | 32 | 1 | 210636 | 1755.2 (1755.2–1755.2) | 15.92 | 31.43 | 54.44 | 0.000% | 120.01 | 1/1 |

The first group-commit soak retained the 60,000-order sweep and completed 207,708
orders with zero HTTP errors. Full ten-second windows fell from 2,408 to 822
orders/s. A three-run check after idle time on that same state recovered a median
2,220 orders/s, so history alone was not established as the cause. Goroutines
fell to 14 after drain in the recorded idle samples.

The repeated old-checkpoint soak started with about 283,000 retained orders and
recorded **29 HTTP 400 deadline errors among 164,192 attempts (0.018%)**. They
clustered around a large periodic checkpoint. The old implementation held the
state lock through encoding and fsync. This failing result is retained; its
accounting check was **skipped because requests failed**, not silently treated
as a pass. Successful-throughput calculations exclude the 29 failed requests.
The broader throughput variability remains incompletely isolated.

The first off-lock checkpoint run started with 446,877 orders and completed
145,022 more without HTTP errors or accounting discrepancies. Its maximum
checkpoint admission pause since process start was 261 ms. After the final
metadata-barrier review, a second run started with **591,899 retained
orders** and completed **210,636 orders in 120.010 seconds** at
**1755.2 orders/s**, with zero HTTP errors and exact cash/share checks.
Its p50/p95/p99 were 15.92/31.43/54.44 ms. The maximum measured checkpoint
admission pause since process start was **88.0 ms**. These aged-state runs have
different starting histories and are not a controlled throughput A/B comparison;
they validate that the checkpoint-timeout failure did not recur in these tests.

During the final aged run, sampled goroutines were 55–79 and sampled Go heap
allocation was 353–1324 MiB. Retained state, temporary snapshot copies and GC all affect
memory. This is not proof of indefinite stability or absence of leaks. The
final fresh-sweep idle samples returned to 14 goroutines across three samples
over 20 seconds ([raw idle metrics](results/checkpoint-final-idle.json)). The
real-stack SIGKILL check also passed on the data set with over 800,000 orders.


## Hardware and workload

Apple M3, 8 CPU cores, 24 GiB host memory; macOS 27.0.1. Docker Engine 29.8.1,
Linux arm64 VM with 8 vCPUs and 8,319,770,624 bytes (~7.75 GiB), without service CPU
or memory limits. API: Go 1.26.8, CGO disabled, trimpath/stripped release build.
Native tests/client: Go 1.27.1 darwin/arm64. Matcher: GCC 13, CMake Release,
Ubuntu 24.04. PostgreSQL 17 Alpine with fsync, synchronous_commit and
full_page_writes all on; Redis 7 Alpine. Local Docker volumes hold the WAL and
PostgreSQL data. [Environment capture](results/optimization-environment.txt)
records final source revision (`90221a6`), image IDs, compiler metadata and durability settings.

The closed-loop client runs on the same host outside Docker. It creates two
users, obtains real JWTs, funds them and creates a fresh market for each run.
Orders have unique IDs, alternate buy_yes/buy_no at 50 cents, and have quantity one.
Setup and final verification are outside the measured interval. Latency includes
HTTP I/O, response-body consumption and acknowledgement validation. No warmup,
external network traffic, connected WebSocket subscribers or delivered fan-out is
measured. Builds/tests do not run during the final measurements.

## Reproduction

Use a disposable stack; the reset command deletes that project's test volumes.
From the repository root:

```bash
export BLUEPRINT_DB_DATABASE=exchange BLUEPRINT_DB_USERNAME=exchange
export BLUEPRINT_DB_PASSWORD=audit-local BLUEPRINT_DB_PORT=15432
export JWT_SECRET=audit-local-secret-for-testing
export SNAPSHOT_INTERVAL=30s ORDER_BATCH_SIZE=32
cd go-backend
docker compose -p pme-audit down -v
docker compose -p pme-audit up -d --build --wait
go build -o /tmp/pme-loadtest ./cmd/loadtest
/tmp/pme-loadtest -label durable-group-commit -orders 5000 -levels 1,8,32,64 \
  -repeats 3 -environment 'record your CPU, RAM, Docker resources and builds' \
  > /tmp/final.jsonl
/tmp/pme-loadtest -label durable-group-soak -duration 2m -concurrency 32 \
  -environment 'same configuration; retained sweep state' > /tmp/soak.jsonl
cd ..
python3 scripts/summarize-load.py /tmp/final.jsonl /tmp/soak.jsonl
python3 scripts/summarize-stages.py /tmp/final.jsonl /tmp/soak.jsonl
python3 scripts/check-stack-recovery.py --project pme-audit
```

The real-stack crash check performs concurrent authenticated retries, kills both
services, checks recovered orders/accounts, verifies better-price/FIFO fills and
cancellation, and independently checks PostgreSQL balances/positions. It is
intentionally destructive to the specified disposable services. Its successful
final run is saved in [the crash result](results/checkpoint-stack-recovery.json).

For comparable baseline and SQL-only runs, build the API from these committed
sources while keeping the engine/client/configuration identical:

| Build | API source |
|---|---|
| Instrumented baseline | `f7afef4` |
| SQL pipeline only | `dd9526c` |
| Group commit, old checkpoint | `26430c7` |
| Final checkpoint protocol | `90221a6` |

For example, extract the baseline without disturbing your checkout, build its
image, then use a Compose override pointing at that image:

```bash
mkdir -p /tmp/pme-timed-baseline
git archive f7afef4 | tar -x -C /tmp/pme-timed-baseline
docker build -t pme-audit-api:timed-baseline /tmp/pme-timed-baseline/go-backend
cat > /tmp/pme-baseline-image.yml <<'YAML'
services:
  api:
    image: pme-audit-api:timed-baseline
YAML
cd go-backend
docker compose -p pme-audit down -v
docker compose -p pme-audit -f docker-compose.yml -f /tmp/pme-baseline-image.yml \
  up -d --no-build --wait
/tmp/pme-loadtest -label sync-sql-baseline -orders 5000 -levels 1,8,32,64 \
  -repeats 3 -environment 'same hardware/configuration; fresh volumes' > /tmp/baseline.jsonl
```

The baseline ignores ORDER_BATCH_SIZE. For the SQL-only build repeat the archive
step with `dd9526c` and a different image/override. To measure the current code's
single-order control instead, set `ORDER_BATCH_SIZE=1`, reset volumes and recreate
the API. Always restore 32/recreate the stack for the final workload. Raw results
include `docs/results/optimization-{baseline,sql-only,group-sweep,final}.jsonl`
and the duration-based files listed above. All diagnostic and failed runs are
retained as well. Results will vary with host load, filesystem and
container/compiler versions; the aim is to reproduce the workload and improvement, not guarantee an exact number.

## Correctness and review

```bash
cd go-backend
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
cd ..
cmake -S . -B build-audit -DCMAKE_BUILD_TYPE=Release
cmake --build build-audit --target OrderBookTests -j4
ctest --test-dir build-audit/engine --output-on-failure
```

Go database tests require Docker and fail rather than silently skipping. Native
C++ builds require the documented gRPC/Protobuf/OpenSSL dependencies. Existing
C++ tests use ASan/UBSan even in Release. Final outputs are saved in
`results/checkpoint-go-{tests,race,vet}.txt` and
`results/checkpoint-cpp-{build,sanitizers}.txt`.

New tests verify SQL admission/order/debit semantics, mixed old/new/repeated events,
concurrent replay, rollback after constraints/insufficient funds, cancellation
while blocked on a row lock and pool reuse. Group tests compare multi-order partial
fills and refunds to sequential execution, test aggregate cash/share reservations,
conflicting/identical retries, bounded admission, cancellation before/after
admission and worker shutdown. Checkpoint tests block snapshot storage while orders
continue, verify frozen maps (including nested positions), inject failed
publication/retry, recover a crash between WAL rename and creation, and SIGKILL
helpers before publication, after publication and after old-segment deletion.
They compare two successive replays and test Close waiting for a checkpoint.
Order tests block completion projection to prove no success
is returned early, inject projection/RPC failure, and replay cuts at reservation
and completion boundaries. Existing SIGKILL helper and checkpoint/retry tests also
exercise the group execution mode.

Complexity review: the design retains the existing state lock and event format.
Checkpoints use two local WAL files instead of a journal framework or new service.
The added queue is private and bounded, uses one worker only while needed and has
no batching timer, distributed coordination or background acknowledgement path.
The SQL connection comes from the existing pool. The single-order path provides a
correctness oracle and measurement control. Checkpoint failure tests use a
private writer parameter; no filesystem abstraction or test hook was added to
the public service configuration. Most added code is targeted validation.

## Remaining limitations and next measurement

A global state lock remains; copying a large snapshot still briefly pauses
admission, while encoding and disk I/O run outside the lock. All
completed orders remain in memory and snapshots, so memory/checkpoint cost grows
with retained history. The intermediate group-commit version slowed substantially
on sustained load,
then recovered short-run throughput after idle time. Large checkpoint pauses
were identified and fixed; the broader throughput variability is not fully
isolated. Host/VM, storage, GC and retained-history effects need controlled
profiling. The soak measures this workload, not indefinite
stability or proof of no leaks. Two quote RPCs and Redis work still occur for each
HTTP response; concurrent refreshes have no quote version ordering. There is no
cross-process transactional protocol between WAL, PostgreSQL and engine; recovery
requires restarting both services. Fail-closed recovery can retain an order for
which the caller received an error, which is why retry IDs matter. Keep both WAL
segments and the snapshot together;
older API builds do not replay `.previous`. Baseline comparisons must use fresh
volumes, and downgrades require a completed checkpoint with no previous segment.

The generator is closed-loop and cannot characterize fixed-arrival overload or
coordinated omission. Deposits are test funds, not an external financial ledger.
JWT authentication throughput does not include bcrypt/login per order. The final
results do not measure WebSocket subscriber fan-out, multiple users/markets at
scale, multi-API replicas or geographically distributed clients.

A proportionate next deployment experiment is **one VM running the existing stack
and a second VM running the client**, followed by a fixed-arrival workload that
reports attempted/successful rates, dropped work, errors and latency near saturation.
Choose and record CPU/RAM/storage, region/network, duration and a spending cap.
Use the client's existing `-base` flag against the API. Independent clients need
independent fixture IDs and separately reported results; do not add percentiles
from different clients together. Multiple API replicas are premature while each
API owns authoritative state and a local WAL. No VMs were provisioned by this work.

## Verified resume options

- Improved a Go/C++ exchange's authenticated order throughput **3.3× (807 →
  2,678 orders/s)** and reduced p99 **49%** at 64 clients using bounded group
  commit and pipelined PostgreSQL projection; validated three repeated synthetic
  5,000-order trials while retaining fsync and synchronous accounting.
- Implemented crash-safe WAL rotation and off-lock checkpoints for a Go/C++
  prediction exchange; validated **210,636 authenticated orders over two minutes**
  on a data set starting with **591,899 orders**, with zero HTTP errors and exact
  balances/positions, plus SIGKILL replay tests, Go race detection and C++ sanitizers.

