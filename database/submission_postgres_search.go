package database

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
)

// pgSubmissionSearch builds numbered parameters directly. The legacy branch is
// the imported masterdb snapshot, never the existing PostgreSQL game tables.
type pgSubmissionSearch struct {
	args             []interface{}
	filters, legacy  []string
	limit, offset    int64
	order, direction string
}

func (q *pgSubmissionSearch) bind(v interface{}) string {
	q.args = append(q.args, v)
	return "$" + strconv.Itoa(len(q.args))
}
func (q *pgSubmissionSearch) onlySubmission() { q.legacy = append(q.legacy, "FALSE") }
func (q *pgSubmissionSearch) like(column, pattern string) string {
	return "submission_like(" + column + ", " + q.bind(utils.FormatLike(pattern)) + ")"
}
func (q *pgSubmissionSearch) multi(column, legacyColumn, value string) {
	var includes, excludes, legacyIncludes, legacyExcludes []string
	for _, value := range strings.Split(value, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		negative := strings.HasPrefix(value, "!")
		if negative {
			value = value[1:]
		}
		predicate := q.like(column, value)
		var legacy string
		if legacyColumn != "" {
			legacy = q.like(legacyColumn, value)
		}
		if negative {
			excludes = append(excludes, "NOT "+predicate)
			if legacy != "" {
				legacyExcludes = append(legacyExcludes, "NOT "+legacy)
			}
		} else {
			includes = append(includes, predicate)
			if legacy != "" {
				legacyIncludes = append(legacyIncludes, legacy)
			}
		}
	}
	for _, group := range []struct {
		values []string
		join   string
		target *[]string
	}{
		{includes, " OR ", &q.filters}, {excludes, " AND ", &q.filters},
		{legacyIncludes, " OR ", &q.legacy}, {legacyExcludes, " AND ", &q.legacy},
	} {
		if len(group.values) > 0 {
			*group.target = append(*group.target, "("+strings.Join(group.values, group.join)+")")
		}
	}
}
func (q *pgSubmissionSearch) membership(column string, status *string, positive string, uid int64) {
	if status == nil {
		return
	}
	// A positive membership predicate rejects NULL cache rows, so PostgreSQL
	// can apply it before the outer joins. Wrapping it in COALESCE prevents
	// that pushdown and can make a one-result search scan the full history.
	predicate := "string_to_array(submission_cache." + column + ", ',') @> ARRAY[" + q.bind(strconv.FormatInt(uid, 10)) + "]::text[]"
	if *status != positive {
		// Missing/NULL collections still mean that this user is not a member.
		predicate = "NOT COALESCE(" + predicate + ", FALSE)"
	} else {
		// string_to_array is not declared STRICT (its optional arguments can
		// be NULL). Make rejection of a missing cache explicit for the planner.
		predicate = "(submission_cache." + column + " IS NOT NULL AND " + predicate + ")"
	}
	q.filters = append(q.filters, predicate)
	q.onlySubmission()
}

