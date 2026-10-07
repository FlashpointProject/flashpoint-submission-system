# Application caching design and implementation

FPFSS runs as one application process with one writer path to each database. Application caches are bounded in-memory read snapshots. Database submission cache tables are outside this change. Direct database maintenance requires restarting the application afterwards.

## Accepted behavior

- Sessions are cached until their actual database expiration, logout, individual revocation, user-wide revocation, or global session deletion. Roles and profiles are independent of sessions.
- Shared submission details and file lists serve both HTML and API endpoints. Viewer identity, authorization, subscriptions, validator tags, and navigation are composed separately. No personalized response is cached.
- Committed comments/actions, uploads, metadata/images, deletion, freezing, and overrides invalidate affected submissions. Subscription writes include automatic subscriptions. Profile changes invalidate dependent display data. Navigation changes when submissions are created/deleted.
- Invalidation is synchronous at the database commit boundary. Old in-flight loads cannot publish after a relevant commit. Rollbacks publish nothing. Failed/ambiguous commits conservatively discard affected memory. Each database commit invalidates independently, including partially successful multi-database operations.
- Cold loads are shared, independently cancellable, bounded by a timeout, and return independent values. Errors and incomplete results are not retained. Mutation validation continues to use transactional database reads.
- Validator tags retain a ten-minute external-data TTL. Discord member roles are fetched freshly during login; the external server role catalogue may use a short TTL. Production templates are parsed once per template set; development reparses.
- Site, individual-user, and whole-user statistics use fixed ten-minute in-memory result caches. Ordinary writes do not invalidate these aggregates; results may be ten minutes old. Aggregate SQL replaces display searches on a miss. Metadata statistics remain invalidated by metadata commits.
- Upload jobs have one tracking ID, processing owner and cleanup owner. Conflicting reuse is rejected. Completed jobs retain a result for bounded retries. Failed jobs preserve chunks and status; retry is explicit. Running jobs cannot be evicted. Memory alone cannot guarantee exactly-once processing across crashes.
- Arbitrary search results, errors, final permission decisions, complete personalized responses, and media bytes are not cached.

## Implementation contract

Typed cache instances belong to SiteService. Every cache has byte and entry limits, LRU eviction, metrics, and no background expiration timer. Session expiration and statistics/external TTLs are checked on access. No cache holds a database session.

Database write methods declare changed dependencies on their transaction. Commit coordinates invalidation with reads and publication; rollback does not. Loads use fresh transactions and a generation fence. Database locks remain authoritative for mutations. For commit-invalidated data, cache hits selected before a concurrent mutation may finish; reads after a successful mutation cannot select an obsolete entry. Statistics intentionally retain their snapshot until the ten-minute TTL expires.

## Cache inventory

| Data | Key | Freshness | Entry limit / encoded payload limit |
| --- | --- | --- | --- |
| Sessions | SHA-256 of bearer secret | Actual expiry; secret/session/user/global revocation | 10,000 / 8 MiB |
| Profiles | User ID | Profile commit | 10,000 / 8 MiB |
| Role names | User ID | Member-role or server-role commit | 10,000 / 4 MiB |
| Submission detail | Submission ID | Submission, file, metadata, image, action, deletion, freeze, override, repair, or dependent profile commit | 20,000 / 512 MiB |
| Submission summaries for middleware | Submission ID | Submission or dependent profile commit | 4,000 / 16 MiB |
| File lists | Submission ID | Submission or dependent profile commit | 1,000 / 16 MiB |
| Single-file records | File ID | File or parent-submission commit | 4,000 / 8 MiB |
| Subscription state | User ID + submission ID | Subscribe/unsubscribe or parent-submission commit | 20,000 / 1 MiB |
| Previous/next submission | Submission ID | Any submission creation/deletion | 4,000 / 1 MiB |
| Site statistics | One key | Fixed 10-minute TTL | 1 / 8 KiB |
| Whole-user statistics table | One key | Fixed 10-minute TTL | 1 / 16 MiB |
| Individual-user statistics | User ID | Fixed 10-minute TTL | 10,000 / 16 MiB |
| Metadata totals | One key | PostgreSQL metadata writes | 1 / 4 KiB |
| Validator tags | One key per validator instance | 10-minute TTL; failures retry | 1 / 32 MiB |
| Discord server role catalogue | One key per service | 2-minute TTL; member roles themselves are fetched on every login | One catalogue |
| Parsed templates | Static template-file set per App | Process lifetime in production; reload in development | Finite route-defined sets |

