# Committed snapshot workflow check

Run this only on fresh disposable clones. Unlike `integration_tests`, it neither
resets a database nor starts background delivery/validator/archive workers. It
leaves committed test history in the clone for inspection. It refuses preexisting
reserved actors, unmanaged host/database names, or reuse of imported identities.

Inside the snapshot Compose network, build/run the same frozen source twice:

```sh
WORKFLOW_SUBMISSION_DSN='user:password@tcp(mariadb:3306)/snapshot_workflow_rehearsal' \
WORKFLOW_NATIVE_PG_DSN='postgres://user:password@postgres:5432/snapshot_workflow_native?sslmode=disable' \
./submission-workflow --engine mariadb --out maria-workflow.json

WORKFLOW_SUBMISSION_DSN='postgres://user:password@postgres:5432/submission_import_workflow_rehearsal?sslmode=disable' \
WORKFLOW_NATIVE_PG_DSN='postgres://user:password@postgres:5432/submission_import_workflow_rehearsal?sslmode=disable' \
./submission-workflow --engine postgres --out postgres-workflow.json
```

Both reports must have `Complete: true`, equal `TranscriptSHA256`, and identical
`Steps`. Compare those fields, not the complete envelope: physical IDs and source
sequence allocation positions can legitimately differ. Every generated submission,
file and comment ID is checked against both pre-run `MAX(id)` and the actual next
sequence/AUTO_INCREMENT position. Gaps are retained rather than renumbering data.
The three fixed high-ID test actors are checked absent before insertion.

The checkpoints exercise real committed service assign/request/approve/verify and
comment operations, implicit unassignment, per-user and optimized Ready filters,
platform/title filtering, subscription persistence, metadata change, replacement
upload boundary, deletion of the replacement and verification, rejected self
assignment, an explicit multi-write rollback, and a subsequent committed comment.
Each successful checkpoint compares its incremental cache and search projection
with a fresh scoped rebuild, plus independent expected reviewer state. All five
reviewer collections, full visible comments/search fields, raw cache columns,
activity-event payloads, queued notification text and deletion reasons are retained.
Only unordered collections and generated IDs are canonicalized. Native activity
and database-side deletion/notification wall-clock timestamps are checked inside
the execution window, then omitted from the cross-run transcript. Service history
uses an injected fixed microsecond clock; stored history is never rewritten.

File insertion and initial upload/validator comments use real DAL methods with
synthetic metadata. The metadata update uses scoped SQL. This check does **not**
claim HTTP authorization, validator/archive processing, actual image/filesystem
storage, or external notification delivery coverage; existing isolated integration
tests cover the small archive upload/HTTP paths. No production or original reference
database may be supplied. A failed run is retained for inspection and must be retried
on fresh clones. Existing imported data preservation is checked by the surrounding
rehearsal, not by blindly hashing every table after the intentional new writes.