func newPGSubmissionSearch(filter *types.SubmissionsFilter, uid int64) (*pgSubmissionSearch, error) {
	q := &pgSubmissionSearch{limit: 100, order: "updated_at", direction: "DESC"}
	if filter == nil {
		return q, nil
	}
	f := *filter
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if pgReadyForFlashpointEligible(&f) {
		// The existing filters imply this constant predicate. Keeping it visible
		// lets generic prepared plans use the small partial index and its stats.
		q.filters = append(q.filters, pgReadyForFlashpointPredicate)
	}
	if len(f.SubmissionIDs) > 0 {
		var values []string
		for _, id := range f.SubmissionIDs {
			values = append(values, q.bind(id))
		}
		q.filters = append(q.filters, "submission.id IN ("+strings.Join(values, ",")+")")
		q.onlySubmission()
	}
	for _, field := range []struct {
		column string
		value  *int64
	}{{"uploader.id", f.SubmitterID}, {"updater.id", f.UpdatedByID}} {
		if field.value != nil {
			q.filters = append(q.filters, field.column+" = "+q.bind(*field.value))
			q.onlySubmission()
		}
	}
	if f.TitlePartial != nil {
		q.filters = append(q.filters, "("+q.like("meta.title", *f.TitlePartial)+" OR "+q.like("meta.alternate_titles", *f.TitlePartial)+")")
		q.legacy = append(q.legacy, "("+q.like("title", *f.TitlePartial)+" OR "+q.like("alternate_titles", *f.TitlePartial)+")")
	}
	if f.SubmitterUsernamePartial != nil {
		q.multi("uploader.username", "", *f.SubmitterUsernamePartial)
		q.onlySubmission()
	}
	if f.PlatformPartial != nil {
		q.multi("meta.platform", "platform", *f.PlatformPartial)
	}
	for _, field := range []struct {
		column, legacy string
		value          *string
	}{
		{"meta.library", "library", f.LibraryPartial}, {"meta.launch_command", "launch_command", f.LaunchCommandFuzzy},
		{"submission_cache.original_filename_sequence", "", f.OriginalFilenamePartialAny},
		{"submission_cache.current_filename_sequence", "", f.CurrentFilenamePartialAny},
		{"submission_cache.md5sum_sequence", "", f.MD5SumPartialAny}, {"submission_cache.sha256sum_sequence", "", f.SHA256SumPartialAny},
	} {
		if field.value != nil {
			q.filters = append(q.filters, q.like(field.column, *field.value))
			if field.legacy != "" {
				q.legacy = append(q.legacy, q.like(field.legacy, *field.value))
			} else {
				q.onlySubmission()
			}
		}
	}
	for _, field := range []struct {
		column string
		values []string
	}{
		{"submission_cache.bot_action", f.BotActions}, {"(SELECT name FROM submission_level WHERE id = submission.fk_submission_level_id)", f.SubmissionLevels},
	} {
		if len(field.values) > 0 {
			var values []string
			for _, value := range field.values {
				values = append(values, q.bind(value))
			}
			q.filters = append(q.filters, field.column+" IN ("+strings.Join(values, ",")+")")
			q.onlySubmission()
		}
	}
	for _, field := range []struct {
		column           string
		status           *string
		positive         string
		me, user         *string
		personalPositive string
	}{
		{"active_assigned_testing_ids", f.AssignedStatusTesting, "assigned", f.AssignedStatusTestingMe, f.AssignedStatusTestingUser, "assigned"},
		{"active_assigned_verification_ids", f.AssignedStatusVerification, "assigned", f.AssignedStatusVerificationMe, f.AssignedStatusVerificationUser, "assigned"},
		{"active_requested_changes_ids", f.RequestedChangedStatus, "ongoing", f.RequestedChangedStatusMe, f.RequestedChangedStatusUser, "ongoing"},
		{"active_approved_ids", f.ApprovalsStatus, "approved", f.ApprovalsStatusMe, f.ApprovalsStatusUser, "yes"},
		{"active_verified_ids", f.VerificationStatus, "verified", f.VerificationStatusMe, f.VerificationStatusUser, "yes"},
	} {
		if field.status != nil {
			predicate := "submission_cache." + field.column + " IS NULL"
			if *field.status == field.positive {
				predicate = "submission_cache." + field.column + " IS NOT NULL"
			}
			q.filters = append(q.filters, predicate)
			q.onlySubmission()
		}
		q.membership(field.column, field.me, field.personalPositive, uid)
		if field.user != nil {
			q.membership(field.column, field.user, field.personalPositive, *f.AssignedStatusUserID)
		}
	}
	if f.IsExtreme != nil {
		q.filters = append(q.filters, "lower(meta.extreme) = lower("+q.bind(*f.IsExtreme)+")")
		q.legacy = append(q.legacy, "lower(extreme) = lower("+q.bind(*f.IsExtreme)+")")
	}
	// Action names are validated fixed strings. Preserve the source's substring
	// regex matching, including NULL semantics, until its contract is changed.
	for _, field := range []struct {
		values   []string
		operator string
	}{{f.DistinctActions, "~*"}, {f.DistinctActionsNot, "!~*"}} {
		if len(field.values) > 0 {
			q.filters = append(q.filters, "submission_cache.distinct_actions "+field.operator+" "+q.bind(strings.Join(field.values, "|")))
			q.onlySubmission()
		}
	}
	if f.LastUploaderNotMe != nil {
		if *f.LastUploaderNotMe == "yes" {
			q.filters = append(q.filters, "newest_file.fk_user_id <> "+q.bind(uid))
		}
		q.onlySubmission()
	}
	if f.SubscribedMe != nil {
		predicate := "EXISTS (SELECT 1 FROM submission_notification_subscription sns WHERE sns.fk_submission_id = submission.id AND sns.fk_user_id = " + q.bind(uid) + ")"
		if *f.SubscribedMe != "yes" {
			predicate = "NOT " + predicate
		}
		q.filters = append(q.filters, predicate)
		q.onlySubmission()
	}
	if f.IsContentChange != nil {
		value := "FALSE"
		if *f.IsContentChange == "yes" {
			value = "TRUE"
		}
		q.filters = append(q.filters, "meta.game_exists = "+value)
		q.onlySubmission()
	}
	if f.IsFrozen != nil {
		predicate := "submission.frozen_at IS NULL"
		if *f.IsFrozen == "yes" {
			predicate = "submission.frozen_at IS NOT NULL"
		}
		q.filters = append(q.filters, predicate)
		q.onlySubmission()
	}
	if f.ExcludeLegacy {
		q.onlySubmission()
	}
	if f.ResultsPerPage != nil {
		q.limit = *f.ResultsPerPage
	}
	if f.Page != nil {
		q.offset = (*f.Page - 1) * q.limit
	}
	if f.OrderBy != nil {
		q.order = map[string]string{"uploaded": "created_at", "updated": "updated_at", "size": "newest_file_size"}[*f.OrderBy]
	}
	if f.AscDesc != nil && *f.AscDesc == "asc" {
		q.direction = "ASC"
	}
	return q, nil
}

