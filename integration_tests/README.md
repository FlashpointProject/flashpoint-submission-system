# Integration tests

Run from the repository root:

```sh
bash integration_tests/run.sh
```

Requirements: Docker with Compose, Bash and Git. Go and database clients run inside
the image; no local Go installation, database setup, production dumps, validator,
Discord connection or external submission/image collection is required. The small
tracked archive and mock validator supply the file fixtures.

Select cases using Go test flags:

```sh
bash integration_tests/run.sh -run '^TestHarness'
bash integration_tests/run.sh -run '^TestSubmissionStateMachine_MainFlow$'
```

Each invocation builds a snapshot of the test inputs, starts a uniquely named
Compose project with fresh MariaDB and PostgreSQL volumes, waits for database
health, applies both complete migration chains, and runs the tests. Migrations and
tests use the same image snapshot. The default invocation runs serially, uncached,
with a 20-minute Go test timeout and fail-fast enabled. A selection matching no
tests is an error. Arguments are passed to `go test`, so explicit flags can change
these defaults.

The build allows only source/test inputs: local `.env`, backups, existing
`test_data`, results and the validator submodule are excluded. Runtime containers
have an internal network with service-name addressing, no published ports, and no
Docker socket. The only host bind mount is the invocation's output directory.
The outer script performs all provisioning and cleanup; Go helpers cannot run
Docker or recreate databases.

Before every fixture reset, the helpers verify both generated database identities
and both clean migration versions against the image's migration files. They refuse
ordinary developer connection settings and do not fall back to recreating a
reachable database. MariaDB cleanup uses one reserved connection for foreign-key
settings and resets. Each top-level setup gets temporary writable paths, temporary
fixture/template symlinks, and environment restoration through Go test cleanup.
Nested subtests retain their parent's data where the existing scenarios require it.
Do not add `t.Parallel()` to this suite; `t.Setenv`/`t.Chdir` intentionally reject it.

Results are saved under `integration_tests/results/run-XXXXXXXX/` (gitignored):

- `manifest.txt`, `runtime.txt`, `image-metadata.json`, `source.sha256` and
  `migrations.sha256`: run identity, source revision/dirty status, runtime/images
  and exact input checksums.
- `tests.jsonl`, `coverage.out`, `coverage.txt`: machine-readable test events and
  application-package statement coverage. Statement coverage is not SQL branch
  coverage.
- Build, startup, migration, runner, container and cleanup logs; `exit-code.txt`.

The script returns a failing status for test/provisioning failures, preserves logs,
and removes only its own containers, network, database volumes and built image.
It retains shared Docker build caches and database images. Ctrl+C/termination also
triggers cleanup; if the process is forcibly killed, use the project recorded in
`manifest.txt` to identify leftover resources. Do not run broad Docker prune commands.

For unit tests without Docker, use:

```sh
go test ./database ./types ./service ./transport ./utils -count=1
go test ./integration_tests -run '^TestHarness(RejectsUnsafeTargets|MigrationValidation|TemporaryEnvironment)$' -count=1
```

Running the database-backed cases directly on the host is intentionally unsupported;
use the runner. Old `absolute.env`, `test_data` and developer database containers
are neither used nor deleted by this setup. `make -C integration_tests test` is an
alias for the full containerized run; the old rebuild/migrate targets are removed.

Scope limits: this is a correctness harness, not the production-dump replay or
performance harness. Lookup seed rows are preserved rather than reconstructed
after every test, so tests that mutate those rows must restore them. Successful
uploads wait for persisted processing results, but failed asynchronous uploads do
not yet have an explicit worker-drain API. Fail-fast limits subsequent contamination;
worker lifecycle coverage is follow-up work before introducing parallel fixtures
or injected upload-worker failures.

## Generated state histories

The generated tests complement the hand-written histories and real-service tests.
They use an independent Go replay model to check evolving cache/search state across
several users and submissions. Default histories stay within 50 comments and four
upload versions per submission; this is correctness coverage, not a load test.

Run the pure model/corpus tests without databases:

```sh
go test ./integration_tests -run '^Test(SubmissionHistoryModel|SubmissionGeneratedHistoryCorpus)$' -count=1
```

Run the default SQL corpus or replay one seed:

```sh
bash integration_tests/run.sh -run '^TestSubmissionGeneratedHistories$'
bash integration_tests/run.sh -run '^TestSubmissionGeneratedHistories$' -args -history-seed=42
```

