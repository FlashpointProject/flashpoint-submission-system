package integration_tests

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

// Expected projections are written from fixture facts, never captured from SQL.
func blankSearchSubmission() *types.ExtendedSubmission {
	return &types.ExtendedSubmission{AssignedTestingUserIDs: []int64{}, AssignedVerificationUserIDs: []int64{}, RequestedChangesUserIDs: []int64{}, ApprovedUserIDs: []int64{}, VerifiedUserIDs: []int64{}, DistinctActions: []string{}}
}
func canonicalSearchSubmission(in *types.ExtendedSubmission) *types.ExtendedSubmission {
	out := *in
	out.AssignedTestingUserIDs = slices.Clone(in.AssignedTestingUserIDs)
	slices.Sort(out.AssignedTestingUserIDs)
	out.AssignedVerificationUserIDs = slices.Clone(in.AssignedVerificationUserIDs)
	slices.Sort(out.AssignedVerificationUserIDs)
	out.RequestedChangesUserIDs = slices.Clone(in.RequestedChangesUserIDs)
	slices.Sort(out.RequestedChangesUserIDs)
	out.ApprovedUserIDs = slices.Clone(in.ApprovedUserIDs)
	slices.Sort(out.ApprovedUserIDs)
	out.VerifiedUserIDs = slices.Clone(in.VerifiedUserIDs)
	slices.Sort(out.VerifiedUserIDs)
	out.DistinctActions = slices.Clone(in.DistinctActions)
	slices.Sort(out.DistinctActions)
	return &out
}
func seedSearchFixture(t *testing.T) (*sqlFixture, map[string]*types.ExtendedSubmission) {
	t.Helper()
	f := newSQLFixture(t)
	for id, name := range map[int64]string{1001: "Alice Alpha", 1002: "Bob Beta", 1003: "Casey Gamma", 1004: "Tester", 1005: "Verifier", 9999: "Unrelated"} {
		f.User(t, id, name)
	}
	for id, level := range map[int64]string{101: "trial", 102: "staff", 103: "audition"} {
		f.Submission(t, id, level)
	}
	a := fixtureMeta("Aurora Café")
	a.AlternateTitles = utils.StrPtr("Northern Light")
	a.Platform = utils.StrPtr("Flash;Arcade")
	a.Extreme = utils.StrPtr("Yes")
	a.GameExists = true
	b := fixtureMeta("Beta Game")
	b.Platform = utils.StrPtr("Unity")
	b.Library = utils.StrPtr("theatre")
	c := fixtureMeta("Comet")
	c.Platform = utils.StrPtr("Shockwave")
	f.File(t, fixtureFile{ID: 10001, SubmissionID: 101, UserID: 1001, At: fixtureEpoch, Size: 50, Original: "historic,first.7z", Current: "historic-current.7z", MD5: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Meta: fixtureMeta("Old title")})
	f.File(t, fixtureFile{ID: 10002, SubmissionID: 101, UserID: 1002, At: fixtureEpoch.Add(10 * time.Second), Size: 300, Meta: a})
	f.File(t, fixtureFile{ID: 20001, SubmissionID: 102, UserID: 1002, At: fixtureEpoch.Add(5 * time.Second), Size: 100, Meta: b})
	f.File(t, fixtureFile{ID: 30001, SubmissionID: 103, UserID: 1001, At: fixtureEpoch.Add(-5 * time.Second), Size: 200, Meta: c})
	comments := []struct {
		id, sid, uid int64
		action       string
		us           int64
	}{
		{1, 101, constants.ValidatorID, constants.ActionApprove, 11000000},
		{2, 101, 1004, constants.ActionAssignTesting, 11100000}, {3, 101, 1004, constants.ActionApprove, 11200000},
		{4, 101, 1005, constants.ActionAssignVerification, 11300000}, {5, 101, 1005, constants.ActionVerify, 11400000},
		{6, 101, 1003, constants.ActionComment, 12000000},
		{7, 102, constants.ValidatorID, constants.ActionRequestChanges, 6000000},
		{8, 102, 1004, constants.ActionRequestChanges, 7000000}, {9, 102, 1004, constants.ActionAssignTesting, 8000000},
		{10, 102, 1002, constants.ActionComment, 20000000},
		{11, 103, constants.ValidatorID, constants.ActionApprove, -4000000}, {12, 103, 1001, constants.ActionReject, 30000000},
	}
	for _, comment := range comments {
		f.Comment(t, comment.id, comment.sid, comment.uid, comment.action, fixtureEpoch.Add(time.Duration(comment.us)*time.Microsecond), nil)
	}
	_, err := f.Maria.Exec(testSQL(`UPDATE submission SET frozen_at=?, should_autofreeze=true WHERE id=101`), fixtureEpoch.Add(40*time.Second))
	require.NoError(t, err)
	// Multiple subscribers must not multiply result rows/counts.
	_, err = f.Maria.Exec(testSQL(`INSERT INTO submission_notification_subscription(fk_submission_id,fk_user_id,created_at) VALUES(101,1004,?),(101,1005,?),(102,1005,?)`), fixtureEpoch, fixtureEpoch, fixtureEpoch)
	require.NoError(t, err)
	f.Rebuild(t, 101, 102, 103)
	l1 := fixtureMeta("Aurora Legacy")
	l1.Extreme = utils.StrPtr("Yes")
	l2 := fixtureMeta("Beta Legacy")
	l2.Platform = utils.StrPtr("Unity")
	l2.Library = utils.StrPtr("theatre")
	f.Legacy(t, 1, l1, fixtureEpoch.Add(15*time.Second), fixtureEpoch.Add(25*time.Second))
	f.Legacy(t, 2, l2, fixtureEpoch.Add(25*time.Second), fixtureEpoch.Add(15*time.Second))
	want := map[string]*types.ExtendedSubmission{}
	for _, spec := range []struct {
		key, level, submitter, updater                               string
		sid, fid, submitID, updateID, lastID, size, created, updated int64
		meta                                                         *types.CurationMeta
		bot                                                          string
	}{
		{"A", "trial", "Alice Alpha", "Casey Gamma", 101, 10002, 1001, 1003, 1002, 300, 0, 12, a, constants.ActionApprove},
		{"B", "staff", "Bob Beta", "Bob Beta", 102, 20001, 1002, 1002, 1002, 100, 5, 20, b, constants.ActionRequestChanges},
		{"C", "audition", "Alice Alpha", "Alice Alpha", 103, 30001, 1001, 1001, 1001, 200, -5, 30, c, constants.ActionApprove},
	} {
		row := blankSearchSubmission()
		row.SubmissionID = spec.sid
		row.SubmissionLevel = spec.level
		row.FileID = spec.fid
		row.SubmitterID = spec.submitID
		row.SubmitterUsername = spec.submitter
		row.UpdaterID = spec.updateID
		row.UpdaterUsername = spec.updater
		row.LastUploaderID = spec.lastID
		row.Size = spec.size
		row.OriginalFilename = fmt.Sprintf("original-%d.7z", spec.fid)
		row.CurrentFilename = fmt.Sprintf("current-%d.7z", spec.fid)
		row.UploadedAt = fixtureEpoch.Add(time.Duration(spec.created) * time.Second)
		row.UpdatedAt = fixtureEpoch.Add(time.Duration(spec.updated) * time.Second)
		row.CurationTitle = spec.meta.Title
		row.CurationAlternateTitles = spec.meta.AlternateTitles
		row.CurationPlatform = spec.meta.Platform
		row.CurationLibrary = spec.meta.Library
		row.CurationExtreme = spec.meta.Extreme
		row.CurationLaunchCommand = spec.meta.LaunchCommand
		row.GameExists = spec.meta.GameExists
		row.BotAction = spec.bot
		row.FileCount = 1
		want[spec.key] = row
	}
	want["A"].FileCount = 2
	want["A"].IsFrozen = true
	want["A"].ShouldAutofreeze = true
	want["A"].AssignedTestingUserIDs = []int64{1004}
	want["A"].AssignedVerificationUserIDs = []int64{1005}
	want["A"].ApprovedUserIDs = []int64{1004}
	want["A"].VerifiedUserIDs = []int64{1005}
	want["A"].DistinctActions = []string{constants.ActionApprove, constants.ActionAssignTesting, constants.ActionAssignVerification, constants.ActionVerify, constants.ActionComment}
	want["B"].AssignedTestingUserIDs = []int64{1004}
	want["B"].RequestedChangesUserIDs = []int64{1004}
	want["B"].DistinctActions = []string{constants.ActionRequestChanges, constants.ActionAssignTesting, constants.ActionComment}
	want["C"].DistinctActions = []string{constants.ActionReject}
	for _, spec := range []struct {
		key              string
		meta             *types.CurationMeta
		created, updated int64
	}{{"L1", l1, 15, 25}, {"L2", l2, 25, 15}} {
		row := blankSearchSubmission()
		row.SubmissionID = -1
		row.SubmissionLevel = "legacy"
		row.GameUUID = utils.StrPtr(fmt.Sprintf("00000000-0000-0000-0000-%012d", map[string]int{"L1": 1, "L2": 2}[spec.key]))
		row.SubmitterID = -1
		row.SubmitterUsername = "legacy"
		row.SubmitterAvatarURL = utils.FormatAvatarURL(-1, "legacy")
		row.UpdaterID = -1
		row.UpdaterUsername = "legacy"
		row.UpdaterAvatarURL = utils.FormatAvatarURL(-1, "legacy")
		row.FileID = -1
		row.OriginalFilename = "legacy"
		row.CurrentFilename = "legacy"
		row.Size = 42
		row.LastUploaderID = -1
		row.UploadedAt = fixtureEpoch.Add(time.Duration(spec.created) * time.Second)
		row.UpdatedAt = fixtureEpoch.Add(time.Duration(spec.updated) * time.Second)
		row.CurationTitle = spec.meta.Title
		row.CurationAlternateTitles = spec.meta.AlternateTitles
		row.CurationPlatform = spec.meta.Platform
		row.CurationLaunchCommand = spec.meta.LaunchCommand
		row.CurationLibrary = spec.meta.Library
		row.CurationExtreme = spec.meta.Extreme
		row.BotAction = "legacy"
		row.DistinctActions = []string{constants.ActionMarkAdded}
		row.GameExists = true
		want[spec.key] = row
	}
	return f, want
}