const pgSubmissionSearchJoins = `
 LEFT JOIN submission_cache ON submission_cache.fk_submission_id = submission.id
 LEFT JOIN submission_file oldest_file ON oldest_file.id = submission_cache.fk_oldest_file_id
 LEFT JOIN submission_file newest_file ON newest_file.id = submission_cache.fk_newest_file_id
 LEFT JOIN comment newest_comment ON newest_comment.id = submission_cache.fk_newest_comment_id
 LEFT JOIN discord_user uploader ON uploader.id = oldest_file.fk_user_id
 LEFT JOIN discord_user updater ON updater.id = newest_comment.fk_user_id
 LEFT JOIN curation_meta meta ON meta.fk_submission_file_id = submission_cache.fk_newest_file_id`

const pgSubmissionSearchFrom = ` FROM submission` + pgSubmissionSearchJoins + ` WHERE submission.deleted_at IS NULL`

// Count and rows share one statement snapshot and reuse filtered matches.
// Display-only fields and file counts are hydrated after pagination.
func (d *postgresSubmissionDAL) SearchSubmissions(dbs DBSession, filter *types.SubmissionsFilter) ([]*types.ExtendedSubmission, int64, error) {
	q, err := newPGSubmissionSearch(filter, utils.UserID(dbs.Ctx()))
	if err != nil {
		return nil, 0, err
	}
	submissionFrom := pgSubmissionSearchFrom
	if len(q.filters) > 0 {
		submissionFrom += " AND " + strings.Join(q.filters, " AND ")
	}
	legacyFrom := " FROM masterdb_game WHERE TRUE"
	if len(q.legacy) > 0 {
		legacyFrom += " AND " + strings.Join(q.legacy, " AND ")
	}
	var counter int64
	// All branches share a single statement snapshot, including empty pages.
	// First choose narrow identities and sort keys. Hydration must happen after
	// LIMIT: selecting the full projection here executes file-count subqueries
	// and joins display metadata for every matching submission before sorting.
	nulls := "NULLS LAST"
	if q.direction == "ASC" {
		nulls = "NULLS FIRST"
	}
	submissionSort := map[string]string{"created_at": "oldest_file.created_at", "updated_at": "newest_comment.created_at", "newest_file_size": "newest_file.size"}[q.order]
	legacySort := map[string]string{"created_at": "date_added", "updated_at": "date_modified", "newest_file_size": "42::bigint"}[q.order]
	// Evaluate filters and sort keys once. Both the page and total consume
	// this narrow materialized relation; expensive text/history predicates
	// must not be repeated by a separate count branch.
	matches := "SELECT submission.id AS submission_id, NULL::bigint AS legacy_id, NULL::text AS game_uuid, " + submissionSort + " AS sort_value" + submissionFrom +
		" UNION ALL SELECT -1::bigint AS submission_id, masterdb_game.id AS legacy_id, uuid AS game_uuid, " + legacySort + " AS sort_value" + legacyFrom
	limitParam, offsetParam := q.bind(q.limit), q.bind(q.offset)
	ordering := " ORDER BY sort_value " + q.direction + " " + nulls + ", submission_id ASC, game_uuid ASC NULLS FIRST LIMIT " + limitParam + " OFFSET " + offsetParam
	keys := "SELECT * FROM matches" + ordering
	prefix := "WITH matches AS MATERIALIZED (" + matches + "), "
	total := "SELECT count(*) AS count FROM matches"
	if len(q.filters) == 0 {
		// Without predicates the count can discard display/sort joins and
		// scan only the parent tables. Materializing all matches would add
		// unnecessary storage and scans, especially for size-ordered pages.
		prefix = "WITH "
		keys = matches + ordering
		if indexedPage := pgUnfilteredDatePage(q, legacyFrom, limitParam, offsetParam); indexedPage != "" {
			keys = indexedPage
		}
		total = "SELECT (SELECT count(*)" + submissionFrom + ") + (SELECT count(*)" + legacyFrom + ") AS count"
	} else if pgPlatformDatePageEligible(filter, q) {
		// Broad platform pages need only a short ordered prefix. Counting can
		// omit the history join used solely for sorting; the page evaluates
		// its platform predicate while walking the timestamp index.
		prefix = "WITH "
		keys = pgPlatformDatePage(q, submissionFrom, legacyFrom, limitParam, offsetParam)
		total = "SELECT (SELECT count(*)" + submissionFrom + ") + (SELECT count(*)" + legacyFrom + ") AS count"
	}
	// Join legacy rows by their source row ID, independently of their public
	// UUID identity and ordering. Both identity kinds survive UNION ALL.
	page := pgSubmissionSearchProjection + " FROM page_keys page_key JOIN submission ON submission.id = page_key.submission_id" + pgSubmissionSearchJoins + " WHERE page_key.legacy_id IS NULL UNION ALL " +
		pgSubmissionLegacyProjection + " FROM page_keys page_key JOIN masterdb_game ON masterdb_game.id = page_key.legacy_id"
	query := prefix + "page_keys AS MATERIALIZED (" + keys + "), page AS MATERIALIZED (" + page + "), total AS (" + total + ") " +
		"SELECT page.*, total.count, FALSE AS count_only FROM page CROSS JOIN total UNION ALL " +
		pgSubmissionEmptyProjection + ", total.count, TRUE AS count_only FROM total WHERE NOT EXISTS (SELECT 1 FROM page) " +
		"ORDER BY " + q.order + " " + q.direction + " " + nulls + ", submission_id ASC, game_uuid ASC NULLS FIRST"
	rows, err := dbs.Tx().QueryContext(dbs.Ctx(), query, q.args...)
	if err != nil {
		return nil, 0, fmt.Errorf("search submissions: %w", err)
	}
	defer rows.Close()
	result := make([]*types.ExtendedSubmission, 0)

	var submitterAvatar string
	var updaterAvatar string
	var assignedTestingUserIDs *string
	var assignedVerificationUserIDs *string
	var requestedChangesUserIDs *string
	var approvedUserIDs *string
	var verifiedUserIDs *string
	var distinctActions *string
	var frozenAt *time.Time

	for rows.Next() {
		s := &types.ExtendedSubmission{}
		var countOnly bool
		if err := rows.Scan(
			&s.SubmissionID,
			&s.SubmissionLevel,
			&s.SubmitterID, &s.SubmitterUsername, &submitterAvatar,
			&s.UpdaterID, &s.UpdaterUsername, &updaterAvatar,
			&s.FileID, &s.OriginalFilename, &s.CurrentFilename, &s.Size,
			&s.UploadedAt, &s.UpdatedAt, &s.LastUploaderID,
			&s.CurationTitle, &s.CurationAlternateTitles, &s.CurationPlatform, &s.CurationLaunchCommand, &s.CurationLibrary, &s.CurationExtreme,
			&s.BotAction,
			&s.FileCount,
			&assignedTestingUserIDs, &assignedVerificationUserIDs, &requestedChangesUserIDs, &approvedUserIDs, &verifiedUserIDs,
			&distinctActions, &s.GameExists, &frozenAt, &s.ShouldAutofreeze, &s.GameUUID, &counter, &countOnly); err != nil {
			return nil, 0, err
		}
		if countOnly {
			continue
		}
		s.SubmitterAvatarURL = utils.FormatAvatarURL(s.SubmitterID, submitterAvatar)
		s.UpdaterAvatarURL = utils.FormatAvatarURL(s.UpdaterID, updaterAvatar)
		if frozenAt != nil {
			s.IsFrozen = true
		}

		s.AssignedTestingUserIDs = []int64{}
		if assignedTestingUserIDs != nil && len(*assignedTestingUserIDs) > 0 {
			userIDs := strings.Split(*assignedTestingUserIDs, ",")
			for _, userID := range userIDs {
				uid, err := strconv.ParseInt(userID, 10, 64)
				if err != nil {
					return nil, 0, err
				}
				s.AssignedTestingUserIDs = append(s.AssignedTestingUserIDs, uid)
			}
		}

		s.AssignedVerificationUserIDs = []int64{}
		if assignedVerificationUserIDs != nil && len(*assignedVerificationUserIDs) > 0 {
			userIDs := strings.Split(*assignedVerificationUserIDs, ",")
			for _, userID := range userIDs {
				uid, err := strconv.ParseInt(userID, 10, 64)
				if err != nil {
					return nil, 0, err
				}
				s.AssignedVerificationUserIDs = append(s.AssignedVerificationUserIDs, uid)
			}
		}

		s.RequestedChangesUserIDs = []int64{}
		if requestedChangesUserIDs != nil && len(*requestedChangesUserIDs) > 0 {
			userIDs := strings.Split(*requestedChangesUserIDs, ",")
			for _, userID := range userIDs {
				uid, err := strconv.ParseInt(userID, 10, 64)
				if err != nil {
					return nil, 0, err
				}
				s.RequestedChangesUserIDs = append(s.RequestedChangesUserIDs, uid)
			}
		}

		s.ApprovedUserIDs = []int64{}
		if approvedUserIDs != nil && len(*approvedUserIDs) > 0 {
			userIDs := strings.Split(*approvedUserIDs, ",")
			for _, userID := range userIDs {
				uid, err := strconv.ParseInt(userID, 10, 64)
				if err != nil {
					return nil, 0, err
				}
				s.ApprovedUserIDs = append(s.ApprovedUserIDs, uid)
			}
		}

		s.VerifiedUserIDs = []int64{}
		if verifiedUserIDs != nil && len(*verifiedUserIDs) > 0 {
			userIDs := strings.Split(*verifiedUserIDs, ",")
			for _, userID := range userIDs {
				uid, err := strconv.ParseInt(userID, 10, 64)
				if err != nil {
					return nil, 0, err
				}
				s.VerifiedUserIDs = append(s.VerifiedUserIDs, uid)
			}
		}

		s.DistinctActions = []string{}
		if distinctActions != nil && len(*distinctActions) > 0 {
			s.DistinctActions = append(s.DistinctActions, strings.Split(*distinctActions, ",")...)
		}

		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read submission results: %w", err)
	}
	return result, counter, nil
}

