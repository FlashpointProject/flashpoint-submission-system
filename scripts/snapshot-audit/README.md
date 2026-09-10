# Restored-snapshot audit

`audit.py` runs read-only schema and aggregate queries against an already restored,
isolated Docker Compose project. It does not inspect application row contents or
credentials. Use the separate snapshot restore tooling to provision that project.

```sh
python3 scripts/snapshot-audit/audit.py \
  --compose PATH/compose.yml --env-file PATH/snapshot.env \
  --project PROJECT --output OUTPUT_DIRECTORY \
  --mariadb-database DATABASE --postgres-database DATABASE
```

Service names default to `mariadb` and `postgres`; PostgreSQL user defaults to
`postgres`. Override with the corresponding `--*-service` / `--postgres-user`
flags. Select one engine with `--engine mariadb` or `--engine postgres` when
restoration is staged. MariaDB obtains the synthetic container password internally from
`MARIADB_ROOT_PASSWORD` (or `MYSQL_ROOT_PASSWORD`). The project must contain trusted
snapshot containers; this is not a production-host auditing tool.

Outputs include exact table row counts, schema/column/index metadata, migration
versions, all-column NULL counts, temporal minima/maxima, MariaDB zero-date and
PostgreSQL infinity counts, and orphan counts for every declared foreign key.
Composite foreign keys are checked as a tuple. Nullable foreign keys are excluded
from orphan counts, matching normal database constraint semantics.

Additional MariaDB checks count duplicate cache, subscription, preference and
metadata keys; missing caches; caches for deleted submissions; eligible submissions
without live files; tied timestamps; deletion/send times before creation; and both
authentication table row counts. Cache pointer checks also detect missing targets,
wrong submission ownership and deleted file/comment targets, which ordinary foreign
keys cannot fully validate. For duplicate checks the first count is the number
of duplicated key groups and the second is the number of excess rows.

The live-comment history summary reports the number of submissions with comments,
maximum history length, and histories above 50 comments. Nonzero fractional-second
counts distinguish newer microsecond data from historical second-resolution data.

Timestamp ranges can reflect meaningful sentinel values (for example the migration's
`1000-01-01` fallback); the audit reports them without deciding to rewrite them.
PostgreSQL also checks fifteen current-entity relationships without foreign keys
(see `postgres-logical-references.sql`), including aliases, game relations, additional
apps and active game data. Changelogs and undeclared cross-database relationships
are intentionally excluded: historical records may legitimately outlive parents.
The logical checks disable parallel workers only for their connection to fit default
Docker shared memory. Zero declared-FK orphans does not establish every logical
relationship.
Each command fails closed on SQL failure; server error text is suppressed because
some diagnostics can include row contents. `audit-complete.json` is only created
when all queries succeed. Use a fresh output directory per run.

These aggregate scans can take time on large tables. Run with the snapshot idle;
separate client invocations intentionally do not promise a cross-query or cross-
database transaction snapshot under concurrent writes.
