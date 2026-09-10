package database

import (
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func TestPGReadyForFlashpointEligibility(t *testing.T) {
	base := func() *types.SubmissionsFilter {
		return &types.SubmissionsFilter{BotActions: []string{"approve"}, RequestedChangedStatus: utils.StrPtr("none"), VerificationStatus: utils.StrPtr("verified"), DistinctActionsNot: []string{"reject", "mark-added"}}
	}
	require.False(t, pgReadyForFlashpointEligible(nil))
	require.False(t, pgReadyForFlashpointEligible(&types.SubmissionsFilter{}))
	require.True(t, pgReadyForFlashpointEligible(base()))
	for _, tc := range []struct {
		name     string
		alter    func(*types.SubmissionsFilter)
		eligible bool
	}{
		{"no bot", func(f *types.SubmissionsFilter) { f.BotActions = nil }, false},
		{"other bot", func(f *types.SubmissionsFilter) { f.BotActions = []string{"request-changes"} }, false},
		{"OR bot", func(f *types.SubmissionsFilter) { f.BotActions = []string{"approve", "request-changes"} }, false},
		{"nil changes", func(f *types.SubmissionsFilter) { f.RequestedChangedStatus = nil }, false},
		{"ongoing changes", func(f *types.SubmissionsFilter) { f.RequestedChangedStatus = utils.StrPtr("ongoing") }, false},
		{"nil verification", func(f *types.SubmissionsFilter) { f.VerificationStatus = nil }, false},
		{"unverified", func(f *types.SubmissionsFilter) { f.VerificationStatus = utils.StrPtr("unverified") }, false},
		{"missing reject", func(f *types.SubmissionsFilter) { f.DistinctActionsNot = []string{"mark-added"} }, false},
		{"missing imported", func(f *types.SubmissionsFilter) { f.DistinctActionsNot = []string{"reject"} }, false},
		{"reversed", func(f *types.SubmissionsFilter) { f.DistinctActionsNot = []string{"mark-added", "reject"} }, true},
		{"extra exclusion", func(f *types.SubmissionsFilter) { f.DistinctActionsNot = []string{"comment", "mark-added", "reject"} }, true},
		{"additional filters", func(f *types.SubmissionsFilter) {
			f.TitlePartial = utils.StrPtr("test")
			f.PlatformPartial = utils.StrPtr("Flash")
			f.SubmissionIDs = []int64{1}
			f.ExcludeLegacy = true
			f.Page = utils.Int64Ptr(2)
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := base()
			tc.alter(f)
			require.Equal(t, tc.eligible, pgReadyForFlashpointEligible(f))
		})
	}
}
