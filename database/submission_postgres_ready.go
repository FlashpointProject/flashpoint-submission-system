package database

import (
	"slices"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

// Keep the expression in sync with migration 17's partial index and statistics.
// IS TRUE rejects both FALSE and NULL, just like the original WHERE predicates.
const pgReadyForFlashpointPredicate = `(submission_cache.bot_action = 'approve'
 AND submission_cache.active_requested_changes_ids IS NULL
 AND submission_cache.active_verified_ids IS NOT NULL
 AND submission_cache.distinct_actions !~* 'mark-added|reject') IS TRUE`

func pgReadyForFlashpointEligible(f *types.SubmissionsFilter) bool {
	return f != nil && len(f.BotActions) == 1 && f.BotActions[0] == "approve" &&
		f.RequestedChangedStatus != nil && *f.RequestedChangedStatus == "none" &&
		f.VerificationStatus != nil && *f.VerificationStatus == "verified" &&
		slices.Contains(f.DistinctActionsNot, "mark-added") && slices.Contains(f.DistinctActionsNot, "reject")
}
