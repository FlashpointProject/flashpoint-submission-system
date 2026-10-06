package service

import (
	"bytes"
	"html/template"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/Masterminds/sprig"
	"github.com/stretchr/testify/require"
)

// Render the real partial with the same helper functions as RenderTemplates.
func TestCommentFormReviewControls(t *testing.T) {
	tmpl, err := template.New("comment-form").Funcs(sprig.FuncMap()).Funcs(template.FuncMap{
		"isDecider": constants.IsDecider, "isAdder": constants.IsAdder,
	}).ParseFiles("../templates/comment-form.gohtml")
	require.NoError(t, err)
	render := func(s types.ExtendedSubmission) string {
		t.Helper()
		var out bytes.Buffer
		err := tmpl.ExecuteTemplate(&out, "comment-form", struct {
			UserID      int64
			UserRoles   []string
			Submissions []*types.ExtendedSubmission
		}{1, []string{constants.RoleModerator}, []*types.ExtendedSubmission{&s}})
		require.NoError(t, err)
		return out.String()
	}
	for _, tt := range []struct {
		name    string
		s       types.ExtendedSubmission
		visible []string
	}{
		{"unassigned reviewer", types.ExtendedSubmission{LastUploaderID: 2, ApprovedUserIDs: []int64{3}}, []string{"assign-testing", "assign-verification", "reject"}},
		{"assigned tester", types.ExtendedSubmission{LastUploaderID: 2, AssignedTestingUserIDs: []int64{1}}, []string{"unassign-testing", "approve", "request-changes"}},
		{"assigned verifier", types.ExtendedSubmission{LastUploaderID: 2, ApprovedUserIDs: []int64{3}, AssignedVerificationUserIDs: []int64{1}}, []string{"unassign-verification", "verify", "request-changes"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := render(tt.s)
			for _, action := range tt.visible {
				require.Contains(t, before, "button-"+action)
			}
			tt.s.DistinctActions = []string{constants.ActionMarkAdded}
			after := render(tt.s)
			for _, action := range []string{"assign-testing", "unassign-testing", "assign-verification", "unassign-verification", "approve", "verify", "request-changes", "reject"} {
				require.NotContains(t, after, "button-"+action)
			}
			require.Contains(t, after, "button-comment")
		})
	}
	t.Run("uploader verification controls", func(t *testing.T) {
		s := types.ExtendedSubmission{LastUploaderID: 1, ApprovedUserIDs: []int64{3}}
		require.NotContains(t, render(s), "button-assign-verification")
		s.AssignedVerificationUserIDs = []int64{1}
		body := render(s)
		require.NotContains(t, body, "button-unassign-verification")
		require.NotContains(t, body, "button-verify")
		require.Contains(t, body, "button-comment")
	})
}
