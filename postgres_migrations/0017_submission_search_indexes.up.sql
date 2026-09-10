-- Submission objects only. The explicit predicate in the search builder also
-- permits generic prepared plans to use this index without knowing parameters.
CREATE INDEX idx_submission_cache_ready_for_fp
ON submission_cache(fk_submission_id) INCLUDE(fk_oldest_file_id)
WHERE (bot_action = 'approve'
 AND active_requested_changes_ids IS NULL
 AND active_verified_ids IS NOT NULL
 AND distinct_actions !~* 'mark-added|reject') IS TRUE;

-- Statistics belong to the inner boolean expression: WHERE expr IS TRUE tests
-- that expression's truth distribution. Statistics on (expr IS TRUE) itself
-- do not provide the same selectivity estimate to the planner.
CREATE STATISTICS stats_submission_cache_ready_for_fp ON
 ((bot_action = 'approve'
 AND active_requested_changes_ids IS NULL
 AND active_verified_ids IS NOT NULL
 AND distinct_actions !~* 'mark-added|reject'))
FROM submission_cache;

-- Narrow covering scans avoid reading full metadata rows for platform counts.
-- COLLATE C matches submission_like's explicit regex comparison collation.
CREATE INDEX idx_submission_meta_platform_search
ON curation_meta (platform COLLATE "C") INCLUDE (fk_submission_file_id);
CREATE INDEX idx_submission_legacy_platform_search
ON masterdb_game (platform COLLATE "C");

ANALYZE submission_cache;
ANALYZE curation_meta;
ANALYZE masterdb_game;
