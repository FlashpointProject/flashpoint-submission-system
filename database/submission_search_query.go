package database

import "strings"

// Only join tables needed to decide membership or order. Display-only joins
// belong after LIMIT, otherwise a broad search hydrates every matching row.
func submissionSearchFrom(expressions string, searchComments bool) string {
	meta := strings.Contains(expressions, "meta.")
	uploader := strings.Contains(expressions, "uploader.")
	updater := strings.Contains(expressions, "updater.")
	newest := meta || strings.Contains(expressions, "newest_file.")
	oldest := uploader || strings.Contains(expressions, "oldest_file.")
	comment := updater || strings.Contains(expressions, "newest_comment.")
	cache := newest || oldest || comment || strings.Contains(expressions, "submission_cache.")
	from := " FROM submission"
	if searchComments {
		// Materialize each matching parent once, before the display/cache joins.
		// On MariaDB 11.1, grouping through the FK index otherwise visits every
		// comment via random table lookups. Ignore that index only for grouping:
		// WHERE can still use it when searching explicit submission IDs.
		// LOCATE + lowercase + binary collation preserves literal, case-insensitive
		// matching with significant accents across every visible comment type.
		from = ` FROM (
			SELECT DISTINCT search_comment.fk_submission_id
			FROM comment search_comment IGNORE INDEX FOR GROUP BY (fk_submission_id)
			WHERE search_comment.deleted_at IS NULL
			AND LOCATE(LOWER(?), LOWER(search_comment.message) COLLATE utf8mb4_bin) > 0
		) comment_matches JOIN submission ON submission.id = comment_matches.fk_submission_id`
	}
	for _, join := range []struct {
		needed bool
		sql    string
	}{
		{cache, ` LEFT JOIN submission_cache ON submission_cache.fk_submission_id = submission.id`},
		{oldest, ` LEFT JOIN submission_file oldest_file ON oldest_file.id = submission_cache.fk_oldest_file_id`},
		{newest, ` LEFT JOIN submission_file newest_file ON newest_file.id = submission_cache.fk_newest_file_id`},
		{comment, ` LEFT JOIN comment newest_comment ON newest_comment.id = submission_cache.fk_newest_comment_id`},
		{uploader, ` LEFT JOIN discord_user uploader ON uploader.id = oldest_file.fk_user_id`},
		{updater, ` LEFT JOIN discord_user updater ON updater.id = newest_comment.fk_user_id`},
		{meta, ` LEFT JOIN curation_meta meta ON meta.fk_submission_file_id = newest_file.id`},
	} {
		if join.needed {
			from += join.sql
		}
	}
	return from
}