func checkSearch(t *testing.T, f *sqlFixture, want map[string]*types.ExtendedSubmission, uid int64, filter *types.SubmissionsFilter, keys []string, total int64) {
	t.Helper()
	rows, count := f.Search(t, uid, filter)
	require.Equal(t, total, count, "unpaginated count")
	require.Len(t, rows, len(keys))
	for i, key := range keys {
		require.Equal(t, canonicalSearchSubmission(want[key]), canonicalSearchSubmission(rows[i]), "result %d (%s)", i, key)
	}
}

func TestSubmissionSearchFilters(t *testing.T) {
	f, want := seedSearchFixture(t)
	cases := []struct {
		name   string
		filter *types.SubmissionsFilter
		keys   []string
	}{
		{"nil defaults", nil, []string{"C", "L1", "B", "L2", "A"}},
		{"empty defaults", &types.SubmissionsFilter{}, []string{"C", "L1", "B", "L2", "A"}},
		{"exclude legacy", &types.SubmissionsFilter{ExcludeLegacy: true}, []string{"C", "B", "A"}},
		{"submission IDs", &types.SubmissionsFilter{SubmissionIDs: []int64{101, 103}}, []string{"C", "A"}},
		{"missing ID", &types.SubmissionsFilter{SubmissionIDs: []int64{999}}, nil},
		{"submitter is oldest uploader", &types.SubmissionsFilter{SubmitterID: utils.Int64Ptr(1001)}, []string{"C", "A"}},
		{"updated by newest comment author", &types.SubmissionsFilter{UpdatedByID: utils.Int64Ptr(1003)}, []string{"A"}},
		{"title both branches", &types.SubmissionsFilter{TitlePartial: utils.StrPtr("Aurora")}, []string{"L1", "A"}},
		{"alternate title", &types.SubmissionsFilter{TitlePartial: utils.StrPtr("Northern")}, []string{"A"}},
		{"old metadata not searched", &types.SubmissionsFilter{TitlePartial: utils.StrPtr("Old title")}, nil},
		{"no text match", &types.SubmissionsFilter{TitlePartial: utils.StrPtr("No such title")}, nil},
		{"username inclusion exclusion", &types.SubmissionsFilter{SubmitterUsernamePartial: utils.StrPtr("Alice,Bob,!Bob")}, []string{"C", "A"}},
		{"platform OR", &types.SubmissionsFilter{PlatformPartial: utils.StrPtr("Flash,Unity")}, []string{"L1", "B", "L2", "A"}},
		{"platform exclusions AND", &types.SubmissionsFilter{PlatformPartial: utils.StrPtr("!Unity,!Shockwave")}, []string{"L1", "A"}},
		{"platform mixed", &types.SubmissionsFilter{PlatformPartial: utils.StrPtr("Flash,Unity,!Arcade")}, []string{"L1", "B", "L2"}},
		{"library", &types.SubmissionsFilter{LibraryPartial: utils.StrPtr("theatre")}, []string{"B", "L2"}},
		{"original historical filename", &types.SubmissionsFilter{OriginalFilenamePartialAny: utils.StrPtr("historic,first")}, []string{"A"}},
		{"current historical filename", &types.SubmissionsFilter{CurrentFilenamePartialAny: utils.StrPtr("historic-current")}, []string{"A"}},
		{"historical MD5", &types.SubmissionsFilter{MD5SumPartialAny: utils.StrPtr("aaaa")}, []string{"A"}},
		{"historical SHA256", &types.SubmissionsFilter{SHA256SumPartialAny: utils.StrPtr("aaaa")}, []string{"A"}},
		{"bot actions", &types.SubmissionsFilter{BotActions: []string{constants.ActionApprove}}, []string{"C", "A"}},
		{"multiple bot actions", &types.SubmissionsFilter{BotActions: []string{constants.ActionApprove, constants.ActionRequestChanges}}, []string{"C", "B", "A"}},
		{"levels", &types.SubmissionsFilter{SubmissionLevels: []string{"trial", "audition"}}, []string{"C", "A"}},
		{"extreme yes", &types.SubmissionsFilter{IsExtreme: utils.StrPtr("Yes")}, []string{"L1", "A"}},
		{"extreme no", &types.SubmissionsFilter{IsExtreme: utils.StrPtr("No")}, []string{"C", "B", "L2"}},
		{"actions any", &types.SubmissionsFilter{DistinctActions: []string{constants.ActionVerify, constants.ActionReject}}, []string{"C", "A"}},
		{"actions excluded", &types.SubmissionsFilter{DistinctActionsNot: []string{constants.ActionReject, constants.ActionVerify}}, []string{"B"}},
		{"include exclude actions", &types.SubmissionsFilter{DistinctActions: []string{constants.ActionAssignTesting}, DistinctActionsNot: []string{constants.ActionVerify}}, []string{"B"}},
		{"launch command", &types.SubmissionsFilter{LaunchCommandFuzzy: utils.StrPtr("game.swf")}, []string{"C", "L1", "B", "L2", "A"}},
		{"subscribed me", &types.SubmissionsFilter{SubscribedMe: utils.StrPtr("yes")}, []string{"A"}},
		{"content change", &types.SubmissionsFilter{ExcludeLegacy: true, IsContentChange: utils.StrPtr("yes")}, []string{"A"}},
		{"new content", &types.SubmissionsFilter{ExcludeLegacy: true, IsContentChange: utils.StrPtr("no")}, []string{"C", "B"}},
		{"frozen", &types.SubmissionsFilter{IsFrozen: utils.StrPtr("yes")}, []string{"A"}},
		{"not frozen", &types.SubmissionsFilter{IsFrozen: utils.StrPtr("no")}, []string{"C", "B"}},
		{"last uploader different when first and last agree", &types.SubmissionsFilter{SubmissionIDs: []int64{102, 103}, LastUploaderNotMe: utils.StrPtr("yes")}, []string{"C", "B"}},
		{"combined predicates", &types.SubmissionsFilter{TitlePartial: utils.StrPtr("Aurora"), PlatformPartial: utils.StrPtr("Flash,!Unity"), ApprovalsStatus: utils.StrPtr("approved"), IsFrozen: utils.StrPtr("yes"), SubscribedMe: utils.StrPtr("yes")}, []string{"A"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { checkSearch(t, f, want, 1004, tc.filter, tc.keys, int64(len(tc.keys))) })
	}
}

func TestSubmissionSearchStateAndUserFilters(t *testing.T) {
	f, want := seedSearchFixture(t)
	cases := []struct {
		name, on, off      string
		uid                int64
		global, me, user   func(*types.SubmissionsFilter, *string)
		positive, negative []string
	}{
		{"testing", "assigned", "unassigned", 1004, func(f *types.SubmissionsFilter, v *string) { f.AssignedStatusTesting = v }, func(f *types.SubmissionsFilter, v *string) { f.AssignedStatusTestingMe = v }, func(f *types.SubmissionsFilter, v *string) { f.AssignedStatusTestingUser = v }, []string{"B", "A"}, []string{"C"}},
		{"verification assignment", "assigned", "unassigned", 1005, func(f *types.SubmissionsFilter, v *string) { f.AssignedStatusVerification = v }, func(f *types.SubmissionsFilter, v *string) { f.AssignedStatusVerificationMe = v }, func(f *types.SubmissionsFilter, v *string) { f.AssignedStatusVerificationUser = v }, []string{"A"}, []string{"C", "B"}},
		{"requested changes", "ongoing", "none", 1004, func(f *types.SubmissionsFilter, v *string) { f.RequestedChangedStatus = v }, func(f *types.SubmissionsFilter, v *string) { f.RequestedChangedStatusMe = v }, func(f *types.SubmissionsFilter, v *string) { f.RequestedChangedStatusUser = v }, []string{"B"}, []string{"C", "A"}},
		{"approvals", "approved", "none", 1004, func(f *types.SubmissionsFilter, v *string) { f.ApprovalsStatus = v }, func(f *types.SubmissionsFilter, v *string) { f.ApprovalsStatusMe = v }, func(f *types.SubmissionsFilter, v *string) { f.ApprovalsStatusUser = v }, []string{"A"}, []string{"C", "B"}},
		{"verified", "verified", "none", 1005, func(f *types.SubmissionsFilter, v *string) { f.VerificationStatus = v }, func(f *types.SubmissionsFilter, v *string) { f.VerificationStatusMe = v }, func(f *types.SubmissionsFilter, v *string) { f.VerificationStatusUser = v }, []string{"A"}, []string{"C", "B"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, on := range []bool{true, false} {
				label := tc.off
				keys := tc.negative
				if on {
					label = tc.on
					keys = tc.positive
				}
				global := &types.SubmissionsFilter{}
				tc.global(global, utils.StrPtr(label))
				checkSearch(t, f, want, 9999, global, keys, int64(len(keys)))
				if tc.name == "approvals" || tc.name == "verified" {
					label = "no"
					if on {
						label = "yes"
					}
				}
				me := &types.SubmissionsFilter{}
				tc.me(me, utils.StrPtr(label))
				checkSearch(t, f, want, tc.uid, me, keys, int64(len(keys)))
				user := &types.SubmissionsFilter{AssignedStatusUserID: &tc.uid}
				tc.user(user, utils.StrPtr(label))
				checkSearch(t, f, want, 9999, user, keys, int64(len(keys)))
				// A missing actor must not inherit another user's positive state.
				absent := &types.SubmissionsFilter{}
				tc.me(absent, utils.StrPtr(label))
				absentKeys := []string{"C", "B", "A"}
				if on {
					absentKeys = nil
				}
				checkSearch(t, f, want, 9999, absent, absentKeys, int64(len(absentKeys)))
			}
		})
	}
}

func TestSubmissionSearchOrderingAndPagination(t *testing.T) {
	f, want := seedSearchFixture(t)
	for _, tc := range []struct {
		order string
		asc   []string
	}{{"uploaded", []string{"C", "A", "B"}}, {"updated", []string{"A", "B", "C"}}, {"size", []string{"B", "C", "A"}}} {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(tc.order+direction, func(t *testing.T) {
				keys := slices.Clone(tc.asc)
				if direction == "desc" {
					slices.Reverse(keys)
				}
				checkSearch(t, f, want, 1004, &types.SubmissionsFilter{ExcludeLegacy: true, OrderBy: &tc.order, AscDesc: &direction}, keys, 3)
			})
		}
	}
	for _, tc := range []struct {
		page int64
		keys []string
	}{{1, []string{"C", "L1"}}, {2, []string{"B", "L2"}}, {3, []string{"A"}}, {4, nil}} {
		t.Run(fmt.Sprintf("page%d", tc.page), func(t *testing.T) {
			checkSearch(t, f, want, 1004, &types.SubmissionsFilter{ResultsPerPage: utils.Int64Ptr(2), Page: &tc.page}, tc.keys, 5)
		})
	}
	checkSearch(t, f, want, 1004, &types.SubmissionsFilter{TitlePartial: utils.StrPtr("missing"), ResultsPerPage: utils.Int64Ptr(2), Page: utils.Int64Ptr(2)}, nil, 0)
}

