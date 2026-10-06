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

## Search quick filters

```sh
bash integration_tests/run.sh -run '^TestSubmission(QuickFilter|UploaderFilter|FilterLayoutSwitch|Search)'
```

The image includes Node and a locked, test-only jsdom dependency. These tests
render the production filter template, execute the actual quick-filter JavaScript
in a DOM, serialize its form, and pass that query through authenticated search
requests. Multi-user histories upload real fixture archives with a mock validator
and check review requests as well as search rows and counts. All seven presets
are checked in both layouts; the original-submitter and latest-uploader options
are checked separately and together. jsdom checks form behavior, not visual layout.
Layout-switch tests cover shared edits, cleared selections, retained advanced
drafts, reset, URL preservation, and switching without submitting a search.

Run the additional metadata search cases with:

```sh
bash integration_tests/run.sh -run '^TestSubmissionSearchMetadata'
```

These cover every new text field, case-insensitive partial matching, Tags
include/exclude combinations, legacy metadata, current versus historical/deleted
versions, NULL/empty metadata, additional-application lists, combined filters,
and pagination counts. Form tests serialize the rendered advanced controls and
send them through authenticated submissions and my-submissions HTTP requests.
The quick-filter and layout-switch cases also check clearing the new dropdown
and retaining metadata drafts without changing basic-search scope.

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

## Notifications and controlled concurrency

Run notification SQL contracts and controlled mutation overlaps with:

```sh
bash integration_tests/run.sh -run '^TestNotificationQueries'
bash integration_tests/run.sh -race -count=3 -run '^TestSubmissionConcurrency'
bash integration_tests/run.sh -race -run '^TestSubmissionQuota'
```

The trigger barriers use MariaDB named locks to control arrival and release.
A separate observer checks waiting parent-lock statements in PROCESSLIST.
Cleanup releases locks and joins workers before closing pools. Go race detection
checks memory access; history/cache/search assertions check SQL behavior.

Tests named `CurrentBehavior` document remaining MariaDB limitations, including
repeated subscription/settings rows. Passing these witnesses does not mean the
behavior is desirable. The runner always uses MariaDB for submissions and the
existing separate PostgreSQL database for launcher metadata.

The MariaDB fixture uses production's `utf8mb4_unicode_ci` defaults and the
historical OAuth columns' `utf8mb4_general_ci` exception. Every reset checks these
collations as well as database identities and migration versions.
