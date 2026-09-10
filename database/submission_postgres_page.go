package database

import (
	"math"
	"reflect"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

// Unfiltered date listings can walk the existing history timestamp indexes.
// Split NULL keys explicitly: PostgreSQL otherwise has to preserve the outer
// join while sorting, often scanning the entire comment history first.
// Each disjoint branch needs at most offset+limit rows for the global page.
func pgUnfilteredDatePage(q *pgSubmissionSearch, legacyFrom, limit, offset string) string {
	if q.order != "updated_at" && q.order != "created_at" || q.offset > math.MaxInt64-q.limit {
		return ""
	}
	table, pointer, legacyDate := "comment", "fk_newest_comment_id", "date_modified"
	if q.order == "created_at" {
		table, pointer, legacyDate = "submission_file", "fk_oldest_file_id", "date_added"
	}
	bound := q.bind(q.offset + q.limit)
	// The non-NULL branches use native index NULL placement; since they cannot
	// contain NULL, this is equivalent to the public ordering below.
	nativeNulls, publicNulls := "NULLS FIRST", "NULLS LAST"
	if q.direction == "ASC" {
		nativeNulls, publicNulls = "NULLS LAST", "NULLS FIRST"
	}
	live := "SELECT submission.id AS submission_id, NULL::bigint AS legacy_id, NULL::text AS game_uuid, history.created_at AS sort_value FROM " + table + " history JOIN submission_cache ON submission_cache." + pointer + " = history.id JOIN submission ON submission.id = submission_cache.fk_submission_id WHERE submission.deleted_at IS NULL ORDER BY history.created_at " + q.direction + " " + nativeNulls + ", submission.id ASC LIMIT " + bound
	// The foreign key and NOT NULL history.created_at guarantee that a NULL
	// sort key is exactly an absent cache or a NULL pointer, not a missing row.
	liveNull := "SELECT submission.id AS submission_id, NULL::bigint AS legacy_id, NULL::text AS game_uuid, NULL::timestamp AS sort_value FROM submission LEFT JOIN submission_cache ON submission_cache.fk_submission_id=submission.id WHERE submission.deleted_at IS NULL AND submission_cache." + pointer + " IS NULL ORDER BY submission.id ASC LIMIT " + bound
	legacy := "SELECT -1::bigint AS submission_id, masterdb_game.id AS legacy_id, uuid AS game_uuid, " + legacyDate + " AS sort_value" + legacyFrom + " AND " + legacyDate + " IS NOT NULL ORDER BY " + legacyDate + " " + q.direction + " " + nativeNulls + ", uuid ASC LIMIT " + bound
	legacyNull := "SELECT -1::bigint AS submission_id, masterdb_game.id AS legacy_id, uuid AS game_uuid, " + legacyDate + " AS sort_value" + legacyFrom + " AND " + legacyDate + " IS NULL ORDER BY uuid ASC LIMIT " + bound
	return "SELECT * FROM ((" + live + ") UNION ALL (" + liveNull + ") UNION ALL (" + legacy + ") UNION ALL (" + legacyNull + ")) candidates ORDER BY sort_value " + q.direction + " " + publicNulls + ", submission_id ASC, game_uuid ASC NULLS FIRST LIMIT " + limit + " OFFSET " + offset
}

// Restrict the first filtered index-walk path to a small requested prefix.
// Large exports/deep pages retain materialized matches: walking most history
// again for a page can cost more than sharing the filter work with its count.
const pgPlatformDatePageMaxPrefix int64 = 1000

func pgPlatformDatePageEligible(filter *types.SubmissionsFilter, q *pgSubmissionSearch) bool {
	if filter == nil || filter.PlatformPartial == nil || len(q.filters) == 0 ||
		(q.order != "created_at" && q.order != "updated_at") ||
		q.offset < 0 || q.limit < 1 || q.limit > pgPlatformDatePageMaxPrefix ||
		q.offset > pgPlatformDatePageMaxPrefix-q.limit {
		return false
	}
	// Fail closed when another filter is present, including fields added in
	// the future. Ordering, pagination and legacy exclusion change neither
	// platform selectivity nor the live predicate's shape.
	rest := *filter
	rest.PlatformPartial = nil
	rest.ResultsPerPage, rest.Page = nil, nil
	rest.OrderBy, rest.AscDesc = nil, nil
	rest.ExcludeLegacy = false
	return reflect.ValueOf(rest).IsZero()
}

func pgPlatformDatePage(q *pgSubmissionSearch, submissionFrom, legacyFrom, limit, offset string) string {
	liveDate, legacyDate, pointer := "newest_comment.created_at", "date_modified", "fk_newest_comment_id"
	if q.order == "created_at" {
		liveDate, legacyDate, pointer = "oldest_file.created_at", "date_added", "fk_oldest_file_id"
	}
	bound := q.bind(q.offset + q.limit)
	nativeNulls, publicNulls := "NULLS FIRST", "NULLS LAST"
	if q.direction == "ASC" {
		nativeNulls, publicNulls = "NULLS LAST", "NULLS FIRST"
	}
	liveSelect := "SELECT submission.id AS submission_id, NULL::bigint AS legacy_id, NULL::text AS game_uuid, " + liveDate + " AS sort_value" + submissionFrom
	// Rejection of the NULL timestamp makes its outer join reducible to an
	// inner join, allowing an ordered history-index walk for the bounded page.
	live := liveSelect + " AND " + liveDate + " IS NOT NULL ORDER BY " + liveDate + " " + q.direction + " " + nativeNulls + ", submission.id ASC LIMIT " + bound
	// A NULL timestamp is exactly a NULL cache pointer (including an absent
	// cache), because its foreign key targets a NOT NULL history timestamp.
	// Project a literal NULL so the planner can remove the unused history join.
	liveNull := "SELECT submission.id AS submission_id, NULL::bigint AS legacy_id, NULL::text AS game_uuid, NULL::timestamp AS sort_value" + submissionFrom + " AND submission_cache." + pointer + " IS NULL ORDER BY submission.id ASC LIMIT " + bound
	legacySelect := "SELECT -1::bigint AS submission_id, masterdb_game.id AS legacy_id, uuid AS game_uuid, " + legacyDate + " AS sort_value" + legacyFrom
	legacy := legacySelect + " AND " + legacyDate + " IS NOT NULL ORDER BY " + legacyDate + " " + q.direction + " " + nativeNulls + ", uuid ASC LIMIT " + bound
	legacyNull := legacySelect + " AND " + legacyDate + " IS NULL ORDER BY uuid ASC LIMIT " + bound
	return "SELECT * FROM ((" + live + ") UNION ALL (" + liveNull + ") UNION ALL (" + legacy + ") UNION ALL (" + legacyNull + ")) candidates ORDER BY sort_value " + q.direction + " " + publicNulls + ", submission_id ASC, game_uuid ASC NULLS FIRST LIMIT " + limit + " OFFSET " + offset
}
