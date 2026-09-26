# Batched RPCs and local market owners

This follow-up to [the durable-persistence report](performance-optimization.md)
adds three bounded changes: ordered engine RPC batches, versioned quote
publication, and an experimental two-engine Docker deployment. It retains one
Go ledger, state lock, admission worker, WAL and PostgreSQL projector. This is
not horizontal scaling of the accounting service or an availability cluster.

## Behavior and ownership

The Go admission worker reserves a group of at most 32 orders durably, then sends
one ordered engine RPC per involved owner. The C++ batch validates all commands
before mutation, executes them in request order, and returns individual fills and
one final bid/ask per affected symbol. Other public engine operations cannot
interleave the batch. Completion events are fsynced and synchronously committed
to PostgreSQL before replies. No durability setting was relaxed.

Quotes are derived from committed results. Publication happens outside the state
lock, once per affected market/group, before the worker sends successful replies.
Redis atomically accepts a strictly newer ledger sequence while the cached value
exists. Sequences are decimal strings in JSON and compared as strings in Lua,
avoiding floating-point truncation above 2^53. Duplicate/stale updates are dropped.
Quote cache writes have a finite one-second deadline and remain best effort;
cache failure does not undo a committed order. The quote endpoint reconstructs
an expired cache from a coherent engine snapshot under the ledger read lock and
returns its ledger sequence. WebSocket consumers must compare versions too:
concurrent publishers can enqueue notifications in a different order.

The cache has a one-minute TTL; its version is not an independent durable
watermark. Redis loss/expiry is repaired from current authoritative state, not
by replaying arbitrary old publisher events. Quote publication is synchronous
and bounded here; there is no separate event-streaming service, durable fan-out
or fixed-rate delivery guarantee. Inactive quotes may require snapshot reads.

`ENGINE_ADDRS` assigns symbols by stable FNV-1a hash modulo the number of
configured endpoints. Support is deliberately limited to one or two distinct
endpoints. Their order is configuration. A group spanning both owners issues
its two RPCs concurrently and reassembles results in original admission order.
A market's YES/NO activity stays on the same engine. The Go ledger globally
coordinates cash/share reservations; two market owners cannot independently
spend the same wallet. Markets must have unique engine symbols.

Each engine allocates its own numeric order IDs. The Go lookup now uses
`(market ID, engine order ID)`, preventing cross-owner ID collisions during fills,
cancellation, snapshots and reconstruction. This changes a reconstructed
in-memory index, not the WAL event format or stored order ID representation.

There is no automatic rerouting or owner failover. Any uncertain engine mutation
freezes the ledger. **Restart the API and ALL configured engines together** to
rebuild from durable state; reservations are retained, not guessed/refunded.
Never run multiple APIs against this same ledger/volume or change endpoint
assignment while engines retain old books. Changing assignment requires stopping
all owners and rebuilding from the one authoritative WAL/snapshot. Never delete
WAL/snapshot volumes to recover real data.

## Measurements

The final one-owner build increased the median at 64 clients from **2,879.4 to
3,816.5 authenticated orders/s (+32.5%)**, with p99 **44.87 to 25.27 ms (-43.7%)**.
Each sweep completed 60,000 orders with zero HTTP errors and exact accounting.
The final single-client median is 729.2 versus 705.5, with overlapping run ranges;
no reliable isolated-request improvement is claimed. The preliminary RPC sweep
had a lower single-client median (616.0), illustrating host/storage variability.
The final repeated source is used for the comparison, rather than selecting the
best number from the intermediate run.

