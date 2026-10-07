package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
)

const uploadJobRetention = 24 * time.Hour
const maxUploadJobs = 1000

type uploadJob struct {
	mu           sync.Mutex
	refs         int // registry mutex
	uid          int64
	sid          *int64
	params       types.ResumableParams
	tracking     string
	state        string
	retryable    bool
	retryAttempt int
	touched      time.Time
}
type uploadJobRegistry struct {
	mu   sync.Mutex
	jobs map[string]*uploadJob
}

// acquire pins a job before releasing the registry lock. Chunk IO and processing
// are serialized per upload, not across unrelated uploads.
func (s *SiteService) acquireUploadJob(uid int64, sid *int64, p *types.ResumableParams) (*uploadJob, func(), error) {
	if strings.ContainsAny(p.ResumableIdentifier, "/\\\x00") || len(p.ResumableIdentifier) == 0 || len(p.ResumableIdentifier) > 4096 || p.ResumableTotalChunks < 1 || p.ResumableTotalSize < 1 {
		return nil, nil, perr("invalid upload parameters", http.StatusBadRequest)
	}
	r := &s.uploadJobs
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.jobs == nil {
		r.jobs = make(map[string]*uploadJob)
	}
	now := time.Now()
	for key, j := range r.jobs {
		if j.refs != 0 || !j.mu.TryLock() {
			continue
		}
		if j.state != "processing" && now.Sub(j.touched) > uploadJobRetention {
			// Keep the registry locked through cleanup so this identifier cannot be
			// reused while an old owner's chunks are being removed.
			if err := s.resumableUploadService.DeleteFile(j.uid, j.params.ResumableIdentifier, j.params.ResumableTotalChunks); err != nil {
				// Retain ownership until cleanup succeeds; never reuse leftover chunks.
				j.mu.Unlock()
				continue
			}
			s.SSK.Remove(j.tracking)
			delete(r.jobs, key)
		}
		j.mu.Unlock()
	}
	key := fmt.Sprintf("%d:%s", uid, p.ResumableIdentifier)
	j := r.jobs[key]
	if j == nil {
		if len(r.jobs) >= maxUploadJobs {
			return nil, nil, perr("upload capacity reached; retry later", http.StatusServiceUnavailable)
		}
		j = &uploadJob{uid: uid, params: *p, state: "receiving", touched: now}
		if sid != nil {
			v := *sid
			j.sid = &v
		}
		r.jobs[key] = j
	}
	j.refs++
	release := func() { r.mu.Lock(); j.refs--; r.mu.Unlock() }
	return j, release, nil
}
func (j *uploadJob) matches(sid *int64, p *types.ResumableParams) bool {
	return (j.sid == nil && sid == nil || j.sid != nil && sid != nil && *j.sid == *sid) && j.params.ResumableTotalSize == p.ResumableTotalSize && j.params.ResumableTotalChunks == p.ResumableTotalChunks && j.params.ResumableChunkSize == p.ResumableChunkSize && j.params.ResumableFilename == p.ResumableFilename && j.params.ResumableRelativePath == p.ResumableRelativePath
}
func (s *SiteService) runUploadJob(ctx context.Context, j *uploadJob) {
	var err error
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("upload worker panic: %v", recovered)
			j.mu.Lock()
			j.retryable = false
			j.mu.Unlock()
			s.SSK.SetFailed(j.tracking, "upload interrupted")
		}
		j.mu.Lock()
		defer j.mu.Unlock()
		if err != nil {
			j.state = "failed"
			if status := s.SSK.Get(j.tracking); status == nil || status.Status != "failed" {
				s.SSK.SetFailed(j.tracking, "upload failed")
			}
			utils.LogCtx(ctx).Error(err)
		} else {
			j.state = "succeeded"
			// Only this worker owns cleanup. Failure leaves the completed job retained;
			// a duplicate completion never repeats database processing.
			if e := s.resumableUploadService.DeleteFile(j.uid, j.params.ResumableIdentifier, j.params.ResumableTotalChunks); e != nil {
				utils.LogCtx(ctx).Error(e)
			}
		}
		j.touched = time.Now()
	}()
	err = s.processReceivedResumableSubmission(context.WithoutCancel(ctx), j.uid, j.sid, &j.params, j.tracking, func() { j.mu.Lock(); j.retryable = false; j.mu.Unlock() })
}
