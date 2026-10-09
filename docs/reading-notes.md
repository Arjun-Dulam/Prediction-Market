# Reading notes

## Books

- [Designing Data-Intensive Applications, second edition](https://www.oreilly.com/library/view/designing-data-intensive-applications/9781098119058/),
  Martin Kleppmann and Chris Riccomini. Transactions, replication, partitioning,
  and consistency. The first edition also covers the core topics.
- [Operating Systems: Three Easy Pieces](https://pages.cs.wisc.edu/~remzi/OSTEP/),
  Remzi and Andrea Arpaci-Dusseau. Locks (28), condition variables (30),
  concurrency bugs (32), files/directories (39), and journaling (42).
  The free chapters include homework simulators.
- LMAX's [Disruptor guide](https://lmax-exchange.github.io/disruptor/user-guide/)
  and [journaling article](https://technology.lmax.com/posts/improving-journalling-latency/).
  Compare its message ownership and pipeline with the Go admission worker.
  The 2015 article's storage/replication assumptions differ from our fsync barriers.

## Papers

- Lamport, [Time, Clocks, and the Ordering of Events in a Distributed System](https://lamport.azurewebsites.net/pubs/time-clocks.pdf)
  (1978): happened-before and logical ordering. Client clocks do not establish
  exchange admission order.
- Herlihy and Wing, [Linearizability](https://www.cs.cmu.edu/~wing/publications/HerlihyWing90.pdf)
  (1990): concurrent histories and their legal sequential interpretations.
  Race detection alone does not establish this property.
- Ongaro and Ousterhout, [Raft](https://raft.github.io/raft.pdf)
  (2014): election, log replication, and safety. Read before designing replicated
  commits or automatic owner failover.
- Mohan et al., [ARIES](https://www.cs.cmu.edu/~15849g/readings/mohan92.pdf)
  (1992): deeper reading on WAL and database recovery. This project replays
  application events rather than implementing page recovery.

## Exercises

1. **Crash boundaries.** Trace an order through reservation, WAL fsync, SQL,
   engine execution, completion persistence, and reply. Predict the outcome of
   a crash between each step, including execution followed by a lost RPC reply.
   Compare with `internal/trading/recovery_test.go` and the Docker recovery scripts.
   SIGKILL tests cover process crashes, not power loss.
2. **Matching oracle.** Implement a small sorted-list book and compare it with
   the C++ engine on a fixed-seed stream. Include equal-price makers, partial
   fills, cancellation, and final quotes.
3. **Retries and wallet contention.** Submit one ID concurrently, then retry
   it with changed parameters. Give one wallet 100 cents and submit two 80-cent
   reservations to different markets. Verify that reservations cannot overdraw.
   Use two psql sessions to observe [row-lock blocking](https://www.postgresql.org/docs/17/explicit-locking.html).
4. **Bounded queues.** Build a Go producer/consumer program with cancellation.
   Test queue-full rejection, cancellation before/after admission, and shutdown
   with queued work. Run the race detector and measure queueing delay.
5. **Stale quotes.** Delay an older publication until a newer version is cached.
   Check versions above 2^53 and near uint64's limit. Repeat after cache expiry;
   consider how a WebSocket client detects missed updates and fetches a snapshot.
6. **Load shape.** Predict one hot book versus eight books, with shared and
   separate wallets. Repeat the measurements and inspect utilization, saturation,
   and errors using the [USE method](https://www.brendangregg.com/usemethod.html).
   Explain closed-loop load and coordinated omission. Stage timings overlap;
   their sum is not an end-to-end latency breakdown.

## Labs

[MIT 6.5840's Raft lab](https://pdos.csail.mit.edu/6.824/labs/lab-raft1.html)
provides a separate consensus exercise. [CMU 15-445/645](https://15445.courses.cs.cmu.edu/fall2025/)
has lectures and projects on concurrency control and recovery.

## Exchange references

- [Cboe matching-unit gateways](https://cdn.cboe.com/resources/release_notes/2024/Schedule-Update-Cboe-BZX-Options-Announces-Release-and-Weekend-Testing-Dates-for-Unitized-Architecture-BOEv3-Protocol-June.pdf)
- [Cboe PITCH units, gap recovery, and snapshots](https://www.cboe.com/document/tech-spec/document/technical-specifications/cboe-titanium-u.s.-equitiesoptions-multicast-pitch-specification)
- [CME EBS market-segment routing](https://cmegroupclientsite.atlassian.net/wiki/spaces/EPICSANDBOX/pages/716177485/iLink%2BBinary%2BOrder%2BEntry%2B-%2BEBS%2BMarket%2Bon%2BCME%2BGlobex)
- [Coinbase's May 2026 outage](https://www.coinbase.com/blog/a-postmortem-of-our-may-7-2026-outage)

These describe specific exchange designs. Our static owner routing has no
replicated ledger or automatic failover.
