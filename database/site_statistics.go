package database

import "github.com/FlashpointProject/flashpoint-submission-system/types"

// GetSiteStatistics aggregates directly, without hydrating seven search pages.
// DISTINCT preserves historical duplicate-row behavior. Missing derived rows
// still contribute to the total. Existing totals include legacy games and all
// comments/files (including soft-deleted ones).
func (d *mysqlDAL) GetSiteStatistics(s DBSession) (*types.StatisticsPageData, error) {
	v := &types.StatisticsPageData{}
	err := s.Tx().QueryRowContext(s.Ctx(), `
 SELECT COUNT(DISTINCT s.id)+(SELECT COUNT(*) FROM masterdb_game),
 COUNT(DISTINCT CASE WHEN c.bot_action='approve' THEN s.id END),
 COUNT(DISTINCT CASE WHEN c.bot_action='request-changes' THEN s.id END),
 COUNT(DISTINCT CASE WHEN c.active_approved_ids IS NOT NULL THEN s.id END),
 COUNT(DISTINCT CASE WHEN c.active_verified_ids IS NOT NULL THEN s.id END),
 COUNT(DISTINCT CASE WHEN c.distinct_actions REGEXP 'reject' THEN s.id END),
 COUNT(DISTINCT CASE WHEN c.distinct_actions REGEXP 'mark-added' THEN s.id END),
 (SELECT COUNT(*) FROM discord_user), (SELECT COUNT(*) FROM comment),
 (SELECT COALESCE(SUM(size),0) FROM submission_file)
 FROM submission s LEFT JOIN submission_cache c ON c.fk_submission_id=s.id
 WHERE s.deleted_at IS NULL`).Scan(&v.SubmissionCount, &v.SubmissionCountBotHappy, &v.SubmissionCountBotSad,
		&v.SubmissionCountApproved, &v.SubmissionCountVerified, &v.SubmissionCountRejected, &v.SubmissionCountInFlashpoint,
		&v.UserCount, &v.CommentCount, &v.TotalSubmissionSize)
	return v, err
}
