package integration_tests

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

// HTTP preferences, automatic subscriptions and message formatting are covered
// elsewhere. Here the DAL is exercised with adversarial join inputs, including
// duplicate source rows, without starting the app or notification worker.
func TestNotificationQueriesRecipientMatrix(t *testing.T) {
	f := newSQLFixture(t)
	for id := int64(11); id <= 18; id++ {
		f.User(t, id, fmt.Sprintf("recipient-%d", id))
	}
	f.Submission(t, 101, "staff")
	f.Submission(t, 102, "staff")
	for i, row := range [][2]int64{{11, 101}, {11, 101}, {11, 102}, {12, 101}, {15, 102}, {16, 101}, {17, 101}} {
		_, err := f.Maria.Exec(testSQL(`INSERT INTO submission_notification_subscription(fk_user_id,fk_submission_id,created_at) VALUES(?,?,?)`), row[0], row[1], fixtureEpoch)
		if postgresSubmissionTests() && i == 1 {
			require.ErrorContains(t, err, "duplicate key", "PostgreSQL prevents duplicate source subscriptions")
		} else {
			require.NoError(t, err)
		}
	}
	actions := append(constants.GetActionsWithNotification(), constants.ActionAuditionUpload, constants.ActionAuditionSubscribe)
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			f.InTx(t, func(s database.DBSession) {
				wrong := constants.ActionApprove
				if action == wrong {
					wrong = constants.ActionComment
				}
				for _, uid := range []int64{11, 13, 15, 17} {
					require.NoError(t, f.DB.StoreNotificationSettings(s, uid, []string{action, action, wrong}))
				}
				for _, uid := range []int64{12, 14} {
					require.NoError(t, f.DB.StoreNotificationSettings(s, uid, []string{wrong}))
				}
				// 16 subscribes without settings; 18 has neither. 17 is the eligible author.
				got, err := f.DB.GetUsersForNotification(s, 17, 101, action)
				require.NoError(t, err)
				require.ElementsMatch(t, []int64{11}, got, "duplicates from either join input must not duplicate recipients")
				got, err = f.DB.GetUsersForNotification(s, 17, 102, action)
				require.NoError(t, err)
				require.ElementsMatch(t, []int64{11, 15}, got)
				got, err = f.DB.GetUsersForNotification(s, 17, 999, action)
				require.NoError(t, err)
				require.Empty(t, got)
				got, err = f.DB.GetUsersForUniversalNotification(s, 17, action)
				require.NoError(t, err)
				require.ElementsMatch(t, []int64{11, 13, 15}, got, "universal recipients need no subscription")
				got, err = f.DB.GetUsersForNotification(s, 17, 101, "unknown-action")
				require.NoError(t, err)
				require.Empty(t, got)
				got, err = f.DB.GetUsersForUniversalNotification(s, 17, "unknown-action")
				require.NoError(t, err)
				require.Empty(t, got)
			})
		})
	}
	f.InTx(t, func(s database.DBSession) {
		require.NoError(t, f.DB.UnsubscribeUserFromSubmission(s, 11, 101))
		subscribed, err := f.DB.IsUserSubscribedToSubmission(s, 11, 101)
		require.NoError(t, err)
		require.False(t, subscribed, "unsubscribe removes every duplicate subscription")
		got, err := f.DB.GetUsersForNotification(s, 17, 101, constants.ActionAuditionSubscribe)
		require.NoError(t, err)
		require.Empty(t, got)
		got, err = f.DB.GetUsersForUniversalNotification(s, 17, constants.ActionAuditionSubscribe)
		require.NoError(t, err)
		require.ElementsMatch(t, []int64{11, 13, 15}, got, "unsubscribing cannot suppress universal notifications")
	})
}

