# MariaDB search snapshot profiling

Requires Docker Compose, Bash, Git, zstd and shasum. Restore only a sanitized
MariaDB dump with empty `session` and `oauth_client` tables:

```sh
bash scripts/mariadb-search/run.sh restore /path/to/dump.sanitized.sql.zst
```

The command prints an absolute artifact directory. Use it for subsequent actions:

```sh
bash scripts/mariadb-search/run.sh benchmark /absolute/run/directory before -repetitions 3
# After changing the implementation, repeat on the same immutable dataset.
bash scripts/mariadb-search/run.sh benchmark /absolute/run/directory after -repetitions 3
bash scripts/mariadb-search/run.sh down /absolute/run/directory
```

Each restore owns a generated Compose project, internal network and named volume.
There are no published ports and no production `.env` settings. The default
MariaDB version is 11.1.5 with a 1 GiB InnoDB buffer pool; change these explicitly
when investigating a different deployment. Restoration writes only that fresh
local database. Benchmarks force read-only database sessions and use at most four
connections. `down` removes only the selected project's containers, volume and
benchmark image; artifacts and shared images remain.

Each benchmark saves revision and source checksums, its working-tree diff and
image metadata; `report.json`, workload filters, page/count query text and bound
arguments, EXPLAIN JSON plans, and canonical complete result/count digests. The
first observation is not a cold-cache measurement. Warm repetitions must retain
identical digests. Collection-valued fields are sorted without dropping duplicates;
row order and NULL versus empty values are retained.

Use `-suite ui` for the existing UI presets, or `-workload submission-id` for
a bounded single-workload check. The lower-level Go tool also retains
an optional `rebuilt` variant from the source branch; it requires a separately
prepared `snapshot_cache_work_scoped` database and is not provisioned here.
The normal runner always selects the unchanged `original` snapshot.

Compare reports only for identical saved workloads and unchanged database state.
These are serial DAL timings, not concurrent HTTP or production-capacity results.
Query text and result corpora contain snapshot data and stay in gitignored local
artifacts. Commit aggregate findings, never raw production-derived rows.