const pgSubmissionSearchProjection = `
			SELECT submission.id AS submission_id,
		(
			SELECT name
			FROM submission_level
			WHERE id = submission.fk_submission_level_id
		) AS submission_level,
		uploader.id AS uploader_id,
		uploader.username AS uploader_username,
		uploader.avatar AS uploader_avatar,
		updater.id AS updater_id,
		updater.username AS updater_username,
		updater.avatar AS updater_avatar,
		newest_file.id AS newest_file_id,
		newest_file.original_filename AS newest_file_original_filename,
		newest_file.current_filename AS newest_file_current_filename,
		newest_file.size AS newest_file_size,
		oldest_file.created_at AS created_at,
		newest_comment.created_at AS updated_at,
		newest_file.fk_user_id AS newest_file_user_id,
		meta.title AS meta_title,
		meta.alternate_titles AS meta_alternate_titles,
		meta.platform AS meta_platform,
		meta.launch_command AS meta_launch_command,
		meta.library AS meta_library,
		meta.extreme AS meta_extreme,
		submission_cache.bot_action AS bot_action,
		(SELECT count(*) FROM submission_file files WHERE files.fk_submission_id = submission.id AND files.deleted_at IS NULL) AS file_count,
		submission_cache.active_assigned_testing_ids AS active_assigned_testing_ids,
		submission_cache.active_assigned_verification_ids AS active_assigned_verification_ids,
		submission_cache.active_requested_changes_ids AS active_requested_changes_ids,
		submission_cache.active_approved_ids AS active_approved_ids,
		submission_cache.active_verified_ids AS active_verified_ids,
		submission_cache.distinct_actions AS distinct_actions,
		meta.game_exists AS meta_game_exists,
		submission.frozen_at as frozen_at,
		submission.should_autofreeze as should_autofreeze,
        NULL AS game_uuid`

