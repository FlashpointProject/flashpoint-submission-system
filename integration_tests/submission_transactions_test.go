package integration_tests

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/transport"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// Snapshot actual persisted values, retaining NULLs and timestamps. Sequence
// allocation is deliberately excluded: rolled-back inserts may consume IDs.
func transactionSnapshot(t *testing.T, f *sqlFixture, pg *pgxpool.Pool, extraTables ...string) map[string][][]sql.NullString {
	t.Helper()
	result := map[string][][]sql.NullString{}
	for _, table := range append([]string{"submission", "submission_file", "comment", "submission_cache", "submission_notification_subscription", "submission_notification"}, extraTables...) {
		order := "id"
		if table == "submission_cache" {
			order = "fk_submission_id"
		}
		rows, err := f.Maria.QueryContext(f.Ctx, "SELECT * FROM "+table+" ORDER BY "+order)
		require.NoError(t, err)
		columns, err := rows.Columns()
		require.NoError(t, err)
		result[table] = [][]sql.NullString{}
		for rows.Next() {
			values := make([]sql.NullString, len(columns))
			args := make([]interface{}, len(columns))
			for i := range values {
				args[i] = &values[i]
			}
			require.NoError(t, rows.Scan(args...))
			result[table] = append(result[table], values)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
	rows, err := pg.Query(f.Ctx, "SELECT to_jsonb(e)::text FROM activity_events e ORDER BY id")
	require.NoError(t, err)
	defer rows.Close()
	result["activity_events"] = [][]sql.NullString{}
	for rows.Next() {
		var value string
		require.NoError(t, rows.Scan(&value))
		result["activity_events"] = append(result["activity_events"], []sql.NullString{{String: value, Valid: true}})
	}
	require.NoError(t, rows.Err())
	return result
}

func newCommentTransactionFixture(t *testing.T) (*transport.App, *sqlFixture, *pgxpool.Pool) {
	t.Helper()
	app, l, ctx, db, _, maria, pg := setupIntegrationTest(t)
	ctx = addContextValues(ctx, l, 8101, "comment-transactions")
	f := &sqlFixture{DB: db, Maria: maria, Ctx: ctx}
	f.User(t, 8101, "reviewer")
	f.User(t, 8102, "uploader")
	for _, sid := range []int64{8001, 8002} {
		f.Submission(t, sid, "staff")
		f.File(t, fixtureFile{ID: sid, SubmissionID: sid, UserID: 8102, At: fixtureEpoch})
		// Submission 8001 is deliberately sorted first in batch search.
		at := fixtureEpoch.Add(time.Duration(8003-sid) * time.Second)
		f.Comment(t, sid, sid, constants.ValidatorID, constants.ActionApprove, at, nil)
	}
	f.Rebuild(t, 8001, 8002)
	return app, f, pg
}

func receiveTransactionComments(app *transport.App, ctx context.Context, ids []int64, action, skip string) error {
	return app.Service.ReceiveComments(ctx, 8101, ids, action, "transaction regression", skip, "", "", "", "", nil)
}

func TestSubmissionCommentBatchEdges(t *testing.T) {
	t.Run("empty IDs reject without mutating unrelated submissions", func(t *testing.T) {
		app, f, pg := newCommentTransactionFixture(t)
		before := transactionSnapshot(t, f, pg)
		for _, ids := range [][]int64{nil, {}} {
			for _, skip := range []string{"false", "true"} {
				err := receiveTransactionComments(app, f.Ctx, ids, constants.ActionAssignTesting, skip)
				var public constants.PublicError
				require.ErrorAs(t, err, &public)
				require.Equal(t, http.StatusBadRequest, public.Status)
				require.Contains(t, public.Msg, "at least one submission")
				require.Equal(t, before, transactionSnapshot(t, f, pg))
			}
		}
	})
	t.Run("duplicate IDs act once and repeated skipped actions do nothing", func(t *testing.T) {
		app, f, pg := newCommentTransactionFixture(t)
		require.NoError(t, receiveTransactionComments(app, f.Ctx, []int64{8001, 8001}, constants.ActionAssignTesting, "false"))
		state := transactionSnapshot(t, f, pg)
		require.Len(t, state["comment"], 3)
		require.Len(t, state["submission_notification_subscription"], 1)
		require.Len(t, state["activity_events"], 1)
		require.Len(t, state["submission_notification"], 0)
		require.Equal(t, "8101", cacheSnapshot(t, f, 8001)["active_assigned_testing_ids"])
		require.NoError(t, receiveTransactionComments(app, f.Ctx, []int64{8001, 8001}, constants.ActionAssignTesting, "true"))
		require.Equal(t, state, transactionSnapshot(t, f, pg))
		require.Error(t, receiveTransactionComments(app, f.Ctx, []int64{8001, 8001}, constants.ActionAssignTesting, "false"))
		require.Equal(t, state, transactionSnapshot(t, f, pg))
	})
	t.Run("all imported skipped and mixed batch writes only active submission", func(t *testing.T) {
		app, f, pg := newCommentTransactionFixture(t)
		f.Comment(t, 8010, 8002, 8102, constants.ActionMarkAdded, fixtureEpoch.Add(time.Second), nil)
		f.Rebuild(t, 8002)
		before := transactionSnapshot(t, f, pg)
		require.NoError(t, receiveTransactionComments(app, f.Ctx, []int64{8002, 8002}, constants.ActionAssignTesting, "true"))
		require.Equal(t, before, transactionSnapshot(t, f, pg))
		// The first active submission is processed before the imported one rejects.
		require.Error(t, receiveTransactionComments(app, f.Ctx, []int64{8001, 8002}, constants.ActionAssignTesting, "false"))
		require.Equal(t, before, transactionSnapshot(t, f, pg), "rollback includes subscriptions, cache, and PG events")
		require.NoError(t, receiveTransactionComments(app, f.Ctx, []int64{8001, 8002, 8001}, constants.ActionAssignTesting, "true"))
		after := transactionSnapshot(t, f, pg)
		require.Len(t, after["comment"], len(before["comment"])+1)
		require.Len(t, after["activity_events"], 1)
		require.Len(t, after["submission_notification_subscription"], 1)
		require.Equal(t, "8101", cacheSnapshot(t, f, 8001)["active_assigned_testing_ids"])
		require.Equal(t, "", cacheSnapshot(t, f, 8002)["active_assigned_testing_ids"])
		require.NoError(t, receiveTransactionComments(app, f.Ctx, []int64{8001, 8002}, constants.ActionAssignTesting, "true"))
		require.Equal(t, after, transactionSnapshot(t, f, pg))
	})
	t.Run("CurrentBehavior restricted actions reject duplicate IDs by raw batch length", func(t *testing.T) {
		app, f, pg := newCommentTransactionFixture(t)
		before := transactionSnapshot(t, f, pg)
		for _, action := range []string{constants.ActionRequestChanges, constants.ActionReject} {
			for _, skip := range []string{"false", "true"} {
				err := receiveTransactionComments(app, f.Ctx, []int64{8001, 8001}, action, skip)
				var public constants.PublicError
				require.ErrorAs(t, err, &public)
				require.Equal(t, http.StatusBadRequest, public.Status)
				require.Contains(t, public.Msg, "multiple submissions at once")
				require.Equal(t, before, transactionSnapshot(t, f, pg))
			}
		}
	})
	t.Run("missing member rejects entire batch before writes", func(t *testing.T) {
		app, f, pg := newCommentTransactionFixture(t)
		before := transactionSnapshot(t, f, pg)
		for _, skip := range []string{"false", "true"} {
			err := receiveTransactionComments(app, f.Ctx, []int64{8001, 8999}, constants.ActionAssignTesting, skip)
			var public constants.PublicError
			require.ErrorAs(t, err, &public)
			require.Equal(t, http.StatusNotFound, public.Status)
			require.Equal(t, before, transactionSnapshot(t, f, pg))
		}
	})
}

func TestSubmissionCommentTransactionRollback(t *testing.T) {
	// Each trigger checks in-transaction prerequisites before signalling its unique
	// stage marker. Seeing that marker proves the operation reached real writes;
	// the full before/after snapshot then proves those writes did not persist.
	cases := []struct{ name, trigger, marker string }{
		{"after comment insertion", "CREATE TRIGGER transaction_fault AFTER INSERT ON comment FOR EACH ROW BEGIN IF NEW.fk_user_id=8101 AND NEW.fk_action_id=(SELECT id FROM action WHERE name='approve') THEN IF NOT EXISTS (SELECT 1 FROM submission_notification_subscription WHERE fk_user_id=8101 AND fk_submission_id=8001) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='missing subscription prerequisite'; END IF; SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected after comment'; END IF; END", "injected after comment"},
		{"after notification insertion", "CREATE TRIGGER transaction_fault AFTER INSERT ON submission_notification FOR EACH ROW BEGIN IF (SELECT COUNT(*) FROM comment WHERE fk_submission_id=8001 AND fk_user_id=8101)=3 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected after notification'; ELSE SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='missing comment prerequisites'; END IF; END", "injected after notification"},
		{"after cache update", "CREATE TRIGGER transaction_fault AFTER UPDATE ON submission_cache FOR EACH ROW BEGIN IF NEW.fk_submission_id=8001 AND NEW.active_approved_ids='8101' THEN IF (SELECT COUNT(*) FROM submission_notification)=1 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected after cache'; ELSE SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='missing cache prerequisites'; END IF; END IF; END", "injected after cache"},
		{"later batch member after first fully updated", "CREATE TRIGGER transaction_fault BEFORE INSERT ON comment FOR EACH ROW BEGIN IF NEW.fk_submission_id=8002 AND NEW.fk_user_id=8101 THEN IF (SELECT active_approved_ids FROM submission_cache WHERE fk_submission_id=8001)='8101' AND (SELECT COUNT(*) FROM submission_notification)=1 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected later member'; ELSE SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='missing first member prerequisites'; END IF; END IF; END", "injected later member"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, f, pg := newCommentTransactionFixture(t)
			for _, sid := range []int64{8001, 8002} {
				f.Comment(t, sid+100, sid, 8101, constants.ActionAssignTesting, fixtureEpoch.Add(time.Duration(8003-sid)*time.Second), nil)
			}
			f.InTx(t, func(s database.DBSession) {
				require.NoError(t, f.DB.StoreNotificationSettings(s, 8102, []string{constants.ActionApprove}))
				for _, sid := range []int64{8001, 8002} {
					require.NoError(t, f.DB.SubscribeUserToSubmission(s, 8102, sid))
				}
			})
			f.Rebuild(t, 8001, 8002)
			before := transactionSnapshot(t, f, pg)
			_, err := f.Maria.ExecContext(f.Ctx, testTriggerSQL(tc.trigger))
			require.NoError(t, err)
			t.Cleanup(func() {
				_, err := f.Maria.ExecContext(f.Ctx, testDropTriggerSQL("transaction_fault"))
				require.NoError(t, err)
			})
			err = receiveTransactionComments(app, f.Ctx, []int64{8001, 8002}, constants.ActionApprove, "false")
			require.ErrorContains(t, err, tc.marker)
			require.Equal(t, before, transactionSnapshot(t, f, pg), "failed batch rolls back both stores before commit")
			_, err = f.Maria.ExecContext(f.Ctx, testDropTriggerSQL("transaction_fault"))
			require.NoError(t, err)
			require.NoError(t, receiveTransactionComments(app, f.Ctx, []int64{8001, 8002}, constants.ActionApprove, "false"), "same batch succeeds when fault is removed")
			after := transactionSnapshot(t, f, pg)
			require.Len(t, after["comment"], len(before["comment"])+4)
			require.Len(t, after["submission_notification_subscription"], 4)
			require.Len(t, after["submission_notification"], 2)
			require.Len(t, after["activity_events"], 4)
			for _, sid := range []int64{8001, 8002} {
				state := cacheSnapshot(t, f, sid)
				require.Equal(t, "8101", state["active_approved_ids"])
				require.Equal(t, "", state["active_assigned_testing_ids"])
			}
		})
	}
}
