package integration_tests

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

// A test-only trigger stops real service requests after search/validation and
// before their first comment write. A second named lock signals arrival outside
// the uncommitted transaction. Locks, not elapsed time, control the interleaving;
// the ticker only observes arrival locks with a deadline.
type commentWriteBarrier struct {
	f           *sqlFixture
	controllers map[int64]*sql.Conn
	workers     []*concurrentMutation
}

func newCommentWriteBarrier(t *testing.T, f *sqlFixture, users ...int64) *commentWriteBarrier {
	t.Helper()
	b := &commentWriteBarrier{f: f, controllers: map[int64]*sql.Conn{}}
	for _, uid := range users {
		conn, err := f.Maria.Conn(f.Ctx)
		require.NoError(t, err)
		b.controllers[uid] = conn
		var acquired int
		if postgresSubmissionTests() {
			var locked bool
			require.NoError(t, conn.QueryRowContext(f.Ctx, `SELECT pg_try_advisory_lock(701, $1::integer)`, uid).Scan(&locked))
			if locked {
				acquired = 1
			}
		} else {
			require.NoError(t, conn.QueryRowContext(f.Ctx, testSQL(`SELECT GET_LOCK(?, 0)`), fmt.Sprintf("test-comment-%d", uid)).Scan(&acquired))
		}
		require.Equal(t, 1, acquired)
	}
	trigger := `CREATE TRIGGER concurrency_comment_barrier BEFORE INSERT ON comment FOR EACH ROW
 BEGIN
  DECLARE acquired INT;
  SET acquired = GET_LOCK(CONCAT('test-arrived-',NEW.fk_user_id),0);
  SET acquired = GET_LOCK(CONCAT('test-comment-',NEW.fk_user_id), 15);
  IF acquired IS NULL OR acquired <> 1 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='concurrency barrier timed out'; END IF;
  SET acquired = RELEASE_LOCK(CONCAT('test-comment-',NEW.fk_user_id));
  SET acquired = RELEASE_LOCK(CONCAT('test-arrived-',NEW.fk_user_id));
 END`
	if postgresSubmissionTests() {
		selected := make([]string, len(users))
		for i, uid := range users {
			selected[i] = fmt.Sprint(uid)
		}
		trigger = `CREATE OR REPLACE FUNCTION concurrency_comment_barrier_fn() RETURNS trigger LANGUAGE plpgsql AS $fixture$
        BEGIN
          IF NEW.fk_user_id IN (` + strings.Join(selected, ",") + `) THEN
          PERFORM set_config('lock_timeout', '15s', true);
          PERFORM pg_advisory_xact_lock(702, NEW.fk_user_id::integer);
          PERFORM pg_advisory_xact_lock(701, NEW.fk_user_id::integer);
          END IF;
          RETURN NEW;
        END; $fixture$;
        CREATE TRIGGER concurrency_comment_barrier BEFORE INSERT ON comment FOR EACH ROW EXECUTE FUNCTION concurrency_comment_barrier_fn()`
	}
	_, err := f.Maria.Exec(trigger)
	require.NoError(t, err)
	t.Cleanup(func() {
		for uid, conn := range b.controllers {
			if postgresSubmissionTests() {
				_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(701, $1::integer)`, uid)
			} else {
				_, _ = conn.ExecContext(context.Background(), testSQL(`SELECT RELEASE_LOCK(?)`), fmt.Sprintf("test-comment-%d", uid))
			}
			_ = conn.Close()
		}
		for _, work := range b.workers {
			work.cancel()
			select {
			case <-work.done:
			case <-time.After(20 * time.Second):
				t.Error("worker did not stop during cleanup")
			}
		}
		_, err := f.Maria.Exec(testDropTriggerSQL("concurrency_comment_barrier"))
		require.NoError(t, err)
	})
	return b
}

func (b *commentWriteBarrier) wait(t *testing.T, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(b.f.Ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var arrived int
		for uid := range b.controllers {
			var owner sql.NullInt64
			if postgresSubmissionTests() {
				require.NoError(t, b.f.Maria.QueryRowContext(ctx, `SELECT max(pid) FROM pg_locks WHERE locktype='advisory' AND classid=702 AND objid=$1::oid AND granted`, uid).Scan(&owner))
			} else {
				require.NoError(t, b.f.Maria.QueryRowContext(ctx, testSQL(`SELECT IS_USED_LOCK(?)`), fmt.Sprintf("test-arrived-%d", uid)).Scan(&owner))
			}
			if owner.Valid {
				arrived++
			}
		}
		if arrived == count {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("service requests did not reach comment barrier")
		case <-ticker.C:
		}
	}
}
func (b *commentWriteBarrier) release(t *testing.T, uid int64) {
	t.Helper()
	var released int
	if postgresSubmissionTests() {
		var unlocked bool
		require.NoError(t, b.controllers[uid].QueryRowContext(b.f.Ctx, `SELECT pg_advisory_unlock(701, $1::integer)`, uid).Scan(&unlocked))
		if unlocked {
			released = 1
		}
	} else {
		require.NoError(t, b.controllers[uid].QueryRowContext(b.f.Ctx, testSQL(`SELECT RELEASE_LOCK(?)`), fmt.Sprintf("test-comment-%d", uid)).Scan(&released))
	}
	require.Equal(t, 1, released)
}

type concurrentMutation struct {
	done   chan struct{}
	err    error
	cancel context.CancelFunc
}

func (b *commentWriteBarrier) start(fn func(context.Context) error) *concurrentMutation {
	ctx, cancel := context.WithTimeout(b.f.Ctx, 20*time.Second)
	work := &concurrentMutation{done: make(chan struct{}), cancel: cancel}
	b.workers = append(b.workers, work)
	go func() { defer close(work.done); defer cancel(); work.err = fn(ctx) }()
	return work
}
func awaitMutation(t *testing.T, work *concurrentMutation) {
	t.Helper()
	select {
	case <-work.done:
		require.NoError(t, work.err)
	case <-time.After(20 * time.Second):
		t.Fatal("concurrent mutation did not finish")
	}
}

// The second request must wait before reading state; both committed reviewers
// must survive the incremental update exactly as they do a fresh rebuild.
func TestSubmissionConcurrencyTwoReviewers(t *testing.T) {
	app, f, pg := newCommentTransactionFixture(t)
	t.Cleanup(func() { require.NoError(t, f.Maria.Close()) })
	t.Cleanup(pg.Close)
	f.User(t, 8103, "second reviewer")
	barrier := newCommentWriteBarrier(t, f, 8101, 8103)
	start := func(uid int64) *concurrentMutation {
		return barrier.start(func(ctx context.Context) error {
			return app.Service.ReceiveComments(ctx, uid, []int64{8001}, constants.ActionAssignTesting, "", "false", "", "", "", "", nil)
		})
	}
	first := start(8101)
	barrier.wait(t, 1)
	second := start(8103)
	barrier.waitForBlockedMutation(t, 8101)
	barrier.release(t, 8101)
	awaitMutation(t, first)
	barrier.wait(t, 1)
	barrier.release(t, 8103)
	awaitMutation(t, second)
	var authors []int64
	rows, err := f.Maria.Query(testSQL(`SELECT fk_user_id FROM comment WHERE fk_submission_id=8001 AND fk_action_id=(SELECT id FROM action WHERE name='assign-testing') ORDER BY fk_user_id`))
	require.NoError(t, err)
	for rows.Next() {
		var uid int64
		require.NoError(t, rows.Scan(&uid))
		authors = append(authors, uid)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, []int64{8101, 8103}, authors)
	before := cacheSnapshot(t, f, 8001)
	visible, count := f.Search(t, 8101, &types.SubmissionsFilter{SubmissionIDs: []int64{8001}})
	require.EqualValues(t, 1, count)
	require.Len(t, visible, 1)
	require.ElementsMatch(t, []int64{8101, 8103}, visible[0].AssignedTestingUserIDs)
	f.Rebuild(t, 8001)
	after := cacheSnapshot(t, f, 8001)
	require.Equal(t, "8101,8103", after["active_assigned_testing_ids"])
	require.Equal(t, before, after)
	result, count := f.Search(t, 8101, &types.SubmissionsFilter{SubmissionIDs: []int64{8001}})
	require.EqualValues(t, 1, count)
	require.Len(t, result, 1)
	require.ElementsMatch(t, []int64{8101, 8103}, result[0].AssignedTestingUserIDs)
}

func TestSubmissionConcurrencyCommentOverlapsFileMutation(t *testing.T) {
	for _, operation := range []string{"upload", "delete"} {
		t.Run(operation, func(t *testing.T) {
			app, f, pg := newCommentTransactionFixture(t)
			t.Cleanup(func() { require.NoError(t, f.Maria.Close()) })
			t.Cleanup(pg.Close)
			f.File(t, fixtureFile{ID: 9001, SubmissionID: 8001, UserID: 8102, At: fixtureEpoch.Add(time.Hour)})
			f.Rebuild(t, 8001)
			barrier := newCommentWriteBarrier(t, f, 8101)
			done := barrier.start(func(ctx context.Context) error {
				return receiveTransactionComments(app, ctx, []int64{8001}, constants.ActionAssignTesting, "false")
			})
			barrier.wait(t, 1)
			fileMutation := barrier.start(func(ctx context.Context) error {
				session, err := f.DB.NewSession(ctx)
				if err != nil {
					return err
				}
				defer session.Rollback()
				if err = database.LockSubmissions(session, 8001); err != nil {
					return err
				}
				if operation == "upload" {
					_, err = session.Tx().ExecContext(ctx, testSQL(`INSERT INTO submission_file (id,fk_submission_id,fk_user_id,original_filename,current_filename,size,created_at,md5sum,sha256sum) VALUES (9002,8001,8102,'original-9002.7z','current-9002.7z',100,?,?,?)`), fixtureEpoch.Add(2*time.Hour), fmt.Sprintf("%032x", 9002), fmt.Sprintf("%064x", 9002))
					if err != nil {
						return err
					}
					meta := fixtureMeta("Concurrent upload")
					meta.SubmissionFileID = 9002
					if err = f.DB.StoreCurationMeta(session, meta); err != nil {
						return err
					}
				} else {
					if err = f.DB.SoftDeleteSubmissionFile(session, 9001, "concurrency regression"); err != nil {
						return err
					}
				}
				if err = f.DB.UpdateSubmissionCacheTable(session, 8001); err != nil {
					return err
				}
				return session.Commit()
			})
			barrier.waitForBlockedMutation(t, 8101)
			barrier.release(t, 8101)
			awaitMutation(t, done)
			awaitMutation(t, fileMutation)
			// Source rows, cached filenames and search must agree before rebuilding.
			wantIDs := []int64{8001, 9001, 9002}
			target := "original-9002.7z"
			beforeMatches, afterMatches := 1, 1
			wantSequence := "original-8001.7z,original-9001.7z,original-9002.7z"
			wantNewest := "9002"
			if operation == "delete" {
				wantIDs = []int64{8001}
				target = "original-9001.7z"
				beforeMatches, afterMatches = 0, 0
				wantSequence = "original-8001.7z"
				wantNewest = "8001"
			}
			source, err := f.Maria.Query(testSQL(`SELECT id FROM submission_file WHERE fk_submission_id=8001 AND deleted_at IS NULL ORDER BY id`))
			require.NoError(t, err)
			var actualIDs []int64
			for source.Next() {
				var id int64
				require.NoError(t, source.Scan(&id))
				actualIDs = append(actualIDs, id)
			}
			require.NoError(t, source.Err())
			require.NoError(t, source.Close())
			require.Equal(t, wantIDs, actualIDs)
			before := cacheSnapshot(t, f, 8001)
			require.Equal(t, wantSequence, before["original_filename_sequence"])
			filter := &types.SubmissionsFilter{SubmissionIDs: []int64{8001}, OriginalFilenamePartialAny: &target}
			visible, count := f.Search(t, 8101, filter)
			require.Len(t, visible, beforeMatches)
			require.EqualValues(t, beforeMatches, count)
			f.Rebuild(t, 8001)
			after := cacheSnapshot(t, f, 8001)
			require.Equal(t, before, after)
			require.Equal(t, wantSequence, after["original_filename_sequence"])
			require.Equal(t, wantNewest, after["fk_newest_file_id"])
			for _, snapshot := range []map[string]string{before, after} {
				require.Equal(t, "8101", snapshot["active_assigned_testing_ids"])
				for _, column := range []string{"active_assigned_verification_ids", "active_requested_changes_ids", "active_approved_ids", "active_verified_ids"} {
					require.Empty(t, snapshot[column])
				}
			}
			visible, count = f.Search(t, 8101, filter)
			require.Len(t, visible, afterMatches)
			require.EqualValues(t, afterMatches, count)
			stable := cacheSnapshot(t, f, 8001)
			f.Rebuild(t, 8001)
			require.Equal(t, stable, cacheSnapshot(t, f, 8001))
		})
	}
}

// Same reviewer and file operations without stale concurrent snapshots must
// remain equivalent to a rebuild. This separates the discrepancy from ordinary
// action or upload semantics already covered by the mutation suite.
func TestSubmissionConcurrencySerialControl(t *testing.T) {
	app, f, pg := newCommentTransactionFixture(t)
	t.Cleanup(func() { require.NoError(t, f.Maria.Close()) })
	t.Cleanup(pg.Close)
	f.User(t, 8103, "second reviewer")
	for _, uid := range []int64{8101, 8103} {
		require.NoError(t, app.Service.ReceiveComments(f.Ctx, uid, []int64{8001}, constants.ActionAssignTesting, "", "false", "", "", "", "", nil))
	}
	for _, operation := range []string{"reviews", "upload", "delete"} {
		if operation == "upload" {
			f.File(t, fixtureFile{ID: 9001, SubmissionID: 8001, UserID: 8102, At: fixtureEpoch.Add(time.Hour)})
			f.Rebuild(t, 8001)
		}
		if operation == "delete" {
			f.InTx(t, func(s database.DBSession) {
				require.NoError(t, f.DB.SoftDeleteSubmissionFile(s, 9001, "serial control"))
				require.NoError(t, f.DB.UpdateSubmissionCacheTable(s, 8001))
			})
		}
		before := cacheSnapshot(t, f, 8001)
		f.Rebuild(t, 8001)
		require.Equal(t, before, cacheSnapshot(t, f, 8001), operation)
		rows, count := f.Search(t, 8101, &types.SubmissionsFilter{SubmissionIDs: []int64{8001}})
		require.EqualValues(t, 1, count)
		require.Len(t, rows, 1)
		require.ElementsMatch(t, []int64{8101, 8103}, rows[0].AssignedTestingUserIDs)
	}
}

// Observe the waiter executing its parent-lock query while the first transaction
// holds that parent lock inside our trigger. MariaDB can park this query in
// optimizer Statistics before exposing it through INNODB_LOCK_WAITS, so observe
// the server statement boundary instead. Timing only bounds failure, not order.
func (b *commentWriteBarrier) waitForBlockedMutation(t *testing.T, uid int64) {
	b.waitForBlockedMutationCount(t, uid, 1)
}

func (b *commentWriteBarrier) waitForBlockedMutationCount(t *testing.T, uid int64, count int) {
	t.Helper()
	conf := *config.GetConfig(nil)
	conf.DBUser, conf.DBPassword = config.EnvString("DB_ROOT_USER"), config.EnvString("DB_ROOT_PASSWORD")
	observer, err := openSubmissionTestDB(&conf)
	require.NoError(t, err)
	defer observer.Close()
	ctx, cancel := context.WithTimeout(b.f.Ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		var err error
		if postgresSubmissionTests() {
			err = observer.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND datname=current_database() AND wait_event_type='Lock' AND query ILIKE '%SELECT id FROM submission%FOR UPDATE%'`).Scan(&waiting)
		} else {
			err = observer.QueryRowContext(ctx, testSQL(`SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE INFO LIKE 'SELECT id FROM submission WHERE id = % FOR UPDATE' AND ID <> IS_USED_LOCK(?)`), fmt.Sprintf("test-arrived-%d", uid)).Scan(&waiting)
		}
		require.NoError(t, err)
		if waiting >= count {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("second mutation did not wait for submission row lock")
		case <-ticker.C:
		}
	}
}

func TestSubmissionConcurrencyRevalidatesConflictingAction(t *testing.T) {
	app, f, pg := newCommentTransactionFixture(t)
	t.Cleanup(func() { require.NoError(t, f.Maria.Close()) })
	t.Cleanup(pg.Close)
	barrier := newCommentWriteBarrier(t, f, 8101)
	action := func(ctx context.Context) error {
		return receiveTransactionComments(app, ctx, []int64{8001}, constants.ActionAssignTesting, "false")
	}
	first := barrier.start(action)
	barrier.wait(t, 1)
	second := barrier.start(action)
	barrier.waitForBlockedMutation(t, 8101)
	barrier.release(t, 8101)
	awaitMutation(t, first)
	select {
	case <-second.done:
		var public constants.PublicError
		require.ErrorAs(t, second.err, &public)
		require.Equal(t, http.StatusBadRequest, public.Status)
		require.Equal(t, "you are already assigned to test submission 8001", public.Msg)
	case <-time.After(20 * time.Second):
		t.Fatal("conflicting action did not finish")
	}
	var comments, subscriptions int
	require.NoError(t, f.Maria.QueryRow(testSQL(`SELECT COUNT(*) FROM comment WHERE fk_submission_id=8001 AND fk_user_id=8101 AND fk_action_id=(SELECT id FROM action WHERE name='assign-testing')`)).Scan(&comments))
	require.NoError(t, f.Maria.QueryRow(testSQL(`SELECT COUNT(*) FROM submission_notification_subscription WHERE fk_submission_id=8001 AND fk_user_id=8101`)).Scan(&subscriptions))
	require.Equal(t, 1, comments)
	require.Equal(t, 1, subscriptions)
	before := cacheSnapshot(t, f, 8001)
	f.Rebuild(t, 8001)
	require.Equal(t, before, cacheSnapshot(t, f, 8001))
}

func TestSubmissionConcurrencyReversedBatchesAndIndependentSubmission(t *testing.T) {
	app, f, pg := newCommentTransactionFixture(t)
	t.Cleanup(func() { require.NoError(t, f.Maria.Close()) })
	t.Cleanup(pg.Close)
	f.User(t, 8103, "second reviewer")
	f.Submission(t, 8003, "staff")
	f.File(t, fixtureFile{ID: 8003, SubmissionID: 8003, UserID: 8102, At: fixtureEpoch})
	f.Comment(t, 8903, 8003, constants.ValidatorID, constants.ActionApprove, fixtureEpoch.Add(time.Microsecond), nil)
	f.Rebuild(t, 8003)
	barrier := newCommentWriteBarrier(t, f, 8101)
	first := barrier.start(func(ctx context.Context) error {
		return app.Service.ReceiveComments(ctx, 8101, []int64{8002, 8001}, constants.ActionAssignTesting, "", "false", "", "", "", "", nil)
	})
	barrier.wait(t, 1)
	second := barrier.start(func(ctx context.Context) error {
		return app.Service.ReceiveComments(ctx, 8103, []int64{8001, 8002}, constants.ActionAssignTesting, "", "false", "", "", "", "", nil)
	})
	barrier.waitForBlockedMutation(t, 8101)
	independent := barrier.start(func(ctx context.Context) error {
		return app.Service.ReceiveComments(ctx, 8103, []int64{8003}, constants.ActionAssignTesting, "", "false", "", "", "", "", nil)
	})
	awaitMutation(t, independent) // Must commit while the other submission remains locked.
	barrier.release(t, 8101)
	awaitMutation(t, first)
	awaitMutation(t, second)
	for _, sid := range []int64{8001, 8002, 8003} {
		rows, count := f.Search(t, 8101, &types.SubmissionsFilter{SubmissionIDs: []int64{sid}})
		require.EqualValues(t, 1, count)
		require.Len(t, rows, 1)
		want := []int64{8101, 8103}
		if sid == 8003 {
			want = []int64{8103}
		}
		require.ElementsMatch(t, want, rows[0].AssignedTestingUserIDs)
		before := cacheSnapshot(t, f, sid)
		f.Rebuild(t, sid)
		require.Equal(t, before, cacheSnapshot(t, f, sid))
	}
}

func TestSubmissionConcurrencyCannotDeleteBothRemainingFiles(t *testing.T) {
	app, f, pg := newCommentTransactionFixture(t)
	t.Cleanup(func() { require.NoError(t, f.Maria.Close()) })
	t.Cleanup(pg.Close)
	f.File(t, fixtureFile{ID: 9001, SubmissionID: 8001, UserID: 8102, At: fixtureEpoch.Add(time.Hour)})
	f.Rebuild(t, 8001)
	barrier := newCommentWriteBarrier(t, f, 8101)
	comment := barrier.start(func(ctx context.Context) error {
		return receiveTransactionComments(app, ctx, []int64{8001}, constants.ActionAssignTesting, "false")
	})
	barrier.wait(t, 1)
	deletions := []*concurrentMutation{}
	for _, id := range []int64{8001, 9001} {
		id := id
		deletions = append(deletions, barrier.start(func(ctx context.Context) error {
			return app.Service.SoftDeleteSubmissionFile(ctx, id, "concurrent deletion")
		}))
	}
	barrier.waitForBlockedMutationCount(t, 8101, 2)
	barrier.release(t, 8101)
	awaitMutation(t, comment)
	successes := 0
	for _, work := range deletions {
		select {
		case <-work.done:
			if work.err == nil {
				successes++
			} else {
				require.Contains(t, work.err.Error(), constants.ErrorCannotDeleteLastSubmissionFile)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("file deletion did not finish")
		}
	}
	require.Equal(t, 1, successes)
	var remaining int
	require.NoError(t, f.Maria.QueryRow(testSQL(`SELECT COUNT(*) FROM submission_file WHERE fk_submission_id=8001 AND deleted_at IS NULL`)).Scan(&remaining))
	require.Equal(t, 1, remaining)
	before := cacheSnapshot(t, f, 8001)
	f.Rebuild(t, 8001)
	require.Equal(t, before, cacheSnapshot(t, f, 8001))
}

func TestSubmissionConcurrencyCanceledWaitDoesNotMutate(t *testing.T) {
	app, f, pg := newCommentTransactionFixture(t)
	t.Cleanup(func() { require.NoError(t, f.Maria.Close()) })
	t.Cleanup(pg.Close)
	f.User(t, 8103, "canceled reviewer")
	barrier := newCommentWriteBarrier(t, f, 8101)
	first := barrier.start(func(ctx context.Context) error {
		return receiveTransactionComments(app, ctx, []int64{8001}, constants.ActionAssignTesting, "false")
	})
	barrier.wait(t, 1)
	canceled := barrier.start(func(ctx context.Context) error {
		return app.Service.ReceiveComments(ctx, 8103, []int64{8001}, constants.ActionAssignTesting, "", "false", "", "", "", "", nil)
	})
	barrier.waitForBlockedMutation(t, 8101)
	canceled.cancel()
	select {
	case <-canceled.done:
		require.ErrorIs(t, canceled.err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation did not stop lock waiter")
	}
	barrier.release(t, 8101)
	awaitMutation(t, first)
	var comments, subscriptions int
	require.NoError(t, f.Maria.QueryRow(testSQL(`SELECT COUNT(*) FROM comment WHERE fk_submission_id=8001 AND fk_user_id=8103`)).Scan(&comments))
	require.NoError(t, f.Maria.QueryRow(testSQL(`SELECT COUNT(*) FROM submission_notification_subscription WHERE fk_submission_id=8001 AND fk_user_id=8103`)).Scan(&subscriptions))
	require.Zero(t, comments)
	require.Zero(t, subscriptions)
	before := cacheSnapshot(t, f, 8001)
	require.Equal(t, "8101", before["active_assigned_testing_ids"])
	f.Rebuild(t, 8001)
	require.Equal(t, before, cacheSnapshot(t, f, 8001))
	require.NoError(t, app.Service.ReceiveComments(f.Ctx, 8103, []int64{8001}, constants.ActionAssignTesting, "", "false", "", "", "", "", nil), "canceled waiter must not retain a lock")
}