func TestSubmissionSearchDefaultLimit(t *testing.T) {
	f := newSQLFixture(t)
	for i := int64(1); i <= 101; i++ {
		f.Legacy(t, i, fixtureMeta(fmt.Sprintf("Legacy %03d", i)), fixtureEpoch, fixtureEpoch.Add(time.Duration(i)*time.Microsecond))
	}
	for _, filter := range []*types.SubmissionsFilter{nil, {}} {
		rows, count := f.Search(t, 1, filter)
		require.EqualValues(t, 101, count)
		require.Len(t, rows, 100)
		for i, row := range rows {
			require.Equal(t, fmt.Sprintf("Legacy %03d", 101-i), *row.CurationTitle)
		}
	}
	rows, count := f.Search(t, 1, &types.SubmissionsFilter{Page: utils.Int64Ptr(2)})
	require.EqualValues(t, 101, count)
	require.Len(t, rows, 1)
	require.Equal(t, "Legacy 001", *rows[0].CurationTitle)
}

func TestSubmissionSearchExactUserMembership(t *testing.T) {
	// D-SEARCH-01: numeric ID substrings must never count as membership.
	f := newSQLFixture(t)
	for _, id := range []int64{12, 112, 120, 999} {
		f.User(t, id, fmt.Sprint(id))
	}
	f.Submission(t, 1, "trial")
	f.File(t, fixtureFile{ID: 1, SubmissionID: 1, UserID: 999, At: fixtureEpoch})
	f.Comment(t, 1, 1, constants.ValidatorID, constants.ActionApprove, fixtureEpoch.Add(time.Microsecond), nil)
	for i, action := range []string{constants.ActionAssignTesting, constants.ActionAssignVerification, constants.ActionApprove, constants.ActionVerify} {
		f.Comment(t, int64(i+2), 1, 112, action, fixtureEpoch.Add(time.Duration(i+2)*time.Microsecond), nil)
	}
	// Separate actor requesting changes avoids disabling 112's approvals.
	f.Comment(t, 9, 1, 120, constants.ActionRequestChanges, fixtureEpoch.Add(9*time.Microsecond), nil)
	f.Rebuild(t, 1)
	rows, _ := f.Search(t, 12, &types.SubmissionsFilter{SubmissionIDs: []int64{1}})
	require.NotContains(t, rows[0].AssignedTestingUserIDs, int64(12))
	require.NotContains(t, rows[0].RequestedChangesUserIDs, int64(12))
	for _, tc := range []struct {
		name, on, off string
		me, user      func(*types.SubmissionsFilter, *string)
	}{
		{"testing", "assigned", "unassigned", func(f *types.SubmissionsFilter, v *string) { f.AssignedStatusTestingMe = v }, func(f *types.SubmissionsFilter, v *string) { f.AssignedStatusTestingUser = v }},
		{"verification assignment", "assigned", "unassigned", func(f *types.SubmissionsFilter, v *string) { f.AssignedStatusVerificationMe = v }, func(f *types.SubmissionsFilter, v *string) { f.AssignedStatusVerificationUser = v }},
		{"approval", "yes", "no", func(f *types.SubmissionsFilter, v *string) { f.ApprovalsStatusMe = v }, func(f *types.SubmissionsFilter, v *string) { f.ApprovalsStatusUser = v }},
		{"verification", "yes", "no", func(f *types.SubmissionsFilter, v *string) { f.VerificationStatusMe = v }, func(f *types.SubmissionsFilter, v *string) { f.VerificationStatusUser = v }},
		{"requests", "ongoing", "none", func(f *types.SubmissionsFilter, v *string) { f.RequestedChangedStatusMe = v }, func(f *types.SubmissionsFilter, v *string) { f.RequestedChangedStatusUser = v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, explicit := range []bool{false, true} {
				for _, positive := range []bool{false, true} {
					value, expected := tc.off, 1
					if positive {
						value, expected = tc.on, 0
					}
					filter := &types.SubmissionsFilter{}
					caller := int64(12)
					if explicit {
						filter.AssignedStatusUserID = utils.Int64Ptr(12)
						tc.user(filter, &value)
						caller = 999
					} else {
						tc.me(filter, &value)
					}
					rows, count := f.Search(t, caller, filter)
					require.Len(t, rows, expected, "D-SEARCH-01: explicit=%v positive=%v", explicit, positive)
					require.EqualValues(t, expected, count)
				}
			}
		})
	}
}