| Build | Clients | Runs | Orders | Median orders/s (range) | p50 ms | p95 ms | p99 ms | Error rate | Median seconds | Verified |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| rpc-before | 1 | 3 | 15000 | 705.5 (674.1–713.5) | 1.40 | 1.64 | 2.10 | 0.000% | 7.09 | 3/3 |
| rpc-before | 8 | 3 | 15000 | 1737.2 (1495.4–1753.2) | 4.49 | 5.05 | 6.60 | 0.000% | 2.88 | 3/3 |
| rpc-before | 32 | 3 | 15000 | 2514.5 (2498.2–2545.7) | 12.30 | 21.72 | 27.07 | 0.000% | 1.99 | 3/3 |
| rpc-before | 64 | 3 | 15000 | 2879.4 (2874.6–2922.9) | 21.44 | 37.00 | 44.87 | 0.000% | 1.74 | 3/3 |
| rpc-final | 1 | 3 | 15000 | 729.2 (683.5–741.4) | 1.35 | 1.60 | 2.04 | 0.000% | 6.86 | 3/3 |
| rpc-final | 8 | 3 | 15000 | 2013.4 (2013.2–2088.7) | 3.79 | 4.51 | 5.56 | 0.000% | 2.48 | 3/3 |
| rpc-final | 32 | 3 | 15000 | 3344.5 (3312.6–3375.4) | 9.19 | 10.87 | 16.83 | 0.000% | 1.49 | 3/3 |
| rpc-final | 64 | 3 | 15000 | 3816.5 (3750.3–3823.7) | 16.38 | 18.11 | 25.27 | 0.000% | 1.31 | 3/3 |
| one-owner-shared | 64 | 3 | 15000 | 3590.3 (3373.6–3715.0) | 16.59 | 20.25 | 28.35 | 0.000% | 1.39 | 3/3 |
| one-owner-independent | 64 | 3 | 15000 | 3649.3 (3636.1–3738.3) | 16.78 | 19.99 | 25.99 | 0.000% | 1.37 | 3/3 |
| two-owner-hot | 1 | 3 | 15000 | 741.2 (720.6–741.7) | 1.35 | 1.55 | 1.97 | 0.000% | 6.75 | 3/3 |
| two-owner-hot | 8 | 3 | 15000 | 2038.5 (1999.2–2061.9) | 3.76 | 4.32 | 5.48 | 0.000% | 2.45 | 3/3 |
| two-owner-hot | 32 | 3 | 15000 | 3328.0 (3297.7–3349.5) | 9.21 | 10.80 | 17.66 | 0.000% | 1.50 | 3/3 |
| two-owner-hot | 64 | 3 | 15000 | 3785.1 (3778.0–3825.7) | 16.28 | 18.61 | 26.10 | 0.000% | 1.32 | 3/3 |
| two-owner-shared | 64 | 3 | 15000 | 3407.9 (3383.4–3698.8) | 17.44 | 22.79 | 26.64 | 0.000% | 1.47 | 3/3 |
| two-owner-independent | 64 | 3 | 15000 | 3531.6 (2853.3–3662.7) | 17.06 | 26.32 | 31.64 | 0.000% | 1.42 | 3/3 |

Adding the second owner produced **no demonstrated throughput gain**. At 64
clients one hot book measured 3,785.1 orders/s, versus 3,816.5 with one owner.
Eight shared-wallet books measured 3,407.9 with two owners versus 3,590.3 with one;
independent-wallet books measured 3,531.6 versus 3,649.3. Run ranges overlap and
some cases vary substantially. The results support keeping one owner as the
performance default and retaining two owners as an opt-in correctness/ownership
experiment. They do not isolate account-row contention: the Go ledger serializes
both wallet layouts before reaching PostgreSQL.

| Build | Clients | Wait ms/order | WAL ms/order | SQL ms/order | Engine ms/order | Quote ms/order | Orders/group |
|---|---:|---:|---:|---:|---:|---:|---:|
| rpc-before | 1 | 0.002 | 0.379 | 0.547 | 0.058 | 0.143 | 1.000 |
| rpc-before | 8 | 0.956 | 0.125 | 0.299 | 0.057 | 0.444 | 3.997 |
| rpc-before | 32 | 2.343 | 0.070 | 0.258 | 0.057 | 0.814 | 11.161 |
| rpc-before | 64 | 5.179 | 0.044 | 0.239 | 0.056 | 1.123 | 21.097 |
| rpc-final | 1 | 0.002 | 0.387 | 0.573 | 0.066 | 0.042 | 1.000 |
| rpc-final | 8 | 1.146 | 0.145 | 0.306 | 0.019 | 0.012 | 3.997 |
| rpc-final | 32 | 3.068 | 0.057 | 0.225 | 0.007 | 0.004 | 15.823 |
| rpc-final | 64 | 7.223 | 0.042 | 0.210 | 0.005 | 0.003 | 31.447 |

