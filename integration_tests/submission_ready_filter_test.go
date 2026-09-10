package integration_tests

import (
	"context"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

// Deliberately edit cache columns here: NULL and empty collections have distinct
// filter meanings, including states the history reducer does not usually emit.
// Existing mutation/cache tests cover deriving these collections from actions.
func TestSubmissionReadyFilterTransitions(t *testing.T) {
	f, expected := seedSearchFixture(t)
	ready := func() *types.SubmissionsFilter {
		return &types.SubmissionsFilter{SubmissionIDs: []int64{101}, BotActions: []string{"approve"}, RequestedChangedStatus: utils.StrPtr("none"), VerificationStatus: utils.StrPtr("verified"), DistinctActionsNot: []string{"reject", "mark-added"}}
	}
	cases := []struct {
		name                            string
		bot, changes, verified, actions *string
		alter                           func(*types.SubmissionsFilter)
		match                           bool
	}{
		{"enters ready", utils.StrPtr("approve"), nil, utils.StrPtr("1005"), utils.StrPtr("approve,verify"), nil, true},
		{"changes exclude", utils.StrPtr("approve"), utils.StrPtr("1004"), utils.StrPtr("1005"), utils.StrPtr("approve,verify"), nil, false},
		{"empty changes still exclude", utils.StrPtr("approve"), utils.StrPtr(""), utils.StrPtr("1005"), utils.StrPtr("approve,verify"), nil, false},
		{"returns after changes cleared", utils.StrPtr("approve"), nil, utils.StrPtr("1005"), utils.StrPtr("approve,verify"), nil, true},
		{"NULL verified excludes", utils.StrPtr("approve"), nil, nil, utils.StrPtr("approve,verify"), nil, false},
		{"empty verified qualifies", utils.StrPtr("approve"), nil, utils.StrPtr(""), utils.StrPtr("approve,verify"), nil, true},
		{"NULL actions excludes", utils.StrPtr("approve"), nil, utils.StrPtr("1005"), nil, nil, false},
		{"empty actions qualifies", utils.StrPtr("approve"), nil, utils.StrPtr("1005"), utils.StrPtr(""), nil, true},
		{"reject excludes", utils.StrPtr("approve"), nil, utils.StrPtr("1005"), utils.StrPtr("approve,reject,verify"), nil, false},
		{"imported excludes", utils.StrPtr("approve"), nil, utils.StrPtr("1005"), utils.StrPtr("approve,mark-added,verify"), nil, false},
		{"NULL bot excludes", nil, nil, utils.StrPtr("1005"), utils.StrPtr("approve,verify"), nil, false},
		{"other bot excludes", utils.StrPtr("request-changes"), nil, utils.StrPtr("1005"), utils.StrPtr("approve,verify"), nil, false},
		{"bot alternatives not narrowed", utils.StrPtr("request-changes"), nil, utils.StrPtr("1005"), utils.StrPtr("approve,verify"), func(f *types.SubmissionsFilter) { f.BotActions = []string{"approve", "request-changes"} }, true},
		{"reversed exclusions", utils.StrPtr("approve"), nil, utils.StrPtr("1005"), utils.StrPtr("approve,verify"), func(f *types.SubmissionsFilter) { f.DistinctActionsNot = []string{"mark-added", "reject"} }, true},
		{"extra exclusion remains effective", utils.StrPtr("approve"), nil, utils.StrPtr("1005"), utils.StrPtr("approve,verify,comment"), func(f *types.SubmissionsFilter) { f.DistinctActionsNot = []string{"mark-added", "comment", "reject"} }, false},
		{"extra exclusion can match", utils.StrPtr("approve"), nil, utils.StrPtr("1005"), utils.StrPtr("approve,verify"), func(f *types.SubmissionsFilter) { f.DistinctActionsNot = []string{"mark-added", "comment", "reject"} }, true},
	}
	check := func(t *testing.T, filter *types.SubmissionsFilter, want *types.ExtendedSubmission) {
		t.Helper()
		modes := []string{"auto"}
		if postgresSubmissionTests() {
			modes = append(modes, "force_generic_plan")
		}
		for _, mode := range modes {
			ctx := context.WithValue(f.Ctx, utils.CtxKeys.UserID, int64(1004))
			session, err := f.DB.NewSession(ctx)
			require.NoError(t, err)
			defer session.Rollback()
			if mode != "auto" {
				_, err = session.Tx().ExecContext(ctx, "SET LOCAL plan_cache_mode=force_generic_plan")
				require.NoError(t, err)
			}
			rows, count, err := f.DB.SearchSubmissions(session, filter)
			require.NoError(t, err, mode)
			direct, err := database.CountSubmissions(f.DB, session, filter)
			require.NoError(t, err, mode)
			require.Equal(t, count, direct, mode)
			if want == nil {
				require.Empty(t, rows, mode)
				require.Zero(t, count, mode)
			} else {
				require.EqualValues(t, 1, count, mode)
				require.Len(t, rows, 1, mode)
				require.Equal(t, canonicalSearchSubmission(want), canonicalSearchSubmission(rows[0]), mode)
			}
			require.NoError(t, session.Rollback())
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.Maria.ExecContext(f.Ctx, testSQL(`UPDATE submission_cache SET bot_action=?, active_requested_changes_ids=?, active_verified_ids=?, distinct_actions=? WHERE fk_submission_id=101`), tc.bot, tc.changes, tc.verified, tc.actions)
			require.NoError(t, err)
			filter := ready()
			if tc.alter != nil {
				tc.alter(filter)
			}
			var want *types.ExtendedSubmission
			if tc.match {
				value := *expected["A"]
				want = &value
				want.BotAction = *tc.bot
				want.RequestedChangesUserIDs = []int64{}
				want.VerifiedUserIDs = []int64{}
				if tc.verified != nil && *tc.verified != "" {
					want.VerifiedUserIDs = []int64{1005}
				}
				want.DistinctActions = []string{}
				if tc.actions != nil && *tc.actions != "" {
					want.DistinctActions = strings.Split(*tc.actions, ",")
				}
			}
			check(t, filter, want)
		})
	}
	t.Run("missing cache excludes", func(t *testing.T) {
		_, err := f.Maria.ExecContext(f.Ctx, `DELETE FROM submission_cache WHERE fk_submission_id=101`)
		require.NoError(t, err)
		check(t, ready(), nil)
	})
	t.Run("rebuild restores ready", func(t *testing.T) {
		_, err := f.Maria.ExecContext(f.Ctx, `INSERT INTO submission_cache (fk_submission_id) VALUES (101)`)
		require.NoError(t, err)
		f.Rebuild(t, 101)
		check(t, ready(), expected["A"])
	})
}