func TestNotificationQueriesReplaceAndClearSettings(t *testing.T) {
	f := newSQLFixture(t)
	f.User(t, 11, "changed")
	f.User(t, 12, "unaffected")
	f.Submission(t, 101, "staff")
	f.InTx(t, func(s database.DBSession) {
		for _, uid := range []int64{11, 12} {
			require.NoError(t, f.DB.SubscribeUserToSubmission(s, uid, 101))
			require.NoError(t, f.DB.StoreNotificationSettings(s, uid, []string{constants.ActionApprove, constants.ActionComment}))
		}
	})
	for _, tc := range []struct {
		name    string
		actions []string
	}{
		{"replace", []string{constants.ActionReject}}, {"empty", []string{}},
		{"restore", []string{constants.ActionReject}}, {"nil", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.InTx(t, func(s database.DBSession) { require.NoError(t, f.DB.StoreNotificationSettings(s, 11, tc.actions)) })
			// Read a new committed session, not merely writes visible to their own transaction.
			f.InTx(t, func(s database.DBSession) {
				got, err := f.DB.GetNotificationSettingsByUserID(s, 11)
				require.NoError(t, err)
				require.ElementsMatch(t, tc.actions, got)
				other, err := f.DB.GetNotificationSettingsByUserID(s, 12)
				require.NoError(t, err)
				require.ElementsMatch(t, []string{constants.ActionApprove, constants.ActionComment}, other)
				recipients, err := f.DB.GetUsersForNotification(s, 999, 101, constants.ActionApprove)
				require.NoError(t, err)
				require.ElementsMatch(t, []int64{12}, recipients)
				recipients, err = f.DB.GetUsersForNotification(s, 999, 101, constants.ActionReject)
				require.NoError(t, err)
				if len(tc.actions) == 0 {
					require.Empty(t, recipients)
				} else {
					require.Equal(t, []int64{11}, recipients)
				}
			})
		})
	}
}

// StoreNotificationSettings deletes old rows before attempting its replacement.
// A failed insert must be rolled back by the caller, preserving committed data.
func TestNotificationQueriesInvalidSettingsRollback(t *testing.T) {
	f := newSQLFixture(t)
	f.User(t, 11, "rollback")
	f.Submission(t, 101, "staff")
	previous := []string{constants.ActionComment, constants.ActionApprove}
	f.InTx(t, func(s database.DBSession) {
		require.NoError(t, f.DB.SubscribeUserToSubmission(s, 11, 101))
		require.NoError(t, f.DB.StoreNotificationSettings(s, 11, previous))
	})
	s, err := f.DB.NewSession(f.Ctx)
	require.NoError(t, err)
	defer s.Rollback()
	err = f.DB.StoreNotificationSettings(s, 11, []string{constants.ActionReject, "unknown-action"})
	require.Error(t, err, "unknown action must fail its NOT NULL/FK constraint")
	require.NoError(t, s.Rollback())
	f.InTx(t, func(s database.DBSession) {
		actions, err := f.DB.GetNotificationSettingsByUserID(s, 11)
		require.NoError(t, err)
		require.ElementsMatch(t, previous, actions)
		for _, action := range previous {
			recipients, err := f.DB.GetUsersForNotification(s, 999, 101, action)
			require.NoError(t, err)
			require.Equal(t, []int64{11}, recipients)
		}
		recipients, err := f.DB.GetUsersForNotification(s, 999, 101, constants.ActionReject)
		require.NoError(t, err)
		require.Empty(t, recipients, "valid prefix of failed replacement must not persist")
	})
}

// Duplicate settings currently persist (there is no unique constraint). This is
// characterization, not a PostgreSQL storage contract: recipients MUST be unique
// regardless; migration may deduplicate settings once explicitly decided.
func TestNotificationQueriesCurrentBehaviorDuplicateSettings(t *testing.T) {
	f := newSQLFixture(t)
	f.User(t, 11, "duplicate")
	f.InTx(t, func(s database.DBSession) {
		require.NoError(t, f.DB.StoreNotificationSettings(s, 11, []string{constants.ActionComment, constants.ActionComment}))
	})
	f.InTx(t, func(s database.DBSession) {
		got, err := f.DB.GetNotificationSettingsByUserID(s, 11)
		require.NoError(t, err)
		if postgresSubmissionTests() {
			require.Equal(t, []string{constants.ActionComment}, got, "PostgreSQL stores a unique preference per user/action")
		} else {
			require.Equal(t, []string{constants.ActionComment, constants.ActionComment}, got)
		}
		recipients, err := f.DB.GetUsersForUniversalNotification(s, 999, constants.ActionComment)
		require.NoError(t, err)
		require.Equal(t, []int64{11}, recipients)
	})
}

