package integration_tests

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func waitQuotaLockStatement(t *testing.T, ctx context.Context) {
	t.Helper()
	conf := *config.GetConfig(nil)
	conf.DBUser, conf.DBPassword = config.EnvString("DB_ROOT_USER"), config.EnvString("DB_ROOT_PASSWORD")
	observer, err := openSubmissionTestDB(&conf)
	require.NoError(t, err)
	defer observer.Close()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		query := `SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE ID <> CONNECTION_ID() AND INFO LIKE '%INSERT INTO submission_creation_lock%'`
		if postgresSubmissionTests() {
			query = `SELECT COUNT(*) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND datname=current_database() AND wait_event_type='Lock' AND query ILIKE '%INSERT INTO submission_creation_lock%'`
		}
		require.NoError(t, observer.QueryRowContext(ctx, query).Scan(&waiting))
		if waiting > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("upload did not reach the held creation lock")
		case <-ticker.C:
		}
	}
}

// History changes after HTTP admission must be evaluated by the worker after
// the lock wait, using a fresh read snapshot and the same rejection semantics.
func TestSubmissionQuotaRechecksHistoryAfterAdmission(t *testing.T) {
	for _, kind := range []string{"live", "rejected", "deleted", "deleted-rejection"} {
		t.Run(kind, func(t *testing.T) {
			app, l, ctx, db, pgdb, maria, _ := setupIntegrationTest(t)
			ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
			user := createExtendedTestUser(t, ctx, l, app, db, pgdb, 98201, nil, "late quota history")
			locker, err := db.NewSession(ctx)
			require.NoError(t, err)
			defer locker.Rollback()
			require.NoError(t, database.LockSubmissionCreation(locker, user.ID))
			var upload string
			t.Cleanup(func() {
				_ = locker.Rollback()
				if upload != "" {
					quotaWait(t, app, upload)
				}
			})
			upload = quotaAccepted(t, quotaUpload(t, l, app, user.Cookie, nil))
			waitQuotaLockStatement(t, ctx)
			f := &sqlFixture{DB: db, Maria: maria, Ctx: ctx}
			f.Submission(t, 98201, constants.SubmissionLevelAudition)
			f.File(t, fixtureFile{ID: 98201, SubmissionID: 98201, UserID: user.ID, At: fixtureEpoch})
			f.Comment(t, 98201, 98201, user.ID, constants.ActionUpload, fixtureEpoch.Add(time.Microsecond), nil)
			f.Comment(t, 98202, 98201, constants.ValidatorID, constants.ActionApprove, fixtureEpoch.Add(2*time.Microsecond), nil)
			if kind == "rejected" || kind == "deleted-rejection" {
				f.Comment(t, 98203, 98201, user.ID, constants.ActionReject, fixtureEpoch.Add(time.Minute), nil)
				if kind == "deleted-rejection" {
					_, err = maria.Exec(testSQL(`UPDATE comment SET deleted_at=? WHERE id=98203`), fixtureEpoch.Add(time.Hour))
					require.NoError(t, err)
				}
			}
			if kind == "deleted" {
				_, err = maria.Exec(testSQL(`UPDATE submission SET deleted_at=? WHERE id=98201`), fixtureEpoch.Add(time.Hour))
				require.NoError(t, err)
			}
			f.Rebuild(t, 98201)
			require.NoError(t, locker.Rollback())
			status := quotaWait(t, app, upload)
			if kind == "live" || kind == "deleted-rejection" {
				require.Equal(t, "failed", status.Status)
				require.NotNil(t, status.Message)
				require.Contains(t, *status.Message, "submission limit")
				require.Equal(t, 1, quotaRowCount(t, maria, "submission"))
				require.Equal(t, 1, quotaRowCount(t, maria, "submission_file"))
				// Status is published before the deferred worker cleanup completes.
				require.Eventually(t, func() bool {
					entries, e := os.ReadDir(app.Conf.SubmissionsDirFullPath)
					return e == nil && len(entries) == 0
				}, 5*time.Second, 10*time.Millisecond, "rejected archive must be removed")
			} else {
				require.Equal(t, "success", status.Status, status.Message)
				require.Equal(t, 2, quotaRowCount(t, maria, "submission"))
				require.Equal(t, 2, quotaRowCount(t, maria, "submission_file"))
			}
		})
	}
}

func TestSubmissionQuotaBlockedUserDoesNotBlockOtherUser(t *testing.T) {
	app, l, ctx, db, pgdb, maria, _ := setupIntegrationTest(t)
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
	firstUser := createExtendedTestUser(t, ctx, l, app, db, pgdb, 98101, nil, "held quota")
	otherUser := createExtendedTestUser(t, ctx, l, app, db, pgdb, 98102, nil, "independent quota")
	locker, err := db.NewSession(ctx)
	require.NoError(t, err)
	defer locker.Rollback()
	require.NoError(t, database.LockSubmissionCreation(locker, firstUser.ID))
	var first string
	t.Cleanup(func() {
		_ = locker.Rollback()
		if first != "" {
			quotaWait(t, app, first)
		}
	})
	first = quotaAccepted(t, quotaUpload(t, l, app, firstUser.Cookie, nil))
	waitQuotaLockStatement(t, ctx)
	other := quotaAccepted(t, quotaUpload(t, l, app, otherUser.Cookie, nil))
	require.Equal(t, "success", quotaWait(t, app, other).Status)
	require.Equal(t, 1, quotaRowCount(t, maria, "submission"), "other user commits while first is blocked on its database lock")
	require.NotEqual(t, "success", app.Service.SSK.Get(first).Status)
	require.NoError(t, locker.Rollback())
	require.Equal(t, "success", quotaWait(t, app, first).Status)
	require.Equal(t, 2, quotaRowCount(t, maria, "submission"))
}
