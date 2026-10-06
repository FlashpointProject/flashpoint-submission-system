package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

// Every rule starts with a valid submission and changes only the state relevant
// to that rule. Assert the exact error so an earlier guard cannot mask it.
func Test_isActionValidForSubmission(t *testing.T) {
	const actor int64 = 1
	const other int64 = 2
	tests := []struct {
		name       string
		action     string
		valid      types.ExtendedSubmission
		invalidate func(*types.ExtendedSubmission)
		message    string
	}{
		{
			name:       "uploader cannot AssignTesting",
			action:     constants.ActionAssignTesting,
			valid:      types.ExtendedSubmission{},
			invalidate: func(s *types.ExtendedSubmission) { s.LastUploaderID = actor },
			message:    "you are the uploader of the newest version of submission 42, so you cannot assign it",
		},
		{
			name:       "uploader cannot Approve",
			action:     constants.ActionApprove,
			valid:      types.ExtendedSubmission{AssignedTestingUserIDs: []int64{actor}},
			invalidate: func(s *types.ExtendedSubmission) { s.LastUploaderID = actor },
			message:    "you are the uploader of the newest version of submission 42, so you cannot approve it",
		},
		{
			name:       "uploader cannot RequestChanges",
			action:     constants.ActionRequestChanges,
			valid:      types.ExtendedSubmission{},
			invalidate: func(s *types.ExtendedSubmission) { s.LastUploaderID = actor },
			message:    "you are the uploader of the newest version of submission 42, so you cannot request changes on it",
		},
		{
			name:       "uploader cannot Verify",
			action:     constants.ActionVerify,
			valid:      types.ExtendedSubmission{AssignedVerificationUserIDs: []int64{actor}, ApprovedUserIDs: []int64{other}},
			invalidate: func(s *types.ExtendedSubmission) { s.LastUploaderID = actor },
			message:    "you are the uploader of the newest version of submission 42, so you cannot verify it",
		},
		{
			name:       "cannot double assign Testing",
			action:     constants.ActionAssignTesting,
			valid:      types.ExtendedSubmission{},
			invalidate: func(s *types.ExtendedSubmission) { s.AssignedTestingUserIDs = []int64{actor} },
			message:    "you are already assigned to test submission 42",
		},
		{
			name:       "cannot unassign without assignment Testing",
			action:     constants.ActionUnassignTesting,
			valid:      types.ExtendedSubmission{AssignedTestingUserIDs: []int64{actor}},
			invalidate: func(s *types.ExtendedSubmission) { s.AssignedTestingUserIDs = []int64{other} },
			message:    "you are not assigned to test submission 42",
		},
		{
			name:       "cannot double assign Verification",
			action:     constants.ActionAssignVerification,
			valid:      types.ExtendedSubmission{ApprovedUserIDs: []int64{other}},
			invalidate: func(s *types.ExtendedSubmission) { s.AssignedVerificationUserIDs = []int64{actor} },
			message:    "you are already assigned to verify submission 42",
		},
		{
			name:       "cannot unassign without assignment Verification",
			action:     constants.ActionUnassignVerification,
			valid:      types.ExtendedSubmission{AssignedVerificationUserIDs: []int64{actor}},
			invalidate: func(s *types.ExtendedSubmission) { s.AssignedVerificationUserIDs = []int64{other} },
			message:    "you are not assigned to verify submission 42",
		},
		{
			name:       "cannot double Approve",
			action:     constants.ActionApprove,
			valid:      types.ExtendedSubmission{AssignedTestingUserIDs: []int64{actor}},
			invalidate: func(s *types.ExtendedSubmission) { s.ApprovedUserIDs = []int64{actor} },
			message:    "you have already approved submission 42",
		},
		{
			name:       "cannot double Verify",
			action:     constants.ActionVerify,
			valid:      types.ExtendedSubmission{AssignedVerificationUserIDs: []int64{actor}, ApprovedUserIDs: []int64{other}},
			invalidate: func(s *types.ExtendedSubmission) { s.VerifiedUserIDs = []int64{actor} },
			message:    "you have already verified submission 42",
		},
		{
			name:       "cannot mark added twice",
			action:     constants.ActionMarkAdded,
			valid:      types.ExtendedSubmission{VerifiedUserIDs: []int64{other}},
			invalidate: func(s *types.ExtendedSubmission) { s.DistinctActions = []string{constants.ActionMarkAdded} },
			message:    "submission 42 is alrady marked as added so it cannot be marked again",
		},
		{
			name:       "cannot assign testing while assigned verification",
			action:     constants.ActionAssignTesting,
			valid:      types.ExtendedSubmission{},
			invalidate: func(s *types.ExtendedSubmission) { s.AssignedVerificationUserIDs = []int64{actor} },
			message:    "you are already assigned to verify submission 42 so you cannot assign it for verification",
		},
		{
			name:       "cannot assign verification while assigned testing",
			action:     constants.ActionAssignVerification,
			valid:      types.ExtendedSubmission{ApprovedUserIDs: []int64{other}},
			invalidate: func(s *types.ExtendedSubmission) { s.AssignedTestingUserIDs = []int64{actor} },
			message:    "you are already assigned to test submission 42 so you cannot assign it for testing",
		},
		{
			name:       "AssignTesting disallows prior VerifiedUserIDs",
			action:     constants.ActionAssignTesting,
			valid:      types.ExtendedSubmission{},
			invalidate: func(s *types.ExtendedSubmission) { s.VerifiedUserIDs = []int64{actor} },
			message:    "you have already verified submission 42 so you cannot assign it for testing",
		},
		{
			name:       "Approve disallows prior VerifiedUserIDs",
			action:     constants.ActionApprove,
			valid:      types.ExtendedSubmission{AssignedTestingUserIDs: []int64{actor}},
			invalidate: func(s *types.ExtendedSubmission) { s.VerifiedUserIDs = []int64{actor} },
			message:    "you have already verified submission 42 so you cannot approve it",
		},
		{
			name:       "AssignVerification disallows prior ApprovedUserIDs",
			action:     constants.ActionAssignVerification,
			valid:      types.ExtendedSubmission{ApprovedUserIDs: []int64{other}},
			invalidate: func(s *types.ExtendedSubmission) { s.ApprovedUserIDs = []int64{actor} },
			message:    "you have already approved (tested) submission 42 so you cannot assign it for verification",
		},
		{
			name:       "Verify disallows prior ApprovedUserIDs",
			action:     constants.ActionVerify,
			valid:      types.ExtendedSubmission{AssignedVerificationUserIDs: []int64{actor}, ApprovedUserIDs: []int64{other}},
			invalidate: func(s *types.ExtendedSubmission) { s.ApprovedUserIDs = []int64{actor} },
			message:    "you have already approved (tested) submission 42 so you cannot verify it",
		},
		{
			name:       "AssignTesting disallows prior ApprovedUserIDs",
			action:     constants.ActionAssignTesting,
			valid:      types.ExtendedSubmission{},
			invalidate: func(s *types.ExtendedSubmission) { s.ApprovedUserIDs = []int64{actor} },
			message:    "you have already approved submission 42 so you cannot assign it for testing",
		},
		{
			name:       "AssignVerification disallows prior VerifiedUserIDs",
			action:     constants.ActionAssignVerification,
			valid:      types.ExtendedSubmission{ApprovedUserIDs: []int64{other}},
			invalidate: func(s *types.ExtendedSubmission) { s.VerifiedUserIDs = []int64{actor} },
			message:    "you have already verified submission 42 so you cannot assign it for verification",
		},
		{
			name:       "approve requires own testing assignment",
			action:     constants.ActionApprove,
			valid:      types.ExtendedSubmission{AssignedTestingUserIDs: []int64{actor}},
			invalidate: func(s *types.ExtendedSubmission) { s.AssignedTestingUserIDs = []int64{other} },
			message:    "you are not assigned to test submission 42 so you cannot approve it",
		},
		{
			name:       "verify requires own verification assignment",
			action:     constants.ActionVerify,
			valid:      types.ExtendedSubmission{AssignedVerificationUserIDs: []int64{actor}, ApprovedUserIDs: []int64{other}},
			invalidate: func(s *types.ExtendedSubmission) { s.AssignedVerificationUserIDs = []int64{other} },
			message:    "you are not assigned to verify submission 42 so you cannot verify it",
		},
		{
			name:       "AssignVerification requires approval",
			action:     constants.ActionAssignVerification,
			valid:      types.ExtendedSubmission{ApprovedUserIDs: []int64{other}},
			invalidate: func(s *types.ExtendedSubmission) { s.ApprovedUserIDs = nil },
			message:    "submission 42 is not approved (tested) so you cannot assign it for verification",
		},
		{
			name:       "Verify requires approval",
			action:     constants.ActionVerify,
			valid:      types.ExtendedSubmission{AssignedVerificationUserIDs: []int64{actor}, ApprovedUserIDs: []int64{other}},
			invalidate: func(s *types.ExtendedSubmission) { s.ApprovedUserIDs = nil },
			message:    "submission 42 is not approved (tested) so you cannot verify it",
		},
		{
			name:       "mark added requires verification",
			action:     constants.ActionMarkAdded,
			valid:      types.ExtendedSubmission{VerifiedUserIDs: []int64{other}},
			invalidate: func(s *types.ExtendedSubmission) { s.VerifiedUserIDs = nil },
			message:    "submission 42 is not verified so you cannot mark it as added",
		},
		{
			name:       "AssignTesting forbidden after mark added",
			action:     constants.ActionAssignTesting,
			valid:      types.ExtendedSubmission{},
			invalidate: func(s *types.ExtendedSubmission) { s.DistinctActions = []string{constants.ActionMarkAdded} },
			message:    "submission 42 is already marked as added so review actions are closed; please submit a bug report or a pending fix",
		},
		{
			name:       "RequestChanges forbidden after mark added",
			action:     constants.ActionRequestChanges,
			valid:      types.ExtendedSubmission{},
			invalidate: func(s *types.ExtendedSubmission) { s.DistinctActions = []string{constants.ActionMarkAdded} },
			message:    "submission 42 is already marked as added so review actions are closed; please submit a bug report or a pending fix",
		},
		{
			name:       "Reject forbidden after mark added",
			action:     constants.ActionReject,
			valid:      types.ExtendedSubmission{},
			invalidate: func(s *types.ExtendedSubmission) { s.DistinctActions = []string{constants.ActionMarkAdded} },
			message:    "submission 42 is already marked as added so review actions are closed; please submit a bug report or a pending fix",
		},
		{
			name:       "Reject forbidden after rejection",
			action:     constants.ActionReject,
			valid:      types.ExtendedSubmission{},
			invalidate: func(s *types.ExtendedSubmission) { s.DistinctActions = []string{constants.ActionReject} },
			message:    "submission 42 is alrady rejected so you cannot reject it",
		},
		{
			name:       "Upload forbidden after rejection",
			action:     constants.ActionUpload,
			valid:      types.ExtendedSubmission{},
			invalidate: func(s *types.ExtendedSubmission) { s.DistinctActions = []string{constants.ActionReject} },
			message:    "submission 42 is alrady rejected so you cannot upload a new version",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.valid.SubmissionID = 42
			tt.valid.LastUploaderID = other
			t.Run("valid prerequisite", func(t *testing.T) {
				assertActionValidation(t, actor, tt.action, &tt.valid, "")
			})
			t.Run("specific rule rejects", func(t *testing.T) {
				invalid := tt.valid
				tt.invalidate(&invalid)
				assertActionValidation(t, actor, tt.action, &invalid, tt.message)
			})
		})
	}
	t.Run("repeated request changes is allowed", func(t *testing.T) {
		assertActionValidation(t, actor, constants.ActionRequestChanges, &types.ExtendedSubmission{
			LastUploaderID: other, RequestedChangesUserIDs: []int64{actor},
		}, "")
	})
}