func TestSubmissionSearchFilterRegressions(t *testing.T) {
	f, want := seedSearchFixture(t)
	t.Run("last uploader is newest file author", func(t *testing.T) {
		checkSearch(t, f, want, 1001, &types.SubmissionsFilter{SubmissionIDs: []int64{101}, LastUploaderNotMe: utils.StrPtr("yes")}, []string{"A"}, 1)
		checkSearch(t, f, want, 1002, &types.SubmissionsFilter{SubmissionIDs: []int64{101}, LastUploaderNotMe: utils.StrPtr("yes")}, nil, 0)
	})
	t.Run("content filter excludes legacy", func(t *testing.T) {
		checkSearch(t, f, want, 1004, &types.SubmissionsFilter{IsContentChange: utils.StrPtr("no")}, []string{"C", "B"}, 2)
		checkSearch(t, f, want, 1004, &types.SubmissionsFilter{IsContentChange: utils.StrPtr("yes")}, []string{"A"}, 1)
	})
	t.Run("subscription membership and counts", func(t *testing.T) {
		for _, tc := range []struct {
			uid     int64
			yes, no []string
		}{
			{1004, []string{"A"}, []string{"C", "B"}},
			{1005, []string{"B", "A"}, []string{"C"}},
			{9999, nil, []string{"C", "B", "A"}},
		} {
			checkSearch(t, f, want, tc.uid, &types.SubmissionsFilter{SubscribedMe: utils.StrPtr("yes")}, tc.yes, int64(len(tc.yes)))
			checkSearch(t, f, want, tc.uid, &types.SubmissionsFilter{SubscribedMe: utils.StrPtr("no")}, tc.no, int64(len(tc.no)))
		}
	})
	t.Run("missing explicit user returns validation error", func(t *testing.T) {
		filter := &types.SubmissionsFilter{AssignedStatusTestingUser: utils.StrPtr("assigned")}
		require.ErrorContains(t, filter.Validate(), "assigned-status-user-id must be set")
		session, err := f.DB.NewSession(f.Ctx)
		require.NoError(t, err)
		defer session.Rollback()
		_, _, err = f.DB.SearchSubmissions(session, filter)
		require.ErrorContains(t, err, "assigned-status-user-id must be set")
	})
	t.Run("ID alone is ignored", func(t *testing.T) {
		checkSearch(t, f, want, 1004, &types.SubmissionsFilter{AssignedStatusUserID: utils.Int64Ptr(12)}, []string{"C", "L1", "B", "L2", "A"}, 5)
	})
}