func submissionSearchPageQuery(predicates, where, legacyWhere, order, direction string, includeCount, searchComments bool) string {
	// These expressions are selected by the validated public ordering enum.
	orders := map[string][2]string{
		"created_at":       {"oldest_file.created_at", "date_added"},
		"updated_at":       {"newest_comment.created_at", "date_modified"},
		"newest_file_size": {"newest_file.size", "42"},
		"meta_title":       {"meta.title", "title"},
		"meta_platform":    {"meta.platform", "platform"},
		"meta_library":     {"meta.library", "library"},
	}
	sort := orders[order]
	expressions := predicates + " " + sort[0] + " submission_cache."
	metaID := "NULL"
	metaJoin := "meta.fk_submission_file_id = newest_file.id"
	if strings.Contains(expressions, "meta.") {
		metaID = "meta.id"
		metaJoin = "meta.id = page.meta_id"
	}
	// The historical cache has no unique key. Carry the selected row's values
	// through grouping instead of joining it again and potentially displaying
	// a different cache row than the one that satisfied the filters.
	cacheColumns := []string{"fk_oldest_file_id", "fk_newest_file_id", "fk_newest_comment_id", "bot_action", "active_assigned_testing_ids", "active_assigned_verification_ids", "active_requested_changes_ids", "active_approved_ids", "active_verified_ids", "distinct_actions"}
	cacheSelect, legacyCache := "", ""
	for _, column := range cacheColumns {
		cacheSelect += ", submission_cache." + column
		legacyCache += ", NULL"
	}
	page := `SELECT submission.id AS submission_id, NULL AS game_uuid, ` + metaID + ` AS meta_id, ` + sort[0] + ` AS sort_value` + cacheSelect +
		submissionSearchFrom(expressions, searchComments) + where + ` GROUP BY submission.id
		UNION ALL SELECT -1, uuid, NULL, ` + sort[1] + legacyCache + ` FROM masterdb_game` + legacyWhere
	if includeCount {
		page = `SELECT matches.*, COUNT(*) OVER () AS matched_count FROM (` + page + `) matches`
	}
	page += ` ORDER BY sort_value ` + direction + `, submission_id ASC, game_uuid ASC LIMIT ? OFFSET ?`

	// Keep the same column order and legacy values as ExtendedSubmission's scan.
	columns := [][3]string{
		{"submission_id", "submission.id", "-1"},
		{"submission_level", "(SELECT name FROM submission_level WHERE id = submission.fk_submission_level_id)", "'legacy'"},
		{"uploader_id", "uploader.id", "-1"}, {"uploader_username", "uploader.username", "'legacy'"}, {"uploader_avatar", "uploader.avatar", "'legacy'"},
		{"updater_id", "updater.id", "-1"}, {"updater_username", "updater.username", "'legacy'"}, {"updater_avatar", "updater.avatar", "'legacy'"},
		{"newest_file_id", "newest_file.id", "-1"}, {"newest_file_original_filename", "newest_file.original_filename", "'legacy'"}, {"newest_file_current_filename", "newest_file.current_filename", "'legacy'"}, {"newest_file_size", "newest_file.size", "42"},
		{"created_at", "oldest_file.created_at", "legacy.date_added"}, {"updated_at", "newest_comment.created_at", "legacy.date_modified"}, {"newest_file_user_id", "newest_file.fk_user_id", "-1"},
		{"meta_title", "meta.title", "legacy.title"}, {"meta_alternate_titles", "meta.alternate_titles", "legacy.alternate_titles"}, {"meta_platform", "meta.platform", "legacy.platform"},
		{"meta_launch_command", "meta.launch_command", "legacy.launch_command"}, {"meta_library", "meta.library", "legacy.library"}, {"meta_extreme", "meta.extreme", "legacy.extreme"},
		{"bot_action", "page.bot_action", "'legacy'"},
		{"file_count", "(SELECT NULLIF(COUNT(*), 0) FROM submission_file count_file WHERE count_file.fk_submission_id = submission.id AND count_file.deleted_at IS NULL)", "0"},
		{"active_assigned_testing_ids", "page.active_assigned_testing_ids", "''"}, {"active_assigned_verification_ids", "page.active_assigned_verification_ids", "''"},
		{"active_requested_changes_ids", "page.active_requested_changes_ids", "''"}, {"active_approved_ids", "page.active_approved_ids", "''"}, {"active_verified_ids", "page.active_verified_ids", "''"},
		{"distinct_actions", "page.distinct_actions", "'mark-added'"}, {"meta_game_exists", "meta.game_exists", "TRUE"},
		{"frozen_at", "submission.frozen_at", "NULL"}, {"should_autofreeze", "submission.should_autofreeze", "FALSE"},
	}
	selects := make([]string, 0, len(columns)+1)
	for _, c := range columns {
		selects = append(selects, "CASE WHEN page.game_uuid IS NULL THEN "+c[1]+" ELSE "+c[2]+" END AS "+c[0])
	}
	selects = append(selects, "page.game_uuid")
	if includeCount {
		selects = append(selects, "page.matched_count")
	}
	return "SELECT " + strings.Join(selects, ", ") + ` FROM (` + page + `) page
		LEFT JOIN submission ON submission.id = page.submission_id
		LEFT JOIN submission_file oldest_file ON oldest_file.id = page.fk_oldest_file_id
		LEFT JOIN submission_file newest_file ON newest_file.id = page.fk_newest_file_id
		LEFT JOIN comment newest_comment ON newest_comment.id = page.fk_newest_comment_id
		LEFT JOIN discord_user uploader ON uploader.id = oldest_file.fk_user_id
		LEFT JOIN discord_user updater ON updater.id = newest_comment.fk_user_id
		LEFT JOIN curation_meta meta ON ` + metaJoin + `
		LEFT JOIN masterdb_game legacy ON legacy.uuid = page.game_uuid
		GROUP BY page.submission_id, page.game_uuid
		ORDER BY page.sort_value ` + direction + `, page.submission_id ASC, page.game_uuid ASC`
}
