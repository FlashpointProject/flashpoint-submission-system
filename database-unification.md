# Database unification: MariaDB into PostgreSQL

Status: implementation and validation, updated 2026-09-10. The MariaDB submission schema and DAL now have a PostgreSQL implementation, a tested importer and cross-backend integration coverage. Existing PostgreSQL application tables and DAL logic are preserved. Production-snapshot import and complete real-data PostgreSQL cache/search comparisons have passed. Nothing has been deployed to production.

## Verified status at the current checkpoint (2026-09-10)

| Work | Status |
| --- | --- |
| Isolated Docker harness and SQL/service regression coverage | Final rehearsal race suites: PostgreSQL `run-HeOOV9lq`, 91 top-level / 423 test and subtest passes, 160.881 seconds; MariaDB `run-rHs8yAbY`, 91 / 423, 145.045 seconds. Includes the upload-progress race correction discovered during this rehearsal. Earlier evidence remains below. |
| Sanitized snapshot restore and audit | Both isolated references restored and audited. Original references are retained. |
| MariaDB cache reconciliation | All 148,164 active submissions rebuilt twice on a disposable clone; no failures, second pass identical. |
| MariaDB search baseline | 28 standard and 14 repair workloads, original/rebuilt cache variants; 24 additional actual UI workloads on rebuilt caches. Three warm repetitions each. |
| PostgreSQL schema and text semantics | Migrations 14–17 add submission objects only. Source-compatible text equality/LIKE verified against MariaDB: 6,945 LIKE, 6,945 equality and 126,974 character-key comparisons, zero differences. |
| Importer and production-shaped import | Clean recovery run `import-15s8rPZG`, target `submission_import_import_15s8rpzg`, migration 17; strengthened collation and column-completeness preflight passes. 3,340,777 source rows become 3,305,411 target rows; the difference is exactly 35,366 duplicate subscriptions. All 19 imported-table canonical checksums match. |
| Existing PostgreSQL preservation | All 23 original application-table count/fingerprint pairs unchanged by the latest import, including existing orphans. Original migration history advances to 17 only in the imported clone; source references remain untouched. |
| Import repeatability | Second import agrees with the first successful import on canonical checksums, counts, sequence high-water marks, all discarded subscription ID mappings and original PostgreSQL fingerprints. Populated-target retry is rejected. |
| PostgreSQL submission DAL/search/cache | Implemented and exercised by the integration suite. Select using `SUBMISSION_DB_ENGINE=postgres`; existing native PostgreSQL DAL remains unchanged. |
| Real-data PostgreSQL cache/search parity and performance | Performance reference `parity-w2NX2F7v` passed (correctness reconfirmed in `parity-wWCn5SWn`): all 148,355 cache rows match semantically, 66/66 workloads and 264 DAL calls match. UI default/Next 102/92 ms; platform first/Next 228–328 ms; Ready for Flashpoint 5.8/5.2 ms. Other presets and titles have mixed run-to-run results; see the complete table below. Similarity candidates identical in order, executor median 17.8 → 3.4 seconds. All timings remain provisional; exact query/plan comparisons and power snapshots are recorded below. |
| Bounded concurrency investigation | `concurrency-4vpFTvXx`: 2,378 operations, zero errors/result mismatches, full cache fingerprint unchanged. Up to eight clients; no tuning justified. See the bounded scope and pool-limit caveat below. |

Implementation is in the working tree. `make snapshot-import` creates an isolated
imported clone; `make snapshot-parity` rebuilds and compares it against retained
MariaDB evidence. These are separate from destructive integration fixture resets.
The original PostgreSQL workload keeps its existing tables, code and transaction
interface. Submission transactions now run on PostgreSQL when selected, but the
two DAL interfaces still own separate transactions: this change does not claim
new cross-domain atomicity. A future transaction consolidation would be a
separate behavior change requiring its own review and tests.

## Objective and recommended sequence

Run the submission system and existing launcher/metadata/indexing workload in one PostgreSQL database. The immediate deliverable should be a trustworthy behavioral test suite for the current implementation, especially search, submission state calculation, and ordering. Use that same suite to validate the PostgreSQL implementation. Schema design, rollout, and optimization below are initial proposals to refine after these tests and production-dump measurements exist.

The recommended sequence is:

1. Make the test environment reproducible; record current behavior and resolve ambiguous contracts.
2. Add deterministic SQL contract tests and strengthen the existing integration tests on MariaDB.
3. Capture a production-shaped correctness and performance baseline using copies of both production databases.
4. Implement a PostgreSQL schema/data transfer and a behavior-preserving submission DAL port, preserving existing PostgreSQL logic and transaction ownership.
5. Optimize individual queries and indexes, rerunning parity and performance checks after each change.
6. Rehearse cutover and recovery, then retire MariaDB after validation.

Moving engines alone is not evidence of a speedup. Keep the semantic port and later optimizations separately measurable. Likewise, do not silently turn suspected bugs into permanent compatibility requirements: record each discrepancy, decide the intended behavior, and test an intentional fix separately.

## Original architecture (before this implementation)

| Area | Current implementation | Migration implication |
| --- | --- | --- |
| Submission data | `database/mysqldal.go`; `migrations/0001` through `0028` | Users/roles, sessions, OAuth client secrets, submissions/files/metadata/images, comments/actions, notifications/subscriptions, submission cache, and legacy master database games must move. |
| Submission search | `database/searchmonster.go: SearchSubmissions`, `addMultifilter`; `types/types.go: SubmissionsFilter.Validate` | Dynamic filters over cached state, joined metadata, and legacy rows. Test the result projection and count, not just membership. |
| Submission state | `database/caching.go: UpdateSubmissionCacheTable` and helpers | State is a derived projection of comment/file history, combined with Go validation in `service/comments_action_validator.go` and mutation orchestration in `service/siteservice_comments.go`. |
| Existing PostgreSQL | `database/postgresdal.go`; `postgres_migrations/0001` through `0013` | Launcher game/tag/platform metadata, changelogs/triggers, game data/indexes, redirects, activity events. These must retain behavior and performance. |
| Database abstraction | `database/interfaces.go`: `DAL`/`DBSession` and `PGDAL`/`PGDBSession` | Two driver-specific abstractions: `*sql.Tx` versus `pgx.Tx`. There is no transparent shared transaction across them. |
| Service composition | `service/siteservice.go`, `service/siteservice_comments.go`, submission receivers | Repeated begin/rollback/commit code; some operations open both engines and commit them separately. Cross-domain commits are not atomic; changing their ownership is outside this behavior-preserving port. |
| Infrastructure and tests | Root and `integration_tests/` Compose files, Makefile, configuration, integration helpers | Two database services, migration chains, drivers, connection configurations, and cleanup implementations. |

Source declarations establish repository behavior, not the deployed schema/version or actual query plans. The restored-snapshot audit below now records production migration histories and schema/data metadata; the completed MariaDB query-plan baselines are recorded below; PostgreSQL plans remain outstanding.

## Implementation progress

The first environment slice is implemented and the full existing integration suite passes: a containerized Go runner, one disposable Compose project and database pair per run, fresh migrations from the same image snapshot, guarded serial fixture resets, and temporary per-setup files/environment. See `integration_tests/README.md` for commands and artifacts, and the validation history below for measured results.

This work does not need production dumps. The existing small archive fixture and validator mocks suffice for the current integration suite. Real dumps remain inputs to later reconciliation and performance work; copying terabytes of external files is not required for database search/state/migration tests.

The next test slice implements A's action-validator repairs and fast DAL fixtures, and a substantial first part of B's search/state/ordering matrix. The subsequent bugfix slice below updates production queries and validation; schema migrations remain unchanged. Synthetic histories intentionally target edge cases; future production samples should supplement these fixtures, especially for collation, dirty data, long histories and performance, rather than replace their controlled coverage.

### Implemented test slice

| File | Implemented coverage |
| --- | --- |
| `service/comments_action_validator_test.go` | 29 valid/rejected pairs establish prerequisites and change the relevant condition, then assert the exact public error/status. Repeated change requests and previously hidden discrepancies now have the approved behavior regressions. |
| `types/submissions_filter_test.go`, `database/searchmonster_test.go` | Filter choices, numeric boundaries, normalization, dependency/unchecked-flag defect witnesses; multifilter OR inclusions/AND exclusions, punctuation/wildcards and parameter ordering for both branches. |
| `integration_tests/sql_fixtures_test.go` | Direct, committed source-record builders for users, submissions, files/metadata, comments and legacy games; explicit UTC file/comment timestamps and IDs; production cache rebuilds. No app, upload, validator or external archive is needed. Backend-specific seeding SQL is isolated here for a future PostgreSQL adapter. |
| `integration_tests/submission_search_test.go` | Hand-authored complete decoded projections and counts for defaults, IDs, first/latest uploader versus comment author, current metadata/historical filenames and hashes, text/multifilter predicates, all five state families with global/Me/explicit-User alternatives, subscriptions and join fanout, legacy mixtures, sorting in both directions, pagination/past-end and the default 100-row limit. Exact user membership covers both Me and explicit User. |
| `integration_tests/submission_search_legacy_test.go` | Legacy-only text/collation/wildcard behavior, apostrophe/non-ASCII/comma input, NULL title/alternate/platform behavior, legacy exclusion and UUID-distinct results with identical projected metadata. |
| `integration_tests/submission_search_edges_test.go` | Deleted file/submission search visibility, tied sort cohorts and uncommitted changes versus the external count query. |
| `integration_tests/submission_cache_test.go` | All five reviewer sets, two independent reviewers, repeated enable/disable histories, deleted enablers/disablers, equal-time opposing actions, approval/verification at upload minus one microsecond/exactly/plus one microsecond, deleted latest upload, bot exclusion/latest live bot, reject/delete-reject, deleted oldest/newest/all files and rebuilding an erased cache row. Repeated rebuilds compare all derived cache columns, retaining NULL validity. |
| `integration_tests/comment_ordering_test.go` | Comment chronology with IDs intentionally out of time order, soft deletion, NULL/empty messages, unrelated submissions and tied comments/files/bot actions. Tied selectors now assert the explicitly approved ID tie-breaker. |

Expected search fields come from fixture facts, not captured query output. Only unordered user/action collections are sorted for comparison; result order, NULL versus empty, and duplicate entries remain observable. Cache CSV comparisons use delimiter-free fixture values and retain duplicates and SQL validity. These tests do not yet exhaust every matrix row below; the remaining acceptance work is listed in package B.

### Rebuild, mutation and rollback slice

The follow-up implementation covers three additional parts of package B:

- **Complete cache rebuild:** `ListSubmissionIDsForCacheRebuild` walks nondeleted source submission IDs in ascending keyset batches, independent of search, cache rows, files and count queries. The service uses batches of 1,000 and commits each submission separately. Missing cache rows are inserted; stale derived fields are recomputed; duplicate cache rows fail explicitly instead of being silently accepted. Results report committed count and last committed ID; errors identify the failed item/stage. The asynchronous admin endpoint logs completion or failure with progress and releases its guard.
- **Service mutation equivalence:** real upload/comment/deletion operations exercise all five reviewer sets, implicit unassignment comments, upload-version invalidation and restoration after deletion. At each checkpoint, expected states and source/API timestamp equality are checked before discarding and rebuilding the cache. Complete search/cache/comment projections must remain equivalent, and an unrelated submission must remain untouched. The small existing archive and validator mock suffice; production external files are unnecessary.
- **Batch and rollback contracts:** empty batches reject with 400; duplicate assignment IDs produce one action; repeated/all-skipped and imported/active mixed batches have explicit persisted-state assertions. Four SQL trigger failures occur after comment insertion, notification insertion, derived-cache writes and a later batch member after earlier work. Exact before/after rows cover submissions, files, comments, caches, subscriptions, queued notifications and PostgreSQL activity events. Removing each fault permits a successful retry with expected cardinalities.

New files: `database/cache_rebuild.go`, `service/submission_cache_rebuild.go`, `service/submission_cache_rebuild_test.go`, `integration_tests/submission_cache_rebuild_test.go`, `integration_tests/submission_mutation_equivalence_test.go`, and `integration_tests/submission_transactions_test.go`.

Rebuild tests split the scale concern from SQL cost: the service exercises 10,001 sparse IDs and exact-once commits through a test DAL; real MariaDB traversal covers 10,001 eligible rows across the old 10,000 boundary without files/caches. Smaller real-service cases verify repair, repeatability, a failed item's rollback, preserved earlier commits, retry and duplicate-cache rejection. This does not claim to benchmark 10,001 full SQL recomputations.

The rebuild can be restarted by rerunning it; it is a maintenance operation, not one atomic transaction across the whole table. On failure, committed earlier items remain repaired; the failing item rolls back and later items are not attempted. A concurrent deletion after ID enumeration causes an explicit failure; a rerun skips the deleted row. Exact-once visitation assertions concern a stable source set, not a global snapshot under concurrent mutations. Ordinary callers of `UpdateSubmissionCacheTable` keep their existing behavior; missing-cache repair is in the bulk rebuild path.

The rollback faults occur **before commits**. They prove rollback of pending work in both stores, not atomicity if PostgreSQL commits and MariaDB later fails. That limitation remains part of the unified-transaction design. Auto-increment gaps after rollback are expected and are excluded from equality assertions. Real-clock timestamps are checked for exact persisted/API equality and microsecond precision; implicit comment offsets are checked for ordering/minimum delay because production reads the clock separately for each write.

### Generated histories: scope and existing coverage audit

The new slice builds on, rather than duplicates, the existing hand-written suites:

| Existing suite | Already established | New generated-history role |
| --- | --- | --- |
| `submission_cache_test.go` | Each enable/disable family, duplicate actions, two-user independence, upload -1/0/+1 microsecond boundary, deleted enablers/disablers/files, reject suppression and erase/rebuild equivalence | Combine families, users and upload versions with interleaved/backdated operations and tombstones. |
| `comment_ordering_test.go`, search ordering tests | Deterministic timestamp/ID ties, oldest/newest/bot selection, stable search pages | Compare selectors throughout mixed generated histories against an independent model. |
| `submission_mutation_equivalence_test.go` | Ten checkpoints through real service upload/review/deletion operations and implicit comments | Keep service orchestration coverage there; the generator writes source history directly to test SQL reconstruction, including combinations not reachable through current service validation. |
| `submission_search_test.go` | Full search projection/filter/count contracts and exact user membership | Reuse those contracts; generated cases check evolving state and source-derived pointers rather than reproduce the entire search filter matrix. |
| Rebuild and transaction suites | Complete traversal, missing/stale cache repair, repeatability, batch/rollback and failure reporting | Repeated comparison to the model detects cases where incremental cache updates and a fresh rebuild agree with each other but are both wrong. |

Reproduction: `-args -history-seed=42` selects a single seed; `-args -history-replay=integration_tests/testdata/histories/name.json` reads an exact saved operation array. Failures report seed/step, log the operation prefix in `tests.jsonl`, and save `history-failure-seed-N-step-N.json` under the Docker run results when available. The narrow history JSON directory is explicitly included in the Docker build. See `integration_tests/README.md` for commands. No SQL mismatch has yet been promoted into a saved regression trace; any discovered failing trace should be minimized and retained with an explicit behavior decision.

Use production-scale histories: a few upload versions and dozens of comments per submission. The objective is broad combinations and reproducible failures, not load testing. Large aggregation-limit stress tests are deliberately out of this slice following the user clarification; no claim is made that existing `GROUP_CONCAT` storage is unbounded or measured at production scale.

### Controlled concurrency: parent-row locking

D-CONCURRENCY-01 and D-CONCURRENCY-02 were reproduced on MariaDB: overlapping reviewers could retain both source comments but lose one cached reviewer, and comment/file overlaps could leave filename filters stale until a rebuild. The user approved fixing these before migration. Their `CurrentBug` witnesses have been replaced with intended-result regressions requiring committed history, cache and search to agree before a rebuild, and full cache equality afterward.

`database.LockSubmissions` locks active parent rows with `SELECT ... FOR UPDATE`, copying, sorting and deduplicating batch IDs. Locks are acquired before any consistent read in the mutation transaction, and held through commit/rollback. Locking only the final cache write cannot refresh an existing REPEATABLE READ snapshot. MariaDB sessions now use context-aware `BeginTx`; cancellation does not leave an unowned transaction holding locks.

The protocol covers service comment batches, file/comment/submission deletion, metadata edits, bot overrides, force approval/verification, freeze/unfreeze and cache rebuild. Existing-submission uploads lock after archive validation and before the first MariaDB read; the preliminary role query uses a separate short session. New submissions already own their inserted parent row. Direct DAL history/cache callers must follow the documented locking precondition; raw fixture builders remain serial test infrastructure. This is a service transaction-ownership protocol, not a trigger or protection against arbitrary SQL bypassing the application.

Deletion resolves immutable child-to-parent identity in a separate read transaction, then starts the mutation transaction, locks the parent and rechecks child liveness. Thus two requests cannot delete both remaining files, and duplicate deletion produces one success and one 404 without duplicate activity events. An operation whose action has become invalid while waiting receives the existing validation error. Missing/deleted parents return not-found; lock/context errors propagate through the existing database error mapping. No automatic retry is added around external side effects.

Tests retain a test-only comment trigger and named arrival/release locks. A separate observer connection using the disposable test root account observes the competing parent-lock statement in MariaDB PROCESSLIST while the first transaction holds that parent, establishing that it has reached the lock before state reads; no application privileges are broadened. Coverage includes two reviewers, comment/upload/delete overlap, same-reviewer conflict, reversed batch IDs, an unrelated submission completing while locks are held, cancellation/retry, last-file protection and duplicate child deletion. Archive overlap uses committed DAL writes; full asynchronous archive-worker scheduling is not claimed.

Metadata edits still hold their parent lock across archive editing/validation because they consume the selected current file. Shortening that section needs a prepare/revalidate design. The existing upload mutex remains; this slice neither measures contention nor redesigns filesystem/validator effects. The PostgreSQL submission port retains this lock protocol. Existing activity/domain transaction ownership is preserved by scope; separate commits remain non-atomic if the second commit fails. Consolidating those transactions is deferred, not claimed as solved by using one engine.

### Notification SQL: recipient and queue contracts

`integration_tests/notification_queries_test.go` complements the existing HTTP preference permissions, subscription lifecycle, message formatting and notification timestamp tests. The new direct-DAL matrix covers every notification action, all subscription/preference combinations, author exclusion, unrelated submissions, duplicate rows on both sides of the join, unknown actions and universal recipients independent of subscriptions. Committed preference replacement, empty/nil clearing and rollback after an invalid replacement are checked through a new transaction. Queue tests check the complete projection, microsecond chronology with IDs opposing time order, sent-row exclusion, marking individual rows and `sql.ErrNoRows` for empty/all-sent queues.

Two existing behaviors are explicitly characterized rather than made target requirements:

- **D-NOTIFY-01:** duplicate preference rows can be stored, but `DISTINCT` guarantees unique recipients. Preserve recipient uniqueness; decide storage deduplication/unique constraints after auditing existing data.
- **D-NOTIFY-02:** equal queue timestamps have no ID tie-breaker. The test requires both entries to drain once and deliberately leaves their relative order unspecified. A future `created_at ASC, id ASC` policy is a small improvement, separate from the existing comment/file tie policy. This suite does not establish multiple-consumer claiming or exactly-once external delivery.

### Discrepancy ledger and decisions

The user decisions below define the corrected pre-migration contract. Fixed witnesses were replaced with intended-result regressions; D-SEARCH-08 remains explicitly named as a current bug. These changes are intentional deviations from the original baseline, and must be present in both the final MariaDB baseline and PostgreSQL implementation.

| ID | Decision and implementation | Regression coverage |
| --- | --- | --- |
| D-SEARCH-01 | Fixed: exact CSV token membership via `FIND_IN_SET`, including NULL-aware negative membership. PostgreSQL can replace CSV storage entirely later. | `TestSubmissionSearchExactUserMembership`: ID 12 must not match 112/120 across all five Me/User families; normal member/absent-user cases retained. |
| D-SEARCH-02 | Fixed: positive/negative subscription `EXISTS`/`NOT EXISTS`; removed the fanout subscription join. Both filters exclude legacy. | `TestSubmissionSearchFilterRegressions/subscription_membership_and_counts`: zero/one/multiple subscriptions, other users' subscriptions, rows and counts. |
| D-SEARCH-03 | Fixed: require a positive ID for explicit User filters; a valid ID without a User predicate is ignored. DAL validates a copy as well, so direct callers receive an error rather than panic. | Unit tests cover all five dependencies; SQL tests cover missing-ID error and ID-only unchanged results. Both submissions HTTP handlers return 400 for validation failures (including the previous My-submissions 500 path). |
| D-SEARCH-04 | Fixed: `LastUploaderNotMe` checks newest file author; `SubmitterID` retains oldest file author semantics. | Different first/latest uploaders, with both included/excluded callers. |
| D-SEARCH-05 | Fixed by user decision: either active content-change alternative excludes legacy; no content-change filter preserves ordinary legacy inclusion. | Both yes/no filters checked without relying on `ExcludeLegacy`. |
| D-SEARCH-06 | Fixed: content-change/frozen accept yes/no only; extreme accepts UI yes/no and existing Yes/No; empty strings still normalize away. | Valid/invalid/empty flags and existing callers' forms. |
| D-SEARCH-07 | Fixed by user decision: new nullable `ExtendedSubmission.GameUUID` identifies legacy rows; submission rows use NULL. `UNION ALL` preserves distinct games; submission branch retains grouping to avoid join duplicates. | UUID-distinct identical metadata produces two results/count, with stable separate pages; full projection tests include UUID. Existing `masterdb_game.uuid` is unique and non-null; no schema migration required. |
| D-SEARCH-08 | Deferred to PostgreSQL query/transaction rewrite. Current parallel count is retained for latency, including its separate snapshot limitation. Compare a single-query/window count against parallel alternatives with measured performance, not an assumed speedup. Target consistent rows/count and explicit failures, including empty/past-end pages, before production cutover. | `TestSubmissionSearchCurrentBugCountUsesSeparateTransaction` remains an explicit witness, not target semantics. |
| D-STATE-01 | Fixed: latest uploader cannot assign **or** unassign verification, and still cannot self-verify. UI follows the same rule. | Valid prerequisite unit cases and actual comment-form rendering tests. |
| D-STATE-02 | Fixed: mark-added closes testing/verification assignment and unassignment, approval, verification, requests for changes and rejection. Ordinary comments remain allowed. Backend guards also cover batch requests; single-submission UI hides closed review controls. | All eight actions, rendered controls, mixed active/imported batch rollback, skip-invalid batch mode and ordinary comments after import. |
| D-TIME-01 | Fixed simple same-table ties: latest timestamp then largest ID, oldest timestamp then smallest ID; comments ascending timestamp/ID; reviewer enable/disable ties choose later comment ID. Search uses primary requested direction then submission ID/legacy UUID ascending. Cross-table approval/verification still requires timestamp strictly after newest upload; explicit version association is deferred and not necessary for this sparse workload. | Both equal-time action orders and deletion of winners; deterministic file/comment/bot selections and rebuilds; tied search pages have no overlaps/omissions on unchanged data. Existing exact/microsecond upload boundary tests retained. |