At 64 clients engine elapsed cost fell from 0.056 to 0.005 ms/order. Quote
publication cost fell from 1.123 to 0.003 ms/order, but the old quote timings
included concurrent waiting and are not an additive latency or CPU saving.
Average orders/group increased from 21.1 to 31.4. SQL projection remains the
largest measured serialized stage (0.210 ms/order versus 0.042 in WAL and 0.005
in engine RPCs), so adding matching instances addresses little of the remaining
work. These measurements are stage observations, not a full CPU/storage profile.

The two-owner, eight-market shared-wallet soak started after 90,000 sweep
orders and completed **322,448 additional orders in 120.010 seconds** at
**2,686.8 orders/s**, with **zero HTTP errors and exact cash/position checks**.
p50/p95/p99 were **10.72/17.80/25.46 ms**. Full ten-second windows varied from
1,974 to 3,176 orders/s; this is not a steady production-capacity claim.
Sampled goroutines were 52–85 and sampled Go heap allocation was 69–237 MiB.
The maximum measured checkpoint admission pause since API startup was 48.1 ms.
Retained history and GC remain relevant, and a two-minute test does not prove
indefinite stability or absence of leaks. [Raw soak](results/partition-two-soak.jsonl).

The initial one-owner-failure check exposed a retry classification bug: a client
deadline could expire before the independent admitted worker reported engine
failure, producing HTTP 400 `context deadline exceeded`. The final API maps
request deadlines/cancellation to retryable HTTP 503; the reservation still
survives and may complete, so clients retry the same ID. A focused HTTP test
verifies this behavior after admission. The failed observation is retained in
[the failure record](results/rpc-partition-failure-before.json).

Real SIGKILL checks were then run against both owners and the API. The standard
check independently compares PostgreSQL accounting and verifies price/FIFO fills
after reconstruction. The two-owner check verifies shared-wallet limits,
concurrent retries, both-owner reconstruction, one-owner failure freezing the
ledger, reservation recovery and cancellation refunds. The aged-state rerun
passed after the HTTP fix ([raw result](results/rpc-partition-recovery-aged.json));
the standard check's [result](results/rpc-stack-recovery.json) includes independent
PostgreSQL verification. A final fresh fixture is recorded separately.

The fresh two-owner fixture assigned both makers numeric engine ID `1`; fills,
reconstruction and cancellation remained correct because the ledger scopes IDs
by market ([raw result](results/rpc-partition-recovery.json)). After the final
quantity-bound validation, three further 5,000-order trials at 64 clients on the
two-owner stack completed 15,000 orders with zero errors and exact accounting:
**3,550.5 orders/s median (3,436.1–3,566.1)**, p50/p95/p99
**17.40/19.53/32.65 ms**, median duration **1.41 seconds**
([raw confirmation](results/rpc-final-confirmation.jsonl)). These trials followed
a small retained recovery fixture and are a final-code confirmation, not a
matched replacement for the fresh one-owner comparison above.

After the final confirmation, three idle samples ten seconds apart all showed
20 goroutines; heap allocation was 45.84–45.95 MiB. Health returned HTTP 200
with database, both engines and Redis up
([idle samples](results/rpc-final-idle.json)). This short return-to-idle check
is evidence against accumulating request goroutines in this run, not a proof
that the service has no leaks.

## Hardware and method

Same Apple M3 host, 8 cores, 24 GiB RAM, Docker Linux arm64 VM with 8 vCPUs and
~7.75 GiB RAM as the previous audit. No service CPU/memory quotas. One API,
PostgreSQL 17 Alpine with fsync/synchronous_commit/full_page_writes enabled,
Redis 7 Alpine, and either one or two C++ owner containers run locally. Go API
builds use the pinned Dockerfile; engine builds use GCC 13/Ubuntu 24.04 Release.
[Environment capture](results/rpc-environment.txt) and
[two-owner image capture](results/rpc-partition-environment.txt) record image IDs,
compiler and durability settings. All code is committed: before API/engine
`59e984a`; first RPC sweep `0b6bae3`; benchmark source `75fd932`; final timeout classification fix
`c82b7b7`; final quantity-bound validation `2e3b52b`. The last two changes
classify timeouts and reject quantities beyond the engine's uint32 wire limit
before any reservation. The measured one-share successful path is unchanged.
[Final environment capture](results/rpc-final-environment.txt) records the final
confirmation build and healthy local deployment.

