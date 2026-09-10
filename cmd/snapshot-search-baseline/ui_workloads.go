package main

import (
	"database/sql"
	"fmt"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

// uiWorkloads maps the actual forms in templates/submission-filter.gohtml and
// static/js.js, rather than treating each independent status as a preset. IDs and
// names come from the snapshot and are persisted in workloads.json for replay.
func uiWorkloads(db *sql.DB) ([]workload, error) {
	var uid int64
	var username string
	if err := db.QueryRow(`SELECT u.id,u.username FROM submission s
 JOIN submission_cache c ON c.fk_submission_id=s.id
 JOIN submission_file f ON f.id=c.fk_oldest_file_id
 JOIN discord_user u ON u.id=f.fk_user_id
 WHERE s.deleted_at IS NULL GROUP BY u.id,u.username
 ORDER BY COUNT(*) DESC,u.id LIMIT 1`).Scan(&uid, &username); err != nil {
		return nil, fmt.Errorf("select UI uploader: %w", err)
	}

	// Use active members for the personal presets, including users who actually
	// match BOTH assignment and requested-changes predicates. Every active member
	// has a comment, so this avoids guessing that the first CSV member overlaps.
	members := make(map[string]int64)
	for _, family := range []struct{ name, column string }{
		{"testing", "active_assigned_testing_ids"},
		{"verification", "active_assigned_verification_ids"},
	} {
		for _, changes := range []bool{false, true} {
			name := family.name
			extra := ""
			if changes {
				name += "-changes"
				extra = " AND FIND_IN_SET(cm.fk_user_id,c.active_requested_changes_ids)>0"
			}
			q := `SELECT cm.fk_user_id FROM submission_cache c
 JOIN submission s ON s.id=c.fk_submission_id
 JOIN comment cm ON cm.fk_submission_id=c.fk_submission_id
 WHERE s.deleted_at IS NULL AND cm.deleted_at IS NULL
 AND FIND_IN_SET(cm.fk_user_id,c.` + family.column + `)>0` + extra + `
 ORDER BY c.fk_submission_id,cm.id LIMIT 1`
			var member int64
			if err := db.QueryRow(q).Scan(&member); err != nil {
				return nil, fmt.Errorf("select nonempty UI %s witness: %w", name, err)
			}
			members[name] = member
		}
	}
	return buildUIWorkloads(uid, username, members), nil
}

func buildUIWorkloads(uid int64, username string, members map[string]int64) []workload {
	w := []workload{
		{"ui-default", uid, types.SubmissionsFilter{}},
		{"ui-my-submissions", uid, types.SubmissionsFilter{SubmitterID: &uid}},
		{"ui-submitter-username", uid, types.SubmissionsFilter{SubmitterUsernamePartial: &username}},
		{"ui-platform-flash", uid, types.SubmissionsFilter{PlatformPartial: ptr("Flash")}},
		{"ui-platform-unity", uid, types.SubmissionsFilter{PlatformPartial: ptr("Unity")}},
		{"ui-platform-html5", uid, types.SubmissionsFilter{PlatformPartial: ptr("HTML5")}},
		{"ui-title-mario", uid, types.SubmissionsFilter{TitlePartial: ptr("Mario")}},
	}
	// changePage preserves every existing filter and changes only the page number.
	for _, first := range w {
		next := first
		next.Name += "-next"
		next.Filter.Page = ptr(int64(2))
		w = append(w, next)
	}
	for _, name := range []string{"ready-testing", "ready-verification", "ready-fp"} {
		f := types.SubmissionsFilter{
			BotActions: []string{"approve"}, RequestedChangedStatus: ptr("none"),
			DistinctActionsNot: []string{"mark-added", "reject"},
			OrderBy:            ptr("uploaded"), AscDesc: ptr("asc"),
		}
		switch name {
		case "ready-testing":
			f.ApprovalsStatus = ptr("none")
			f.VerificationStatus = ptr("none")
			f.AssignedStatusTesting = ptr("unassigned")
			f.AssignedStatusVerification = ptr("unassigned")
			f.LastUploaderNotMe = ptr("yes")
		case "ready-verification":
			f.ApprovalsStatus = ptr("approved")
			f.VerificationStatus = ptr("none")
			f.AssignedStatusVerification = ptr("unassigned")
			f.ApprovalsStatusMe = ptr("no")
			f.LastUploaderNotMe = ptr("yes")
		case "ready-fp":
			f.VerificationStatus = ptr("verified")
		}
		w = append(w, workload{"ui-preset-" + name, uid, f})
		next := f
		next.Page = ptr(int64(2))
		w = append(w, workload{"ui-preset-" + name + "-next", uid, next})
	}
	for _, name := range []string{"testing", "verification", "testing-changes", "verification-changes"} {
		f := types.SubmissionsFilter{}
		switch name {
		case "testing", "testing-changes":
			f.AssignedStatusTestingMe = ptr("assigned")
		case "verification", "verification-changes":
			f.AssignedStatusVerificationMe = ptr("assigned")
		}
		if name == "testing-changes" || name == "verification-changes" {
			f.RequestedChangedStatusMe = ptr("ongoing")
		}
		w = append(w, workload{"ui-preset-me-" + name, members[name], f})
	}
	return w
}
