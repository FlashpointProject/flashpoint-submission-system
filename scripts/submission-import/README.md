# Submission database import rehearsal

Run `bash scripts/submission-import/launch.sh` after the isolated snapshot restore.
`SNAPSHOT_RUN` can select a completed `snapshot-results/run-*` reference.
Each invocation creates **a new** `submission_import_*` PostgreSQL database by
cloning the existing PostgreSQL reference, applies the new submission migration,
and imports the MariaDB reference over a read-only repeatable-read transaction.
The reference databases are never reset, updated, or used by app workers.
`CREATE DATABASE ... TEMPLATE` requires the reference to have no active clients;
stop another snapshot operation and retry if PostgreSQL refuses. The wrapper never
terminates reference connections. No external archive/image files are required.

Artifacts live in the reference's `import-*` directory; `latest-import` identifies
the most recent attempt. The retained target name is in `target-database`.
`results/manifest.json` records migration versions, per-table source/imported row
counts, transformed-source and target canonical SHA-256 digests, sequence next
values, FK/duplicate checks and before/after original-catalog row fingerprints.
`results/discarded-subscriptions.jsonl` maps every collapsed ID to its retained ID.
The SHA-256 comparison covers *all* imported columns with row order by source ID,
including NULL/empty distinctions, 64-bit IDs and six-digit timestamp precision.
Catalog fingerprints combine row count and an order-independent sum of PostgreSQL
64-bit row hashes; these are change detectors, not cryptographic canonical hashes.
Input dump hashes, source-code hashes, revision, image ID, timings and exit status
are recorded outside the manifest. Logs contain counts and identifiers, not
row payloads or auth values.

The importer refuses source auth rows and unrecognized hosts/database names. It
only mutates a fixed list of new submission tables. All loading is atomic; a COPY,
constraint or checksum failure rolls back imported rows and restores verified
bootstrap rows. Rerunning against a successfully populated target is refused;
rerun the wrapper to get another fresh target. Sequence setval is PostgreSQL
nontransactional, but only new submission sequences are touched and retry resets
them from source high-water marks before any successful commit. No preexisting
catalog sequences are touched.

Bootstrap enum definitions must exactly match source rows. The two known
bootstrap Discord user IDs may have changed profile fields since migrations were
written; their complete source profiles replace target bootstrap profiles. Extra
target users are refused. All IDs and source rows remain intact except subscription
duplicates: retain earliest created_at, then lowest ID within a user/submission
pair. No metadata, comment, file, preference, role or cache rows are silently merged.
The quota coordination table starts empty. Original cache rows, including deleted
parents, are imported exactly; subsequent cache rebuilding and comparison belongs
to the PostgreSQL parity command and is intentionally a separate measured stage.

Text is validated as UTF-8 without NUL, JSON text remains JSON text, bool domains
must be 0/1, zero timestamps fail, and all declared source foreign keys are checked.
PostgreSQL target constraints provide a second validation layer. Existing catalog
orphans are retained. Cross-catalog missing relations/UUID comparisons are audit
questions, never reasons to rewrite game_exists or discard source history.

Focused normalization/checksum tests: `go test ./cmd/submission-import`.

Compare two successful result directories with
`python3 scripts/submission-import/compare.py FIRST/results SECOND/results`.
This checks every table's canonical hashes, row accounting and sequence high-water
marks, the exact subscription discard mapping, shared audit results, and unchanged
original PostgreSQL fingerprints across targets. Different import timings are
ignored. A later target may contain new submission-only comparison lookup tables.

`relationship-audit.sql` provides identifier-only details for the snapshot's
noncanonical metadata UUIDs and historical events with missing comment parents.
Run it read-only on the imported target; these observations do not authorize
rewriting existing catalog data or dropping submission history.