Application-owned read caches have about **606 MiB** of combined encoded payload
capacity; tags add 32 MiB. This is a capacity ceiling, not allocated memory or an
RSS limit: indexes, dependency keys, decoded responses, in-flight loads, jobs and
parsed templates use additional memory. Oversized values are served without
retention. Ordinary search results and multi-file arbitrary queries bypass these
caches.

The administrator-only `GET /api/internal/application-cache-stats` reports loads,
hits, shared waiters, errors, invalidations, evictions, resident entries, payload
bytes and cumulative load duration. It reports no keys or bearer credentials.
Tags use a separate validator-owned cache; that cache and templates are not in
this endpoint.

## Concurrency and ownership details

- The coordinator invalidates affected resident entries immediately before and
  after each database commit. SQL never runs while holding its mutex. Unrelated
  resident entries remain usable; misses wait for outstanding commits to settle.
- A process-wide generation fences in-flight loads. This deliberately retries
  some unrelated concurrent misses, avoiding an unbounded generation/tombstone
  map. It does **not** evict unrelated resident entries.
- Each shared load has an independent 30-second timeout. Cancelling one HTTP
  request does not cancel another request's shared load. Callers have their own
  cancellation and a 30-second total retry bound.
- Values are stored as immutable encoded snapshots and decoded for each caller.
  No caller receives a shared mutable slice or map. Profile names already joined
  into submission/file data are tracked as user dependencies, so a rename evicts
  those snapshots rather than forcing extra profile queries on every page.
- Database sessions record write dependencies. Rollback never publishes or
  invalidates. A failed commit conservatively invalidates because its outcome
  may be ambiguous. PostgreSQL and MariaDB commits invalidate independently;
  this does not introduce a distributed transaction.
- New writes to cached source data must declare dependencies in the DAL. Direct
  SQL maintenance outside these methods requires a process restart. A future
  second application replica would require a different invalidation mechanism.

## Upload contract

Jobs are keyed by user plus upload identifier, bound to submission ID and file
parameters, and synchronized per job. All completion requests share one tracking
ID. Processing owns cleanup; GET checks and POST chunk writes pin the job so
expiration cannot remove their files. The browser stores a random attempt ID in
session storage keyed by target, name, size and last-modified time. Reloads reuse
an unfinished attempt; confirmed success clears it so a later intentional upload
gets a new ID.

A failed job keeps its chunks and status. The browser's **Retry upload** button
sends the next integer `resumableRetry` sequence. Ordinary or delayed duplicate
requests, including duplicate requests from that retry, reuse its existing
status. Retrying after a commit was attempted is refused because the outcome may
already be persisted. A new process cannot reconstruct these job outcomes.

The registry retains at most 1,000 jobs. Unpinned, non-running jobs idle for 24
hours are cleaned up on the next acquisition. Running jobs never expire; if all
slots remain occupied, new jobs receive 503 rather than discarding their retry
protection. Chunk filenames use SHA-256 of the complete identifier instead of a
truncated prefix. Cleanup tolerates missing chunks and attempts every chunk and
partial file. Failed cleanup retains ownership for a later sweep.

Deployment changes chunk filenames: uploads left incomplete by the previous
version must resend their chunks. Previously stored chunk files are not migrated
or deleted automatically. Duplicate-processing protection lasts only for a retained job within this
process, not after expiration or a crash.

## Acceptance checklist

- [x] Bounded typed caches, cancellation, generations, commit invalidation and independent returned values
- [x] Session expiry/revocation and profile/role invalidation
- [x] Shared submission/file snapshots, viewer separation, subscription and navigation dependencies
- [x] Upload job ownership, retry, cleanup and retention
- [x] External-data and template cache policy
- [x] Ten-minute statistics caches with measured miss-query cost
- [x] Invalidation tests through real service/DAL mutations, rollback and partial-commit cases
- [x] Race tests and isolated integration tests
- [x] Zero warm-page SELECTs, bounded payload/entry tests, HTTP behavior and DOM browser checks

## Validation record

Validation is local and isolated. No production measurements or deployment were
performed. Full integration run: `integration_tests/results/run-kIT1RdNN/` —
**520 tests/subtests passed** (119 top-level tests), with `-race`, isolated
MariaDB/PostgreSQL, and 40.4% aggregate statement coverage across database, types,
service and transport. Exit code 0; the runner removed its containers and volumes.
The full run includes the existing submission state-machine and statistics DOM
tests plus the new cache and upload tests. The later cleanup-error ownership
guard was additionally checked with the focused race-enabled upload tests.

