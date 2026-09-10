# Bounded index write-cost experiment

Inside the snapshot Docker network, set `POSTGRES_PARITY_DSN` to the disposable
`postgres` service / `submission_import_*` database and run:

```sh
go run ./cmd/submission-index-writes \
  --ddl /results/candidate-indexes.sql \
  --out /results/index-write-costs.json
```

Defaults: 20 ready-for-Flashpoint and 20 ordinary submissions, each with 1–50
comments and current metadata; deterministic ascending-ID selection. Every
operation has one warmup (iteration zero) and three measured repetitions. Two
rounds alternate baseline/candidate then candidate/baseline. Flags
`--cohort-size`, `--repetitions`, `--rounds` allow smaller smoke runs.

Candidate SQL accepts simple semicolon-separated `CREATE INDEX`, `CREATE
STATISTICS`, `CREATE EXTENSION` and `ANALYZE` statements. Transaction control and
DML are rejected. Candidate objects must not already exist in the baseline;
otherwise this does not measure their incremental cost. Do not include functions
or semicolons inside SQL strings. Each phase creates its candidate DDL within a
transaction and rolls the entire transaction back.

Measured operations:

- Actual DAL cache rebuild, checking exact `row_to_json` equality before/after.
- Low-level entry into and departure from the ready-for-Flashpoint predicate;
  the opposite initial state is established outside the measured interval.
- Low-level current-metadata title, alternate-title and platform updates.

Each iteration rolls back to a savepoint and verifies exact cache/metadata row
restoration. Results contain IDs, row counts, timings, WAL bytes, candidate index
sizes and DDL SHA-256, never row contents. An existing report path is refused.

These are database-operation costs, not service end-to-end timings. Savepoint,
validation, cohort selection, DDL and index-build costs are excluded from the
operation timers (DDL time is reported separately). WAL uses server-global
insert-LSN differences: run without competing database jobs. Full-page images,
checkpoint timing and aborted transactions affect WAL; aborted writes still
produce WAL and dead tuples. Alternating phases reduces order bias but does not
restore pristine physical storage. Use a fresh disposable copy for cleaner
physical comparisons if needed. A successful report ends with `Complete: true`.