Additional batch decisions discovered in this slice:

- **D-BATCH-01 (fixed):** an empty `ReceiveComments` ID list previously became an unfiltered search and could act on unrelated submissions. Reject it with 400 before opening either transaction; nil/empty and both skip modes leave persisted state unchanged.
- **D-BATCH-02 (characterized, unchanged):** request-changes/reject reject multiple raw IDs before deduplication, including `[id,id]`, while ordinary assignment batches deduplicate via search. An explicitly named current-behavior test records the 400 and absence of writes. A future decision may normalize duplicates before applying the single-submission restriction; this slice does not silently generalize assignment semantics to those actions.

Deterministic pagination guarantees order for unchanged data; inserts/deletes between page requests can still shift offset pages. Higher ID is an explicit tie policy, not a claim about transaction commit order. The new UUID is an additive result/API field and does not convert legacy game UUIDs into submission IDs.

The legacy text suite records current MariaDB case/accent-insensitive LIKE behavior and unescaped wildcard meaning. That is a concrete baseline to reconcile with production collations and the PostgreSQL text policy, not a claim that every database default is equivalent.

## 1. Test coverage: the first implementation milestone

### Existing coverage and its limits

The state machine is **not untested**. Preserve and build on these suites:

| Existing files | What they already exercise | Remaining gap |
| --- | --- | --- |
| `integration_tests/submissionstatemachine_test.go` | Main lifecycle, action counters, allowed actions, rendered controls, validator overrides | Mostly selected service histories; not an exhaustive SQL history/ordering matrix. The file explicitly contains a TODO for search tests. |
| `integration_tests/submissionstatemachine_fixuploads_test.go` | Fix uploads after bot/tester/verifier rejection or change requests | Need exact timestamp boundaries, deletion/rebuild equivalence, multiple users, and interleaved submissions. |
| `integration_tests/deletion_test.go`, `freeze_test.go` | Deletion/freeze permissions and workflows, last-file restrictions, frozen restrictions | Assert every affected cache/search projection, not just successful responses. |
| `integration_tests/subscription_test.go`, `notification_preferences_test.go`, `notification_queries_test.go` | Subscriptions, preference permissions, queue production, audition behavior; full recipient matrix, duplicate inputs, queue order/no-row contract and preference rollback | Multiple-consumer claiming and external delivery remain separate worker contracts. |
| `integration_tests/timestamp_test.go` | Expiration, timestamp roundtrips/order (including a sub-millisecond delta assertion), nullability, deletion/freeze/notifications | Exact microsecond fidelity and tied times. `TestMicrosecondPrecision` uses a 1ms per-value roundtrip tolerance; tighten this alongside the existing delta assertion. |
| Other integration tests | Upload/validator, metadata-edit limits, file access and resource binding | Retain as end-to-end regression coverage through DAL changes. |
| `service/comments_action_validator_test.go` | Pure Go action validation | Repaired in this slice; outstanding policy discrepancies are isolated in the ledger above. |

At initial investigation, no direct tests existed in `database/` or `types/`; the implemented slice above adds them. Search is used by state/deletion tests, but that does not establish coverage of its many filters, sorting, legacy union, counts, and combinations. No Go `Benchmark` functions were found.

### Test placement and fixture design

Use both unit tests and database-backed integration tests. SQL mocks cannot validate grouping, collation, NULL handling, window functions, transaction visibility, or query plans.

- **Pure Go tests:** add `types/submissions_filter_test.go`, `database/searchmonster_test.go` for `addMultifilter` while it remains there, and improve `service/comments_action_validator_test.go`. Test parsing/validation and any independently extracted state reducer without a database. Avoid asserting an entire SQL string as the main contract.
- **Fast SQL contract tests:** put the public-DAL cases in `integration_tests/`, with new fixture helpers that create users, submissions, files, metadata, comments and subscriptions directly. Most search/state cases should not upload an archive or invoke the validator. Use explicit IDs, UTC times, and a deterministic clock.
- **Service/HTTP tests:** retain existing workflows and add the smaller set that proves authorization, form decoding, implicit comments, queue/event production, and transaction boundaries.
- **Dump parity and benchmarks:** separate harnesses operating on disposable restored clones; do not run the existing destructive integration setup against the reference dump.

Define fixtures as logical records and histories, with small backend-specific seed/read helpers. Keep expected public results and assertions shared. A test runner should eventually support the current MariaDB + PostgreSQL stack and the unified PostgreSQL stack, whether through test-only adapters or separate binaries/checkouts. Do not retain a production dual-engine compatibility framework solely to run historical tests.

Every contract fixture should include an unrelated submission/user to detect missing predicates. Commit seed data before search: the current count query uses a separate connection and cannot see uncommitted fixture rows. Use a separate visibility test to characterize that limitation.

### P0-A: search contract matrix

Proposed files: `integration_tests/submission_search_test.go`, `integration_tests/submission_search_legacy_test.go`, and `types/submissions_filter_test.go`.

For each case assert the **complete decoded submission fields**, ordered result sequence where ordering is defined, and **total count before pagination**. Include empty results and confirm no duplicate submission rows when joins fan out. Give fixtures different titles, first/latest uploaders, comment authors, file sizes, dates, states, and legacy counterparts so accidental matches cannot pass.

| Family | Cases to add |
| --- | --- |
| Defaults and identity | Nil versus empty filter; default 100 rows and updated-descending order; one/multiple/nonexistent submission IDs; `SubmitterID` versus `UpdatedByID`; oldest-file submitter versus latest-file uploader. |
| Text | Title and alternate-title matches; platform, library, submitter username, launch command; match/no match/empty input; mixed case, accents, non-ASCII, trailing spaces, `%`, `_`, backslash, apostrophe, and comma. `FormatLike` currently wraps the input in `%` without escaping wildcards. |
| Multifilter | Comma-separated inclusion terms are ORed; exclusion terms beginning `!` are ANDed. Test mixed include/exclude, spaces, repeated terms, blank terms, only `!`, and NULL source fields. Cover both submission and legacy branches where applicable. |
| File history | Original/current filename and MD5/SHA256 substring matches in any active version; deleted version does not match; newest metadata versus historical filename match; long histories and delimiter-containing filenames. |
| Global state | Both alternatives for testing assignment, verification assignment, requested changes, approvals and verification; bot actions and submission levels; rejected state; NULL versus empty cached sets. |
| Per-user state | Every `Me` and explicit `User` filter for all five sets, positive and negative, absent users and missing user ID. Seed users `12`, `112`, and `120`: the exact-membership fix must prevent ID-substring matches. Test current-user context explicitly. |
| Actions | One/multiple `DistinctActions` and `DistinctActionsNot`, inclusion plus exclusion, no actions, reject, overlapping names and regex metacharacters. Current code uses regex against a concatenated action list, not exact relational membership. |
| Other filters | `IsExtreme`, `LastUploaderNotMe`, `SubscribedMe`, `IsContentChange`, `IsFrozen`, `ExcludeLegacy`; multiple subscribers and preference changes. Establish behavior for each accepted value. |
| Sorting and pages | Uploaded/updated/size × ascending/descending; default ordering; first/middle/final/past-end page; total independent of page size; equal sort keys; mixed legacy/submission rows; nullable legacy dates. Check adjacent pages using the agreed identity tie-breakers. |
| Legacy union | Legacy-only, submission-only, mixed results, every filter's legacy inclusion/exclusion behavior, two legacy UUIDs with identical projected fields, and NULL legacy metadata. The initial `UNION` deduplicated identical projected legacy rows; the approved `UNION ALL`/UUID fix preserves them; all legacy rows expose submission ID `-1` plus their game UUID after D-SEARCH-07. |
| Interactions | Pairwise combinations across text/state/user/legacy/pagination plus realistic UI presets: unassigned trial submissions; assigned-to-me pending review; subscribed frozen items; title + platform + not-rejected; latest updater + page. Add targeted triples for branches with independent parameter lists. |
| Failures/visibility | Invalid filter inputs at HTTP boundary; direct-service calls; row and count query failures; canceled context; uncommitted mutation versus count; concurrent insert/delete between page and count. Define the target snapshot/error contract explicitly. |

Cover every field in `SubmissionsFilter`, every accepted branch value, and all important exclusions on the legacy branch. This is more useful than an arbitrary line-coverage percentage for one long SQL string.

#### Source-level concerns to characterize before porting

These original source-level concerns guided the matrix. Items fixed below are historical motivation; the discrepancy ledger above is the authoritative current status. Untested failure/concurrency paths remain follow-up work:

- `SearchSubmissions` uses `LIKE '%uid%'` on comma-separated user IDs. Add an exact-membership desired-behavior test and separately record the MariaDB result if it differs.
- `SubscribedMe="no"` is accepted by validation, but search appends the user argument without adding a predicate for that branch. Reproduce and decide the intended negative-subscription behavior.
- Explicit-user filter validation appears to check the dependency in the wrong direction; a user filter without `AssignedStatusUserID` can reach a nil dereference in search. Test missing IDs and ID-only input separately.
- `LastUploaderNotMe` filters `uploader.id` from the oldest file, despite its name. Use different first and last uploaders to force a decision.
- `IsContentChange` affects the submission branch without excluding/filtering the legacy branch. Preserve or change deliberately.
- Page sorting, latest-row selectors and window rankings omit stable tie-breakers. SQL dialect translation cannot promise the same arbitrary ordering.
- Count uses `d.db.QueryRowContext` outside the caller transaction; failures become `-1`. A parity suite should report this as current behavior, not require the final design to retain silent failures.

Maintain a small discrepancy ledger with fixture, old result, intended result, decision, and regression test. Do not broadly skip failing cases or regenerate expected outputs from the new implementation.

### P0-B: submission state and cache histories

Proposed file: `integration_tests/submission_cache_test.go`. Exercise `UpdateSubmissionCacheTable`, then inspect both the persisted cache and `SearchSubmissions`/submission detail output.

Current semantics in `database/caching.go` are unusually important:

| Projection | Enabler | Disabler | Newest-file restriction |
| --- | --- | --- | --- |
| Testing assignment | `assign-testing` | `unassign-testing` | No |
| Verification assignment | `assign-verification` | `unassign-verification` | No |
| Requested changes | `request-changes` | `approve`, `verify` | No |
| Approval | `approve` | `request-changes` | Yes: comment time strictly greater than newest file time |
| Verification | `verify` | `request-changes` | Yes: comment time strictly greater than newest file time |

The enabling/disabling comparison is **per user and submission**. Validator comments are excluded from these human sets. The newest nondeleted validator comment supplies `BotAction`. Any nondeleted reject action collapses distinct actions to reject and clears all five human sets. These rules need explicit examples, even where surprising.

Required histories:

1. Enabler only, disabler only, enable → disable → enable, repeats, unrelated intervening actions. Run for all five sets.
2. Two users with interleaved actions: one user's request-changes does not cancel another user's approval under the current per-user calculation. Interleave a second submission too.
3. Actions before, exactly at, and 1µs after the newest upload. Confirm approvals/verifications reset across uploads while assignment/requested-changes histories have different boundaries.
4. Bot and human actions interleaved; bot-only submission; bot override; deleted newest bot action; multiple bot comments at one timestamp.
5. Reject followed by unrelated actions, and deletion of the reject that had suppressed state.
6. Delete the latest enabler/disabler; verify whether earlier history becomes effective. Delete newest/oldest/intermediate file; check pointers, file count, metadata, searchable file sequences, and revived prior-version state.
7. Empty and deleted-only histories; absent cache row; duplicate cache row in a diagnostic fixture. Decide which are invalid data and make integrity failures explicit.
8. Recompute twice and get the same semantic output; incremental updates equal full history recomputation; other submissions remain unchanged.
9. Keep generated histories representative: production submissions normally have at most dozens of comments (roughly 50 at the upper end) and only a few upload versions. Oversized histories and artificial `GROUP_CONCAT` overflow stress are deferred following the user's workload clarification; revisit only if production data or observed failures justify them.
10. Full-cache rebuilding now uses source-ID keyset traversal and visits every eligible submission exactly once on a stable source set. The reproduced 10,000-row omission is fixed; traversal, missing/stale caches, repeatability and failure progress have dedicated tests.

Assert all five **user identities**, not merely their counts; distinct actions; bot action; newest/oldest file and newest comment pointers; filenames/hashes; file count; submitter/updater; and freeze/autofreeze state. Normalize user/action collections only if they are declared sets, while preserving duplicates as an error.

Implemented in `submission_history_model_test.go` and `submission_generated_histories_test.go`: a pure Go chronological replay model, with its own hand-authored oracle checks, is compared against incrementally refreshed SQL state, periodic erased-cache rebuilds and search projections. Three fixed seeds (7, 42, 20260909) provide 234 operation checkpoints across three submissions and four human users plus the validator. Corpus checks require all action/operation families, ties, backdating, nonempty observations in all five reviewer sets and restoration after reject deletion. A reject is removed on the next operation affecting its submission so it cannot mask the remaining corpus. Each default history stays within four uploads and 50 comments per submission. These raw DAL histories deliberately cover combinations outside service policy; existing service tests continue to cover valid workflows.

### P0-C: ordering and timestamps

Proposed file: `integration_tests/comment_ordering_test.go`; extend `timestamp_test.go`.

- `GetExtendedCommentsBySubmissionID` now orders by `created_at ASC, id ASC`; cache latest/oldest selectors and bot/action rankings also include ID tie-breakers. Audit remaining file-history presentation queries, which still use `created_at DESC`. Test distinct times, exact ties, 1µs differences, IDs deliberately out of time order, and deleted rows.
- `ReceiveComments` creates implicit unassign comments at clock +1µs and system freeze comments at +2µs. Verify exact action/author/message ordering for approve, verify and autofreeze, including batches.
- Compare stored UTC timestamps exactly after microsecond normalization; test NULL separately from zero time/empty values. Distinguish application-clock writes from database-clock writes, and test expiration just before/at/after its boundary.
- The agreed stable order is `(created_at, id)` within a table. It does not define order between separate comment and file ID sequences; the strict timestamp version boundary still needs its own rule. Historical second-resolution timestamps converted by migration `0027` can remain tied.
- Search also needs stable keys for legacy rows; visible `SubmissionID=-1` cannot distinguish them. Retain an internal source kind and legacy identity if needed without changing the public response.

Use the implemented tie-breakers as the corrected baseline. Do not sort ordered result lists in the parity harness to conceal drift. Test any chosen tie-break rule on the baseline branch first as a separately reviewed behavior clarification.

### P0-D: validation, batch behavior and transactions

The first slice repaired `service/comments_action_validator_test.go`. Historically, the case named “user cannot verify if he is not assigned for verification” supplies `ActionApprove`; “user cannot assign for verification if they verified” supplies `ActionAssignTesting`. The uploader/verification case can fail because approvals are missing instead of testing the named guard. Use positive/negative pairs with other prerequisites satisfied and assert the intended error reason/category, not just `wantErr`.

Proposed `integration_tests/submission_transactions_test.go` should cover:

- Valid first item then invalid/missing second item in a batch; no partial MariaDB domain changes on error.
- Empty batch, duplicate submission IDs, all-skipped and mixed skipped/valid actions.
- `ignore-duplicate-actions=true`: currently skips any action-validation error, not only duplicates. Characterize and decide explicitly.
- Failure after comment insertion, after cache update, after notification insertion, and at commit: inspect persisted rows, not only returned errors.
- Exact implicit-comment, subscription, queued-notification and activity-event cardinality on success and retry.
- Controlled concurrent reviewers and comment/file mutations now have parent-lock regressions (D-CONCURRENCY-01/02 fixed above), requiring cache equality with committed history. Full asynchronous upload overlap and deadlock/serialization-error retry coverage remain follow-up work; this fix deliberately adds no automatic retry.
- Submission changes, cache and queued notifications remain one submission transaction. Existing PostgreSQL activity/metadata transactions retain their current ownership. A future cross-domain atomic transaction is desirable but deferred under the agreed preservation scope; distinguish this existing limitation from a migration regression.

Use real database failure scenarios where practical and narrow transaction fault injection for hard-to-reproduce commit errors. Do not retry a whole transaction automatically if its body also sends notifications, calls a validator, or moves files; those effects are not undone by database rollback.

### P1: other migrated SQL contracts

| Proposed coverage | Specific assertions |
| --- | --- |
| `notification_queries_test.go` (implemented) | Recipient requires matching subscription and action preference; exclude author; no duplicates/cross-submission leak; universal recipients independent of subscription; empty results; queue oldest-unsent order, ties, sent exclusion and empty-queue contract. Replacing preferences with empty/duplicate/invalid values preserves the decided atomic behavior. |
| Extend deletion tests | Deleting influential comments changes cache/search/detail consistently; last active file restrictions; deleted submission hidden; direct `GetCommentByID` currently differs from filtered comment lists. Nonexistent/repeated mutation and affected-row behavior. |
| `session_queries_test.go` | Store/read/revoke/expire/delete-all sessions, scope/client/IP roundtrip, expiry boundary, missing session and no-row mapping. OAuth client secret upsert and missing client. |
| `user_role_queries_test.go` | User upserts, role replacement, empty/duplicate roles, preserved role IDs and user associations; rollback on invalid role references. |
| `submission_storage_test.go` | Generated IDs; nullable metadata and large messages; booleans represented as strings versus actual booleans; additional apps JSON, images and file ownership; empty ID lists; uniqueness/FK failures; exact roundtrips. |
| `submission_statistics_test.go` | Totals/user counts/file-size sums on empty/nonempty/deleted data; per-user action stats and memoization behavior; next/previous submission with gaps/deleted neighbors; similarity input rows and legacy IDs. |
| Existing PostgreSQL regression | Launcher sync/date pagination, game/tag/platform edits and changelog triggers, revisions with MariaDB user-name enrichment, redirects, game-data/index reads, activity events, freeze/unfreeze/add-to-Flashpoint workflows. Shared transactions/resources can change these even if their SQL is not ported. |

### Harness changes needed before trusting before/after results

The original helpers overwrote shared environment/files and reused reachable schemas without migrations. That path has been replaced by the following implementation:

1. **Run isolation:** `bash integration_tests/run.sh` generates a unique project/database identity, creates fresh volumes, and cleans up only that invocation. The runtime network is internal, without host ports, fixed addresses or a Docker socket. The production `.env` and local dumps are excluded from the test image.
2. **Migration gate:** the outer script waits for database health and applies both full migration chains. Migration containers and the Go runner use the same built source/SQL snapshot. Helpers verify both database identities and exact clean schema versions before resetting either one. They cannot invoke Docker or silently recreate a database.
3. **Fixture reset:** MariaDB session settings and reset statements use one reserved connection, with restoration errors propagated. PostgreSQL resets tables in `public`; adapting this to any future schema split remains migration work. Tests verify repeated reset and that a dirty PostgreSQL schema leaves MariaDB probe rows intact.
4. **Files/lifetimes:** each top-level setup uses temporary directories and restores environment/cwd via Go cleanup. Validator mock servers, database handles and resumable services have cleanup hooks. Existing nested subtests intentionally share their parent fixture. Fast domain-specific DAL builders remain to be added.
5. **Evidence:** retain runtime/image metadata, source and migration checksums, migration logs, test JSON, coverage and cleanup status in a unique ignored results directory. Harness tests log engine version, SQL mode, collation and timezone. MariaDB remains `11.1.5`, PostgreSQL `15`; actual image IDs are captured. Verify production versions separately.
6. **Execution:** default `-count=1`, serial execution and fail-fast, with application packages in `-coverpkg`. No-test selections fail rather than producing a false successful baseline. Run the same suite against the future PostgreSQL implementation; backend comparison support remains planned.

Commands:

```sh
go test ./database ./types ./service ./transport ./utils -count=1
bash integration_tests/run.sh
bash integration_tests/run.sh -run '^TestHarness'
```

Keep these suites serial: `-p=1` alone does not disable `t.Parallel()`, while the new `t.Setenv`/`t.Chdir` setup deliberately rejects parallel tests. The runner defaults to a 20-minute timeout. Add targeted `-race` runs for concurrency work; the Go race detector does not prove SQL isolation correctness. Seed lookup rows are preserved, not reconstructed after mutation. Asynchronous upload workers still lack explicit failure-path draining; fail-fast limits subsequent contamination, but worker lifecycle tests are needed before parallel fixtures or upload failure injection.

Coverage acceptance: every search filter branch and state-history row above has a named passing test; all P0 ordering and mutation paths have positive, negative and boundary cases; every approved discrepancy has a dedicated expected result. Use Go coverage to find unexecuted paths, with a reviewed behavior matrix as the SQL coverage gate. Do not claim a numerical SQL coverage percentage from Go line coverage.

## 2. Schema and data migration direction

### Preserve identities and separate schema history from data transfer

Append new migrations to `postgres_migrations/` after the current head `0013`, using the actual next free version when implementation starts. Keep historical MariaDB migrations intact for baseline/recovery environments. Do not import MariaDB's `schema_migrations` row over PostgreSQL's migration history.

