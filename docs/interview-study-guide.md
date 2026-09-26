# Learning and defending the exchange design

Read alongside experiments. For each topic, explain the invariant, change or
break a small implementation, predict its behavior, and compare the prediction
with a test. The exercises below are suggestions for this project, not claims
that a source prescribes this implementation. Sources were checked October 2026.

## Start with three resources

1. **Designing Data-Intensive Applications**, Martin Kleppmann and Chris Riccomini,
   [second edition, O'Reilly](https://www.oreilly.com/library/view/designing-data-intensive-applications/9781098119058/)
   (published February 2026). Focus on transactions, replication, sharding,
   distributed-system failures, consistency/consensus and stream processing.
   If you already have the first edition, use its corresponding topics; there
   is no need to buy another edition for this project. The payoff is explaining
   which component owns truth, what can be stale, and why adding replicas does
   not automatically add independent write capacity.
2. **Operating Systems: Three Easy Pieces**, Remzi and Andrea Arpaci-Dusseau,
   [free author-hosted chapters](https://pages.cs.wisc.edu/~remzi/OSTEP/).
   Read locks (28), condition variables (30), concurrency bugs (32), files and
   directories (39), and journaling/crash consistency (42). Use its linked
   homework simulators. Explain mutex protection, queue shutdown, memory versus
   disk visibility, and why file fsync does not substitute for directory fsync.
3. **LMAX's engineering material**:
   [Disruptor user guide](https://lmax-exchange.github.io/disruptor/user-guide/)
   and [historical exchange/journaling architecture](https://technology.lmax.com/posts/improving-journalling-latency/).
   Study single ownership, bounded buffers, consumer dependencies, journaling,
   replication and output publication. These explain exchange design choices;
   they are not a reason to implement a lock-free ring buffer prematurely.
   The journaling article is from 2015 and describes storage/replication
   assumptions different from this project's per-barrier fsync guarantees.

## Papers: read selected parts, then draw a failure timeline

- Leslie Lamport, **Time, Clocks, and the Ordering of Events in a Distributed
  System** (1978), [author-hosted paper](https://lamport.azurewebsites.net/pubs/time-clocks.pdf).
  Start with happened-before and logical clocks. Draw two clients whose local
  timestamps disagree with server admission order. Explain why price-time
  priority uses an exchange-defined ordering point, not clients' clocks.
- Maurice Herlihy and Jeannette Wing, **Linearizability: A Correctness Condition
  for Concurrent Objects** (1990), [author-hosted paper](https://www.cs.cmu.edu/~wing/publications/HerlihyWing90.pdf).
  Read the definition and history examples first. Distinguish a race-free
  implementation from one whose results correspond to legal sequential
  operations. A replicated system and durability have additional requirements;
  a mutex alone proves neither.
- Diego Ongaro and John Ousterhout, **In Search of an Understandable Consensus
  Algorithm** (2014), [paper](https://raft.github.io/raft.pdf).
  Read the overview, leader election, log replication and safety. Explain why a
  leader cannot acknowledge a replicated commit just because it wrote locally,
  and why an isolated old leader must not keep committing orders.
- **ARIES** (Mohan et al., 1992), [paper](https://www.cs.cmu.edu/~15849g/readings/mohan92.pdf),
  is optional deeper reading after the basics. Focus on WAL and recovery ideas.
  This exchange replays application events; it does not implement database
  page recovery or claim to implement ARIES.

## Six project exercises worth doing yourself

### 1. Trace one trade and every crash boundary

Draw: authenticate -> reserve -> WAL/fsync -> SQL commit -> engine -> completion
WAL/fsync -> SQL commit -> reply. Label which state is in memory, on disk, or
remote. Predict outcomes for a crash between every pair of steps, including an
engine that executes but loses the RPC reply. Then read the existing crash,
batch-barrier and reconstruction tests. Explain the need to restart the API and
all engine owners together. Process SIGKILL testing is not power-loss testing.

**Interview prompt:** A client timed out. Can it assume the order did not trade?

### 2. Implement a tiny independent price-time oracle

Use a sorted list rather than the optimized book. Feed both implementations the
same fixed-seed submissions/cancellations. Compare fills, remaining quantities
and best quotes. Include multiple equal-price makers and partially filled orders.
Do this on a scratch branch; keep the oracle easy to inspect.

**Interview prompt:** Where is time priority assigned, and how is it restored?

### 3. Break idempotency and shared-wallet reservations

Retry the same ID concurrently, then retry it with a changed quantity. Expect one
logical order and rejection of conflicts. Give an account 100 cents and submit
80-cent reservations to two markets simultaneously. Predict why independent
market locks permit double spending without coordinated account reservations.
Use two psql sessions to observe row-lock blocking and transaction rollback.
[PostgreSQL locking reference](https://www.postgresql.org/docs/17/explicit-locking.html)

**Interview prompt:** Does an idempotency key make every remote side effect
exactly once? Explain the lost-acknowledgement case.

### 4. Build and shut down a bounded queue

Write a small Go program with producers, one consumer, a fixed capacity and
cancellation. Test full-queue rejection, cancellation before admission, accepted
work completing after client cancellation, and shutdown while queued work
exists. Run `go test -race`. Compare queueing delay to service time; increasing
queue capacity can increase latency without increasing processing capacity.

**Interview prompt:** Which component applies backpressure, and what does a
cancelled HTTP context mean after durable admission?

### 5. Force stale quote publication

Delay an older quote update until a newer one has reached Redis. Check that the
older update cannot replace it. Repeat with versions above 2^53 and at uint64's
limit; explain why converting sequence numbers to floating point is unsafe.
Explain why WebSocket clients also check versions, and how a disconnected client
obtains a fresh snapshot. A quote is derived state, not proof of an order commit.

**Interview prompt:** How can two successful requests publish quotes backwards?

### 6. Explain a benchmark before running it

Predict the difference between one hot book, many books sharing accounts, and
many books with independent accounts. Repeat runs; preserve errors and raw data.
Report successful and attempted throughput, latency percentiles, duration,
concurrency, hardware, retained state and accounting checks. Explain closed-loop
load and coordinated omission; the current client does not establish fixed-rate
arrival capacity. Do not average percentiles from separate clients.

Use Brendan Gregg's [USE method](https://www.brendangregg.com/usemethod.html) to
inspect utilization, saturation and errors of resources before picking an
optimization. Read the stage metrics as elapsed time, not CPU samples. Profile
CPU, heap and blocking only when the question calls for it.

**Interview prompt:** Why can extra clients increase throughput initially but
mostly increase latency after saturation?

## Optional substantial labs

- [MIT 6.5840 Raft lab](https://pdos.csail.mit.edu/6.824/labs/lab-raft1.html):
  begin with leader election, then replication and persistence. This is a
  substantial separate learning project. Keep toy Raft out of the exchange;
  understand the safety arguments before claiming expertise.
- [CMU 15-445/645 database systems](https://15445.courses.cs.cmu.edu/fall2025/):
  choose lectures on concurrency control and recovery before attempting the full
  database implementation projects. Use the provided course material to connect
  PostgreSQL transactions with the exchange's event projection and rollback.

## A manageable order

First study OSTEP locks/files and trace the current code. Next study DDIA
transactions and do the retry/wallet exercises. Then read LMAX and run the
queue/quote/benchmark exercises. Finally read Lamport and Raft while comparing
static ownership with replication. These are stages, not a promise that everyone
can finish the reading or labs in a fixed number of hours.

## Practice a five-minute explanation

1. State the problem and invariants: matching priority, funds/shares, retries,
   acknowledgement durability and deterministic recovery.
2. Draw Go, C++, PostgreSQL, WAL/snapshot, Redis and WebSocket responsibilities.
3. Describe the measured bottleneck and a concrete change, including its cost.
4. Give the actual workload and results from the committed report, including
   errors, single-client behavior and sustained-run limitations.
5. Walk through one failure and the test that validates the recovery rule.

Be able to locate the implementation and test for each claim, change a small
case yourself, and explain its result. Describe which parts you personally built,
which were assisted, and what you verified. Avoid claiming production readiness,
replicated-ledger throughput, lock-free matching, or power-loss guarantees that
the project has not established.

## Real exchange references for the partitioning discussion

[Cboe matching-unit gateways and ordering](https://cdn.cboe.com/resources/release_notes/2024/Schedule-Update-Cboe-BZX-Options-Announces-Release-and-Weekend-Testing-Dates-for-Unitized-Architecture-BOEv3-Protocol-June.pdf),
[Cboe PITCH units and recovery](https://www.cboe.com/document/tech-spec/document/technical-specifications/cboe-titanium-u.s.-equitiesoptions-multicast-pitch-specification),
[CME EBS market-segment routing](https://cmegroupclientsite.atlassian.net/wiki/spaces/EPICSANDBOX/pages/716177485/iLink%2BBinary%2BOrder%2BEntry%2B-%2BEBS%2BMarket%2Bon%2BCME%2BGlobex),
and [Coinbase's 2026 replicated-engine outage](https://www.coinbase.com/blog/a-postmortem-of-our-may-7-2026-outage).
These are specific disclosed architectures, not a universal exchange blueprint.
Our static engine owners do not implement Coinbase's Raft cluster or automatic
failover.