The generator uses real JWT registration/funding, unique IDs and alternating
one-share buy_yes/buy_no orders at 50 cents. Each YES/NO pair uses the same market.
`-markets 8 -accounts shared` spreads pairs over eight books with two wallets;
`-accounts independent` instead uses sixteen wallets. Verification checks each
wallet's cash and positions in each relevant book, including uneven pair counts.
Setup/verification are excluded from timing. Three 5,000-order trials per case
produce run-level medians and ranges; primary sweeps use 1/8/32/64 clients.
These are closed-loop synthetic local workloads, without external network or
WebSocket subscribers. Stage times are elapsed-time averages, not CPU profiles.

Primary before/final sweeps each start with fresh disposable volumes and retain
state across runs. Multi-market trials follow their primary sweep; the final
soak follows the partitioned trials. Exact order history, runtime samples and
error/accounting results are in JSONL. Partition comparisons are experiments
under this one ledger, not evidence that adding a second API doubles capacity.

## Reproduce locally

From the repository root:

```bash
export BLUEPRINT_DB_DATABASE=exchange BLUEPRINT_DB_USERNAME=exchange
export BLUEPRINT_DB_PASSWORD=audit-local BLUEPRINT_DB_PORT=15432
export JWT_SECRET=audit-local-secret-for-testing
export ORDER_BATCH_SIZE=32 SNAPSHOT_INTERVAL=30s ENGINE_RPC_BATCH=true
(cd go-backend && go build -o /tmp/pme-loadtest ./cmd/loadtest)
cd go-backend
docker compose -p pme-audit down -v  # deletes only this disposable project's data
docker compose -p pme-audit up -d --build --wait
cd ..
/tmp/pme-loadtest -label rpc-final -orders 5000 -levels 1,8,32,64 -repeats 3 \
  -environment 'record hardware, Docker resources and compiler builds' > /tmp/rpc-final.jsonl
/tmp/pme-loadtest -label one-owner-shared -orders 5000 -concurrency 64 \
  -markets 8 -accounts shared -repeats 3 > /tmp/one-shared.jsonl
/tmp/pme-loadtest -label one-owner-independent -orders 5000 -concurrency 64 \
  -markets 8 -accounts independent -repeats 3 > /tmp/one-independent.jsonl
```

Switch to two owners with fresh disposable volumes for comparison:

```bash
cd go-backend
docker compose -p pme-audit down -v
docker compose -p pme-audit -f docker-compose.yml -f docker-compose.partitioned.yml \
  up -d --build --wait
cd ..
/tmp/pme-loadtest -label two-owner-hot -orders 5000 -levels 1,8,32,64 -repeats 3 \
  > /tmp/two-hot.jsonl
/tmp/pme-loadtest -label two-owner-shared -orders 5000 -concurrency 64 \
  -markets 8 -accounts shared -repeats 3 > /tmp/two-shared.jsonl
/tmp/pme-loadtest -label two-owner-independent -orders 5000 -concurrency 64 \
  -markets 8 -accounts independent -repeats 3 > /tmp/two-independent.jsonl
/tmp/pme-loadtest -label two-owner-soak -duration 2m -concurrency 32 \
  -markets 8 -accounts shared > /tmp/two-soak.jsonl
python3 scripts/summarize-load.py /tmp/rpc-final.jsonl /tmp/two-hot.jsonl \
  /tmp/one-shared.jsonl /tmp/one-independent.jsonl /tmp/two-shared.jsonl \
  /tmp/two-independent.jsonl /tmp/two-soak.jsonl
python3 scripts/summarize-stages.py /tmp/rpc-final.jsonl /tmp/two-soak.jsonl
python3 scripts/check-stack-recovery.py --project pme-audit --partitioned
python3 scripts/check-partitioned-stack.py --project pme-audit
```