Create the migrated tables in the same PostgreSQL database as the existing game tables. A dedicated submission schema is an option, but needs explicit qualification/search-path and test-cleanup changes; choose it deliberately rather than moving existing game tables as collateral work. Keep domain repository interfaces separate if useful, with a shared underlying pool/transaction.

The source table inventory to map is:

- Identity/auth: `session`, `discord_user`, `discord_role`, `discord_user_role`, `oauth_client`.
- Submissions/state: `submission_level`, `submission`, `submission_file`, `curation_meta`, `action`, `comment`, `submission_cache`.
- Notifications: `notification_settings`, `submission_notification_subscription`, `submission_notification_type`, `submission_notification`.
- Images: `curation_image_type`, `curation_image`.
- Legacy snapshot: `masterdb_game`.

Prepare a table-by-table mapping from the **final migrated source schema**, not only `0001_init.up.sql`. Record column type/null/default, identity, uniqueness, indexes, references/cascades, seed values, and transfer checks. Migration history includes dropped flashfreeze/fixes structures; do not recreate obsolete tables just because they appear in old migrations.

Preserve submission/file/comment IDs, Discord IDs, lookup IDs, session/client identifiers, UUID text and all relationships. Import explicit IDs, then reset generated sequences so the next insertion exceeds the maximum imported value, including empty-table handling. Distinguish lookup seed rows from imported duplicates.

Audit composite duplicates in user-role, preference and subscription tables before introducing unique constraints. Preserve soft-deleted records and reasons. Delay new cross-domain foreign keys until historical activity/changelog user and game references have been checked; historical references may intentionally outlive active records. Preserve `curation_meta`'s later UUID, game-exists, primary-platform, Ruffle and additional-app fields, and the nullability of submission autofreeze settings.

In particular, PostgreSQL `activity_events.uid` and submission/comment/file identifiers inside `event_data` refer to MariaDB identities. Catalog/changelog `user_id` values are enriched through MariaDB users by `PopulateRevisionInfo`. `curation_meta.uuid/game_exists` and `masterdb_game.uuid` relate to catalog game IDs without proving that every referenced game currently exists. Include these links in reconciliation, not just declared foreign keys.

### Mapping decisions requiring tests

