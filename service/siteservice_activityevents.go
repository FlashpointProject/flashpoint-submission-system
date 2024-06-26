package service

import (
	"context"
	"strconv"

	"github.com/FlashpointProject/flashpoint-submission-system/activityevents"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
)

func (s *SiteService) EmitSubmissionDownloadEvent(ctx context.Context, userID, submissionID, fileID int64) error {
	dbs, err := s.pgdal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	event := activityevents.BuildSubmissionDownloadEvent(userID, submissionID, fileID)

	err = s.pgdal.CreateActivityEvent(dbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	return nil
}

func (s *SiteService) EmitSubmissionCreatedEvent(pgdbs database.PGDBSession, userID, submissionID int64) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildSubmissionCreatedEvent(userID, submissionID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitSubmissionCommentEvent(pgdbs database.PGDBSession, userID, submissionID, commentID int64, action string, fileID *int64) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildSubmissionCommentEvent(userID, submissionID, commentID, action, fileID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitSubmissionOverrideEvent(pgdbs database.PGDBSession, userID, submissionID, commentID int64) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildSubmissionCommentEvent(userID, submissionID, commentID, "approve-override", nil)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitSubmissionDeleteEvent(pgdbs database.PGDBSession, userID, submissionID int64, commentID, fileID *int64) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildSubmissionDeleteEvent(userID, submissionID, commentID, fileID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitSubmissionFreezeEvent(pgdbs database.PGDBSession, userID, submissionID int64, toFreeze bool) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildSubmissionFreezeEvent(userID, submissionID, toFreeze)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitAuthLoginEvent(pgdbs database.PGDBSession, userID int64) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildAuthLoginEvent(userID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitAuthLogoutEvent(pgdbs database.PGDBSession, userID string) error {
	ctx := pgdbs.Ctx()

	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return err
	}

	event := activityevents.BuildAuthLogoutEvent(uid)

	err = s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitGameLogoUpdateEvent(ctx context.Context, userID int64, gameUUID string) error {
	pgdbs, err := s.pgdal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer pgdbs.Rollback()

	event := activityevents.BuildGameLogoUpdateEvent(userID, gameUUID)

	err = s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := pgdbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	return nil
}

func (s *SiteService) EmitGameScreenshotUpdateEvent(ctx context.Context, userID int64, gameUUID string) error {
	pgdbs, err := s.pgdal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer pgdbs.Rollback()

	event := activityevents.BuildGameScreenshotUpdateEvent(userID, gameUUID)

	err = s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := pgdbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	return nil
}

func (s *SiteService) EmitGameDeleteEvent(pgdbs database.PGDBSession, userID int64, gameUUID string) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildGameDeleteEvent(userID, gameUUID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitGameRestoreEvent(pgdbs database.PGDBSession, userID int64, gameUUID string) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildGameRestoreEvent(userID, gameUUID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitGameFreezeEvent(pgdbs database.PGDBSession, userID int64, gameUUID string) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildGameFreezeEvent(userID, gameUUID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitGameUnfreezeEvent(pgdbs database.PGDBSession, userID int64, gameUUID string) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildGameUnfreezeEvent(userID, gameUUID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitAuthRevokeSessionEvent(pgdbs database.PGDBSession, userID, sessionID int64) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildAuthRevokeSessionEvent(userID, sessionID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitAuthSetClientSecretEvent(pgdbs database.PGDBSession, userID int64, clientID string) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildAuthSetClientSecretEvent(userID, clientID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitTagUpdateEvent(pgdbs database.PGDBSession, userID, tagID int64) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildTagUpdateEvent(userID, tagID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitGameSaveEvent(pgdbs database.PGDBSession, userID int64, gameUUID string) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildGameSaveEvent(userID, gameUUID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitGameSaveDataEvent(pgdbs database.PGDBSession, userID int64, gameUUID string) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildGameSaveDataEvent(userID, gameUUID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitAuthDeviceEvent(ctx context.Context, userID int64, clientID string, approved bool) error {
	pgdbs, err := s.pgdal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer pgdbs.Rollback()

	event := activityevents.BuildAuthDeviceEvent(userID, clientID, approved)

	err = s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := pgdbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	return nil
}

func (s *SiteService) EmitAuthNewTokenEvent(ctx context.Context, userID int64, clientID string) error {
	pgdbs, err := s.pgdal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer pgdbs.Rollback()

	event := activityevents.BuildAuthNewTokenEvent(userID, clientID)

	err = s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	if err := pgdbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	return nil
}

func (s *SiteService) EmitAuthDeleteUserSessionsEvent(pgdbs database.PGDBSession, userID, targetID int64) error {
	ctx := pgdbs.Ctx()

	event := activityevents.BuildAuthDeleteUserSessionsEvent(userID, targetID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}

func (s *SiteService) EmitGameRedirectEvent(pgdbs database.PGDBSession, userID int64, fromGameUUID, toGameUUID string) error {
	ctx := pgdbs.Ctx()
	event := activityevents.BuildGameRedirectEvent(userID, fromGameUUID, toGameUUID)

	err := s.pgdal.CreateActivityEvent(pgdbs, event)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	return nil
}
