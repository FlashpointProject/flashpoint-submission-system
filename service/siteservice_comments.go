package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
)

func (s *SiteService) ReceiveComments(ctx context.Context, uid int64, sids []int64, formAction, formMessage,
	formIgnoreDupeActions, subDirFullPath, dataPacksDir, frozenPacksDir, imagesDir string, r *http.Request) error {
	dbs, err := s.dal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer dbs.Rollback()

	pgdbs, err := s.pgdal.NewSession(ctx)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	defer pgdbs.Rollback()

	var message *string
	if formMessage != "" {
		message = &formMessage
	}

	actions := constants.GetAllowedActions()
	isActionValid := false
	for _, a := range actions {
		if formAction == a {
			isActionValid = true
			break
		}
	}

	if !isActionValid {
		return perr("invalid comment action", http.StatusBadRequest)
	}

	actionsWithMandatoryMessage := constants.GetActionsWithMandatoryMessage()
	isActionWithMandatoryMessage := false
	for _, a := range actionsWithMandatoryMessage {
		if formAction == a {
			isActionWithMandatoryMessage = true
			break
		}
	}

	if isActionWithMandatoryMessage && (message == nil || *message == "") {
		return perr(fmt.Sprintf("cannot post comment action '%s' without a message", formAction), http.StatusBadRequest)
	}

	ignoreDupeActions := false
	if formIgnoreDupeActions == "true" {
		ignoreDupeActions = true
	}

	// stop request changes on comment batches
	if formAction == constants.ActionRequestChanges && len(sids) > 1 {
		return perr("cannot request changes on multiple submissions at once", http.StatusBadRequest)
	}
	if formAction == constants.ActionReject && len(sids) > 1 {
		return perr("cannot reject multiple submissions at once", http.StatusBadRequest)
	}

	utils.LogCtx(ctx).Debugf("searching submissions for comment batch")
	foundSubmissions, _, err := s.dal.SearchSubmissions(dbs, &types.SubmissionsFilter{SubmissionIDs: sids})
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	for _, sid := range sids {
		found := false
		for _, s := range foundSubmissions {
			if sid == s.SubmissionID {
				found = true
			}
		}
		if !found {
			return perr(fmt.Sprintf("submission %d not found", sid), http.StatusNotFound)
		}
	}

	commentCounter := 0

	// TODO optimize batch operation even more
	for _, submission := range foundSubmissions {
		sid := submission.SubmissionID

		err := isActionValidForSubmission(uid, formAction, submission)
		if err != nil {
			if ignoreDupeActions {
				continue
			}
			return err
		}

		// If marking as added, make sure we update the live metadata before approving the comment
		if formAction == constants.ActionMarkAdded {
			gameId, err := s.AddSubmissionToFlashpoint(ctx, submission, subDirFullPath, dataPacksDir,
				frozenPacksDir, imagesDir, r)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				return err
			}
			addedMessage := fmt.Sprintf("Marked the submission as added to Flashpoint. Game ID: %s", *gameId)
			message = &addedMessage
		}

		// actually store the comment
		c := &types.Comment{
			AuthorID:     uid,
			SubmissionID: sid,
			Message:      message,
			Action:       formAction,
			CreatedAt:    s.clock.Now().UTC(),
		}

		// clear messages for assigns and unassigns
		if formAction == constants.ActionAssignTesting ||
			formAction == constants.ActionUnassignTesting ||
			formAction == constants.ActionAssignVerification ||
			formAction == constants.ActionUnassignVerification {
			c.Message = nil
		}

		// subscribe the commenter
		if formAction == constants.ActionAssignTesting ||
			formAction == constants.ActionUnassignTesting ||
			formAction == constants.ActionAssignVerification ||
			formAction == constants.ActionUnassignVerification ||
			formAction == constants.ActionApprove ||
			formAction == constants.ActionRequestChanges ||
			formAction == constants.ActionVerify ||
			formAction == constants.ActionReject {

			subscribed, err := s.dal.IsUserSubscribedToSubmission(dbs, uid, sid)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				return dberr(err)
			}
			if !subscribed {
				if err := s.dal.SubscribeUserToSubmission(dbs, uid, sid); err != nil {
					utils.LogCtx(ctx).Error(err)
					return dberr(err)
				}
			}
		}

		cid, err := s.dal.StoreComment(dbs, c)
		if err != nil {
			utils.LogCtx(ctx).Error(err)
			return dberr(err)
		}
		if err := s.EmitSubmissionCommentEvent(pgdbs, c.AuthorID, c.SubmissionID, cid, c.Action, nil); err != nil {
			utils.LogCtx(ctx).Error(err)
			return dberr(err)
		}

		// unassign if needed
		if formAction == constants.ActionApprove {
			c = &types.Comment{
				AuthorID:     uid,
				SubmissionID: sid,
				Message:      nil,
				Action:       constants.ActionUnassignTesting,
				CreatedAt:    s.clock.Now().UTC().Add(time.Microsecond),
			}

			cid, err := s.dal.StoreComment(dbs, c)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				return dberr(err)
			}
			if err := s.EmitSubmissionCommentEvent(pgdbs, c.AuthorID, c.SubmissionID, cid, c.Action, nil); err != nil {
				utils.LogCtx(ctx).Error(err)
				return dberr(err)
			}
		} else if formAction == constants.ActionVerify {
			c = &types.Comment{
				AuthorID:     uid,
				SubmissionID: sid,
				Message:      nil,
				Action:       constants.ActionUnassignVerification,
				CreatedAt:    s.clock.Now().UTC().Add(time.Microsecond),
			}

			cid, err := s.dal.StoreComment(dbs, c)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				return dberr(err)
			}
			if err := s.EmitSubmissionCommentEvent(pgdbs, c.AuthorID, c.SubmissionID, cid, c.Action, nil); err != nil {
				utils.LogCtx(ctx).Error(err)
				return dberr(err)
			}
		}

		if err := s.createNotification(dbs, uid, sid, formAction); err != nil {
			utils.LogCtx(ctx).Error(err)
			return dberr(err)
		}

		// freeze the curation if it autofreeze is true
		if formAction == constants.ActionVerify && submission.ShouldAutofreeze {
			if err := s.dal.FreezeSubmission(dbs, submission.SubmissionID); err != nil {
				utils.LogCtx(ctx).Error(err)
				return dberr(err)
			}

			if err := s.createFreezeNotification(dbs, submission.SubmitterID, uid, sid); err != nil {
				utils.LogCtx(ctx).Error(err)
				return dberr(err)
			}

			msg := "This submission has been automatically frozen."
			c := &types.Comment{
				AuthorID:     constants.SystemID,
				SubmissionID: sid,
				Message:      &msg,
				Action:       constants.ActionSystem,
				CreatedAt:    s.clock.Now().UTC().Add(time.Microsecond * 2),
			}

			cid, err := s.dal.StoreComment(dbs, c)
			if err != nil {
				utils.LogCtx(ctx).Error(err)
				return dberr(err)
			}
			if err := s.EmitSubmissionCommentEvent(pgdbs, c.AuthorID, c.SubmissionID, cid, c.Action, nil); err != nil {
				utils.LogCtx(ctx).Error(err)
				return dberr(err)
			}
		}

		if err := s.dal.UpdateSubmissionCacheTable(dbs, sid); err != nil {
			utils.LogCtx(ctx).Error(err)
			return dberr(err)
		}

		commentCounter++
	}

	if err := pgdbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}
	if err := dbs.Commit(); err != nil {
		utils.LogCtx(ctx).Error(err)
		return dberr(err)
	}

	utils.LogCtx(ctx).WithField("amount", commentCounter).WithField("commentAction", formAction).Debug("comments received")

	s.announceNotification()

	return nil
}
