package database

import (
	"database/sql"
	_ "embed"
	"strings"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

//go:embed user_statistics.sql
var userStatisticsSQL string

// GetAllUserStatistics counts submissions and actions without fetching search
// pages or comment messages. Last activity includes every non-deleted action,
// including comments on a subsequently deleted submission.
func (d *mysqlDAL) GetAllUserStatistics(dbs DBSession) ([]*types.UserStatistics, error) {
	return d.getUserStatistics(dbs, nil)
}
func (d *mysqlDAL) GetUserStatisticsAggregate(dbs DBSession, uid int64) ([]*types.UserStatistics, error) {
	return d.getUserStatistics(dbs, &uid)
}
func (d *mysqlDAL) getUserStatistics(dbs DBSession, uid *int64) ([]*types.UserStatistics, error) {
	roles := constants.StaffRoles()
	query := strings.Replace(userStatisticsSQL, "/*STAFF_ROLES*/", "?"+strings.Repeat(",?", len(roles)-1), 1)
	args := make([]interface{}, 0, len(roles)+1)
	if uid != nil {
		args = append(args, *uid, *uid)
	}
	for _, role := range roles {
		args = append(args, role)
	}
	args = append(args, constants.RoleTrialCurator)
	replacements := []string{"/*SUBMITTED_USER*/", "", "/*ACTION_USER*/", "", "/*ROLE_USER*/", "", "/*USER*/", ""}
	if uid != nil {
		replacements = []string{"/*SUBMITTED_USER*/", "AND f.fk_user_id=?", "/*ACTION_USER*/", "AND c.fk_user_id=?", "/*ROLE_USER*/", "WHERE ur.fk_uid=?", "/*USER*/", "WHERE u.id=?"}
		args = append(args, *uid, *uid)
	}
	query = strings.NewReplacer(replacements...).Replace(query)
	rows, err := dbs.Tx().QueryContext(dbs.Ctx(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]*types.UserStatistics, 0)
	for rows.Next() {
		s := &types.UserStatistics{Role: "User"}
		var staff, trial bool
		var last sql.NullTime
		if err := rows.Scan(&s.UserID, &s.Username, &staff, &trial, &last,
			&s.UserCommentedCount, &s.UserRequestedChangesCount, &s.UserApprovedCount,
			&s.UserVerifiedCount, &s.UserAddedToFlashpointCount, &s.UserRejectedCount,
			&s.SubmissionsCount, &s.SubmissionsBotHappyCount, &s.SubmissionsBotUnhappyCount,
			&s.SubmissionsRequestedChangesCount, &s.SubmissionsApprovedCount,
			&s.SubmissionsVerifiedCount, &s.SubmissionsAddedToFlashpointCount,
			&s.SubmissionsRejectedCount); err != nil {
			return nil, err
		}
		if trial {
			s.Role = constants.RoleTrialCurator
		}
		if staff {
			s.Role = "Staff"
		}
		if last.Valid {
			s.LastUserActivity = last.Time
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

// GetLatestSubmissionActivity uses the existing (uid, created_at) index for
// one bounded lookup per ID inside a single database statement/round trip.
func (d *postgresDAL) GetLatestSubmissionActivity(dbs PGDBSession, userIDs []int64) (map[int64]time.Time, error) {
	result := make(map[int64]time.Time, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	rows, err := dbs.Tx().Query(dbs.Ctx(), `
        SELECT u.id, a.created_at
        FROM unnest($1::bigint[]) AS u(id)
        JOIN LATERAL (
            SELECT created_at FROM activity_events
            WHERE uid = u.id AND event_area = 'submission'
            ORDER BY created_at DESC LIMIT 1
        ) a ON true`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		result[id] = at
	}
	return result, rows.Err()
}
