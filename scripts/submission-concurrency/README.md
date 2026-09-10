# Bounded concurrency investigation

`make snapshot-concurrency` uses the latest successfully reconciled migration-17
snapshot import. It freezes its source and runs separately from integration
tests. Original snapshot databases are never targeted. Set `CONCURRENCY_SECONDS`
to 5–60 seconds per scenario (default 12). The UI workload reference currently
comes from the retained `search-s620fWEK` capture.

The closed-loop probe runs 1, 4 and 8 workers with reads only or four reads per
rolled-back cache write. It also runs eight mixed workers with a four-connection
pool, and eight mixed workers writing the same parent. Production database/sql
pool defaults are preserved except for that explicitly capped scenario. Workload
selection rotates deterministically through eight common UI searches.

Searches use the actual submission DAL and compare full result/count digests to
serial warmups. Writes acquire the actual parent lock and rebuild the cache using
the DAL, then roll back. The full cache fingerprint must remain unchanged. Eight
ordinary submissions with 1–50 comments are selected, independently of search
filters. These probes do not insert comments, commit writes, send notifications,
use external files, or execute native PostgreSQL launcher operations.

Reports retain individual operation timings, per-query p50/p95/p99, transaction
begin and parent-lock timings, database/sql pool wait totals and observed peak
connections. An independent connection samples active worker wait-event types
every 50 ms. Wait observations are sampled backend counts, not wait durations;
CPU/runnable means no reported wait event, not measured CPU utilization. Monitor
errors or any result/operation error fail the run. SQL has a 10-second timeout.

Timings include transaction acquisition, query execution, decoding and rollback;
digest verification is outside the timer. Aggregate throughput is completed
operations per elapsed time including final in-flight requests. Search/write
mix varies slightly by scenario because workers finish whole operations. There
is no think time or externally paced arrival rate. This measures DAL contention,
not HTTP throughput or service transaction duration. Short-run tail quantiles,
especially per-query write/lock tails, are exploratory. Rolled-back writes still
produce WAL/dead tuples. Scenarios run in fixed order; repeat with longer and
reordered runs on deployment hardware before selecting pool limits or claiming
capacity. No automatic production tuning is performed.

Artifacts include source hashes, image identity, raw samples, error/completion
status and power snapshots. Run without other database benchmarks or tests.
