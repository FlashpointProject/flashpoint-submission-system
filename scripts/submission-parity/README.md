# PostgreSQL snapshot parity

Run `make snapshot-parity` after `make snapshot-import`. This uses the retained
isolated snapshot project and the latest completed import at migration 17. It
never targets the reference PostgreSQL database or integration-test databases.

The runner freezes its Go sources before building, rebuilds every active imported
submission cache twice, verifies repeatability, refreshes cache statistics, and
compares every cache row with
the rebuilt MariaDB snapshot. Only unordered collections are normalized; NULL,
empty values, duplicate members and filename bytes remain significant. It then
replays the saved standard and repair search corpora, checking complete ordered
results and counts before reporting warm timings and PostgreSQL execution plans.

Artifacts live under the import's `parity-*` directory, with an `exit-code.txt`,
progress log, source hashes, image identity, environment metadata and `data/`
reports. Failed runs retain their evidence. Timings should be collected without
other test/import/database jobs running; the saved MariaDB baseline is a local
comparison, not a guarantee about production hardware.

Overrides: `SNAPSHOT_RUN`, `SUBMISSION_IMPORT_RUN`,
`SUBMISSION_STANDARD_BASELINE`, `SUBMISSION_REPAIR_BASELINE`, and
`SUBMISSION_PARITY_REPETITIONS` (default 3). Baseline overrides are directory names
inside the reference run. Both baselines must have completed successfully.

The live PostgreSQL container must have at least 1 GiB `/dev/shm`, matching the
snapshot Compose definition. Updating Compose does not resize an already-running
container; recreate that one service with its retained data volume before retrying
if the launcher reports the old 64 MiB allocation. Do not remove the volume.

For query-only iterations on an already reconciled imported target, set
`SUBMISSION_PARITY_REBUILD=0`. Full read-only cache comparison still runs before
search; a mismatch fails the run. The default `1` retains both cache rebuilds.
The selected option is recorded in `rebuild-requested.txt`.

On macOS the runner records `pmset -g batt` before and after the database workload
in `power-before.txt` / `power-after.txt`. These snapshots help qualify timings;
they do not establish constant power or thermal conditions throughout the run.

Set `SUBMISSION_UI_BASELINE=search-XXXXXXXX` to include a completed `-suite ui`
MariaDB capture alongside the standard and repair corpora. This adds the actual
page defaults, basic searches, next pages and all seven filter-button presets.

Additional isolated probes:

- `benchmark-other.py --target submission_import_... --project fpfss-snapshot-... --user-id ID --out NEW_DIRECTORY`
  records first/three warm EXPLAIN ANALYZE plans for aggregates, user-action
  histories/counts and similarity candidates. Each statement is read-only and
  limited to 90 seconds. Executor timings exclude network transfer and decoding.
- `compare-similarity.py` takes the same target/project, `--previous-sql FILE`
  and `--out NEW_DIRECTORY`. It compares complete ordered old/current candidate
  rows by count and SHA-256 while preserving NULLs and original text; it saves
  no row contents.
- `cmd/submission-stats-benchmark` measures all fifteen submission statistics
  counters and checks them against full search totals. See its README.

Run these separately from the search timings and integration jobs. Saved SQL,
plans and reports identify the measured query versions; retain failed reports.

`benchmark-indexes.py --config FILE --out NEW_DIRECTORY --target submission_import_... --project fpfss-snapshot-...`
compares saved query/argument JSON under transactional index/statistics phases.
Configuration names each workload file and phase DDL. Both custom and generic
prepared plans run by default; optional `modes` can restrict an explicitly scoped
trial. All DDL rolls back, plans and errors are retained, and `complete: true`
marks success. Run without competing workloads. `cmd/submission-index-writes`
separately measures rollback-verified write costs; see its README.
