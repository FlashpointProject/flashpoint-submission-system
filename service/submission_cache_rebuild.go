package service

import (
	"context"
	"fmt"
)

// SubmissionCacheRebuildResult reports only durably committed work. A failed run
// can be safely restarted; earlier submissions may have already been repaired.
type SubmissionCacheRebuildResult struct {
	Recomputed       int64
	LastSubmissionID int64
}

func (s *SiteService) RecomputeSubmissionCacheAll(ctx context.Context) (SubmissionCacheRebuildResult, error) {
	return s.recomputeSubmissionCacheAll(ctx, 1000)
}

func (s *SiteService) recomputeSubmissionCacheAll(ctx context.Context, batchSize int) (result SubmissionCacheRebuildResult, err error) {
	if batchSize <= 0 {
		return result, fmt.Errorf("cache rebuild batch size must be positive")
	}
	for {
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("cache rebuild after %d submissions (last ID %d): %w", result.Recomputed, result.LastSubmissionID, err)
		}
		dbs, err := s.dal.NewSession(ctx)
		if err != nil {
			return result, fmt.Errorf("list cache rebuild IDs after %d (%d rebuilt): %w", result.LastSubmissionID, result.Recomputed, err)
		}
		ids, listErr := s.dal.ListSubmissionIDsForCacheRebuild(dbs, result.LastSubmissionID, batchSize)
		dbs.Rollback()
		if listErr != nil {
			return result, fmt.Errorf("list cache rebuild IDs after %d (%d rebuilt): %w", result.LastSubmissionID, result.Recomputed, listErr)
		}
		if len(ids) == 0 {
			return result, nil
		}
		for _, sid := range ids {
			if sid <= result.LastSubmissionID {
				return result, fmt.Errorf("cache rebuild IDs did not advance after %d", result.LastSubmissionID)
			}
			err := func() error {
				if err := ctx.Err(); err != nil {
					return err
				}
				dbs, err := s.dal.NewSession(ctx)
				if err != nil {
					return err
				}
				defer dbs.Rollback()
				if err := s.dal.RebuildSubmissionCacheTable(dbs, sid); err != nil {
					return err
				}
				return dbs.Commit()
			}()
			if err != nil {
				return result, fmt.Errorf("cache rebuild failed for submission %d after %d committed submissions (last ID %d): %w", sid, result.Recomputed, result.LastSubmissionID, err)
			}
			result.Recomputed++
			result.LastSubmissionID = sid
		}
	}
}