const pgSubmissionLegacyProjection = `SELECT -1 AS submission_id,
			(SELECT 'legacy') AS submission_level,
			(SELECT -1) AS uploader_id,
			(SELECT 'legacy') AS uploader_username,
			(SELECT 'legacy') AS uploader_avatar,
			(SELECT -1) AS updater_id,
			(SELECT 'legacy') AS updater_username,
			(SELECT 'legacy') AS updater_avatar,
			(SELECT -1) AS newest_file_id,
			(SELECT 'legacy') AS newest_file_original_filename,
			(SELECT 'legacy') AS newest_file_current_filename,
			(SELECT 42) AS newest_file_size,
			date_added AS created_at,
			date_modified AS updated_at,
			(SELECT -1) AS newest_file_user_id,
			title AS meta_title,
			alternate_titles AS meta_alternate_titles,
			platform AS meta_platform,
			launch_command  AS meta_launch_command,
			library  AS meta_library,
			extreme AS meta_extreme,
			(SELECT 'legacy') AS bot_action,
			(SELECT 0) AS file_count,
			(SELECT '') AS active_assigned_testing_ids,
			(SELECT '') AS active_assigned_verification_ids,
			(SELECT '') AS active_requested_changes_ids,
			(SELECT '') AS active_approved_ids,
			(SELECT '') AS active_verified_ids,
			(SELECT 'mark-added') AS distinct_actions,
			(SELECT TRUE) as meta_game_exists,
			NULL::timestamp as frozen_at,
			(SELECT FALSE) as should_autofreeze,
            uuid AS game_uuid`