`go test -race ./appcache ./service ./transport ./database ./resumableuploadservice`
also passed. `git diff --check` passed. The statistics snapshot container was
removed after measurements. These checks do not claim exhaustive concurrency or
production performance proof.

- Cache unit tests cover old-load/commit races, independent returned values,
  independent cancellation, shared loads, failure retry, exact expiry, payload
  and entry limits, oversized values, ambiguous commit failure, and unrelated
  warm reads during a commit.
- Upload tests cover concurrent completion, parameter conflicts, stable tracking
  IDs, failure retention, explicit retries and delayed retry replays, uncertain
  outcomes, pinned/running-job retention, capacity, full-identifier collisions,
  and cleanup with missing chunks.
- Real HTTP/DAL tests warm HTML/API pages for different viewers, assert **zero
  additional MariaDB SELECTs** for repeated warm requests, and exercise comments,
  assignment, profile changes, subscriptions, freeze/unfreeze, override, uploads,
  file/comment/submission deletion, navigation, and a rejected batch rollback.
- Auth tests cover individual revocation, logout, all-user sessions, bans, global
  deletion, and login profile/role refresh affecting an existing session.
- A real two-database test commits metadata, then fails the MariaDB commit, and
  verifies that metadata invalidation survives independently.
- DOM tests exercise upload attempt identity and explicit retry controls, plus
  the existing statistics-table browser behavior. These are JavaScript/DOM tests,
  not a production browser/network performance benchmark.

### Statistics measurements

A sanitized local snapshot was restored into an isolated MariaDB 11.1.5 container
with no network, 4 CPUs, 8 GiB memory limit, a 512 MiB InnoDB buffer pool, and
amd64 emulation. It contains 148,355 submissions, 1,290,139 comments, and 3,781
users. The existing user-statistics index migration was applied only to this
throwaway restore. One warm-up preceded three timed runs:

| Workload | Measured warm runs |
| --- | --- |
| Former site-statistics query paths, run serially | 5,218–5,506 ms |
| New site aggregate | 809–832 ms |
| All-user MariaDB aggregate | 1,060–1,062 ms |
| Selected low-activity user aggregate | 30–31 ms |
| Highest-comment-count user aggregate | 79–80 ms |

Exact site totals matched the previous search-based counts; selected per-user
rows matched the full aggregate. The profiler lives in
`tools/performance/statistics/`. Raw results are retained locally in
`snapshot-results/application-caching-20261007/statistics.jsonl`.

These broad queries are **not trivial**. Site statistics now use one aggregate
statement instead of seven display searches plus extra totals. Individual-user
predicates are pushed into the aggregates. These measurements describe cache
misses; site and user results now remain cached for ten minutes. The old page ran
work concurrently, so the serial baseline
is a reduction in measured database work, **not a claimed 6x page-speed gain**.
User timings exclude PostgreSQL activity lookup and HTML/network/browser costs.
The all-user aggregate remains proportional to the history scanned. Repeated
requests share its cached result until expiration, when one shared load refreshes
it. No new
persistent counter tables or changes to submission cache tables were introduced.

Expected outcome: repeated submission views avoid database reads once that
viewer's auth/profile/roles/subscription and the shared content/navigation are
warm. A mutation evicts the affected data; the next reader rebuilds it once.
Actual production hit rates, RSS and end-to-end latency still need observation
through the counters and normal request timing after deployment.


### Ten-minute statistics policy update

Site totals, the user-statistics table (`/api/user-statistics/all`), and individual
user statistics (`/api/user-statistics/{uid}`) now use the typed bounded caches.
The user page loads its table from the cached bulk endpoint. HTML/API site pages
share the same cached aggregate. Viewer identity and authorization are composed
or checked for each request; they are never retained in the aggregate. The bulk
response's `generated_at` remains the actual snapshot generation time.

Expiry is ten minutes after a successful load completes, not ten minutes after
the last access. A write does not reset it. Refresh is lazy on the first request
after expiry, concurrent refreshes share a load, and failures are not cached.
Statistics cache counters are included in the administrator metrics endpoint.

Validation of this policy update: race-enabled cache/service tests passed for
fixed expiry, reuse, independent returned values, and failure retry. The focused
isolated integration run `integration_tests/results/run-KMcAKoup/` passed all
5 selected tests, including warm-page zero-SELECT checks, viewer isolation,
retention across writes, shared HTML/API data, authorization on cached bulk
statistics, and the user-statistics DOM test. The runner exited 0 and cleaned up
its containers.
