package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/resumableuploadservice"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
)

func (ru resumableUpload) GetReadCloserInformer() (resumableuploadservice.ReadCloserInformer, error) {
	return ru.rsu.NewFileReader(ru.uid, ru.fileID, ru.chunkCount)
}

func (s *SiteService) ReceiveSubmissionChunk(ctx context.Context, sid *int64, p *types.ResumableParams, chunk []byte) (*string, error) {
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, resumableLog(ctx, p))
	uid := utils.UserID(ctx)
	if uid == 0 {
		return nil, perr("not authenticated", http.StatusUnauthorized)
	}
	if p.ResumableChunkNumber < 1 || p.ResumableChunkNumber > p.ResumableTotalChunks || len(chunk) == 0 {
		return nil, perr("invalid chunk", http.StatusBadRequest)
	}
	j, release, err := s.acquireUploadJob(uid, sid, p)
	if err != nil {
		return nil, err
	}
	defer release()
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.matches(sid, p) {
		return nil, perr("upload identifier already belongs to different upload parameters", http.StatusConflict)
	}
	j.touched = time.Now()
	if j.state == "failed" && p.ResumableRetry > j.retryAttempt {
		if p.ResumableRetry != j.retryAttempt+1 {
			return nil, perr("invalid upload retry attempt", http.StatusConflict)
		}
		if !j.retryable {
			return nil, perr("upload outcome may already be committed; automatic retry is unsafe", http.StatusConflict)
		}
		j.retryAttempt = p.ResumableRetry
		j.state = "receiving"
	}
	if j.state != "receiving" {
		name := j.tracking
		return &name, nil
	}
	if err := s.resumableUploadService.PutChunk(uid, p.ResumableIdentifier, p.ResumableChunkNumber, chunk); err != nil {
		return nil, err
	}
	complete, err := s.resumableUploadService.IsUploadFinished(uid, p.ResumableIdentifier, p.ResumableTotalChunks, p.ResumableTotalSize)
	if err != nil {
		return nil, err
	}
	if !complete {
		return nil, nil
	}
	if j.tracking == "" {
		j.tracking = s.randomStringProvider.RandomString(32)
	}
	s.SSK.SetReceived(j.tracking)
	j.state = "processing"
	j.retryable = true
	go s.runUploadJob(ctx, j)
	name := j.tracking
	return &name, nil
}

type resumableUpload struct {
	uid        int64
	fileID     string
	chunkCount int
	rsu        *resumableuploadservice.ResumableUploadService
}

func newResumableUpload(uid int64, fileID string, chunkCount int, rsu *resumableuploadservice.ResumableUploadService) *resumableUpload {
	return &resumableUpload{
		uid:        uid,
		fileID:     fileID,
		chunkCount: chunkCount,
		rsu:        rsu,
	}
}

///////////

func (s *SiteService) IsChunkReceived(ctx context.Context, resumableParams *types.ResumableParams) (bool, error) {
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, resumableLog(ctx, resumableParams))

	uid := utils.UserID(ctx)
	if uid == 0 {
		utils.LogCtx(ctx).Panic("no user associated with request")
	}

	// Pin any existing job while testing chunks, so deletion and chunk writes
	// cannot race filesystem checks. A POST can then retrieve its tracking ID.
	registry := &s.uploadJobs
	registry.mu.Lock()
	job := registry.jobs[fmt.Sprintf("%d:%s", uid, resumableParams.ResumableIdentifier)]
	if job != nil {
		job.refs++
	}
	registry.mu.Unlock()
	if job == nil {
		// The first POST registers ownership before any filesystem checks or writes.
		return false, nil
	}
	if job != nil {
		defer func() { registry.mu.Lock(); job.refs--; registry.mu.Unlock() }()
		job.mu.Lock()
		defer job.mu.Unlock()
		if job.state != "receiving" {
			return false, nil
		}
	}

	isComplete, err := s.resumableUploadService.IsUploadFinished(uid, resumableParams.ResumableIdentifier, resumableParams.ResumableTotalChunks, resumableParams.ResumableTotalSize)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return false, err
	}

	if isComplete {
		return false, perr("file already fully received", http.StatusConflict)
	}

	utils.LogCtx(ctx).Debug("testing chunk")
	isReceived, err := s.resumableUploadService.TestChunk(uid, resumableParams.ResumableIdentifier, resumableParams.ResumableChunkNumber, resumableParams.ResumableCurrentChunkSize)
	if err != nil {
		utils.LogCtx(ctx).Error(err)
		return false, err
	}

	return isReceived, nil
}
