package service

import (
	"errors"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

var errAuditionSubmissionQuota = errors.New("audition submission limit reached: only one non-rejected submission is allowed")

// Reuse the admission predicate, including its historical live-reject exemption.
// This authoritative check and the new submission/cache writes share the same
// transaction. The HTTP check remains only an early rejection of excess uploads.
func (s *SiteService) checkAuditionSubmissionQuota(dbs database.DBSession, uid int64) error {
	if err := database.LockSubmissionCreation(dbs, uid); err != nil {
		return dberr(err)
	}
	limit := int64(1)
	submissions, _, err := s.dal.SearchSubmissions(dbs, &types.SubmissionsFilter{
		SubmitterID: &uid, DistinctActionsNot: []string{constants.ActionReject}, ResultsPerPage: &limit,
	})
	if err != nil {
		return dberr(err)
	}
	if len(submissions) != 0 {
		return errAuditionSubmissionQuota
	}
	return nil
}