A failure logs the seed, operation index and `HISTORY_TRACE_JSON` containing the
exact operation prefix in `tests.jsonl`. In Docker it also saves a
`history-failure-seed-N-step-N.json` file alongside the run artifacts. Copy that
JSON array under
`integration_tests/testdata/histories/` to replay it independently of future RNG
or generator changes, and reduce it into a focused regression when diagnosing a bug:

```sh
bash integration_tests/run.sh -run '^TestSubmissionGeneratedHistories$' -args -history-replay=integration_tests/testdata/histories/example.json
```

That narrow JSON directory is included in the Docker build; arbitrary host paths
and the ignored results directories are not. Replay traces use the test fixture's
three submissions and four human user IDs plus the validator. Preserve any source
operations that later deletions refer to when reducing a trace.

## Notification SQL and controlled concurrency

Run the recipient/settings/queue contract tests with:

```sh
bash integration_tests/run.sh -run '^TestNotificationQueries'
```

The tests distinguish unique recipients from duplicate stored preferences, and
leave equal-time queue entries unordered until an explicit tie policy is adopted.
`CurrentBehavior`/`CurrentBug` tests record named limitations in
`database-unification.md`; a passing witness is not evidence that its underlying
behavior is correct or should be retained in PostgreSQL.

Run controlled mutation overlaps, including the serial control, with:

```sh
bash integration_tests/run.sh -run '^TestSubmissionConcurrency'
bash integration_tests/run.sh -race -count=3 -run '^TestSubmissionConcurrency'
```

The test triggers signal arrival after service validation, using MariaDB named
locks or PostgreSQL advisory locks, then wait for explicit release. Each commit is awaited before the
next release; no timing delay determines the outcome. Cleanup releases locks and
joins workers before closing pools. The upload/delete overlap uses direct
committed file/DAL mutations, not asynchronous archive processing. The backend-specific barriers preserve the same interleaving.
The earlier stale-cache witnesses are now rebuild-equality regressions. A separate
test observer checks pending parent-lock statements in MariaDB PROCESSLIST or PostgreSQL pg_stat_activity
while the first writer holds the parent, before competing writers read state; application database privileges remain unchanged. Go's race detector checks Go memory access;
source/cache/search assertions establish the separate SQL behavior.

### PostgreSQL submission parity

`SUBMISSION_DB_ENGINE=postgres bash integration_tests/run.sh` executes the same
synthetic suite with the submission DAL connected to PostgreSQL. The existing
PostgreSQL DAL uses that same fresh database. The default remains `mariadb` for
before/after comparison; `make -C integration_tests test-both` runs both engines
sequentially. Each invocation has its own project, volumes, generated database
name and result directory. The manifest records the selected submission engine.
Both schemas are migrated and verified before reset, including when the MariaDB
service is only a baseline reference. Reset retains submission lookup seeds and
recreates the two built-in users. Production snapshots are never test targets.

Fixture `testSQL` only translates positional parameters and fixture INSERT IGNORE;
application SQL is exercised through the actual backend implementation. Engine-
specific trigger and lock tests provide PostgreSQL equivalents, not skips.

The few intentional before/after differences are asserted explicitly: PostgreSQL
returns search rows/count from one snapshot, rejects duplicate cache/subscription
keys, and stores repeated subscriptions/preferences once. The MariaDB branches
retain their original bug witnesses. Concurrent subscription tests verify both
successful commits and identical recipient selection, while persisted pair counts
show the deliberate duplicate prevention. Explicit-ID fixture helpers advance
PostgreSQL identity sequences to reproduce MariaDB's AUTO_INCREMENT behavior for
subsequent service writes.

The MariaDB container uses `utf8mb4_unicode_ci` for its fresh schema, matching
all audited production application table defaults. After source migrations, the
runner sets only the two `oauth_client` text columns to `utf8mb4_general_ci`,
preserving its Unicode table default; MariaDB's JSON alias independently gives
`curation_meta.additional_applications` its source `utf8mb4_bin` collation.
Every reset preflight checks schema, table and text-column collations. Historical
migrations are not rewritten to reproduce defaults that changed during production
history.

This alignment fixes a harness gap discovered by the similarity collision test:
fresh MariaDB tables previously inherited `utf8mb4_general_ci`, while production
uses `utf8mb4_unicode_ci`. Full-width decimal identities compare differently in
those collations. Read-only production-schema probes confirmed the PostgreSQL
similarity Unicode comparison was correct; the old synthetic MariaDB failure was
not a production-query regression. The identity fixture avoids trailing UUID
spaces because MariaDB `CHAR(36)` trims them at storage/read time, independently
of comparison semantics.