| Source behavior | Initial target direction / check |
| --- | --- |
| `AUTO_INCREMENT`, `LastInsertId` | PostgreSQL identity or sequence-backed bigint and `INSERT ... RETURNING`. Preserve source IDs on transfer; verify first post-import writes. |
| `DATETIME(6)` after `0027` | Treat verified UTC values as UTC instants. Choose timestamp types explicitly and preserve microseconds. Existing PostgreSQL timestamp conventions need separate inspection; do not convert all existing metadata timestamps casually. |
| Older timestamp data | `0027` converted Unix seconds; legacy negative modified dates were mapped to year 1000. Preserve documented sentinel/NULL values and validate source version. Session `created_at` is a separate TIMESTAMP field. |
| `NOW(6)` and application clock | Select statement/transaction/application time intentionally. PostgreSQL `now()` is transaction-start time, so a mechanical substitution can collapse events that currently have distinct times. [PostgreSQL date/time semantics](https://www.postgresql.org/docs/15/functions-datetime.html). |
| Boolean-looking values | Separate SQL boolean fields from metadata strings such as `extreme` and numeric user flags. Preserve accepted spellings/NULLs before normalizing. |
| `CHAR`, `VARCHAR`, TEXT/MEDIUMTEXT | Verify trailing-space, length, Unicode and case/accent comparison behavior. PostgreSQL `ILIKE` is not a blanket reproduction of every MariaDB collation. Test literals and escaping. [PostgreSQL pattern matching](https://www.postgresql.org/docs/15/functions-matching.html). |
| JSON/additional apps | Preserve NULL versus empty structures, decoded values and ordering. Decide text versus JSONB separately from the engine port; bytewise JSON equality is not always the public contract. |
| `INSERT IGNORE`, duplicate-key updates | Use explicit `ON CONFLICT` targets and defined update/no-op behavior. Inventory duplicates and other ignored source errors; do not silently discard transfer rows. |
| CSV cache fields and `GROUP_CONCAT` | Initially preserve decoded public state. Prefer normalized memberships or typed arrays once tested; inspect truncation, duplicates, delimiter collisions, NULL and ordering first. |
| Constraints/indexes | Preserve intended FK/cascade rules; audit actual violations. MariaDB `submission_cache.fk_submission_id` lacks an explicit unique key in the initial schema: verify duplicates before adding one. Index foreign-key/query access deliberately in PostgreSQL. |

`masterdb_game` must not simply be replaced with the live PostgreSQL `game` table. Search and similarity currently use that legacy snapshot, which has its own projection and timestamps; the old update service/handler is commented out. First compare data sources and intended meaning. Preserving it as an imported table is the conservative parity option. Removing it or replacing it with a view is a later semantic change with legacy-union tests.

### Transfer and reconciliation rehearsal

1. Obtain both database dumps with a documented consistency boundary. Since current writes can span engines, independent snapshots can already disagree; use a writer pause or an explicitly reconciled boundary for the eventual cutover.
2. Restore untouched reference copies and separate working clones. Inventory row counts, schema versions, duplicates, orphan references, timestamp ranges/ties, text edge cases and source cache consistency.
3. Apply target schema migrations. Load lookup/users/parents, then dependent rows. Handle cache pointers after files/comments exist. Stream/chunk large tables; produce per-table counts and failures with a restart strategy. Avoid hand-editing a SQL dump into PostgreSQL syntax as the migration procedure.
4. Verify keys, row counts, canonical row hashes, NULL counts, ranges and relationships. Check JSON decoded values and timestamps explicitly. Check sequences and every new constraint.
5. Compare the **persisted source cache**, recomputed source cache on a clone, and recomputed target cache. Report pre-existing stale cache separately from transfer/state-algorithm errors. Rebuild by a tested keyset traversal over source submissions, not through a cache-dependent search function.
6. Replay read contracts and deterministic write histories. Confirm existing PostgreSQL metadata/changelog/index data was neither lost nor unexpectedly regenerated; preserve trigger semantics and avoid import-generated history pollution.
7. Repeat migration on a fresh target, and exercise interruption/restart and final reconciliation. Schema migration success alone is not data-migration success.

## 3. Code and database-wrapper changes

### Target database boundary

Prefer the already-used `pgx/v5` pool for the unified implementation. Replace the split raw transaction types with one session/transaction boundary. A small query executor interface (`Exec`, `Query`, `QueryRow`) shared by pool/transaction can support read paths; domain operations that must be atomic must receive the same transaction-bound repositories.

Centralize begin/commit/rollback handling with a helper such as `WithinTx(ctx, options, fn)`. The owner starts and commits once, rolls back on failure/panic, propagates commit errors and preserves context cancellation. Nested service calls must accept the existing transaction instead of starting their own. Keep domain-specific repositories for submissions, users, notifications and game metadata rather than creating an even larger monolithic DAL interface.

Test canceled acquisition/execution, no leaked transactions, cleanup after commit, and bounded pool concurrency. MariaDB session acquisition now uses `BeginTx(ctx, nil)` as part of the parent-lock fix; retain context-aware acquisition in the target. Add an explicit PostgreSQL database-name setting: `OpenPostgresDB` currently uses the username as the database name.

Do not concurrently issue queries on a single pgx transaction. Audit current goroutines/errgroups and separate-connection reads before consolidating them. Where multiple reads need a consistent snapshot, select isolation explicitly: separate statements under PostgreSQL READ COMMITTED do not automatically share one snapshot. Use one statement or an appropriate explicit transaction where required. [PostgreSQL transaction isolation](https://www.postgresql.org/docs/15/transaction-iso.html).

Centralize driver error translation (`sql.ErrNoRows` versus `pgx.ErrNoRows`, unique/FK violations, canceled calls), check row-iteration errors, and define affected-row behavior. Parameterize user values with numbered placeholders; allow-list sorting/SQL identifiers. Adapt batches/upserts and NULL scanning explicitly, rather than doing global text substitutions.

### Change inventory

| Files/area | Work |
| --- | --- |
| `database/interfaces.go`, `mysqldal.go`, `postgresdal.go` | Unified pool/session API, PostgreSQL submission methods, error mapping, generated IDs, parameterization and stats. Split files by responsibility as useful. |
| `database/searchmonster.go`, `caching.go` | Rewrite MariaDB syntax, grouping, concatenation, regex and window queries; retain shared behavioral fixtures. Optimize in separate measured steps. |
| `service/siteservice.go` | Constructors/fields, reads/writes, user stats/similarity, full-cache rebuild, revision user enrichment, imports, freeze/unfreeze, add-to-Flashpoint and manual transaction pairs. |
| `service/siteservice_comments.go`, `siteservice_submissionreceiver.go`, `siteservice_resumablereceiver.go` | Pass shared transactions through mutation workflows; align cache/notification/event commits; preserve ordering and filesystem/validator behavior. |
| `service/siteservice_activityevents.go`, `notifications.go`, `notificationconsumer.go`, `autounfreezer.go`, `zipindexer.go` | Session signatures, background consumers, shared pool capacity and transaction ownership. Notification delivery/file effects need explicit post-commit or recovery behavior. |
| `transport/app.go`, `main/main.go`, `config/config.go` | One connection lifecycle, construction and configuration; shutdown/readiness and explicit database name. |
| `service/siteservice_mocks.go`, other mocks and `integration_tests/helpers.go` | Repair stale mock signatures (including search return values), add interface conformance checks, fixture builders, reset/seed changes and both-backend comparison support during migration. |
| `types/types.go`, filter handlers/templates | Prefer preserving response/filter contracts; adjust validation only through explicit discrepancy decisions. No general UI rewrite is required. |
| `.env.template`, root/integration Compose and Makefiles, `DockerfileMigrate`, README | One-engine setup, migration/backup/restore/run targets and test instructions; remove MariaDB requirements only after parity. Do not overwrite developer `.env`. |
| `pgdb-utils.sql`, `run-on-psql-after-import.sql`, `test.sql`, import tooling | Audit manual maintenance SQL, import triggers/sequences and stale assumptions; classify scratch utilities separately from required operations. The existing sequence-reset script covers selected catalog sequences, not the future imported submission identities. |
| `go.mod`, `go.sum` and remaining imports | Remove MySQL driver after final call sites/test strategy no longer require it. Keep historical baseline reproducible by revision. |

One transaction fixes database partial commits, but cannot roll back a moved archive, external validator action or Discord delivery. Keep external work outside long transactions where possible and retain/review compensation or post-commit queues. Avoid automatic retries that duplicate external effects.

The existing `DeveloperImportDatabaseJson` catalog importer is not the MariaDB transfer mechanism: it replaces catalog data and manipulates triggers. Preserve it as a separately tested maintenance operation, including its behavior once submission tables share the database.

## 4. Performance investigation and optimization

### Measured baseline first

Use three configurations: **current two-engine stack**, **PostgreSQL semantic port**, and **optimized PostgreSQL**. Record source revision, engine versions/settings, schema/index definitions, dump checksums, row distributions, workload seed, hardware/resources and pool settings. Give each configuration comparable resources. Root `dc-db.yml` still limits PostgreSQL to 0.50 CPU/4GB without the same MariaDB limit. The new integration Compose file removes that asymmetric cap, but correctness-test timings still are not a controlled engine benchmark.

Measure both query-only paths and whole service requests. Keep fixture import/reset outside timed sections. Run warmups and repeated trials; separate warm-cache from fresh/restored-cache results. Run serial and concurrent loads, plus the existing launcher/index workload alongside submission operations.

| Workload | Variants |
| --- | --- |
| Search | Default list, selective/broad/no-match text, common state queues, per-user filters, first/deep page, legacy on/off, many subscribers, large file history, skewed popular platforms/users. |
| State writes | Comment/approve/verify, upload, deletion, single cache refresh, long comment history, many users, batch changes and full rebuild. |
| Mixed traffic | Search while comments/uploads occur; same-submission contention versus unrelated submissions; notification processing and launcher/index activity. |
| Existing PostgreSQL | Launcher incremental sync, game/tag/platform reads/edits, changelogs, archive hash/path lookup and metadata stats. |
| Similarity | Database fetch separately from Go normalization/Levenshtein scoring in `getSimilarityScores`; engine migration cannot remove its application CPU cost. |

Collect request p50/p95/p99, throughput and errors; SQL durations/counts/rows; connection-pool wait/saturation; CPU, I/O, lock waits, temporary spills, memory and storage/index sizes. Existing duration logging and `PostgresStats` are a starting point. Capture MariaDB execution plans/runtime statistics and PostgreSQL `EXPLAIN (ANALYZE, BUFFERS)` on clones for representative parameters. Actual execution analysis of writes belongs only on disposable data. Use PostgreSQL statement statistics if available and record instrumentation overhead.

Choose numerical targets after the first baseline: identify the important endpoint SLOs, a material improvement threshold, and acceptable regressions for other workloads. A faster median with worse tail latency or existing launcher performance is not enough. Store raw measurements and plans alongside summarized results.

### Candidate changes, in priority order

1. **Stop multiplying search rows.** Subscription membership now uses `EXISTS`/`NOT EXISTS`; benchmark this corrected baseline. Enforce one cache row per submission after data checks. Ensure legacy and unsubscribed behavior stays explicit.
2. **Make count cheaper and correct.** The current count wraps the full unpaginated projection, including joins/union/order. Build a count-specific query from the same predicates; remove irrelevant work. Test deduplication and the empty/past-end page. A window count is an alternative to benchmark, but cannot return a count on a completely empty page without extra handling.
3. **Reduce cache refresh scans/round trips.** Ten statements run per submission: two updates, five state queries, file sequences, bot action and distinct actions. Filter histories by submission at the input and compute related state together. Inspect plans before claiming the current outer filters cause whole-table scans.
4. **Replace CSV state storage.** Evaluate normalized `(submission_id, user_id, state)` membership or typed arrays. The MariaDB `FIND_IN_SET` correction already removes ID-substring ambiguity; relational membership or arrays may allow better indexing. Retain a tested cache of newest pointers and other expensive projections if it benefits reads; do not replace synchronous state with a stale materialized view casually.
5. **Add query-shaped indexes.** Evaluate active-row partial/composite indexes on comment/file submission ID plus time/ID, and action/user fields where actual plans support them; subscription `(user_id, submission_id)`; unsent notification ordering. Check write overhead, cache rebuild cost and existing index redundancy before keeping them.
6. **Text search indexes.** Consider `pg_trgm` for the existing substring search contract. GIN/GiST trigram indexes support LIKE/ILIKE, but usefulness depends on patterns/selectivity, especially short terms. Benchmark representative text. Full-text search is a different matching contract and should be a separate feature decision. [PostgreSQL pg_trgm](https://www.postgresql.org/docs/15/pgtrgm.html).
7. **Restrict expensive projection work to candidates.** Explore counting files and fetching metadata after filtering/page selection when all filters/order keys permit it. Do not page before a predicate or sort that depends on those values. `UNION ALL` with legacy UUID is already approved and implemented; retain its identity semantics during optimization.
8. **Reduce deep-page/rebuild costs.** Use keyset traversal for maintenance; consider cursor pagination for the UI only as a separately specified API change. Avoid repeated per-row rebuild transactions where bounded batches are safe.

Each candidate requires unchanged contract results (or an approved discrepancy), before/after plans and latency, and a write-cost measurement. The PostgreSQL rewrite may simplify search/state substantially; keep semantic corrections and optimization measurements attributable, using this corrected suite as the reference.

## 5. Production-dump validation and rollout outline

Production data is essential for realistic distributions and reconciliation, but will not reliably contain every tie, malformed input, NULL combination or state-history boundary. Keep synthetic fixtures and dump replay complementary.

Initially obtain only the two database dumps and their capture times/consistency boundary. Stored paths, hashes and file sizes are enough for most SQL work. Keep background jobs/external integrations disabled on restored copies; use small synthetic or existing repository files for filesystem-dependent workflows. Request individual real archives only if a specific case requires them. Filesystem consistency and production archive/storage performance cannot be established from database dumps alone.

For shared test copies, omit row data from `session` and `oauth_client` or sanitize them on a disposable restored copy; preserve their schemas and create synthetic credentials for tests. The God Tools “Nuke Session Table” action only deletes `session`; it does not remove OAuth client-secret hashes. Nuking production is unnecessary for preparing a sanitized test copy. If row distributions in these tables become relevant, preserve noncredential characteristics through synthetic replacements and document the transformation.

Before import, capture a named read corpus with parameters, caller IDs, ordered full results and counts; include state/detail/comments/files, notifications, statistics/similarity inputs and existing PostgreSQL APIs. Capture timestamps and values without formatting away meaningful differences. Legacy rows need full projections/internal identities for comparison, not just `-1` IDs. Normalize only declared unordered sets and agreed timestamp representation.

Replay the corpus against the target. Compare both existing persisted state and recomputation. On resettable clones replay writes as well: response/errors, affected domain records, cache, notifications, activity events, and ID generation. Separate baseline defects, allowed changes and target regressions in the report. Do not use a fresh execution of the rewritten code to generate its own expected output.

Initial cutover preference is a bounded maintenance window, subject to measured data volume/import duration. Stop all writers/background jobs, take consistent final copies, transfer and validate, start the PostgreSQL-only application, and run smoke/read checks before enabling writes. Rehearse backup restoration and document operator commands later. If downtime is too long, evaluate incremental transfer/CDC as another design phase rather than assuming dual writes are safe.

Before writes resume, recovery can switch back to the intact source. After PostgreSQL accepts writes, switching back loses changes unless reverse transfer or replay has been designed. Define that recovery boundary explicitly; retain backups and the previous application/database environment until the acceptance window closes. Do not describe a destructive down migration as the production recovery plan.

## 6. Work packages and acceptance gates

| Package | Deliverable | Done when |
| --- | --- | --- |
| A: reproducible baseline | Isolated harness, schema assertions, domain fixture helpers and unit-test repairs implemented | Fresh and reset runs use the same known schema; no shared-data surprises; current suites have recorded results. |
| B: SQL behavior coverage | P0 search, state, ordering, batch/transaction matrices plus prioritized P1 contracts | Cases pass on baseline or have explicit discrepancy decisions; expected values are independent of the rewrite. |
| C: production reference | Restored clones, schema/data inventory, read/write corpus and performance manifest | Repeated runs are reproducible; stale caches and cross-database snapshot discrepancies are classified. |
| D: schema and port | PostgreSQL migrations/importer, submission transaction port and SQL | Empty install and imported upgrade pass the same contract suite; IDs/rows/relationships/cache reconcile. |
| E: optimization | One measured query/index change at a time | Meaningful gains demonstrated, no unapproved parity differences, no unacceptable mixed-workload regressions. |
| F: operational cutover | Tested runbook and recovery boundary | Repeated rehearsal, final reconciliation and smoke checks succeed within the agreed downtime budget. |

**Next implementation slice:** retain these corrected contracts as the MariaDB baseline; D-SEARCH-08 is deliberately deferred to the PostgreSQL rewrite. Rebuild traversal, key batch/rollback paths and service mutation-to-cache/search equivalence are now implemented. Notification recipient/queue contracts and controlled concurrency regressions are now implemented. D-CONCURRENCY-01/02 now use parent-row locking and equality regressions; row/count error handling remains part of the PostgreSQL rewrite. Smaller migrated contracts and existing PostgreSQL workflow coverage are deferred by the current user prioritization. Generated histories are now implemented at the practical workload size described above. Timestamp tie ordering and post-added action policy are now decided and implemented as described above. The tests implemented here are a substantial slice, not completion of every P0/P1 acceptance gate. Production dumps can proceed in parallel without blocking this work.

Open decisions for subsequent revisions: PostgreSQL type/collation mapping and constraint cleanup informed by the measured snapshot inventories; cross-table upload boundary design if needed; legacy snapshot versus live metadata; timestamp/case comparison policy; cache representation; exact transaction boundaries around external effects; performance targets and downtime budget.

## Validation history

Initial investigation package baseline (also rerun successfully after the environment changes):

```sh
go test ./database ./types ./service ./transport ./utils -count=1
```

Result: service, transport and utils passed; database and types reported no test files. The initial document-only investigation did not run database-backed tests. The implementation phase now has the following evidence under ignored `integration_tests/results/`:

| Check | Result | Artifact directory |
| --- | --- | --- |
| Fresh-stack session smoke | Passed after all MariaDB 1–27 and PostgreSQL 1–13 migrations | `run-K0dvWRnl` |
| Initial full run | Stopped at a new test's incorrect directory assertion on the templates symlink; assertion corrected | `run-9tyVagBQ` |
| Full suite after correction and shared migration snapshot | Passed: 28 top-level tests, 137 test/subtest passes, 67.7s Go test duration | `run-HJKkmM62` |
| Concurrent fresh-stack invocations | Harness selection passed while a second independent stack exercised an empty selection; distinct projects/databases and successful cleanup | `run-rGKMUZpu`, `run-QeKoueOT` |
| Empty selection | Expected exit 1 rather than false success; volumes/containers cleaned | `run-QeKoueOT` |
| SIGTERM during test execution | Expected exit 143; cleanup completed in about 1.1s; no labeled project containers, volumes or networks remained | `run-kc9xXmgf` |
| Final runner success path | All four harness tests passed after termination-handling changes; cleanup succeeded | `run-CyZRJC0Z` |

The full integration profile reports **30.5% Go statement coverage** across database/types/service/transport. This is a starting measurement, not SQL predicate/history coverage. Four new top-level harness tests cover unsafe connection rejection, migration metadata validation, temporary environment restoration and repeated resets/preflight refusal. Existing domain tests remain intact.

The host's sandbox initially blocked access to parts of Go's runtime/cache; the focused package baseline passed with the required local access, and database tests ran in the pinned Go container. No Go dependency changes were needed. Shell syntax and `git diff --check` passed. The test infrastructure received independent source review.

### SQL coverage slice validation (2026-09-09)

- `go test ./database ./types ./service ./transport ./utils -count=1 -coverprofile=/tmp/fpfss-test-slice-coverage.out`: all five packages pass. `isActionValidForSubmission`, `uidIn` and `addMultifilter` each reach 100% statement coverage; `SubmissionsFilter.Validate` reaches 92.4% (some duplicate/dead branches remain). These percentages measure Go control flow, not SQL semantics.
- First focused Docker run `run-y3apY3ie` caught an invalid synthetic submission-level name; fixture corrected to the schema's `staff` level. Cache/comment tests had passed before that failure. No application fix was needed.
- Focused search/cache/comment suite then passed in `run-NmiXE7WF`.
- Full suite including legacy/text/deletion/transaction/tie additions: `bash integration_tests/run.sh -failfast=false` passed in `run-Pbd8wHSU`: **45 top-level tests, 241 test/subtest passes, 73.005s Go test duration**, successful cleanup. The 17 new SQL top-level tests together took approximately **5.41s**, including their individual database resets; image build/startup is outside that duration. This is correctness-suite runtime, not an application performance benchmark.
- The full integration profile reports **33.2%** across database/types/service/transport, with database `SearchSubmissions` at **98.1%** and `UpdateSubmissionCacheTable` at **78.7%**. Remaining error/transaction paths and untested SQL combinations are still explicit follow-up work; high line coverage alone is not parity proof.
- Independent review checked fixture isolation, expected projections, NULL/duplicate preservation and tie behavior. It prompted an explicit empty-string comment case and a precise negative-subscription argument-error assertion, both verified by the final focused Docker run `run-V9fSpKvJ` (two top-level tests and four bug subcases passed; cleanup succeeded). `git diff --check` passes.

### Agreed bugfix slice validation (2026-09-09)

- Implemented D-SEARCH-01 through 07, D-STATE-01/02 and the simple deterministic portion of D-TIME-01. D-SEARCH-08 and the strict cross-table upload boundary remain deferred exactly as agreed. No schema or dependency changes.
- `go test ./database ./types ./service ./transport ./utils -count=1`: all five packages passed after the final HTTP fix. New template tests render the actual comment form. New HTTP tests cover both search handlers; ten My-submissions invalid-input cases reproduced the previous 500 response before the one-line correction, and all 34 cases now pass.
- `bash integration_tests/run.sh -failfast=false`: **46 top-level tests, 251 test/subtest passes, 73.06s**, in `integration_tests/results/run-PPwVMP2J`; successful cleanup. This includes corrected search/identity/pagination/history assertions, all eight imported-review actions and mixed-batch rollback/skip behavior. The later HTTP-only correction was verified by the final host unit suites.
- Integration statement coverage is 33.3% overall, database search 97.3%, cache update 78.7%. Added validation branches change the denominator; these numbers are not measurements of SQL performance or complete semantic coverage.
- Independent search review found no actionable issues. `git diff --check` passed. Performance against production distributions and data migration remain unmeasured.

### Rebuild, mutation and rollback validation (2026-09-09)

- Pre-fix reproduction: the original HEAD `RecomputeSubmissionCacheAll` method was copied unchanged into a temporary standalone test with 10,001 eligible submissions. It failed as expected: only 10,000 were rebuilt, last ID 10,000. Source and failed-run output are retained under ignored `integration_tests/results/rebuild-before-fix/` (test source stored as text so it cannot enter normal Go test discovery).
- Related unit suites passed: `go test ./database ./types ./service ./transport ./utils -count=1`. Rebuild units include 0/1/1,000/1,001/10,001 records, repeat runs, listing and item session failures, computation/commit failures, committed progress, retry and cancellation. The review-driven item-session regression also passed its focused rerun.
- Repository-wide compilation passed with `go test ./... -run '^$'`; this is compile verification, not host database execution.
- Focused real rebuild/batch/rollback run passed in `run-ecg2Burv`: six top-level tests, 15 test/subtest passes, 6.366s. The mutation scenario passed all ten checkpoints in `run-xiYqxWFJ` (1.844s). Earlier mutation attempts caught test fixture/assertion issues (missing context, integer assertion types and cleared assignment messages); no production mutation defect was inferred from those failures.
- Final full Docker suite: `bash integration_tests/run.sh -failfast=false` passed in `run-4nOYJAYB`: **53 top-level tests, 277 test/subtest passes, 81.151s** Go test duration; cleanup succeeded. This includes the final exact source for all three slices.
- Independent review found no blocking rebuild or input-guard issues. `git diff --check` passed. No schema/dependency changes, production rebuild, production dump replay or performance benchmark was performed.

### Generated-history validation (2026-09-09)

- Audited the existing cache, ordering, search, service-mutation, rebuild and transaction tests first; the scope table above records how the generated tests complement them. Oversized history/aggregation stress was dropped following the user's production workload clarification.
- Pure model/corpus checks passed with `go test ./integration_tests -run '^Test(SubmissionHistoryModel|SubmissionGeneratedHistoryCorpus)$' -count=1`.
- Initial generated SQL selection passed in `run-5p0bJ0hh`. Review added nonempty-state/restoration corpus requirements, shorter reject lifetimes, NULL-validity assertions and saved failure JSON.
- Full Docker suite with those refinements passed in `run-1jvyas8d`: **56 top-level tests, 283 test/subtest passes, 86.765s**; cleanup succeeded. The three generated seeds took **4.92s** combined, including fixture resets. No SQL/model disagreement was found in the tested corpus; this does not prove arbitrary histories or concurrency correct.
- The JSON replay smoke test initially failed in `run-2kakX1vp` because Go's package working directory differed from the documented repository-relative path. The reader was corrected to resolve relative paths from the repository root. This was test tooling only; application code is unchanged. Final focused replay passed in `run-5mAP99hS`, using the documented `-args -history-replay=integration_tests/testdata/histories/replay-smoke.json` command. Its temporary input is retained as `replay-input.json` in that ignored result directory, then removed from source fixtures. `git diff --check` passed.

**Deployment/cache note:** search and validator fixes take effect with the new code. Persisted cache values adopt deterministic tie rules when rebuilt. The new source-ID rebuild removes the previous 10,000-row limitation and repairs missing caches; use its explicit completion/failure log and committed progress when performing a controlled rebuild. Duplicate cache rows require investigation and cause a reported failure. No production rebuild was performed here.

At the generated-history checkpoint, no production dump replay had been run. The later restored-snapshot audit below supersedes that status. At that checkpoint no MariaDB-to-PostgreSQL transfer rehearsal or controlled performance benchmark had run. The later MariaDB baseline below supersedes the performance status; cross-engine transfer and PostgreSQL performance remain unimplemented. Applying the existing migration chains on fresh test databases is not a rehearsal of the future cross-engine importer.

### Controlled concurrency and notification SQL validation (2026-09-09)

- Notification SQL focused Docker run `run-Wlv4B4kG`: **6 top-level tests, 19 test/subtest passes, 3.402s**. Full recipient matrix, committed preference replacement/clearing, invalid-replacement rollback, queue projection/order/filtering and explicit duplicate/tie characterizations passed.
- The first concurrency harness attempt (`run-To4tODSi`) failed because a MyISAM arrival table was locked by the paused trigger; that harness was discarded. Named-lock arrival signals avoid this self-blocking observation path. The initial corrected run `run-IWnii4s5` reproduced the source/cache discrepancies; assertions were then strengthened with filename-filter visibility, precise source rows and a serial control.
- Final controlled-concurrency Docker run `run-Y3ab2JSC`, with `-race -count=3`: **9 top-level executions, 15 test/subtest passes, 8.251s**, no Go race reports. At that revision these included successful known-bug witnesses; the subsequent parent-lock fix is recorded below.
- Final full Docker suite `run-wFG5NQT3`: **65 top-level tests, 307 test/subtest passes, 98.271s**. Both final Docker runs exited 0 and cleaned their isolated containers, volumes and runner images. `git diff --check` passed. No production code, schema or Go dependency changes were needed for this slice.
- Production dumps remain unnecessary for these tests. They are still needed for data reconciliation, representative query plans and performance measurements. Async upload-worker overlap, notification consumer claiming/delivery and smaller unrelated SQL contracts are not claimed as covered here.

### Parent-lock fix validation (2026-09-09)

- Implemented the approved per-submission serialization protocol and replaced D-CONCURRENCY-01/02 witnesses with equality regressions. Added same-item deletion liveness checks under the parent lock; one concurrent deletion succeeds and the other returns 404, with only one deletion activity event.
- Early test runs exposed observer limitations: the application account lacks PROCESS, configuration root fields are not populated by `GetConfig`, and MariaDB can park a prepared parent-lock query in optimizer `Statistics` before listing an InnoDB lock wait. Tests now use the existing disposable root credentials directly for a separate PROCESSLIST observer. No application grant or production query was changed to accommodate the observer. The cancellation assertion was corrected to require the preserved `context.Canceled` error chain.
- The intermediate full run `run-zrnhPH1r` caught the initially missing post-lock comment-liveness check in the duplicate-deletion case; the check was added before final validation. Historical failing run artifacts are not successful baselines.
- Final unit checks passed: `go test ./service ./database ./types -count=1`. Initial sandboxed execution could not bind existing httptest listeners; the authorized host run passed. Final integration compilation and `git diff --check` also passed.
- Final concurrency Docker run `run-I2BF53qz`, `-race -count=3`: **24 top-level executions, 36 test/subtest passes, 16.957s**, no race reports. Covers both corrected cache cases, conflicting validation, reversed batches, unrelated progress, canceled waiter/retry, last-file protection and duplicate file/comment deletion.
- Final full Docker suite `run-tt48QqLv`: **70 top-level tests, 314 test/subtest passes, 96.082s**. Both final runs exited 0 and cleaned their isolated resources. No schema migration or dependency change was required.
- These results establish the tested service/DAL interleavings on MariaDB, not filesystem atomicity, arbitrary raw SQL compliance with the locking protocol, or cross-engine atomic commits. Preserve parent ownership and these equality tests during PostgreSQL consolidation.

### Received dumps and auth sanitization (2026-09-09)

The received production snapshots were originally named `fpfss-mariadb-dump-08-09-2026_00_00_01.sql.zst` and `fpfss-postgres-dump-08-09-2026_00_00_24.sql.zst`. Only table/data-section metadata was surfaced during inspection; credential row values were not displayed. Both credential tables (`session`, `oauth_client`) occur in MariaDB; neither occurs in the PostgreSQL table inventory.

The user subsequently moved `fpfss-mariadb-dump-08-09-2026_00_00_01.sanitized.sql.zst` into the repository root and removed the unsanitized MariaDB input. Use that sanitized root file for restore work. Its credential-table INSERT statements have been removed, retaining schemas and every other plaintext byte. This removes three session INSERT statements and one OAuth-client INSERT statement (statement counts are not row counts). Sanitization did not modify the original inputs; the user later removed the unsanitized MariaDB file. The PostgreSQL dump needs no change for these two auth tables; its cluster-level role password is separately removed from the restore stream below. Dumps and sanitized artifacts are ignored by Git.

Reusable command:

```sh
python3 scripts/sanitize_mariadb_dump.py INPUT.sql.zst OUTPUT.sql.zst
python3 -m unittest discover -s scripts -p test_sanitize_mariadb_dump.py
```

The streaming tool supports standard MariaDB dump auth INSERTs, including multiline/quoted values, rejects unsupported auth data sections, publishes only after verification, and refuses to overwrite files. It never logs values. Verification decompresses the output, requires zero remaining auth INSERTs and both auth schemas, and compares retained plaintext byte count/SHA256 to the filtered source. Five synthetic tests pass, including multiline quoting, empty tables, compressed roundtrip, overwrite refusal and cleanup on malformed input. The actual sanitized dump passed these stream checks. At that checkpoint no database restore had run; see the subsequent audit below. Local evidence is in `sanitized-dumps/sanitization-report.json`.

### Restored production-snapshot audit (2026-09-09)

This phase uses the sanitized MariaDB archive and the original PostgreSQL archive
now in the repository root. The SQL-only workflow needs no external submission or
image files. The reference databases are isolated from integration fixtures and
application workers. Existing integration tests remain the source of controlled
edge cases; these snapshots add actual distributions and historical data.

Implemented commands (see `scripts/snapshot-db/README.md`):

```sh
make snapshot-restore  # new Docker project and volumes for every invocation
make snapshot-status
make snapshot-audit    # read-only schema/data aggregates on the reference pair
make snapshot-cache    # fresh MariaDB clone; preserve cache; rebuild twice
# Only when the retained databases are no longer needed:
make snapshot-down
```

The targets do not load `.env`, publish ports or run application services. The
network is internal. `snapshot-results/latest` selects the current run;
`SNAPSHOT_RUN="$PWD/snapshot-results/run-XXXXXXXX"` selects an older one. Mixed
snapshot/development Make invocations are rejected. Cache reruns refuse an existing
clone so that original evidence is not silently overwritten. Cleanup deletes only
the selected snapshot resources, retaining local reports. These databases must
never be supplied to integration tests, which intentionally reset their fixtures.

The current run is `snapshot-results/run-ZhQH8Txh`, Docker project
`fpfss-snapshot-run-zhqh8txh`. Source archive SHA256 hashes, source revision and
runtime image IDs are retained there. PostgreSQL's input is a cluster dump that
recreates databases and its application role, rather than a single-database dump.
The restore adapter conditionally drops absent databases/roles and strips the role
password from the stream without displaying it or changing COPY data. Both restored
MariaDB auth tables contain **zero rows**. Source archive bytes are unchanged.

#### MariaDB schema and data findings

The snapshot runs on **MariaDB 11.1.5**, matching its dump header. Migration version
is **27, clean**, matching the repository chain at audit time (before the later quota-lock migration 28). The inventory contains 20 tables;
19 use `utf8mb4_unicode_ci`, while `schema_migrations` uses
`utf8mb4_general_ci`. The server/connection defaults use `utf8mb4_general_ci`;
therefore a PostgreSQL port must consider explicit column/table comparison
semantics, not just the connection default. Cache audit sessions use UTC,
REPEATABLE READ and `group_concat_max_len=1048576`.

| Data | Rows / finding |
| --- | --- |
| Submissions | 148,355 total; 148,164 active; 191 deleted |
| Persisted cache | 148,355 rows; no duplicate submission keys; no active submission missing its cache |
| Comments | 1,290,139 |
| Submission files / curation metadata | 184,307 each |
| Curation images | 367,203 database records; image bytes were not needed |
| Users | 3,781 |
| Legacy games | 122,726 |
| Notifications | 425,068 |
| Subscriptions | 441,680; **26,760 duplicate user/submission groups, 35,366 excess rows** |
| Notification preferences | 23,174; no duplicate preference keys |

All **declared** foreign-key orphan checks returned zero; this alone does not
establish every undeclared logical or cross-database relationship. No duplicate
metadata keys or active submissions without live files were found. There are 191
cache rows attached to deleted submissions, with corresponding NULL derived
fields; these are not active missing-cache errors. All-column NULL counts,
column types/defaults/precision, full index inventory and FK definitions are saved
under `audit-mariadb/` in the run directory. Existing cache and subscription
indexes do **not** impose the unique keys proposed for the new schema.

Timestamp checks found no zero dates, deletion-before-creation or
notification-send-before-creation values. However, **6,149 live-comment timestamp
tie groups (6,164 excess rows)** remain, and **165,417 live comments equal a live
file's timestamp within the same submission**. No live-file timestamp tie groups
were found. Only 65,714 comment creation timestamps and 13,127 file creation
timestamps have nonzero fractional seconds. Historical second-resolution data
therefore still matters to the agreed deterministic ordering and deferred strict
cross-table boundary decision. Live comment histories have a maximum of **75**;
only **five** exceed 50, confirming that huge per-submission histories are not a
representative optimization target. Legacy `masterdb_game.date_modified` reaches
`1000-01-01`, the migration's sentinel; preserve or explicitly map it rather than
silently treating it as corruption.

NULLs are materially present, not hypothetical: 954,009 comments have no message;
85,738 metadata rows have no UUID and 604 have no title. These may represent valid
historical/partial metadata and require explicit importer handling, not blanket
NOT NULL constraints or coercion to empty strings. Two notifications are unsent.

Importer implications: preserve IDs, NULL distinctions, microseconds and historical
ties; keep the legacy snapshot separate from live game metadata. Before adding a
unique subscription constraint, explicitly deduplicate the 35,366 excess rows and
retain a documented timestamp policy (proposed: earliest subscription creation).
Confirm recipient behavior against the existing duplicate-subscription tests;
this audit does not silently change notification behavior. Unique cache and
preference constraints fit this snapshot, subject to the cutover-time recheck.

#### PostgreSQL schema and data findings

Migration version is **13, clean**, matching the repository chain. There are **24
tables and 59 indexes**. The dump was produced by PostgreSQL **15.7**; this restore
ran PostgreSQL **15.18**, UTF8 with `en_US.utf8` collation/ctype and `Etc/UTC`.
The Compose definition now pins that tested minor version. This is not an exact
production-runtime performance comparison. Both declared foreign keys have zero
orphans, and all restored indexes are valid/ready and constraints validated.
No infinite timestamps were found. The inventory also identifies **22 `citext`
columns**; preserve those types and their comparison behavior rather than mapping
every string to plain text. Full column, NULL, timestamp and index metadata
are retained in `audit-postgres/`.

| PostgreSQL data | Rows |
| --- | --- |
| Games | 221,913 |
| Game data | 207,236 |
| Game data index | 16,370,988 |
| Activity events | 758,732 |
| Game/tag memberships | 831,501 |
| Game/platform memberships | 229,692 |
| Largest changelog: game/tag membership | 2,263,528 |

The index table and changelogs are substantial existing PostgreSQL workloads that
must survive unification. Only two relationships have declared foreign keys;
15 additional logical checks found **143 `additional_app` rows with a missing
parent game**, while the other checks (game/data/index parents, memberships,
aliases and active-data ownership) returned zero. Preserve and investigate these
orphans; do not silently drop them or add an immediately failing foreign key.
Historical changelogs are excluded from current-parent checks because their
entities may legitimately have been deleted. In current game rows, 190,432
`active_data_id` and 166,612 `parent_game_id` values are NULL; these optional
relationships must remain optional unless a separate change is agreed. Audit-table coverage must not be mistaken for validation of every
existing PostgreSQL operation.

The initial restore's database commands completed, but its shell wrapper exited
2 afterward because its source was edited while the long-running shell was still
reading it. Both databases were independently verified rather than restored again:
MariaDB auth counts are zero; PostgreSQL's full metadata/data audit completed,
indexes/constraints are valid, no restored role password remains, and its restore
log has no SQL errors. The original exit status is preserved alongside
`restore-verification.json`; `restores-complete` records verified readiness.
The new `launch.sh` reads the whole runner into memory before executing it. A
regression test modifies the runner mid-execution and confirms the original script
finishes correctly. No database repair was required. An extra PostgreSQL logical-reference query
initially exceeded Docker's default shared-memory allocation; its successful retry
uses session-local `max_parallel_workers_per_gather=0`. The repeatable audit keeps
that setting for these aggregate checks, and future snapshot containers allocate
1 GiB of shared memory. This is audit infrastructure, not a production tuning
recommendation.

#### Cache reconciliation method and evidence

The restored `fpfss` database remains the reference. The cache tool clones it into
`snapshot_cache_work_scoped`, then copies its cache into
`submission_cache_snapshot_original` **before any rebuild**. Reports export counts,
field names and sample submission IDs, not comment text, filenames or credentials.

An initial direct full-table rebuild was stopped before its first 1,000-item
progress report because of its runtime. Its clone (`snapshot_cache_work`), source
copy and logs are retained separately as `unscoped-*` evidence. This observation
is not a controlled benchmark: PostgreSQL was concurrently restoring indexes.
The production SQL contains whole-table aggregation/window work that merits query
plan investigation; no production query was optimized in this phase.

To make complete static reconciliation practical, the audit uses the **unchanged
production DAL SQL** against per-worker tables holding all submission, comment and
file records, including deleted records, for each 100-ID batch. Actions remain a
full reference view and cache writes target the full disposable cache through a
view. This preserves each submission's complete input history while limiting
unrelated rows. **23 selected submissions** (source-range samples, tied events,
larger histories and deleted-history cases) were rebuilt first against the full
source tables and then against bounded inputs: **zero raw or normalized field
differences**. This sample cross-check supports, but does not mathematically prove,
the transformation. The complete traversal uses four workers and two passes.

Compare raw values first. A separate normalized comparison sorts only reviewer
IDs, action names and hash collections, retaining duplicates, empty strings and
NULL distinctions. Filenames remain byte-exact because embedded commas make the
old concatenated representation ambiguous. Collection order changes must not be
mistaken for changed membership; conversely, normalization must not hide duplicate
or NULL changes. Deleted parents are deliberately excluded from rebuilding while
their original cache rows remain available for comparison.

This is a **static cache correctness audit**, not validation of full-source rebuild
performance, live concurrency or a PostgreSQL importer. The existing synthetic
integration tests cover the production rebuild orchestration and mutation locking.
A final report records every visited/rebuilt count, failures, per-field differences
and second-pass repeatability in `cache/cache-reconciliation.json`.

An independent binary/NULL-safe comparison in both directions confirmed all
**148,355 original cache rows exactly match the untouched reference**, with zero
duplicate IDs. Additional logical checks found no missing, wrong-parent or deleted
targets for any oldest-file, newest-file or newest-comment pointer. Reproducible SQL
and aggregate results are retained in `cache-reference-check.sql` and
`cache-reference-check.tsv`; the repeatable audit also includes pointer checks.

#### Completed cache comparison and repeatability

All **148,164 eligible submissions** were visited and rebuilt successfully; **zero
failures**, 1,043.50 seconds for the first bounded traversal and comparison. This
includes the entire active source set, not the previous 10,000-row limit. All
148,355 original cache rows, including deleted-parent rows, were compared.

**523 submissions changed** (raw and normalized counts are identical):

| Changed cache field | Submissions |
| --- | --- |
| Newest comment pointer | 520 |
| Active approval IDs | 1 |
| Original/current filename sequences | 2 each |
| MD5/SHA256 sequences | 2 each |

No other cache fields changed. These are snapshot discrepancies to classify from
history, not evidence by themselves that the current implementation is wrong or
that every difference was caused by the fixed concurrency bugs. The two file cases
remain different after hash collection sorting; they are not merely list-order
changes. The second traversal also visited and rebuilt **all 148,164 eligible submissions**
with **zero failures**, taking 1,193.57 seconds including comparison. It produced
**zero raw differences and zero normalized differences** against the first rebuilt
projection. The cache command exited **0**. Both full bounded passes therefore
completed and the repaired projection was byte-for-byte stable at every compared
field. These timings describe this audit workload, not the unbounded production
rebuild or a PostgreSQL speedup.

The history-level classification is:

| Classification | Count | Evidence and decision |
| --- | --- | --- |
| Agreed comment tie rule | 520 | Old/new comment timestamps are identical; every new pointer chooses a higher ID. Both targets are live. Accept the current deterministic result; this is not 520 independently stale timestamps. |
| Stale file collections | 2 | Submissions 80680 and 142578 each have two live files and no deleted files. The original newest-file pointer already selected the later file, but hashes/filenames represented only the older file. Rebuilding includes both; accept repair and extend the existing repair fixture with stale file collections. |
| Stale approval membership | 1 | Submission 12551 had one cached reviewer with no comment history on that submission, including deleted history, while omitting a reviewer with an explicit post-upload approval. Rebuild exchanges that member and retains the other valid member. Accept repair; the existing repair regression already covers a poisoned approval collection. |

None of these three stale cases has deleted file/comment history. The snapshot
establishes internal inconsistency, **not its historical cause**; do not attribute
it to concurrency without a mutation reproducer. These are documented data-repair
decisions, not newly discovered current-code defects. Preserve the current history
semantics and avoid treating the old cache as the PostgreSQL correctness oracle.
The saved aggregate queries are replayable database witnesses. Coverage review
confirmed `TestSubmissionCacheRebuildRepairsAndRepeats` already poisons the approval
collection (`999`), newest-file pointer and bot action, then checks exact repair and
repeatability. Do not add a duplicate approval-repair test. A small extension can
poison filename/hash collections while leaving the newest-file pointer correct;
the concurrency and mutation-equivalence suites already cover normal multi-file
histories and source/cache agreement. The original source
cache remains untouched.

Evidence is retained as `cache-difference-characterization`,
`cache-difference-specific`, `cache-difference-histories` and
`cache-approval-token-integrity` SQL/TSV pairs in the run directory. History
projections contain action names, comment IDs, relative times and anonymized
reviewer ordinals, not messages, filenames, hash values or user identities.
The final `make snapshot-audit` invocation also passed end to end and writes the
combined canonical schema/data inventory under `audit/`; earlier per-engine audit
directories are retained as intermediate evidence. After both rebuild passes,
the binary/NULL-safe reference check was rerun successfully: all 148,355 original
rows still match in both directions, with zero missing/wrong-parent/deleted pointer
targets. Final evidence is `cache-reference-check-final.tsv`. The isolated database
containers and original/rebuilt caches remain available for the next phase.

#### Next implementation steps after reconciliation

1. Reuse the existing deterministic-tie and stale-approval repair regressions;
   extend the existing repair fixture with pointer-current/sequence-stale data if
   adding the production-shaped case. The SQL witnesses above already classify
   this snapshot. No newly demonstrated current mutation bug requires blocking the
   migration. Keep original and rebuilt variants available for search comparisons.
2. Specify the PostgreSQL schema/import mapping against these actual inventories:
   collation/search behavior, integer/UUID types, timestamp precision and sentinels,
   NULL handling, foreign keys, sequence reseeding and subscription deduplication.
   Recheck constraints at the final cutover boundary. The two dump timestamps are
   23 seconds apart; their filenames do not establish an atomic cross-engine
   snapshot, so audit logical cross-engine relationships separately.
3. Capture a repeatable search workload on the real distributions: selective and
   broad filters, per-user memberships, legacy unions, counts, sorting and deep
   pagination. Save ordered result IDs/projections separately from measured
   latency, with original-cache versus rebuilt-cache context explicit. Record
   query plans, warm/cold conditions, concurrency, database settings, CPU and I/O;
   exclude simultaneous restore/audit work from timing. Measure the count/page
   strategy (D-SEARCH-08) rather than assuming a window-count query will be faster.
4. Rehearse the importer into a fresh PostgreSQL target, retaining current
   PostgreSQL data and validating both engines' workloads. Run synthetic parity
   tests plus the production-shaped result corpus before optimizing. Inspect
   whole-table cache aggregations and missing composite indexes using measured
   plans; bounded-audit runtime is not evidence of production query performance.

Tool validation: all three Python tests pass (two PostgreSQL stream-adapter tests and the immutable-launcher regression); cache
comparison/traversal tests pass with Go's race detector; shell syntax, Python
compilation and `git diff --check` pass. No application behavior, migration chain
or Go dependency was changed in this snapshot-tooling phase. Existing integration
fixtures were not reset or used against these references.

### Follow-up: upload quota and snapshot bug signals (2026-09-09)

**D-QUOTA-01 — initial finding; subsequently reproduced and repaired below.** The current one-submission rule applies to audition
users (`IsInAudit`), not the `Trial Curator` role. Both new-upload routes in
`transport/router.go` admit Trial Curators directly. Audition users pass
`IsUserWithinResourceLimit(..., 1)` in `transport/middlewares.go`, which searches
existing submissions and excludes those with a reject action. Upload completion
then launches an asynchronous worker in `siteservice_resumablereceiver.go`.
`processReceivedResumableSubmission` / `processReceivedSubmission` never repeat
the quota check when creating the submission. Two requests can both see zero
before either worker commits. Distinct upload identifiers have distinct memoizer
keys. The recent parent locks cover updates to existing submissions; new submissions
have no shared parent, and the receiver mutex contains no quota recheck. Therefore
those locking fixes do not close this admission race. No quota concurrency test
was found in the current test suite, and no runtime reproducer was run in this
follow-up inspection.

Initial repair proposal (refined to a dedicated lock table below): keep the early UI/admission check, but enforce the quota in the
creation transaction under a database lock on the submitting user's row, before
its first consistent MariaDB read; recheck and retain ownership through commit.
Archive validation can happen before that short critical section. Both workers
must use the same locking protocol across application instances. If in-progress
uploads must reserve capacity before validation, introduce explicit reservations
with cleanup instead of holding a transaction throughout file transfer. Changing
the actual Trial Curator policy would be a separate decision. Also specify whether
"rejected" means a historical live reject action (the current search predicate) or
current-version rejection; do not silently change that quota rule during repair.

Test first with two distinct uploads for the same audition user paused at a barrier:
both reach admission before either completes, then exactly one submission may
commit. Include sequential second-upload denial, rollback/retry, different-user
progress, rejected/deleted submissions, existing-submission updates, and unchanged
Trial Curator/staff policy. Assert committed rows and final async statuses.

Other snapshot signals have different confidence levels. Duplicate subscriptions
have a current ordinary-code explanation: file/meta updates unconditionally call
`SubscribeUserToSubmission`, which unconditionally INSERTs and has no unique key.
This does not require concurrent spamming. Recipient selection uses DISTINCT, so
duplicated storage is not by itself proof of duplicate notification delivery.
The 143 PostgreSQL additional-app orphans warrant import/deletion-path investigation,
but their historical cause is unproven; the normal `DeleteGame` path soft-deletes
its game row, so the snapshot does not establish that path caused missing parents.
The three stale caches likewise have no demonstrated current mutation cause.
Timestamp ties and nullable/partial metadata alone are not evidence of new bugs.

### Audition quota race repair (2026-09-09)

The user confirmed the policy: **audition users are limited; Trial Curators and
staff remain unrestricted**. D-QUOTA-01 is now fixed with a regression and
an authoritative creation-time check. The early middleware check remains for
fast rejection, but cannot authorize creation by itself.

The new worker check runs after archive validation and before the MariaDB
transaction's first consistent read. It acquires a per-uploader transaction lock,
then uses the existing submitter/non-rejected search predicate with one result
requested. The lock is retained through creation, cache writes and commit or
rollback. A waiting creator therefore sees its predecessor's committed submission.
Quota failure produces a specific failed asynchronous-upload status and cleans up
the received file. Existing-submission updates bypass this new-creation check.

**Migration 0028** adds `submission_creation_lock`, an InnoDB table containing only
one primary-key user ID per uploader. A lazy upsert acquires its exclusive row
lock. This is coordination state, not a quota counter or a reservation; no backfill
is needed and failed creation rolls back a newly inserted lock row. It works across
application instances. The table is separate from `discord_user`: exclusively
locking that foreign-key parent can block other upload writes inside the legacy
receiver mutex, introducing an application/database lock cycle. The lock table's
user reference uses ordinary compatible parent-reference locks instead.

Apply migration 28 before deploying this code. The retained production reference
snapshot remains at MariaDB version 27; it was not altered for this fix. During
PostgreSQL consolidation, preserve transaction-owned per-user serialization (the
coordination table can start empty). This change preserves the existing historical
live-reject exemption, soft-deletion behavior and oldest-live-file submitter
semantics; it does not redefine which submissions count against the quota.

The pre-fix integration witness in `run-Fr724uof` admitted two distinct uploads
while validation was paused and then observed **two successful submissions where
one was expected**. It failed its regression assertion as intended. The first
attempt failed to compile due to a test-fixture argument error and is not counted
as a reproducer. Final validation results follow below.

The added integration coverage exercises both early admission and authoritative
finalization: two admitted uploads; two service instances; a second worker observed
waiting on the first creator's database lock; Trial Curator/staff exemptions;
non-rejected, rejected, deleted, deleted-rejection and original/live-uploader
ownership cases; existing-submission updates; history committed after admission;
other-user progress while one quota lock is held; rejected-file cleanup; and a
failure injected after source/notification writes, with MariaDB and PostgreSQL
rollback checks followed by a successful retry. Lock ordering is established with
barriers and observed database statements, not a sleep-based race assumption.

**D-UPLOAD-STATUS-01 — discovered by the quota race tests and fixed.** The first
repeated race-detector run (`run-7LN3Xs0t`) passed its functional quota assertions
but exposed `SubmissionStatusKeeper.Get` returning the keeper's mutable pointer.
Workers could change status fields while callers polled or serialized them.
`Get` now copies the status and its optional message/submission-ID values under the
keeper lock. Pure tests verify that snapshots remain unchanged after worker updates,
caller mutation cannot corrupt keeper state, unknown IDs remain absent, and polling
can overlap status transitions. Those tests pass with `-race -count=3`.

Several initial integration attempts corrected test-only issues: incomplete
synthetic upload/bot history, the existing middleware's 401 response contract, and
a MariaDB lock-observer limitation. They are not additional product fixes or
successful validation baselines. The final waiter tests use the same server
statement observation approach as the existing parent-lock concurrency tests.

Final focused validation: `bash integration_tests/run.sh -run
'^TestSubmissionQuota' -count=3 -race -failfast=false` passed in
`integration_tests/results/run-hfMwKUCy`: **21 top-level executions, 66 total
test/subtest passes, 34.471 seconds**, no race warnings, successful cleanup.
The final related unit suites also passed with
`go test -race ./service ./database ./types ./transport ./utils -count=1`.
Independent source review found no remaining issue in quota serialization,
role/ownership exemptions, snapshot freshness, cleanup or status-copy behavior.

Final full integration validation: `bash integration_tests/run.sh -failfast=false`
passed in `integration_tests/results/run-x203n5iC`: **77 top-level tests, 336 total
test/subtest passes, 121.923 seconds**, exit status 0 and successful cleanup.
Apply migration **0028** before deploying this quota fix. The production snapshot
databases were not changed by this work.

### Importer contract draft (2026-09-09)

This is the implementation contract for the first rehearsal, not an implemented
importer or an approval to repair source databases. It is grounded in the restored
`run-ZhQH8Txh` column/index/FK inventories, MariaDB migrations 1–28,
PostgreSQL migrations 1–13, and the search/receiver/event DAL code. It refines the
schema proposals above. The reference pair stays immutable; staging and target
are new databases. A successful transfer must explain every input row, including
rows intentionally omitted or collapsed, rather than merely finish without SQL
errors.

#### Table mappings and import order

Keep the existing PostgreSQL tables, `citext` types, sequences, indexes, triggers
and changelogs intact in the target. Do not merge `masterdb_game` into `game`:
legacy search is a distinct source with its own identity and historical metadata.
MariaDB application table names can remain unchanged because they do not collide
with the existing application tables. Migration bookkeeping **does** collide:
both engines have `schema_migrations`. Record both source versions in the import
manifest; use one explicitly designed target migration history, never overwrite
PostgreSQL version 13 with MariaDB version 27/28 or concatenate their histories.

| Source/group | Initial unified-target mapping and dependencies |
| --- | --- |
| `discord_user`, `discord_role`; `action`, `submission_level`, `submission_notification_type`, `curation_image_type` | Preserve names, IDs and actual rows, including system users and all 15 snapshot actions. Import before children. Target bootstrap seeds must be compared with these rows; conflicting same-ID/name definitions fail, rather than `ON CONFLICT DO NOTHING` hiding a mismatch. |
| `discord_user_role` | Preserve membership IDs and both FKs after users/roles. Audit duplicate `(fk_uid,fk_rid)` pairs before proposing a new pair-unique constraint; this was not part of the reported subscription/preference checks. |
| `submission` | Preserve IDs, level, tombstone/reason, freeze time and nullable `should_autofreeze`. Deleted parents remain present. |
| `submission_file` | Preserve IDs, uploader/parent, exact filenames, size, hashes, creation/deletion timestamps and reason. Keep existing uniqueness of current filename and hashes with explicitly tested comparison semantics. No archive reading, copying or hash recomputation is required for database import. |
| `curation_meta`, `curation_image` | Preserve source IDs and file FKs, nullable metadata, UUID spelling, `game_exists`, JSON and stored image filename/type. Metadata/file one-to-one currently fits the snapshot, but adding a unique file FK still requires a cutover-time check. External image/archive availability is a separate deployment concern. |
| `comment` | Preserve IDs, nullable message/action, author/parent and microsecond timestamps/tombstones. Import history verbatim; do not replay service operations, regenerate comments or recalculate timestamps. |
| `submission_notification_subscription`, `notification_settings` | Load staged rows after users/submissions/actions; apply only the explicitly described duplicate policy below before enabling pair-unique constraints. |
| `submission_notification` | Preserve notification ID, type, message, creation and sent time. Keep the two unsent rows unsent. Disable consumers in rehearsal; do not call notification-producing service methods during import. |
| `masterdb_game` | Preserve its numeric ID and unique textual UUID, all legacy fields, nullable dates and year-1000 sentinel. Its rows are not additional `game` inserts and a UUID overlap with `game` is not a duplicate to remove. |
| `submission_cache` | Retain an exact staging copy for evidence. Build target active caches from imported source history using the agreed reducer; compare against the **rebuilt** MariaDB baseline, while separately reporting known original-cache repairs. Preserve deleted-parent cache semantics explicitly; active-only rebuilding must not accidentally convert the 191 deleted-parent rows into unexplained row losses. |
| `session`, `oauth_client` | Rehearsal imports zero rows from both sanitized tables and asserts this before and after transfer. Create their target schemas; do not fabricate replacement secrets. A later live cutover needs an explicit session invalidation and OAuth-client reprovision/preservation decision; sanitized rehearsal cannot establish live-auth migration correctness. |
| `submission_creation_lock` (migration 28) | Coordination state only; create an empty target table with a unique user key/FK. Snapshot version 27 has no such table; a version-28 live source may have rows, but historical lock rows carry no quota value and need no transfer. Preserve the per-user transaction-lock protocol in the PostgreSQL service port. |

Copy existing PostgreSQL data using PostgreSQL-native restore into the empty
rehearsal target, preserving its trigger definitions without generating new
changelog/event rows by replaying mutations. Then load MariaDB staging, transform
in dependency order, validate constraints and rebuild derived data. Circular
cache pointers are avoided by loading source submissions/files/comments first.
Never import directly into the reference pair or a partially live destination.

#### Types, NULLs and text semantics

- Map signed MariaDB `BIGINT` IDs, Discord snowflakes, sizes and numeric flags to
  PostgreSQL `bigint`. Preserve explicit IDs; do not serialize through floating
  point. Auto-increment application IDs become identity/sequence-backed columns;
  external IDs and enum IDs remain explicit. Reseed each affected sequence to at
  least the greater of the imported maximum and the recorded source allocation
  high-water mark (when available), respecting PostgreSQL's `is_called`/empty
  table behavior. Verify the first generated value cannot collide. Keep existing
  PostgreSQL sequences from its restore independently verified.
- Map MariaDB `DATETIME(6)` to `timestamp(6) without time zone` interpreted as UTC
  by the application for the initial parity port. Export connections use UTC;
  serialize six digits without local-time conversion or rounding. The MariaDB
  `session.created_at` is a seconds-precision `TIMESTAMP`, so decode it in UTC
  explicitly. A later switch to `timestamptz` should be its own tested change.
  Preserve year `1000-01-01` and NULL dates; do not substitute epoch, infinity or
  current time. Do not perturb the 6,149 comment tie groups or 165,417 cross-table
  timestamp equalities to manufacture ordering: retain the agreed ID tie-breaks
  and current strict file/comment boundary until its separate decision changes.
- Map `TEXT`/`MEDIUMTEXT` to `text` and preserve nullable values, including the
  954,009 NULL comment messages and 604 NULL metadata titles. Keep empty strings
  distinct. Preserve original file/path/hash bytes; never trim or split filenames
  on commas. Check for embedded NUL characters and incompatible encodings before
  COPY: PostgreSQL text cannot contain NUL. Report affected IDs/columns and block
  promotion rather than silently removing bytes.
- Map actual boolean domains (`game_exists`, `should_autofreeze`) to `boolean`
  only after checking every non-NULL source value is 0 or 1; preserve nullability.
  Numeric flags stay numeric. JSON `additional_applications` can initially use
  PostgreSQL `json` to avoid introducing `jsonb` normalization as part of transfer;
  preserve SQL NULL versus JSON null and validate PostgreSQL acceptance. A later
  `jsonb` choice needs duplicate-key/number-range/Unicode checks and a deliberate
  decision about normalization, not just MariaDB `JSON_VALID` success.
- Keep `curation_meta.uuid` and legacy UUID values textual for the initial port.
  MariaDB metadata permits arbitrary nullable text and existing `game.id` is
  textual; native `uuid` is optional optimization, not a prerequisite for one
  database. Before any cast, enumerate malformed/empty values, normalization
  collisions and join changes. Never replace malformed UUIDs with NULL or drop
  them silently.
- Do not claim `citext`, lowercasing or a PostgreSQL locale reproduces
  `utf8mb4_unicode_ci`. Staging keeps original text. Target equality/uniqueness
  and search need their own fixture matrix for case, accents, Unicode, trailing
  spaces, `%`, `_` and backslash; existing PostgreSQL `citext` remains unchanged.
  Select indexes/collations only after those contracts are measured. A new
  collation's collision inventory is a promotion gate for unique text keys.

#### Duplicate and orphan policy

For subscriptions, the proposed deterministic rule is one row per
`(fk_user_id,fk_submission_id)`, keeping the row with the earliest `created_at`,
then lowest `id`. Preserve that winner's ID/time and export every discarded ID
with its winner mapping. It would remove **35,366** snapshot rows, leaving
**406,314** from 441,680. There is no declared FK to subscription IDs in the
inventory. Still check code consumers and recipient parity before applying this
rule; the existing notification tests require unique recipients, not duplicate
storage. Treat the rule as the target proposal until the importer regression
proves membership, unsubscribe and notification behavior unchanged.
The target DAL must make subscription insertion idempotent on that pair (for
example, `ON CONFLICT (fk_user_id,fk_submission_id) DO NOTHING`) when the unique
constraint is introduced. Merely adding the constraint would turn ordinary repeat
subscriptions during uploads into errors. Keep the existing pair-based unsubscribe
behavior. Preference writes also need duplicate input handling if a pair-unique
constraint is added; validate repeated/concurrent writes, not just imported rows.

Preferences have no duplicates in this snapshot. Recheck pair uniqueness at
cutover; if duplicates appear, propose the same lowest-ID representative (there
is no preference creation timestamp), record the mapping and verify recipient
parity. Do not silently generalize this rule to roles, metadata, events, files or
comments. Metadata/cache duplicates are ambiguous and must fail validation with
an actionable report. Cache values cannot be merged by picking an arbitrary row;
source-history reconstruction is the authoritative repair. Add the proposed
unique cache parent key only after checking NULL keys and duplicates in staging.

Copy the **143 existing PostgreSQL additional-app rows with missing games**
unchanged. Label them accepted preexisting anomalies in the rehearsal report;
do not add a validated FK, delete the rows or invent parent games in this scope.
Keep their complete original records available. Any remediation/new FK is a
separate decision with a deletion/import-path reproducer. Preserve changelogs
and activity history even when their historical entity has been deleted.
Declared MariaDB FK violations or new unexpected source-to-target orphan changes
block promotion; retaining a raw staging row is not equivalent to silently
excluding it from the application target.

Unification exposes relationships that the existing audit did not fully check.
Before finalizing new FKs, aggregate `activity_events.uid` against
`discord_user.id`, enumerate event-area/operation-specific identifiers in
`event_data`, and compare nonempty metadata/legacy UUIDs with `game.id` and
redirects under existing comparison semantics. `curation_meta.game_exists` is
recorded during validation/upload and is searched as persisted metadata; it is
not a live FK assertion. A new submission legitimately need not already exist in
`game`, and historic events may outlive their entity. Report missing references
by relationship and category; do not recalculate `game_exists` or rewrite event
JSON merely because the databases now share a connection.

#### Rehearsal acceptance and remaining choices

Produce an import manifest with source hashes/versions, target migration version,
row counts before/after, explicit transformations, sequence states, constraint
results, and per-table canonical checksums that retain NULL/empty distinctions.
Every exception has a count and reproducible row mapping. Rerun into another
fresh target and require identical logical contents, excluding expected runtime
metadata. Keep restore/import/cache-build durations separate from search timings.

Promotion requires shared synthetic suites plus real-data ordered search/count
parity against rebuilt MariaDB caches; original cache mismatches remain an
independent report. Verify existing PostgreSQL table counts and logical contents
are unchanged, including changelogs and the 16.37-million-row index table. Exercise
new writes and rollback after sequence reseeding and constraint validation.
Importer reruns must either refuse a populated target or recreate their own
explicitly disposable target; broad conflict-ignore/upsert behavior is not an
acceptable substitute for restart semantics.

These implementation choices are now resolved: preserve measured source text
semantics, enforce idempotent subscription pairs after mapped deduplication, and
retain the existing cache representation while rebuilding it through scoped
PostgreSQL history queries. Typed cache collections are a possible later change. The 143 orphan
repairs, live-auth cutover handling and strict tied-time boundary changes remain
separate decisions. Missing audit checks above are implementation work, not
assumptions that the snapshot is clean.

### Search baseline and focused cache coverage (2026-09-09)

Added the production-shaped stale-collection case to the existing
`TestSubmissionCacheRebuildRepairsAndRepeats`, retaining its stale approval,
missing-cache and repeatability checks. The new phase deliberately leaves the
newest-file pointer correct while replacing original/current filename and
MD5/SHA256 collections with values from the older live file. Each newest-file
search must miss before repair and return the exact expected projection/count
after rebuilding; a further unchanged rebuild still agrees. All four
`TestSubmissionCacheRebuild*` integration tests passed in
`integration_tests/results/run-wsx8GPco` (2.174 seconds, exit 0, successful cleanup).
This covers the observed cache inconsistency without claiming a reproduced
current mutation defect or adding a duplicate approval-repair test.

`make snapshot-search` now captures the actual current `SearchSubmissions` DAL
against the full original `fpfss` and rebuilt `snapshot_cache_work_scoped`
databases. It requires a successful, complete and repeatable cache audit. Every
pooled connection is read-only, including the separate count connection. It does
not modify either snapshot or require external files. A fresh artifact directory
contains deterministic workload parameters, full ordered projections/counts,
parameterized page/count SQL, JSON EXPLAIN plans, timing samples, engine settings,
source/image identity and Docker CPU/memory/I/O context. Artifacts contain actual
application projections and remain in ignored `snapshot-results/`.

The first corpus covers 28 workloads: broad searches with and without legacy,
second/deep pages, updated/uploaded/size sorting, selective title/ID/hash filters,
broad platform inclusion/exclusion, a busy original uploader, approval/assignment
and historical-action predicates, content changes, subscriptions and positive and
negative membership in each of the five reviewer sets. Workload identities and
parameters come from the original snapshot and are reused unchanged against the
rebuilt one. Each initial result is followed by three timed repetitions that must
match its canonical digest. Canonicalization sorts only reviewer/action arrays,
retaining duplicates, NULL/empty distinctions and the exact outer result order.
This captured behavior complements independently expected synthetic tests; it is
not a claim that the old database is a correctness oracle.

One logical request runs at a time, with the existing DAL's parallel page/count
strategy intact. Timings distinguish first-observed requests, subsequent warm DAL
requests, and serial standalone page/count queries drained without DAL projection.
**First-observed is not cold**: the prior restore/audit and earlier workloads have
already warmed caches. Three samples do not establish a production p95 or capacity
limit. Plans are estimated EXPLAIN, not runtime row counts. Integration tests and
other database work finished before measurement. Cold starts, sustained concurrent
load and page-plus-window-count versus parallel count comparisons remain separate
controlled experiments once PostgreSQL query candidates exist.

The first instrumentation attempt (`search-YDazVArJ`) failed because embedding the
MySQL driver promoted its connector method and bypassed query capture. No completed
baseline is claimed from it. The wrapper now uses its own Open path, with a
regression test against that bypass. The baseline also rejects context errors,
failed counts, repeated-result drift and page/count cardinality disagreement. The
last check guards against accepting truncated pages: the existing DAL does not
check `rows.Err()` at the end of iteration. This is an inspected error-handling
gap, not a newly reproduced snapshot failure; do not infer a SQL semantic change
from it. Canonicalization tests preserve duplicates, NULLs and outer ordering.

The broad-query plans support three concrete PostgreSQL experiments, after parity:

1. Build a predicate-only count, counting live and legacy branches separately
   under the agreed `UNION ALL` semantics. Keep every filter/ownership/deletion
   predicate, but avoid projecting filenames, avatars and other display fields
   that cannot affect membership. Compare its parallel execution with a window
   count; no strategy is assumed faster beforehand.
2. Select matching identities and sort keys before hydrating the requested page.
   Fetch display metadata/users and file counts only for those IDs when filter
   semantics permit it. The current broad plan aggregates the entire live-file
   table even for a 100-row page. Preserve global live/legacy ordering and all
   deterministic ties; filters that use metadata must still be applied before
   pagination.
3. Prove and enforce cache/metadata one-to-one parent relationships, then test
   removing defensive `GROUP BY submission.id`. Current estimates multiply those
   joins into substantially more rows than the actual submission count. Do not
   remove grouping without uniqueness and imported-data validation, or compare
   estimated rows as though they were measured runtime rows.

These are port/optimization candidates, not MariaDB behavior changes made in this
slice. Keep counts, ordered identities and projections fixed during each
experiment; report query-plan and latency changes independently from cache repair.

#### Completed standard baseline

`search-yowm3oMb` under `snapshot-results/run-ZhQH8Txh/` completed successfully
(exit 0 and `complete` marker): **28 workloads × 2 cache variants**, **224 DAL
requests** including three warm repetitions per variant, **56 ordered corpora**
(4,530 projected rows across those pages) and **112 page/count plans**. All counts,
page cardinalities and repeated projections passed. All 28 original/rebuilt corpus
pairs agree. This does not mean the caches were identical: the known repairs can
fall outside these selected pages/memberships. A separate targeted suite below
covers them. The standard measurement phase took approximately 796 seconds;
that is workload collection time, not one-request latency.

Selected results (milliseconds, median of three warm DAL calls):

| Workload | Matches | Original cache | Rebuilt cache |
| --- | ---: | ---: | ---: |
| Default, including legacy | 270,890 | 4,233.4 | 4,488.3 |
| Live only | 148,164 | 3,088.5 | 3,107.4 |
| Combined page 1,000 | 270,890 | 4,511.9 | 4,700.1 |
| Single submission ID | 1 | 109.2 | 109.7 |
| Selective title | 1 | 1,549.1 | 1,534.5 |
| Exact hash | 1 | 250.7 | 261.6 |
| Busy original uploader | 19,556 | 576.9 | 552.0 |
| Subscribed by selected user | 75,406 | 2,225.1 | 2,280.4 |
| Testing assigned to selected reviewer | 1 | 188.2 | 203.2 |
| Testing not assigned to that reviewer | 148,163 | 3,252.2 | 3,856.4 |

These are local exploratory measurements on Docker Desktop/aarch64 with 10
advertised CPUs, approximately 9.7 GiB VM memory and a 1 GiB InnoDB buffer pool,
MariaDB 11.1.5, UTC, REPEATABLE READ and query cache OFF. Connection collation was
`utf8mb4_general_ci`; column collations remain as audited. CPU/memory/I/O snapshots
and exact engine variables are retained in the run. They are not isolated physical
hardware capacity measurements or production performance promises. The difference
between original/rebuilt timings alone is not evidence that cache rebuilding
improves or regresses query performance.

Default search returns 148,164 live submissions plus 122,726 legacy games. On the
rebuilt copy, its standalone page query took 3,282.6 ms and count took 4,216.2 ms;
the count plan still includes display joins and a wide derived union. Even the
single-ID case takes roughly 110 ms and a one-match title filter roughly 1.53 s.
This strengthens the case for reducing unnecessary aggregation/projection and
measuring substring-index options with the agreed collation contract; it does not
justify replacing substring behavior with full-text search. Preserve this corpus
before changing query structure.

#### Targeted repair witnesses and validation

`SNAPSHOT_SEARCH_SUITE=repairs make snapshot-search` completed after the standard
run, without overlapping its measurements. `search-4B4rfrRq` contains **14
workloads × 2 variants**, **112 DAL requests**, **28 corpora** and **56 plans**,
with exit 0, a `complete` marker and no query/cardinality/repetition failures.
Filters are derived from source-file values for the two changed file caches;
bounded history batches include all 521 changed approval/comment-pointer cases.
The suite is a correctness witness, not representative latency sampling.

- Seven filename/hash searches changed from **0 to 1** match after repair: current
  filename and both hashes for 80680, and all four filters for 142578. The original
  filename filter for 80680 already matched and remains **1 to 1**. These are
  precisely the cache-collection repairs, not new source files or importer changes.
- Of the 521 history witnesses, **12 search projections changed**: 11 selected a
  different updater ID/name/avatar through the agreed newest-comment tie rule,
  and 12551 changed its approval membership. The remaining 509 projections were
  unchanged. No other projected fields changed; in particular the comment ties
  did not change the displayed timestamps. A pointer change need not produce a
  visible change when both comments have the same author/time.
- `difference-summary.json` records the field counts and affected IDs. The
  original and rebuilt corpora remain separate. PostgreSQL parity should match
  the rebuilt projection for these agreed repairs; retaining the original
  behavior here would preserve stale data or the superseded tie rule.

Tool verification passed: baseline Go tests with `-race` (connector-bypass guard,
page-cardinality edge cases and canonicalization fidelity), the three existing
snapshot Python tests, shell syntax and `git diff --check`. The existing cache
integration extension passed as recorded above. No application SQL or production
snapshot contents were changed in this slice.

**Next at that checkpoint (implemented below):** implement a restart-safe staging/import rehearsal into a fresh PostgreSQL
target, starting with the explicit preflight gaps in the importer contract
(encoding/NUL, boolean domains, UUID/collation collisions and logical references).
Test transformation/deduplication/sequence behavior on synthetic edge cases, then
account for every real row. Preserve the existing PostgreSQL workload while porting
the MariaDB DAL and run synthetic plus saved-corpus parity before query optimization.
The baseline establishes what to compare; it does not resolve collation semantics
or promise a speedup merely from changing engines.


### PostgreSQL implementation and validation (2026-09-09)

The user confirmed the scope: move MariaDB contents and their SQL/code only;
existing PostgreSQL tables, objects and application behavior are preserved. No
legacy/current game table merge or existing orphan cleanup is authorized or
needed. Public row IDs are contractual and must survive exactly. The only planned
row reduction is internal duplicate subscription pairs, with a survivor mapping.

A requested Terra history investigation found no evidence linking the duplicate
subscriptions to arbitrary subscription-menu values. Commit `440a03f` introduced
`subscribed-me` together with yes/no validation. Commit `3f7cf34` introduced
unconditional subscription insertion without a pair-unique constraint; repeated
PUT subscribe calls and file/meta uploads still repeat that insert. Commit
`b2b1ed5` changed the subscribed check from `count == 1` to `count > 0`, which handles
existing duplicates without removing them. Recipients use DISTINCT and pair-based
unsubscribe deletes all copies. The PostgreSQL port therefore combines import
survivor mappings with a pair-unique key and idempotent writes; tests explicitly
check persisted pair counts, recipient membership and unsubscribe, including two
simultaneous subscribers. This is evidence of ordinary duplicate-producing paths,
not proof of the historical cause of every snapshot duplicate.

The broadened history search found the likely remembered menu bug in notification
preferences: `8fc447cb` introduced profile action submission without an application
allowlist; `b7bfd02` (2026-04-10, “Audit notificaitons staff only (#27)”) added the
role-aware action allowlist and 400/403 rejection. Unknown strings previously
failed the parameterized database insert and rolled back; existing but unintended
action names could be stored. This was not SQL injection. Preference duplicates
can separately be produced by repeating a valid form parameter, whereas ordinary
subscription flows already repeat inserts. The current snapshot contains no
preference duplicates. The PostgreSQL preference writer now collapses repeated
valid actions with its pair-unique constraint; invalid/staff-only action checks and
rollback remain tested. These are separate tables and causes.

Implementation now includes new submission-only PostgreSQL migrations, a separate
submission DAL using PostgreSQL SQL, scoped state/cache aggregation, single-statement
search page/count, backend configuration and dialect-specific mutation locks. The
existing native PostgreSQL DAL remains untouched. `SUBMISSION_DB_ENGINE=postgres`
selects the same database identified by the existing POSTGRES_* configuration;
MariaDB mode remains available to run the reference tests. Import/rebuild/parity
commands operate only on explicitly disposable targets. Both-engine synthetic suites and the real-data import are validated above. The final
MariaDB duplicate-upload/quota race run `run-vBRdoYkp` passed 8 top-level / 25
test/subtest checks in 12.382 seconds. Complete real-data
cache/search parity and performance results are recorded below; this is not a
production deployment claim.

### First PostgreSQL submission import rehearsals (2026-09-09)

The clean, frozen-input run `snapshot-results/run-ZhQH8Txh/import-tO503ZAO`
completed with exit **0**, importing MariaDB snapshot version **27** into the new
PostgreSQL database `submission_import_import_to503zao` at migration **16**.
It cloned the existing PostgreSQL reference before adding the submission-only
schema; neither reference database was modified. Run it again with
`make snapshot-import` (the wrapper uses `scripts/submission-import/launch.sh`).
The launcher freezes the shell body, importer source, dependency metadata and
migrations before the long clone operation. Each invocation creates a fresh
explicitly disposable target and retains its manifest, source/dump hashes, image
ID and logs.

`results/manifest.json` reconciles **3,340,777 source rows** to **3,305,411 target
rows** across **19 imported tables**. The only row reduction is the approved
subscription deduplication: **441,680 → 406,314** rows, with **35,366** discarded
IDs mapped to their retained earliest-timestamp/lowest-ID representative in
`results/discarded-subscriptions.jsonl`. Every table's canonical transformed-source
and target SHA-256 matches, preserving 64-bit IDs, NULL/empty distinctions,
microsecond times, deleted history, and lexical JSON/text contents. Numeric
boolean domains are explicitly converted to PostgreSQL booleans. Both sanitized
auth tables contain zero rows; the quota coordination table starts empty. All
**148,355** original cache rows were copied, including the **191** deleted-parent
rows; rebuilding active caches and comparing search behavior is a separate stage.

All **23 original PostgreSQL data-table fingerprints** match before/after import,
including the **16,370,988-row `game_data_index`** and existing changelogs/activity
history. These fingerprints combine row counts with an order-independent sum of
PostgreSQL 64-bit row hashes; they are change detectors, not cryptographic
canonical checksums. Existing PostgreSQL migration bookkeeping deliberately moves
from 13 to 16, while the source MariaDB version is recorded separately. Existing
catalog tables, functions and application behavior are not merged into submission
tables.

`repeatability.json` compares the clean run against the earlier successful
exploratory target `submission_import_import_oncjg47l` (`import-oNCjg47L`, schema
14). It confirms identical per-table canonical hashes, source/imported/discarded
counts, sequence high-water marks, the complete subscription discard mapping and
original PostgreSQL fingerprints. The exploratory wrapper initially failed a
checksum gate; its successful retry is recorded separately in `retry.log` and
`retry-exit-code.txt`. The clean run is the authoritative reproducible command
result. COPY plus per-table reconciliation in that run took **180.75 seconds**;
clone time, preflight and original-catalog scans are excluded, so this is not an
end-to-end migration or application performance measurement.

Preflight and completed COPY/constraints established:

- No unexpected source tables, declared MariaDB FK orphans, duplicate cache parents,
  duplicate metadata file keys, duplicate preference pairs or duplicate role pairs.
- All copied text passed UTF-8/NUL validation; JSON, zero-date and boolean-domain
  checks passed. Target comparison-based unique indexes accepted the complete
  imported corpus without dropping conflicting rows. Imported identities were
  reseeded to at least the source allocation high-water mark and beyond every
  preserved ID; this includes empty sanitized session rows' prior allocation.
- Four noncanonical metadata UUIDs remain textual and unchanged: metadata IDs
  **105145, 110699, 110729, 159530**. There are **85,738 NULL metadata UUIDs**.
  Under existing catalog `citext` comparison, **78,597** nonempty metadata UUID
  rows and **334** legacy UUID rows have no current catalog game. These are audit
  observations, not foreign-key requirements or permission to recalculate
  persisted `game_exists` values.
- Existing activity-event user IDs all resolve to imported Discord users. Event
  **363585** (`submission`/`update`) references absent comment **1069982**; the
  historic event is preserved. The checked numeric `submission_id` and `file_id`
  event references resolve. This audit covers those explicitly named JSON keys,
  not arbitrary future event payload shapes.
- The **143 existing PostgreSQL additional-app orphans** remain unchanged.
  Identifier-only anomaly details are reproducible with
  `scripts/submission-import/relationship-audit.sql`; the first run's
  `relationship-audit.tsv` records the five UUID/event observations above.

The importer uses a read-only source snapshot and one atomic target transaction;
verified bootstrap enum definitions must match, while the two known bootstrap
user profiles are replaced by their authoritative mutable source profiles. A
checksum failure during development demonstrably rolled the target back to its
seed-only state. The cause was MySQL text-protocol integer decoding, corrected by
strict signed-int64 parsing with large-ID/overflow regression coverage. A second
import into a populated target was also rejected. PostgreSQL `setval` is
nontransactional: only new submission sequences are touched, and a retry reseeds
them before successful commit; no app may use an incomplete disposable import.

Focused Go tests cover endpoint guards, integer precision, boolean domains,
UTF-8/NUL/JSON handling, NULL/empty and microsecond distinctions, and sequence
high-water/exhaustion behavior. The launcher source-edit regression and final
manifest acceptance checks pass. These are importer and real-data transfer
results, not proof of the complete runtime port, live-auth cutover or deployment
readiness. Cross-engine cache/search parity, performance results and final
original-catalog preservation after those operations are recorded separately.
The prepared `scripts/submission-import/check-catalog.py` performs a bounded,
read-only post-parity fingerprint scan; run it **after** timed workloads because
it scans the complete existing catalog/index data.

### Real-data PostgreSQL cache reconciliation and initial search plan

The first parity run, `import-tO503ZAO/parity-s1fhPXbP`, completed both cache
rebuilds: 148,164 eligible/visited/rebuilt submissions in 61.25 and 60.06 seconds,
zero failures. The second persisted cache digest was identical. The first pass
changed 523 caches semantically, matching the previously documented 520 comment
pointer tie repairs, two stale file collections and one stale approval. There
were 560 raw changes because deterministic ordering also changes unordered sets.

Full comparison with `snapshot_cache_work_scoped` covered all 148,355 cache rows,
including deleted parents. Zero semantic field differences remained. The 37 rows
with raw differences differ only in unordered collections; filenames, NULL/empty
values and duplicate members remain significant. Detailed named-field evidence is
in `data/full-cache-parity.json`.

This first search run is **incomplete**, not a passing baseline. It recorded 14
workloads before being stopped following a broad-platform query shared-memory
failure. The retained container had 64 MiB `/dev/shm` even though the current
snapshot Compose definition specifies 1 GiB. It was recreated with exactly the
same image and data volume and verified healthy at 1 GiB; the parity launcher now
checks this condition before running. `environment-followup.json` records the
correction. No global PostgreSQL tuning or existing application SQL was changed.

The completed default workload matched the entire saved MariaDB projection but
took 4.372 seconds versus MariaDB's 4.488-second median. Its plan hydrated 148,164
live submissions before LIMIT, evaluated their file-count subqueries, sorted wide
rows and wrote about 232 MiB of temporary blocks. The next source revision instead
materializes narrow IDs/sort keys first and hydrates only the selected page.
Legacy hydration uses its source row ID, while public UUID identity and ordering
remain unchanged. The full synthetic rerun and complete real-data parity for this separately
measured optimization passed below.

### Final PostgreSQL parity and measured performance

Final frozen run **`import-tO503ZAO/parity-KHSSX9Fj` passed**, exit 0, with
`data/report.json` complete and no error. It ran from 21:08:03 to 21:11:37 UTC:

- Both complete cache traversals rebuilt all **148,164** active submissions in
  **59.70 / 59.90 seconds**, with no failures or changes from the already repaired
  cache. Both persisted SHA-256 digests match the first successful rebuild.
- Full named-column cache comparison covers **148,355** rows, including deleted
  parents: zero semantic differences from rebuilt MariaDB. The 37 raw collection
  ordering differences are retained in the report, not silently discarded.
- **42/42 search workloads match**, across 28 standard and 14 repair cases:
  complete ordered projections, NULLs, duplicate members, counts and pagination.
  First calls plus three warm repeats give **168 matching DAL calls**, followed
  by 42 PostgreSQL `EXPLAIN (ANALYZE, BUFFERS)` plans. No timeout, result mismatch,
  repeat mismatch or query error occurred.
- **34 workloads have lower warm medians; 8 have higher medians** than the saved
  rebuilt-MariaDB baseline. This is not a claim that every query improved.

Representative median page-plus-count latencies:

| Workload | MariaDB | PostgreSQL | MariaDB / PostgreSQL |
| --- | ---: | ---: | ---: |
| Default listing | 4,488 ms | 631 ms | 7.11× |
| Live only | 3,107 ms | 303 ms | 10.26× |
| Deep page | 4,700 ms | 753 ms | 6.24× |
| Size ascending | 4,601 ms | 294 ms | 15.67× |
| Selective title | 1,534 ms | 504 ms | 3.04× |
| Broad platform | 3,834 ms | 1,258 ms | 3.05× |
| Subscription membership | 2,280 ms | 405 ms | 5.64× |
| Submission ID | 110 ms | 5 ms | 21.67× |
| Selective hash | 262 ms | 336 ms | 0.78× |
| My testing assignment | 203 ms | 293 ms | 0.69× |
| My verification assignment | 183 ms | 304 ms | 0.60× |
| My ongoing changes | 187 ms | 309 ms | 0.61× |
| My approval | 213 ms | 349 ms | 0.61× |
| My verification | 289 ms | 372 ms | 0.78× |

The other two slower cases are small repair-history batches: 5.08→6.81 ms and
2.80→3.83 ms. Keep them visible, but three warm repetitions cannot establish the
stability of millisecond-scale differences. Positive reviewer membership and
unconstrained hash searches are the meaningful remaining selective-query targets;
negative membership and broad searches already improve. Inspect their saved plans
before choosing expression indexes or a typed collection representation. Do not
change substring/collation semantics merely to use an index.

The default optimized plan evaluates the file-count subquery **100 times instead
of 148,164**, and writes **3,784 temporary blocks instead of 29,670**. Narrow page
selection is the measured change; the original wide-query run is retained as an
incomplete diagnostic, not a second complete baseline.

Measurements used the same Docker Desktop VM, with no concurrent test/import jobs.
MariaDB is 11.1.5 aarch64 and PostgreSQL is 15.18 x86_64 under the host's emulation;
these are local comparative results, not universal production speedups. PostgreSQL
retains default 128 MiB shared buffers and 4 MiB work memory, UTC/read-committed
sessions, and the corrected container 1 GiB shared-memory allocation. First calls
are recorded separately and are not described as cold-cache measurements. The
MariaDB baseline was collected earlier; there is no contemporaneous mixed-load,
p95/p99 or throughput claim. The earlier MariaDB cache reconciliation used scoped
scratch histories, so its duration is not a fair direct cache speedup denominator.

### Remaining rollout work

The schema, importer, submission SQL port and correctness comparison are now
implemented. Keep MariaDB mode available as the reference until cutover is accepted.
The follow-up query work below addresses the measured selective-query regressions.
Next collect longer controlled timings, then exercise mixed submission and existing
launcher/index workloads under representative
concurrency to measure latency tails, throughput, pool/lock waits and contention.
Existing PostgreSQL query text has not changed, but sharing its server with the
submission workload still needs that capacity check.

A production cutover is a separate operational step: rehearse a maintenance/write
freeze, fresh consistent snapshots, deployment configuration, final import and
cache reconciliation, application smoke tests, and recovery to the retained old
pair. The current importer deliberately accepts sanitized isolated snapshots and
refuses populated targets; handling live authentication and production endpoints
requires an explicit cutover workflow. No production database or deployment was
changed here. Both DAL transaction interfaces remain separate as agreed; this
port does not add cross-domain commit atomicity.

Final preservation verification: `import-tO503ZAO/post-parity-catalog.json` reports
all **23** original PostgreSQL table fingerprints unchanged after both parity
runs, including the full index table. The checker used a read-only repeatable-read
transaction. Existing `database/postgresdal.go` and migrations 1–13 have no changes.

Final compatibility review also repaired duplicate-file error classification:
wrapped MariaDB 1062 and PostgreSQL 23505 now produce the same public conflict
response. Unit tests cover both codes and unrelated integrity errors; real async
uploads cover duplicate new-submission and existing-submission uploads, complete
persisted rollback, the checksum failure message and rejected-archive cleanup.
The final full PostgreSQL and focused MariaDB runs above include these cases.
Go race tests pass for database, service, types, transport, utils, importer and
parity packages; four snapshot/import launcher Python tests, shell syntax checks
and `git diff --check` also pass.

### Optimization inventory and timing qualification (before follow-up)

The user reports that the Mac may have been unplugged during part of the
measurements. Power state was not controlled or recorded, so these baseline
latencies and ratios remain provisional. Collect more repetitions under stable
power and load before making precise regression or acceptance claims. No new
optimization is included in this inventory update.

These are workload variants of `SearchSubmissions`, not 42 unrelated SQL methods.
Times are median warm end-to-end DAL calls from three repeats, including page and
count (parallel statements on MariaDB, one statement on PostgreSQL), transaction
setup and row decoding. MariaDB values use the rebuilt-cache reference. Each
workload has saved parameters, complete output and SQL/plans.

| Saved workload | MariaDB median (ms) | PostgreSQL median (ms) |
| --- | ---: | ---: |
| `all-updated` | 4488.31 | 631.32 |
| `live-updated` | 3107.43 | 302.89 |
| `all-page-2` | 4297.25 | 713.28 |
| `all-deep-page` | 4700.09 | 753.39 |
| `all-size-asc` | 4600.73 | 293.52 |
| `all-uploaded-asc` | 4457.52 | 301.47 |
| `title-selective` | 1534.49 | 504.41 |
| `platform-broad` | 3834.17 | 1258.46 |
| `platform-exclusion` | 4021.19 | 1620.30 |
| `submission-id` | 109.72 | 5.06 |
| `busy-submitter` | 551.99 | 270.46 |
| `hash-selective` | 261.62 | 336.22 |
| `approved` | 2696.46 | 428.45 |
| `unassigned-testing` | 3241.11 | 408.28 |
| `not-rejected` | 3496.77 | 559.36 |
| `subscribed-me` | 2280.36 | 404.56 |
| `not-subscribed-me` | 1716.19 | 624.28 |
| `content-changes` | 1597.44 | 278.40 |
| `me-testing-assigned` | 203.24 | 293.44 |
| `me-testing-unassigned` | 3856.43 | 694.13 |
| `me-verification-assigned-assigned` | 183.38 | 304.31 |
| `me-verification-assigned-unassigned` | 3111.91 | 644.97 |
| `me-changes-ongoing` | 187.03 | 308.65 |
| `me-changes-none` | 3116.77 | 627.23 |
| `me-approval-yes` | 213.19 | 348.54 |
| `me-approval-no` | 3398.08 | 746.50 |
| `me-verified-yes` | 289.21 | 372.09 |
| `me-verified-no` | 3110.19 | 715.86 |
| `repair-file-80680-original` | 119.59 | 3.97 |
| `repair-file-80680-current` | 105.16 | 4.40 |
| `repair-file-80680-md5` | 114.91 | 4.12 |
| `repair-file-80680-sha256` | 131.38 | 5.13 |
| `repair-file-142578-original` | 109.08 | 1.99 |
| `repair-file-142578-current` | 120.38 | 1.65 |
| `repair-file-142578-md5` | 118.78 | 0.84 |
| `repair-file-142578-sha256` | 116.27 | 1.78 |
| `repair-history-000` | 5.08 | 6.81 |
| `repair-history-001` | 4.99 | 2.85 |
| `repair-history-002` | 6.08 | 4.01 |
| `repair-history-003` | 6.24 | 3.17 |
| `repair-history-004` | 6.03 | 3.37 |
| `repair-history-005` | 2.80 | 3.83 |

Primary next candidates are the five positive per-user reviewer membership
filters and the unrestricted selective hash query, followed by broad platform
inclusion/exclusion and remaining broad search/count work. The 14 repair
workloads remain correctness guards, not primary optimization targets. Individual
cache-refresh, notification-recipient/queue and similarity query timings have not
yet been baselined comparably; full cache traversal durations must not substitute
for that measurement. Existing PostgreSQL application SQL remains outside the
rewrite scope.

### Follow-up query optimization: shared matches and early membership filtering

Saved PostgreSQL plans in `parity-KHSSX9Fj` identified two avoidable costs:
positive reviewer membership wrapped in COALESCE stayed above outer joins (the
one-row testing-assignment result scanned all 1,290,139 comments), and separate
page/count branches repeated expensive hash/platform filtering. Platform plans
also spent substantial time compiling the duplicated expression/join work.

The candidate keeps the same schema and cache representation. Positive membership
uses exact text-array containment without COALESCE, allowing PostgreSQL to reject
NULL cache rows and filter before joining history. Negative membership retains
`NOT COALESCE(..., FALSE)`, so missing/NULL collections still mean nonmembership.
A narrow materialized `matches` relation supplies both page keys and total count;
projection and file counts remain limited to the selected page. No new indexes,
text/collation rules or existing PostgreSQL application code change.

Existing search coverage already checks all five Me/explicit-user families,
absent actors, exact numeric identity rather than substring matches, NULL sets,
legacy identities, mixed ordering, empty/past-end pages, tombstones and the
single-statement visibility contract. The focused PostgreSQL race run
`run-x5JocUvQ` passed all 12 top-level / 92 test-and-subtest checks in 8.641 seconds.
Real-data evaluation is recorded below when complete. For these query-only
iterations, `SUBMISSION_PARITY_REBUILD=0 make snapshot-parity` skips redundant
cache writes but still requires full read-only cache equivalence before searching.

First measured candidate `parity-J73sCrIJ` passed all 42 workloads/168 DAL calls
and full read-only cache comparison. Positive membership medians fell from
293–372 ms to 85–125 ms, hash 336→166 ms, platform inclusion 1,258→761 ms and
platform exclusion 1,620→824 ms. Size-ascending unfiltered listing increased
294→413 ms; unconditional match materialization was therefore not retained.

The refined implementation shares matches only when submission predicates exist.
Unfiltered listings retain the previous narrow-page and cheap parent-table count
path, in the same SQL statement/snapshot. Positive membership also explicitly
requires the cache column to be non-NULL: PostgreSQL marks both `string_to_array`
overloads non-STRICT, so array containment alone does not fully establish
outer-join rejection for the planner. Negative membership behavior is unchanged.
The full PostgreSQL suite and another complete corpus replay validate this final
revision below. No database migrations or indexes are added in this slice.

Final refined run **`parity-VJeRW57X` passed**, exit 0, with all **42 workloads / 168
DAL calls** matching saved MariaDB projections/counts and all **148,355 cache
rows** still semantically equal. This run was read-only against both snapshots;
cache rebuilds were deliberately skipped. The full PostgreSQL race suite
**`run-F3xrPiwH` passed 84 top-level / 345 test-and-subtest checks in 133.191 seconds**.
Database/service/types/transport/utils/parity unit race tests and shell/diff checks
also passed. No new test oracle was derived from the optimized SQL; the existing
independent expected-result, history and mutation tests supplied validation.

| Target workload | MariaDB median ms | Previous PostgreSQL ms | Refined PostgreSQL ms |
| --- | ---: | ---: | ---: |
| My testing assignment | 203.24 | 293.44 | 140.56 |
| My verification assignment | 183.38 | 304.31 | 120.42 |
| My ongoing changes | 187.03 | 308.65 | 142.25 |
| My approval | 213.19 | 348.54 | 130.23 |
| My verification | 289.21 | 372.09 | 119.65 |
| Selective MD5 | 261.62 | 336.22 | 164.28 |
| Selective title | 1,534.49 | 504.41 | 218.44 |
| Broad platform | 3,834.17 | 1,258.46 | 742.35 |
| Platform inclusion/exclusion | 4,021.19 | 1,620.30 | 755.79 |

The positive membership plans now filter directly in the cache scan and fetch
only matching comment rows by index: the one-result testing case reads one
comment for matching rather than scanning all 1,290,139. Hash/platform predicates
are evaluated in one matching branch; page and count reuse it. The explicit NULL
guard changes plan choices, including parallel worker startup; it is not evidence
that this refinement is faster than the first candidate on every execution.
Both variants improve the targeted workloads relative to the previous port.

All six unfiltered workloads have **byte-identical saved SQL and parameters** to
`parity-KHSSX9Fj` after retaining the old count path. Their timing differences must
not be attributed to a query rewrite: default listing measured 907 ms this time
versus 631 ms previously, while size ascending measured 264 versus 294 ms.
`optimization-comparison.json` records this check and all 42 before/after medians.
The final run has 40 lower medians than MariaDB; the two remaining higher values
are the small repair-history batches (about 6.18 versus 5.08 ms and 3.55 versus
2.80 ms). These are provisional measurements, not stable regression thresholds.

The runner now records macOS power source at workload start/end; both final
snapshots report AC power. This does not reconstruct earlier power/thermal state
or replace the planned longer controlled runs. Further index work should follow
those runs and include cache-write cost: this improvement introduces no new index
maintenance or schema/importer changes. Existing PostgreSQL application SQL and
objects remain untouched.

### UI flows and broader query optimization

The actual `/web/submissions` default is an empty filter (100 rows, updated DESC,
legacy included); Next preserves every filter and changes the page number.
`/web/my-submissions` sets the authenticated user's SubmitterID. The seven preset
buttons in `static/js.js` comprise three ready queues and four personal queues:
Ready for Testing, Ready for Verification, Ready for FP, My Testing, My
Verification, and each personal assignment combined with requested changes.
Ready queues sort by oldest upload ascending and combine multiple cache/action
predicates; they are not represented by a single global state filter.

A new `-suite ui` baseline supplies 24 exact UI workloads: first/next pages for
default, My submissions, username, Flash/Unity/HTML5 and Mario title searches;
first/next for the three ready presets; and all four personal presets. Preset
unit tests reconstruct selected form controls from the actual template/JavaScript
and assert all seven decoded filter definitions. Actors are selected from real
snapshot membership, including overlapping assignment/requested-change sets.
`SNAPSHOT_SEARCH_SUITE=ui SNAPSHOT_SEARCH_VARIANTS=rebuilt make snapshot-search`
records this suite without repeating the historical stale-cache variant.

Search DAL timings are not browser render timings. Authentication adds an indexed
session lookup and a role lookup on authorization-cache miss; base-page loading
separately reads the user profile and roles. Template rendering and transport are
also outside these timings. There are no additional broad queries in the listing
handler. The sanitized snapshot's session table is empty, so it cannot establish
production auth-lookup distributions. Existing PostgreSQL launcher/index SQL
remains outside the rewrite scope.

The next unfiltered date-listing candidate splits live/legacy and NULL/non-NULL
sort keys. Non-NULL live keys use an inner join, permitting timestamp-index order
through the existing cache-reference index; each branch needs at most offset+limit
rows before the final global merge. NULL branches preserve previous ordering;
NOT NULL history timestamps plus the cache foreign keys justify treating NULL
pointers as NULL dates. Both sorting directions and tie identities remain the
same. No denormalized timestamp columns or new indexes are introduced.

Statistics callers now use an optional submission-DAL count capability: seven
global and eight per-user searches no longer hydrate a page solely to discard it.
The PostgreSQL counter reuses search's filter builder and counts live/legacy rows
in one statement; MariaDB retains its original fallback and transaction behavior.
Six per-user action statistics similarly count comments directly instead of
loading their entire history. The remaining history loader resolves the action
name once, preserving source-compatible equality, instead of evaluating a
correlated action-name lookup and collation key for every comment.

The auxiliary read-only `other-queries-before` capture on the imported snapshot
used the busiest human commenter (241,175 active comments), first observation plus
three warm repetitions. These are EXPLAIN ANALYZE executor times, excluding row
transfer/Go decoding: the prior user-action query took 5,005.7 ms versus 103.0 ms
with the scalar action lookup. Total comments, users and file-size aggregates had
warm medians 91.5, 3.8 and 55.9 ms respectively; these do not currently justify new
counter caches or their write-maintenance cost.

Similarity candidate loading was 17,775.1 ms in that capture. The revised query
materializes identity collation keys once and computes title/launch-command keys
only for colliding identities. Singletons cannot participate in tuple deduplication;
collisions retain all three comparison keys and live-source preference. Synthetic
coverage checks collation-equivalent identity spellings, NULL versus empty,
different titles/commands for the same identity, missing files and deleted live
rows. `compare-similarity.py` compares old/new ordered real-data output by count
and SHA-256 without retaining row contents. This optimization affects database
candidate loading; subsequent Go Levenshtein comparisons still need separate
end-to-end profiling.

`similarity-comparison/report.json` confirms **270,890 ordered tuples identical**
between saved previous SQL and current SQL (SHA-256
`5c3c9e7740b0b97f11fbc4d6435b92ed67f03620bf841d4eaa4bffc9d43b7fa6`).
The `other-queries-after` warm median is **3,406.4 ms**, versus 17,775.1 ms before;
these comparable executor measurements show about 5.2x improvement. User-action
history is 71.3 ms and its direct count is 69.2 ms in the follow-up capture;
aggregate timings remain roughly unchanged. Both auxiliary captures are under
`import-tO503ZAO`; each retains exact SQL and plans.

`statistics-counts/report.json` completes all 15 real-data count/search-total
comparisons. Warm direct-count medians range from **20.6 to 156.4 ms**: global all
20.6, bot-happy 97.9, bot-unhappy 53.7, approved 77.9, verified 89.3, rejected 79.1,
imported 150.7; busiest uploader all 67.6, bot-happy 73.3, bot-unhappy 32.7,
changes-requested 39.0, approved 53.3, verified 98.2, imported 156.4, rejected 80.9.
Its full-search calls are count-validation observations, not repeated warm timing
baselines; the complete statistics HTTP page runs several calls concurrently and
requires separate endpoint measurement. The benchmark enforces read-only
repeatable-read and a statement timeout, preserving one snapshot for comparison.

A further platform-only date-page experiment separates predicate-only counting
from four bounded date branches, still in one SQL statement. Eligibility fails
closed if any other filter is present; offset+limit must not exceed 1,000 rows.
That named bound limits exposure to long index walks on deep pages/exports, not
an assumption about particular platform names. The NULL live branch tests the
cache pointer and projects a literal NULL timestamp, using the same enforced
foreign-key/NOT-NULL equivalence as the unfiltered path so its sort-only history
join can disappear. New fixtures cover Flash, Unity, HTML5, negative/mixed/absent
platforms, both date sorts/directions and every small page including past-end;
unit coverage verifies eligibility across all other filter fields and bounds.
Keep this path only if the real-data comparison supports it.

#### UI correctness and timing results (2026-09-10)

Final search capture `parity-aDWqbtEa` passes **66 workloads / 264 DAL calls**
(first plus three warm repetitions), with identical ordered projections and counts.
All **148,355 cache rows** still match the rebuilt MariaDB reference. No cache
rebuild was needed for these read-only query changes. The table uses three-run
warm medians in milliseconds; both page and count are included. MariaDB UI
reference is `search-s620fWEK`.

| Flow | MariaDB first page | PostgreSQL first page | MariaDB Next | PostgreSQL Next |
| --- | ---: | ---: | ---: | ---: |
| Default submissions | 5176.4 | 120.7 | 5199.8 | 111.8 |
| My submissions | 651.5 | 121.7 | 641.0 | 87.5 |
| Submitter username | 1064.0 | 97.1 | 1094.9 | 93.4 |
| Platform: Flash | 4684.3 | 344.5 | 4154.3 | 305.9 |
| Platform: Unity | 2021.7 | 372.8 | 1688.1 | 321.5 |
| Platform: HTML5 | 2290.0 | 442.7 | 2437.6 | 468.3 |
| Title: Mario | 1784.5 | 213.9 | 1983.8 | 181.6 |
| Ready for Testing | 868.0 | 149.2 | 827.2 | 151.4 |
| Ready for Verification | 521.5 | 118.9 | 449.3 | 120.2 |
| Ready for Flashpoint | 284.7 | 243.0 | 308.1 | 248.1 |
| My Testing | 200.7 | 119.0 | — | — |
| My Verification | 224.0 | 115.7 | — | — |
| My Testing + changes | 204.0 | 123.0 | — | — |
| My Verification + changes | 208.2 | 117.2 | — | — |

The default page improvement is from avoiding the full comment-history scan to
choose 100 rows. Platform first/next pages also improve, so the bounded platform
path is retained. Ready-for-Flashpoint remains about 243–248 ms: its plan spends
about 185 ms scanning/filtering the cache and overestimates 156 matches as 26,368,
leading to broad parent/file joins. A measured partial index or expression
statistics experiment is a remaining candidate; validate prepared-plan behavior
and cache-write maintenance before adding one. Other ready/personal presets are
roughly 116–151 ms.

The standard deep page (100,000-row prefix) measured 865.9 ms versus the earlier
721.3 ms observation, but an alternating executor
comparison with the same connection settings (`deep-page-comparison`, previous/new/ reversed order across four
rounds) did not reproduce a regression: warm medians 1,812.7/1,572.6 ms. EXPLAIN
instrumentation times are not DAL times and these noisy short runs do not justify
a hard threshold. Retain the path and repeat deep-page measurements under the
planned controlled conditions.

Both final UI workload power snapshots report AC power. Hardware, emulation,
cache warmth and thermal variation still limit exact ratios. These results are
query-level evidence, not full HTTP latency or concurrent-load capacity. No new
indexes, schema migrations, importer changes or existing PostgreSQL application
SQL changes were needed for these optimizations.

#### Test-schema discrepancy discovered by the new similarity fixture

The first MariaDB focused run, `run-sCyY7ZgK`, failed its full-width identity
witness: fresh integration tables inherited `utf8mb4_general_ci`, whereas the
production snapshot's application tables use `utf8mb4_unicode_ci`. This was a
harness discrepancy, not evidence that PostgreSQL should change its Unicode
identity comparison. Read-only `probe-similarity-mariadb.sql` on the restored
source confirms the actual UNION identity uses `utf8mb4_unicode_ci`, deduplicates
full-width/ASCII identities, and retains Unicode title/command comparison.
`similarity-mariadb-probe.txt` records expression metadata and synthetic values.

The disposable MariaDB server now creates schemas/tables with the audited Unicode
default. After original migrations, the harness changes only OAuth `client_id`
and `client_secret` to their audited `general_ci` column collations; table default
stays Unicode. MariaDB JSON `additional_applications` retains its binary collation.
Original migration files are unchanged. Preflight verifies the schema, all
application table defaults and every text column before resets, including direct
harness initialization. A deliberately wrong OAuth column must fail preflight
without deleting a marker row. This closes a real gap in earlier cross-backend
synthetic coverage; earlier timings on restored snapshots are unaffected.

The fixture no longer mixes CHAR(36) trailing-space storage behavior into identity
comparison; imported source UUIDs already reflect MariaDB CHAR reads. It retains
full-width identity collision and adds a same-ASCII-ID title/command collation
collision. The PostgreSQL similarity optimization itself is unchanged after the
real-source probe confirmed its semantics.

Next performance work should start with the remaining measured plans, not a
blanket index translation: (1) Ready-for-Flashpoint cache selectivity/partial-index
trial, including custom versus generic prepared plans and mutation cost; (2)
platform/text search indexing on the preserved LIKE semantics; (3) separate
similarity database-fetch, Go scoring and HTTP measurements. Repeat longer runs
with stable power/resources, randomized workload order and concurrent searches
plus writes, then include the existing PostgreSQL launcher/index workload to
measure shared-engine contention. No throughput or HTTP-tail-latency improvement
is claimed from the single-client query timings above.

#### Final verification for this optimization pass

- Full MariaDB race suite `run-qOTVeIgp`: **90 top-level tests / 404 test and
  subtest passes**, 134.693 seconds.
- Full PostgreSQL race suite `run-JfktTQt1`: **90 / 404**, 147.375 seconds.
  Both runners exited successfully and cleaned up their separate projects.
  These final suites ran concurrently only after all timing jobs finished;
  their durations are validation durations, not performance comparisons.
- Unit suites for database, service, types and the search/parity/statistics tools
  pass. Service tests required permission for their local httptest listener;
  the successful rerun resolves that sandbox-only failure. Shell syntax and
  `git diff --check` pass.
- Final submission DAL/search/page/count sources are byte-identical to the
  frozen sources measured in `parity-aDWqbtEa`. The later harness alignment did
  not change measured PostgreSQL SQL. Independent review found no actionable
  ordering, NULL, count or deduplication regression.

All changes remain local implementation work. Production deployment, controlled
concurrent endpoint benchmarks and the remaining index experiments are separate
next steps.

### Index and selectivity optimization pass (2026-09-10, verified)

Scope: Ready-for-Flashpoint and platform/title searches. Further similarity work
is deferred by user preference. Concurrent request/load testing remains a later
exercise; the present trials serialize database workloads on the disposable
imported snapshot.

`index-experiments/` under `import-tO503ZAO` retains transactional DDL trials and
custom/generic prepared-query plans. `benchmark-indexes.py` creates candidate
indexes/statistics inside a transaction, executes saved page-plus-count SQL,
then rolls everything back. Plans use EXPLAIN ANALYZE executor times, not HTTP
latency. A separate write-cost tool measures cache rebuild, ready-membership
entry/exit and metadata updates with savepoint rollback and row restoration.

Ready-for-Flashpoint trials:

- Existing query about 265 ms; multivariate statistics alone did not help.
- A simple partial index reduced custom plans to about 80 ms, but generic plans
  could not infer its predicate from parameters and remained near 270 ms.
- Exposing an equivalent constant eligibility predicate in the query makes the
  partial index available to both plan types. The original filters remain as
  checks; eligibility requires exactly the approve bot action, no requested
  changes, verified state and exclusions containing both reject and mark-added.
- Statistics must describe the **inner boolean expression**, whose truth value
  `IS TRUE` tests. With those statistics, executor medians are about 2–4 ms for
  first/next pages in both custom and generic modes (`ready-inner`).

The retained implementation adds a small partial cache index and expression
statistics in migration 17, plus the implied predicate in the query builder.
Tests include NULL versus empty collections, alternate bot actions, reversed and
additional exclusions, missing cache/rebuild, and generic plans. Final parity,
write cost and integration results are recorded below.

Rejected text experiments are retained rather than silently discarded:
raw title/alternate-title trigram indexes worsened the Unicode-regex queries
(~180–245 ms to ~460–570 ms). A dictionary of platform spellings also added work,
especially for exclusions, including with covering indexes. Neither is adopted.
An exact one-character LIKE folding candidate, derived from the existing frozen
MariaDB equivalence classes, passed 63,487 BMP character checks and 34 pattern
cases with zero differences. It preserves length and escapes folded wildcard
lookalikes; the original predicate remains a recheck. Selective title execution
fell to ~2 ms in custom plans. However, grouped folding/recheck still measured
Mario ~133 ms versus ~117 ms for the simpler direct join; common terms `game`
and `the` measured ~378/~776 ms versus ~144/~174 ms without folding in the
follow-up baseline. Four GIN indexes and another normalization function are not
adopted. The artifact-only representation is separate from the existing equality
key, which expands/drops characters and cannot replace LIKE semantics. No new
text-normalization helper or pg_trgm extension is added to production migrations.

The simpler platform candidate performs better than the dictionary: metadata is
joined directly to `submission_cache.fk_newest_file_id`. Both foreign keys require
the referenced file to exist, so bypassing that intermediate join preserves NULL
and deleted-file behavior while letting count plans remove the file-table join.
With narrow covering platform indexes, custom-plan trials measured Flash ~199 ms,
Unity ~139 ms and HTML5 ~205 ms (`direct-meta`). The direct join also reduced the
unindexed Mario trial to ~117 ms. These executor trials precede final DAL parity.

`write-ready-platform/report.json` completed **2,560 rollback-verified operations**
across 20 ready and 20 ordinary submissions (at most 50 comments), alternating
baseline/candidate twice. Warm pooled medians, baseline → candidate: cache rebuild
0.483 → 0.483 ms; entering ready 0.144 → 0.158 ms; leaving ready 0.145 → 0.153 ms;
metadata update 0.122 → 0.123 ms. Median WAL deltas increase for membership updates,
as expected from index maintenance. These are local operation timings, excluding
savepoint/validation overhead, not end-to-end mutation latency. Candidate index
sizes: ready partial index 16 KiB, metadata platform covering index ~5.72 MiB,
legacy platform index ~0.83 MiB. Aborted writes still produce WAL/dead tuples; the
alternating trials reduce order bias but do not restore pristine physical storage.

Validation for this index pass: full PostgreSQL race suite `run-1ATRH6ca`
passed **91 top-level tests / 423 test and subtest passes** in 146.223 seconds.
Focused MariaDB `run-bg2TpI7p` passed the new Ready filter suite (1 top-level /
19 total passes, 2.397 seconds). Both isolated projects cleaned up successfully.
Database, importer, parity and index-write benchmark unit suites pass.
The snapshot harness explicitly analyzes imported tables after loading, and
refreshes cache statistics after a full rebuild before measuring searches.

Final fresh import `import-sNcjxCw1` passed at migration 17: 3,340,777 source rows
→ 3,305,411 imported rows, with exactly 35,366 duplicate subscriptions discarded
under the established rules. All 19 canonical table checksums match and all 23
preexisting PostgreSQL fingerprints remain unchanged. The final
`parity-w2NX2F7v` rebuilt 148,164 active caches twice (55.4/54.4 seconds), with
identical second-pass digests and no failures. All 148,355 cache rows match the
MariaDB reference semantically; all 66 workloads / 264 complete DAL result sets
and counts match. The original references remain untouched.

Actual DAL warm medians below compare previous PostgreSQL `parity-aDWqbtEa`
with migration-17 `parity-w2NX2F7v`. Each uses three warm repetitions; these are
separate runs/clones, not a controlled simultaneous A/B or HTTP/load benchmark.
The final run had no concurrent test/import jobs. Broad-query changes remain
sensitive to planner statistics and machine conditions. The large Ready preset
gain is also supported by isolated custom/generic trials; title improvement is
not consistent across pages. Do not attribute every small timing difference to
this patch.

| UI workload | Previous PostgreSQL ms | Current PostgreSQL ms |
| --- | ---: | ---: |
| `ui-default` | 120.7 | 102.5 |
| `ui-my-submissions` | 121.7 | 87.4 |
| `ui-submitter-username` | 97.1 | 95.1 |
| `ui-platform-flash` | 344.5 | 227.7 |
| `ui-platform-unity` | 372.8 | 287.0 |
| `ui-platform-html5` | 442.7 | 328.3 |
| `ui-title-mario` | 213.9 | 181.6 |
| `ui-default-next` | 111.8 | 92.0 |
| `ui-my-submissions-next` | 87.5 | 103.6 |
| `ui-submitter-username-next` | 93.4 | 85.4 |
| `ui-platform-flash-next` | 305.9 | 238.3 |
| `ui-platform-unity-next` | 321.5 | 288.6 |
| `ui-platform-html5-next` | 468.3 | 327.1 |
| `ui-title-mario-next` | 181.6 | 197.1 |
| `ui-preset-ready-testing` | 149.2 | 170.3 |
| `ui-preset-ready-testing-next` | 151.4 | 183.8 |
| `ui-preset-ready-verification` | 118.9 | 137.4 |
| `ui-preset-ready-verification-next` | 120.2 | 123.4 |
| `ui-preset-ready-fp` | 243.0 | 5.8 |
| `ui-preset-ready-fp-next` | 248.1 | 5.2 |
| `ui-preset-me-testing` | 119.0 | 128.7 |
| `ui-preset-me-verification` | 115.7 | 119.8 |
| `ui-preset-me-testing-changes` | 123.0 | 114.9 |
| `ui-preset-me-verification-changes` | 117.2 | 112.5 |

Accepted changes: Ready predicate/partial index/expression statistics; direct
cache-to-metadata join; two covering platform indexes. Existing PostgreSQL DAL,
application tables and text semantics remain unchanged. Similarity is outside
this pass. Rejected title/dictionary probes remain reproducible artifacts.

Next performance work, if desired: measure mixed concurrent searches and writes
at increasing client counts, recording throughput, p50/p95/p99, pool wait and
lock wait. Only then choose a demonstrated bottleneck to address, such as pool
sizing, query resource use or request backpressure. Do not relax per-submission
mutation locking established by the concurrency correctness fixes. Include the
existing PostgreSQL workload when measuring contention on the unified engine.

Migration 17 down/up round-trip assertions passed inside a rolled-back
transaction on the disposable imported target (`migration17-roundtrip.log`).
All three indexes and the expression statistics were removed and recreated.
Shell syntax, benchmark Python compilation and `git diff --check` pass.

### Bounded concurrency investigation (2026-09-10)

Decision: **stop query optimization here for now**. The bounded probe found no
new submission correctness failure or sustained parent-lock bottleneck requiring
an implementation change. It does not establish production capacity. No pool,
locking, existing PostgreSQL DAL or application behavior was changed.

`make snapshot-concurrency` now runs a reproducible probe against the latest
successfully reconciled migration-17 import, isolated from fixture-reset tests.
See `scripts/submission-concurrency/README.md`. Successful artifacts:
`import-sNcjxCw1/concurrency-4vpFTvXx`; 2,378 completed operations across eight
12-second scenarios, zero errors or result/count digest mismatches, and an
unchanged fingerprint of the entire cache afterward. The initial runner attempt
`concurrency-sBe8Klj6` failed before load because the fingerprint query referenced
an absent cache `id`; the corrected query orders by its actual submission key.
That was a probe error, not an application defect; both reports are retained.

The probe rotates eight actual UI DAL searches: default, next page, Flash,
HTML5, Mario, submitter username, Ready for Flashpoint and Ready for Testing.
Mixed scenarios execute four reads per cache write. Writes use the real parent
lock and cache rebuild on eight ordinary submissions with 1–50 comments, then
roll back. The same-parent case directs all writers to one submission. Serial
warmups provide full result/count digest references. Search timings include
transaction acquisition, SQL, decoding and rollback; digest checking is outside
the timer. All reads are compared to the serial reference.

| Scenario | Operations/s | Search p95 ms | Cache write p95 ms | Parent lock p95 ms | Pool waits |
| --- | ---: | ---: | ---: | ---: | ---: |
| `reads-1` | 8.0 | 216.1 | — | — | 0 |
| `mixed-1` | 10.1 | 209.7 | 2.652 | 0.397 | 0 |
| `reads-4` | 16.4 | 610.1 | — | — | 0 |
| `mixed-4` | 21.1 | 535.9 | 45.562 | 6.557 | 0 |
| `reads-8` | 29.5 | 473.8 | — | — | 0 |
| `mixed-8` | 37.8 | 464.3 | 31.036 | 3.986 | 0 |
| `mixed-8-pool-4` | 33.8 | 495.7 | 329.819 | 3.408 | 415 |
| `mixed-8-same-parent` | 37.6 | 498.2 | 31.143 | 7.558 | 0 |

Interpretation:

- With normal pool defaults, all phases recorded **zero connection-pool waits**,
  with observed open connections matching the worker count. Eight mixed workers
  completed ~37.8 operations/s. Ready for Flashpoint in that mix had median
  16.9 ms, p95 36.4 ms; broad searches account for much of the higher aggregate
  search tail. This is intentionally heavier than occasional user activity.
- A deliberately constrained four-connection pool with eight mixed clients
  recorded **415 pool waits totaling 48.4 seconds across callers** during its
  ~12-second scenario. Transaction-begin p95 rose to 333.7 ms, cache-write p95 to
  329.8 ms, and Ready-for-Flashpoint p95 to 353.2 ms. This is connection queueing,
  not a reason to rewrite those otherwise short operations. It supports rejecting
  a blindly small pool cap, not choosing an unlimited production budget forever.
- Same-parent writes completed at ~37.6 mixed operations/s; parent-lock p95 was
  7.6 ms and p99 11.2 ms. No heavyweight Lock waits were caught by the 50 ms
  sampler. Short waits can fall between samples; writes only rebuild short
  histories and roll back, so this does not bound full service lock duration.
- Most active-backend observations had no reported wait event (CPU/runnable),
  with smaller IO, IPC and lightweight-lock categories. These sampled counts
  include backend observations, potentially including parallel workers, and
  are not CPU measurements or exact wait durations. There is no evidence here
  for changing parent serialization or adding another index.

Scope and limitations: one short fixed-order closed-loop run, no think time,
no independently paced arrival rate, no concurrent benchmark jobs, AC power
reported before/after. PostgreSQL runs under the existing local Docker/emulation
setup. Cache warmth and plan reuse can explain non-monotonic four/eight-client
results; do not interpret them as a reliable scaling curve. Per-query p99 uses
small samples. Rollback writes omit commit/fsync latency, new history rows,
notifications and full service work. HTTP/authentication, file processing,
launcher/index traffic and cross-pool interactions were not load-tested.
Existing integration tests remain the evidence for committed mutation/cache
correctness; this probe supplements rather than replaces them.

Connection ownership review identifies a **preexisting constraint to investigate
before changing limits**: `ReceiveComments` holds both submission and native
PostgreSQL sessions; its mark-added path calls `AddSubmissionToFlashpoint`, which
opens another native PostgreSQL session and calls the service
`GetSubmissionFiles`, which opens another submission session. The metadata mutex
also serializes the add path. A pool filled by outer transactions can therefore
leave nested acquisition waiting for capacity held by those callers. This is a
source-level risk, not a deadlock reproduced by this DAL probe. Do not lower pool
limits based on this probe without a dedicated service-level acquisition test or
reviewing session reuse. Changing existing PostgreSQL transaction behavior stays
outside this migration's agreed scope.

Recommended next work is cutover/recovery rehearsal and operational readiness,
not another speculative optimization pass. If production-like load later shows
queueing, reproduce it with complete service flows plus the native PostgreSQL
workload on deployment hardware, and then select connection budgets or request
limits. The new probe has passing target-guard/summary unit tests under `-race`;
shell syntax and `git diff --check` pass. Production code is unchanged by this
investigation.

### Stopped-production correctness rehearsal (2026-09-10, verified)

The accepted deployment model is a complete maintenance window: stop the app and
workers, dump both databases, migrate and validate, then reopen production only
after acceptance. Downtime is acceptable; silent data loss or changed SQL meaning
is not. This work therefore emphasizes reproducible correctness evidence and
failure detection rather than online synchronization or a detailed operator guide.
Original sanitized dumps/reference databases remain untouched.

Runtime clarification: `SUBMISSION_DB_ENGINE=postgres` makes `database.OpenDB`
return the PostgreSQL submission connection directly. It does not contact
MariaDB, and MariaDB credentials are optional in that mode. Both submission
implementations, the default `mariadb` setting and MariaDB Compose/test tooling
remain in the repository. PostgreSQL mode still has a separate submission sql.DB
pool and native pgx pool; native PostgreSQL behavior is unchanged. Removing the
MariaDB implementation/driver/configuration and normal runtime container is a
**separate subsequent phase**, after migration acceptance. Keeping MariaDB as a
rehearsal reference is not a requirement to keep it running in switched production.

Two acceptance gaps were closed in the importer:

- Verify actual source text-column collations, including the audited OAuth
  `utf8mb4_general_ci` and MariaDB JSON `utf8mb4_bin` exceptions. A byte-perfect
  copy alone cannot prove equivalent comparison semantics if the source schema
  has drifted. Unexpected collations now cause explicit preflight rejection.
- Require identical source/target column-name sets before any COPY/bootstrap
  deletion. Otherwise a missing source field could silently become a target
  default/NULL while source-column-only checksums still passed. Order differences
  are allowed; omitted or extra columns are rejected with named diagnostics.

The negative SQL fixture in `rehearsal-20260910/column-guard-results` deliberately
adds a target-only defaulted column; the real importer rejects it by name and
leaves zero submission rows. Its tiny disposable database was removed afterward.
The new collation/column comparator unit cases also pass.

Failure injection `import-RRN8g1aj` terminated the importer backend after the
curation metadata COPY/checksum checkpoint (the next image COPY reported a lost
connection). All imported rows rolled back: all 19 table counts matched their
pre-commit visible bootstrap counts, including zero submissions/files/comments/
metadata/cache. `interruption-report.json` and the script/log are retained. The
aborted target was removed after verification to reclaim disk space; full copies
had reduced host free space to ~1 GiB, and deleting this disposable copy recovered
space without touching source dumps, reference databases or completed reports.

Failure semantics remain explicit: PostgreSQL `setval` changes are not rolled
back with rows; retry must reseed rather than assume pristine counters. A lost
success report is not proof of rollback because commit precedes manifest writing.
A populated target refuses blind reimport. Partial migration bootstrap is also
not an in-place restart protocol. With production stopped, recovering into a fresh
clone from the preserved backups and rerunning validation is the simple option.

New committed-workflow runner `cmd/submission-workflow` was executed on populated
copies of the rebuilt MariaDB source and migration-17 PostgreSQL target. Both
**13-checkpoint transcripts match exactly**, including full search/comment/cache
projections, all five reviewer collections, filters, subscriptions, native
activity payloads, queued notification text and deletion reasons. All generated
submission/file/comment IDs exceeded existing maxima and respected actual source
AUTO_INCREMENT/sequence positions. An aborted comment leaves an identity gap;
the subsequent committed comment remains valid and equivalent.

The workflow creates files through the DAL, commits real service assignment,
request-changes, approval, verification and comments, edits metadata, crosses a
replacement-file boundary, deletes the replacement and verification, rejects
self-assignment, and checks multi-write rollback. Each checkpoint checks expected
state and scoped rebuild equivalence. Exact microsecond service-history timestamps
are injected through the test/mock constructor; existing callers still use real
time. Only generated identities and unordered collections are normalized.
Database-generated side-effect wall-clock times are range-checked and omitted
from cross-run comparison. No existing history is rewritten to force a match.

Artifacts: `rehearsal-20260910/workflow-comparison.json` and the individual reports;
matching transcript SHA-256 is
`8d5f67095e480cc14971a2dedec4db52fa28b123f92ae45e34345e12bb3d9af4`.
Synthetic archive metadata/file records avoid needing external production files;
actual archive upload, HTTP authorization and image paths are covered by the
separate full integration suites. This runner does not deliver notifications or
claim real archive import into the native catalog was exercised.

The broader race suite found a preexisting upload-progress bug, independent of
SQL: `fileReadCloserInformer.Read` changed chunk state while the progress goroutine
read it through `GetFractionRead`. A mutex now protects that state. The new
32-chunk regression reconstructs exact bytes while polling monotonic progress;
it fails under the race detector on the old implementation and passes ten runs
on the fix. The initial PostgreSQL full run `run-DdtP5XGb` exposed the race; its
failure is retained rather than counted as a pass. MariaDB `run-SdSasTTC` passed
before the correction. Final full suites with the fix passed; results are recorded below.

Preserved architectural limitation: some service flows still commit native
activity/catalog work and submission work in separate transactions, even when
both point to PostgreSQL. Migration does not add cross-domain atomicity. This is
an existing behavior boundary, not a newly merged transaction guarantee; any
future change needs separate design and commit-failure coverage.

Final product suites after the upload fix: PostgreSQL `run-HeOOV9lq` passed
**91 top-level / 423 test and subtest passes** in 160.881 seconds; MariaDB
`run-rHs8yAbY` passed **91 / 423** in 145.045 seconds. Both projects cleaned up
successfully. Focused importer/workflow/upload-reader race-enabled unit packages
also pass (`rehearsal-20260910/focused-tests.log`).

Recovery import `import-15s8rPZG` completed with the new preflight checks.
`repeatability.json` confirms identical 19-table canonical checksums, counts,
sequence next positions and full discarded-subscription mappings compared with
`import-sNcjxCw1`: 3,340,777 source rows → 3,305,411 imported rows, exactly 35,366
approved duplicate subscriptions removed. All 23 existing PostgreSQL table
fingerprints also match. The `populated-retry` attempt correctly refuses the
already populated target before making changes. Preserve a validated successful
target or start from a fresh backup clone; never infer target state merely from
whether a wrapper/report was interrupted.

Final recovery-target replay `parity-wWCn5SWn` passed **66 workloads / 264 complete
DAL result/count comparisons**, plus all **148,355 cache rows**. Both full rebuilds
visited/rebuilt all 148,164 active submissions; the second pass was identical.
`rehearsal-20260910/workflow-preservation.json` additionally confirms all **42
preexisting imported/native table fingerprints** were unchanged by committed
workflows, excluding only their deliberately allocated new rows/test actors.

The one-off workflow copies and interrupted/negative-test targets were removed
after evidence capture to reclaim disk space. Frozen sources, scripts, individual
reports and failure logs remain; the successful `submission_import_import_15s8rpzg`
target and original references are retained. Rehearsal elapsed/query times ran
alongside validation scans/tests and temporary disk pressure; they are **not**
replacement performance baselines or reliable maintenance-window estimates.

Acceptance conclusion: no migration/search semantic mismatch or unexpected
preexisting-row change was found in this rehearsal. The concrete fixes are two
import preflight guards and the unrelated upload-progress race. Use the same
checksum, schema, sequence and cache/search gates on fresh stopped-production
backups. Current importer auth gates require both source auth tables to be empty
as in these sanitized snapshots; fresh-data handling must remain explicit. There
is no need for an online migration or reverse-sync implementation under the
agreed stopped-production procedure. MariaDB removal remains a separate phase;
this work neither deploys production nor changes native PostgreSQL transaction
ownership.
