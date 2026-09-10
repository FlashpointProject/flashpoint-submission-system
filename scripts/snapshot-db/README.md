# Production snapshots in disposable Docker databases

Run from the repository root with Docker, Python 3 and `zstd` available:

```sh
make snapshot-restore
make snapshot-status
make snapshot-audit
make snapshot-cache
```

Restore selects the single root `fpfss-mariadb-dump-*.sanitized.sql.zst` and
`fpfss-postgres-dump-*.sql.zst`. Override with `MARIA_DUMP` and `PG_DUMP` environment
variables if needed. MariaDB input must have the `.sanitized.sql.zst` suffix;
after restoration the runner also requires both auth tables to be empty.

Every restore creates a new project and volumes, with no published ports and an
internal Docker network. It starts only databases, never the application or its
workers. Neither root `.env` nor integration-test configuration is loaded. Do not
mix snapshot and development Make targets in one invocation. Integration fixtures
must never be pointed at these databases: they reset their data.

The PostgreSQL input is a cluster-level `pg_dumpall --clean` snapshot. A separate
bootstrap database/user allows its database recreation commands to run. The
streaming adapter makes role/database drops conditional and replaces role password
clauses with `PASSWORD NULL`, preserving COPY data. It does not print credentials
or rewrite the source archive. This is intended for these trusted repository
snapshots, not as a general SQL sanitizer for arbitrary input.

Artifacts and the latest run pointer live in ignored `snapshot-results/`. Source
dump hashes, source revision, image IDs, restore logs and exit status are retained.
The audit writes aggregate/metadata TSV files, never application row contents.
See `../snapshot-audit/README.md` for the checks and their limitations. Preserve
this directory alongside the Docker volumes for future comparisons.

`snapshot-cache` first clones the MariaDB database, preserving its original cache
as `snapshot_cache_work_scoped.submission_cache_snapshot_original`. It does not
modify the restored `fpfss` reference. It refuses to overwrite an existing clone;
start a new restore for a clean rerun. The cache audit cross-checks selected full
database rebuilds against the same production SQL over bounded input tables, then
visits every active submission twice. Each input batch contains complete histories
(including deleted records), and results write into the full disposable cache.
The JSON report distinguishes raw and collection-order-normalized changes. NULL,
duplicates and exact filename strings remain significant. This static audit does
not measure full-source rebuild performance or validate concurrency. Nonzero exit
means a failed/incomplete rebuild or non-repeatability; inspect the report before
using it as a baseline.

The running tool uses a separately built Go image with an allowlisted build
context: dumps and local configuration do not enter the image. Cache logs and
`cache/cache-reconciliation.json` remain in the run directory.

Commands normally select `snapshot-results/latest`. To select an older run:

```sh
SNAPSHOT_RUN="$PWD/snapshot-results/run-XXXXXXXX" make snapshot-status
```

When finished, `make snapshot-down` removes only the selected snapshot project's
containers, volumes and cache-runner image. It deliberately keeps result files.
The cache's preserved original table is in a Docker volume and disappears with
that volume; retain the source dumps to recreate it. No external submission/image
files are needed for this database-only workflow.

Tooling checks:

```sh
python3 -m unittest discover -s scripts/snapshot-db -p 'test_*.py'
go test -race ./cmd/snapshot-cache-audit
bash -n scripts/snapshot-db/run.sh
```

Make targets use `launch.sh`, which reads the runner in full before executing it;
editing the source during a long restore cannot affect that running script.
`restores-complete` is written after both database imports and auth verification.
Use the exit-status files and this readiness marker together when investigating a
failed invocation. The snapshot images pin MariaDB 11.1.5 and PostgreSQL 15.18;
image IDs in each run record the actual builds used.

## Search correctness and latency baseline

After successful cache reconciliation, run `make snapshot-search`. Every invocation
creates a fresh `search-XXXXXXXX` artifact directory under the selected snapshot
run; it never replaces an earlier corpus. `SNAPSHOT_SEARCH_REPETITIONS=3` controls
the number of warm repetitions (minimum one). The runner requires successful,
complete and repeatable cache traversal before calling the clone “rebuilt”.

The tool executes the current `SearchSubmissions` DAL against the full original
and rebuilt databases, using read-only sessions on the internal Docker network.
It does not run integration fixtures, mutate caches or access external files.
Workload parameters are selected deterministically from the original snapshot and
saved once in `workloads.json`, then reused for both cache variants. The workloads
cover broad/selective filters, legacy unions, all three sorts, pagination, a busy
submitter, subscriptions and positive/negative membership in all five reviewer
sets. They are a fixed migration corpus, not a traffic-frequency model.

Each workload saves a full ordered `corpus.json` (count and projected rows), the
actual parameterized page/count SQL and arguments, and both JSON EXPLAIN plans.
Reviewer/action collections are sorted without removing duplicates. Result order,
NULLs, empty values, IDs/UUIDs and timestamps remain significant. Every repetition
must match its initial corpus and page cardinality must agree with total count.
These local ignored artifacts contain actual application projections and filter
values; do not commit them. Captured behavior supplements the independent
synthetic expectations; it cannot establish that every historical value is right.

`report.json` separates first-observed, warm DAL, and serial standalone page/count
latencies. First-observed is **not cold**: no restart or cache eviction occurs.
There is one logical request at a time; the production DAL still runs page and
count concurrently on separate connections. Standalone measurements drain SQL
rows and exclude DAL projection. Three warm samples are useful for a first local
comparison, not a defensible p95/SLA. Estimated EXPLAIN plans are not actual row
counts or EXPLAIN ANALYZE. Docker CPU/memory/I/O snapshots, engine variables,
source hashes, image ID and exit status accompany the result. Run without
simultaneous restore, audit, integration tests or other database workloads. Cold
starts, sustained concurrent load and a PostgreSQL window-count alternative need
separate controlled experiments after the port exists.

A nonzero exit or absent `complete` marker means the run is incomplete/failed;
inspect progress and the partial report. Successful original/rebuilt differences
are reported separately and do not fail the run: reconciled history legitimately
changes some caches. Review those differences before adopting the rebuilt corpus
as the PostgreSQL parity reference.

For targeted reconciliation witnesses, run
`SNAPSHOT_SEARCH_SUITE=repairs make snapshot-search` after the standard run finishes.
This derives filename/hash filters from source files for caches with changed
collections and captures bounded batches of every changed approval/comment-pointer
projection. It prevents first-page sampling from hiding known repairs. Its small
ID-constrained workloads are correctness witnesses, not representative latency
measurements. It also writes a fresh directory and retains original/rebuilt
results separately. Do not run the two suites concurrently when measuring latency.
