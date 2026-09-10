# Submission text compatibility

Migration `0015` adds helpers used only by the migrated MariaDB submission DAL.
It does not alter any original PostgreSQL table, collation, or query.

- `submission_like(value, pattern)` implements `utf8mb4_unicode_ci` LIKE.
  Patterns use `%`, `_`, and backslash escaping. Literal character equivalence
  uses MariaDB's frozen UCA4 weights. One pattern character still consumes one
  value character: `ß` equals `ss` under equality, but does not LIKE-match it.
  Combining sequences preserve character boundaries. NULL propagates.
- `submission_text_key(value)` implements UCA4 primary equality keys, including
  MariaDB PAD SPACE behavior. Use it for submission text equality and unique
  expression indexes. `submission_equal(left, right)` wraps key equality.
- `submission_general_text_key(value)` preserves `utf8mb4_general_ci`, used by
  the source OAuth client ID and secret columns. Other migrated text columns
  use `unicode_ci`; the additional-applications JSON column was binary.

These helpers preserve original text and row IDs. PostgreSQL still rejects NUL
text and the importer must fail its preflight for such rows. ICU/unaccent is
not substituted: it changes expansion, Unicode-version, and character-boundary
behavior. Existing PostgreSQL additional-app orphans are unrelated and untouched.

The mapping is generated from public collation weights, not production data.
The source behavior is defined by MariaDB 11.1.5
[`my_uca_charcmp_onelevel`](https://github.com/MariaDB/server/blob/mariadb-11.1.5/strings/ctype-uca.c)
and its [implicit weight formulas](https://github.com/MariaDB/server/blob/mariadb-11.1.5/strings/ctype-uca.h).
Missing UCA4 pages use literal codepoints for LIKE, whereas supplementary-plane
characters share `FFFD` for equality. This surprising distinction is intentional.

## Reproduce

Use the isolated snapshot MariaDB container and a **new disposable** PostgreSQL
database, replacing the container names below. The source queries read no rows
from any application table. Never apply the migration to the reference snapshot.

```sh
docker exec -i "$MARIA_CONTAINER" mariadb -uroot -psnapshot-root \
  --default-character-set=utf8mb4 --batch --skip-column-names fpfss \
  < scripts/submission-collation/weights.sql > /tmp/submission-weights.tsv
python3 scripts/submission-collation/generate.py /tmp/submission-weights.tsv \
  /tmp/submission-text-semantics.sql
cmp /tmp/submission-text-semantics.sql postgres_migrations/0015_submission_text_semantics.up.sql

docker exec "$PG_CONTAINER" createdb -U snapshot_admin submission_collation_probe
docker exec -i "$PG_CONTAINER" psql -U snapshot_admin \
  -d submission_collation_probe -v ON_ERROR_STOP=1 \
  < postgres_migrations/0015_submission_text_semantics.up.sql
python3 scripts/submission-collation/probe.py \
  --maria-container "$MARIA_CONTAINER" --postgres-container "$PG_CONTAINER" \
  --postgres-database submission_collation_probe --weights /tmp/submission-weights.tsv
```

Validated on MariaDB 11.1.5 / PostgreSQL 15.18: **6,945 LIKE cases, 6,945 equality
cases, 126,974 individual Unicode/general collation keys; zero differences**.
The key check covers every PostgreSQL-representable BMP character under both
collations, and the pair matrix adds supplementary characters, expansions,
accents, case, trailing spaces, wildcards, metacharacters, escaping and NULL.

`EXPLAIN ANALYZE` confirms a constant LIKE pattern is compiled during planning
and the SQL helper inlines to PostgreSQL's native regex operator. A disposable
100,000-row synthetic title scan took 87 ms execution / 32 ms planning locally;
this is an implementation smoke check, not the production search benchmark.

## Integration requirements

Ordinary PostgreSQL `=`/`LIKE` are not replacements for these helpers. Route
submission text comparisons through the appropriate source-collation helper;
use unique expression indexes to enforce the same duplicate rejection rules.
Bytea keys model primary ordering, but any change of existing SQL ordering or
UNION deduplication also needs to preserve its original collation and tie rules.
Indexes depending on these immutable functions must be rebuilt if mapping data
is ever changed. Do not change this frozen mapping merely because the target
PostgreSQL/ICU version is upgraded.