Repeat the final-code confirmation independently after the recovery fixture:

```bash
/tmp/pme-loadtest -label final-code-confirmation -orders 5000 -concurrency 64 \
  -repeats 3 > /tmp/final-confirmation.jsonl
python3 scripts/summarize-load.py /tmp/final-confirmation.jsonl
```

Both recovery scripts deliberately SIGKILL the specified disposable services.
The second additionally kills one owner and verifies ledger failure, preservation
of its uncertain reservation, recovery and idempotent retry. It checks shared
wallet overdraw, cross-market positions, cancellation refunds and quote versions.
Use a fresh project for these tests; they create synthetic accounts and orders.

The first RPC sweep is retained as `results/rpc-after.jsonl`; it precedes the
quote-endpoint fallback fix and is not the final result. For a before build,
archive `59e984a` and build BOTH API and engine using their Dockerfiles, then
select those images through a Compose override with `--no-build`. Follow the
archive/override procedure in the previous report, replacing its revision and
adding the engine image override. Use the current generator and fresh volumes.
`ENGINE_RPC_BATCH=false` retains individual engine calls in the current build
while keeping group persistence and versioned quote publication; it isolates RPC
batching without reverting other changes. It is not identical to the before API.

Local services are API `localhost:8080`, engines `localhost:50051/50052`, Redis
`localhost:6379`, PostgreSQL `localhost:15432`. Shut down without deleting data:

```bash
cd go-backend
docker compose -p pme-audit -f docker-compose.yml -f docker-compose.partitioned.yml down
```

## Correctness and complexity review

Final Go suite/race/vet outputs are in `results/rpc-final-code-go-*.txt`; native C++
ASan/UBSan output is `results/rpc-final-cpp-sanitizers.txt`. Reproduce checks as in the
previous report. Native batch tests compare fills/quotes against sequential
execution, validate FIFO and prove invalid groups do not partially execute.
Go tests exercise concurrent owner RPCs, reassembly, overlapping engine IDs,
shared-wallet reservations, repeated reconstruction, batch uncertainty, committed
quote visibility, publication outside the lock, and stale/duplicate/full-width
Redis versions. The generator tests cover uneven per-market counts and both
wallet layouts. Quantity-bound tests also verify rejection before any WAL sequence or wallet
change. The existing durability, idempotency and crash tests remain.

Proto bindings are committed. Regenerate from the root with protoc and the Go
plugins (measured versions: protoc 36.2, protoc-gen-go 1.36.11,
protoc-gen-go-grpc 1.5.1):

```bash
PATH="$(go env GOPATH)/bin:$PATH" protoc -I proto --go_out=go-backend \
  --go_opt=module=go-backend --go-grpc_out=go-backend \
  --go-grpc_opt=module=go-backend proto/exchange.proto
```

No additional Go pool, journal format, deployment service or distributed
transaction protocol was added. The router supports only two owners, uses
bounded groups and at most two concurrent RPCs, and has no dynamic assignment or
rebalancing. Most extra code is generated bindings or targeted tests. The
single-order and single-owner paths remain controls. The Go ledger/state lock,
PostgreSQL commits, retained history and checkpoint copies remain scaling limits;
replicating those requires a separate correctness design and fresh measurements.

Verified resume option 1: improved a durable Go/C++ exchange's authenticated
throughput **33% (2,879 to 3,817 orders/s)** and reduced p99 **44%** at 64 clients
using ordered gRPC batches and versioned quote coalescing; validated three
repeated synthetic 5,000-order trials per concurrency level while retaining WAL
fsync, synchronous accounting and deterministic recovery.

Verified resume option 2: implemented static market ownership across two C++
matching engines behind a Go ledger; validated shared-wallet reservations,
32 concurrent idempotent retries, overlapping engine IDs and SIGKILL recovery,
plus a two-minute 322,448-order synthetic soak with zero errors and exact
accounting. This describes partitioned matching, not a replicated ledger or a
measured horizontal throughput gain.

For books, papers and exercises that explain these choices, see the
[study guide](interview-study-guide.md).