func TestActionValidator_UploaderVerificationGuard(t *testing.T) {
	submission := types.ExtendedSubmission{
		SubmissionID: 42, LastUploaderID: 1, ApprovedUserIDs: []int64{2},
	}
	t.Run("uploader cannot assign verification even when somebody else approved", func(t *testing.T) {
		assertActionValidation(t, 1, constants.ActionAssignVerification, &submission, "you are the uploader of the newest version of submission 42, so you cannot assign it")
	})
	t.Run("uploader cannot remove own verification assignment", func(t *testing.T) {
		submission.AssignedVerificationUserIDs = []int64{1}
		assertActionValidation(t, 1, constants.ActionUnassignVerification, &submission,
			"you are the uploader of the newest version of submission 42, so you cannot assign it")
	})
}

func TestActionValidator_MarkedAddedClosesReview(t *testing.T) {
	tests := []struct {
		action     string
		submission types.ExtendedSubmission
	}{
		{constants.ActionUnassignTesting, types.ExtendedSubmission{AssignedTestingUserIDs: []int64{1}}},
		{constants.ActionAssignVerification, types.ExtendedSubmission{ApprovedUserIDs: []int64{3}}},
		{constants.ActionUnassignVerification, types.ExtendedSubmission{AssignedVerificationUserIDs: []int64{1}}},
		{constants.ActionApprove, types.ExtendedSubmission{AssignedTestingUserIDs: []int64{1}}},
		{constants.ActionVerify, types.ExtendedSubmission{AssignedVerificationUserIDs: []int64{1}, ApprovedUserIDs: []int64{3}}},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			tt.submission.LastUploaderID = 2
			// Confirm the prerequisites independently before setting marked-added.
			assertActionValidation(t, 1, tt.action, &tt.submission, "")
			tt.submission.SubmissionID = 42
			tt.submission.DistinctActions = []string{constants.ActionMarkAdded}
			assertActionValidation(t, 1, tt.action, &tt.submission, "submission 42 is already marked as added so review actions are closed; please submit a bug report or a pending fix")
		})
	}
}

func TestActionValidator_MarkedAddedAllowsOrdinaryComments(t *testing.T) {
	assertActionValidation(t, 1, constants.ActionComment, &types.ExtendedSubmission{
		LastUploaderID: 1, DistinctActions: []string{constants.ActionMarkAdded},
	}, "")
}

func assertActionValidation(t *testing.T, uid int64, action string, submission *types.ExtendedSubmission, wantMessage string) {
	t.Helper()
	err := isActionValidForSubmission(uid, action, submission)
	if wantMessage == "" {
		if err != nil {
			t.Fatalf("action %q unexpectedly rejected: %v", action, err)
		}
		return
	}
	var publicError constants.PublicError
	if !errors.As(err, &publicError) {
		t.Fatalf("action %q error = %v, want PublicError(%q)", action, err, wantMessage)
	}
	if publicError.Status != http.StatusBadRequest || publicError.Msg != wantMessage {
		t.Fatalf("action %q error = %#v, want status %d and message %q", action, publicError, http.StatusBadRequest, wantMessage)
	}
}