func insertNotificationFixture(t *testing.T, f *sqlFixture, n types.Notification, sent interface{}) {
	t.Helper()
	_, err := f.Maria.Exec(testSQL(`INSERT INTO submission_notification(id,fk_submission_notification_type_id,message,created_at,sent_at) VALUES(?,(SELECT id FROM submission_notification_type WHERE name=?),?,?,?)`), n.ID, n.Type, n.Message, n.CreatedAt, sent)
	require.NoError(t, err)
}

func TestNotificationQueriesOldestUnsentQueue(t *testing.T) {
	f := newSQLFixture(t)
	empty := func() {
		t.Helper()
		f.InTx(t, func(s database.DBSession) {
			n, err := f.DB.GetOldestUnsentNotification(s)
			require.ErrorIs(t, err, sql.ErrNoRows)
			require.Nil(t, n)
		})
	}
	empty()
	// IDs/insertion order oppose chronology; a sent older row must be ignored.
	sent := types.Notification{ID: 90, Type: constants.NotificationDefault, Message: "already sent", CreatedAt: fixtureEpoch.Add(-time.Hour)}
	sentAt := fixtureEpoch.Add(-time.Minute)
	insertNotificationFixture(t, f, sent, sentAt)
	empty()
	expected := []types.Notification{
		{ID: 40, Type: constants.NotificationCurationFeed, Message: "oldest\n<@11> café 'quoted'", CreatedAt: fixtureEpoch},
		{ID: 20, Type: constants.NotificationDefault, Message: "", CreatedAt: fixtureEpoch.Add(time.Microsecond)},
		{ID: 10, Type: constants.NotificationCurationFeed, Message: "newest", CreatedAt: fixtureEpoch.Add(2 * time.Microsecond)},
	}
	for i := len(expected) - 1; i >= 0; i-- {
		insertNotificationFixture(t, f, expected[i], nil)
	}
	for _, want := range expected {
		f.InTx(t, func(s database.DBSession) {
			got, err := f.DB.GetOldestUnsentNotification(s)
			require.NoError(t, err)
			require.Equal(t, want, *got, "full projection including zero SentAt")
		})
		f.InTx(t, func(s database.DBSession) { require.NoError(t, f.DB.MarkNotificationAsSent(s, want.ID)) })
		var storedSentAt sql.NullTime
		require.NoError(t, f.Maria.QueryRow(testSQL(`SELECT sent_at FROM submission_notification WHERE id=?`), want.ID).Scan(&storedSentAt))
		require.True(t, storedSentAt.Valid)
	}
	empty()
	var unchanged time.Time
	require.NoError(t, f.Maria.QueryRow(testSQL(`SELECT sent_at FROM submission_notification WHERE id=90`)).Scan(&unchanged))
	require.Equal(t, sentAt, unchanged, "marking other rows must preserve previously sent timestamps")
}

// The queue has no ID tie-breaker. Do not bless whichever tied row MariaDB
// happens to return first as an ordering contract for PostgreSQL.
func TestNotificationQueriesCurrentBehaviorTimestampTie(t *testing.T) {
	f := newSQLFixture(t)
	for _, id := range []int64{20, 10} {
		insertNotificationFixture(t, f, types.Notification{ID: id, Type: constants.NotificationDefault, Message: fmt.Sprint(id), CreatedAt: fixtureEpoch}, nil)
	}
	var seen []int64
	for range 2 {
		f.InTx(t, func(s database.DBSession) {
			got, err := f.DB.GetOldestUnsentNotification(s)
			require.NoError(t, err)
			require.Contains(t, []int64{10, 20}, got.ID)
			require.Equal(t, fmt.Sprint(got.ID), got.Message)
			seen = append(seen, got.ID)
			require.NoError(t, f.DB.MarkNotificationAsSent(s, got.ID))
		})
	}
	require.ElementsMatch(t, []int64{10, 20}, seen)
}