// A count-only row preserves totals on empty/past-end pages. Its placeholder
// projection is never returned to callers; all non-nullable scan targets are
// populated so malformed real rows still fail instead of being hidden.
const pgSubmissionEmptyProjection = `SELECT
 -1::bigint AS submission_id, ''::text AS submission_level,
 -1::bigint AS uploader_id, ''::text AS uploader_username, ''::text AS uploader_avatar,
 -1::bigint AS updater_id, ''::text AS updater_username, ''::text AS updater_avatar,
 -1::bigint AS newest_file_id, ''::text AS newest_file_original_filename, ''::text AS newest_file_current_filename,
 0::bigint AS newest_file_size, TIMESTAMP '1970-01-01' AS created_at, TIMESTAMP '1970-01-01' AS updated_at,
 -1::bigint AS newest_file_user_id, NULL::text AS meta_title, NULL::text AS meta_alternate_titles,
 NULL::text AS meta_platform, NULL::text AS meta_launch_command, NULL::text AS meta_library,
 NULL::text AS meta_extreme, ''::text AS bot_action, 0::bigint AS file_count,
 NULL::text AS active_assigned_testing_ids, NULL::text AS active_assigned_verification_ids,
 NULL::text AS active_requested_changes_ids, NULL::text AS active_approved_ids, NULL::text AS active_verified_ids,
 NULL::text AS distinct_actions, FALSE AS meta_game_exists, NULL::timestamp AS frozen_at,
 FALSE AS should_autofreeze, NULL::text AS game_uuid`
