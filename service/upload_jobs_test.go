package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/resumableuploadservice"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type uploadJobTestDAL struct {
	database.DAL
	calls            atomic.Int32
	started, release chan struct{}
}

func (d *uploadJobTestDAL) NewSession(context.Context) (database.DBSession, error) {
	if d.calls.Add(1) == 1 {
		close(d.started)
	}
	<-d.release
	return nil, errors.New("transient DB failure")
}
func TestUploadJobsShareTrackingRetainFailedChunksAndRetryExplicitly(t *testing.T) {
	dal := &uploadJobTestDAL{started: make(chan struct{}), release: make(chan struct{})}
	rsu, e := resumableuploadservice.New(t.TempDir())
	require.NoError(t, e)
	s := &SiteService{dal: dal, resumableUploadService: rsu, randomStringProvider: testSubmissionRandomStringer{}}
	l := logrus.New()
	l.SetOutput(io.Discard)
	ctx := context.WithValue(context.Background(), utils.CtxKeys.UserID, int64(100001))
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, logrus.NewEntry(l))
	p := types.ResumableParams{ResumableIdentifier: "one-job", ResumableFilename: "test.7z", ResumableChunkNumber: 1, ResumableTotalChunks: 1, ResumableTotalSize: 3}
	first, e := s.ReceiveSubmissionChunk(ctx, nil, &p, []byte("zip"))
	require.NoError(t, e)
	<-dal.started
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name, e := s.ReceiveSubmissionChunk(ctx, nil, &p, []byte("zip"))
			require.NoError(t, e)
			require.Equal(t, first, name)
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, dal.calls.Load())
	sid := int64(12)
	_, e = s.ReceiveSubmissionChunk(ctx, &sid, &p, []byte("zip"))
	require.Error(t, e)
	close(dal.release)
	failed := func() bool {
		s.uploadJobs.mu.Lock()
		j := s.uploadJobs.jobs["100001:one-job"]
		s.uploadJobs.mu.Unlock()
		j.mu.Lock()
		defer j.mu.Unlock()
		return j.state == "failed"
	}
	require.Eventually(t, failed, time.Second, time.Millisecond)
	finished, e := rsu.IsUploadFinished(100001, "one-job", 1, 3)
	require.NoError(t, e)
	require.True(t, finished, "failure must preserve retry input")
	_, e = s.ReceiveSubmissionChunk(ctx, nil, &p, []byte("zip"))
	require.NoError(t, e)
	require.EqualValues(t, 1, dal.calls.Load(), "ordinary completion retry reuses failed job")
	p.ResumableRetry = 1
	name, e := s.ReceiveSubmissionChunk(ctx, nil, &p, []byte("zip"))
	require.NoError(t, e)
	require.Equal(t, first, name)
	require.Eventually(t, func() bool { return dal.calls.Load() == 2 && failed() }, time.Second, time.Millisecond)
	// A delayed retransmission from the same explicit retry must not retry again.
	_, e = s.ReceiveSubmissionChunk(ctx, nil, &p, []byte("zip"))
	require.NoError(t, e)
	require.EqualValues(t, 2, dal.calls.Load())
	p.ResumableRetry = 2
	s.uploadJobs.mu.Lock()
	j := s.uploadJobs.jobs["100001:one-job"]
	s.uploadJobs.mu.Unlock()
	j.mu.Lock()
	j.retryable = false
	j.mu.Unlock()
	_, e = s.ReceiveSubmissionChunk(ctx, nil, &p, []byte("zip"))
	require.ErrorContains(t, e, "already be committed")
}

func TestUploadJobRetentionPinsAndCapacity(t *testing.T) {
	rsu, err := resumableuploadservice.New(t.TempDir())
	require.NoError(t, err)
	s := &SiteService{resumableUploadService: rsu}
	p := types.ResumableParams{ResumableIdentifier: "expired", ResumableTotalChunks: 1, ResumableTotalSize: 3}
	old, unpin, err := s.acquireUploadJob(1, nil, &p)
	require.NoError(t, err)
	require.NoError(t, rsu.PutChunk(1, "expired", 1, []byte("zip")))
	old.touched = time.Now().Add(-2 * uploadJobRetention)
	old.tracking = "expired-status"
	s.SSK.SetReceived(old.tracking)
	p.ResumableIdentifier = "active"
	active, release, err := s.acquireUploadJob(1, nil, &p)
	require.NoError(t, err)
	require.Contains(t, s.uploadJobs.jobs, "1:expired", "pinned jobs cannot be swept")
	active.state = "processing"
	active.touched = old.touched
	release()
	unpin()
	p.ResumableIdentifier = "new"
	_, release, err = s.acquireUploadJob(1, nil, &p)
	require.NoError(t, err)
	release()
	require.NotContains(t, s.uploadJobs.jobs, "1:expired")
	require.Contains(t, s.uploadJobs.jobs, "1:active", "running jobs cannot expire")
	finished, err := rsu.IsUploadFinished(1, "expired", 1, 3)
	require.NoError(t, err)
	require.False(t, finished)
	for len(s.uploadJobs.jobs) < maxUploadJobs {
		s.uploadJobs.jobs[fmt.Sprint(len(s.uploadJobs.jobs))] = &uploadJob{state: "processing", touched: time.Now()}
	}
	p.ResumableIdentifier = "over-capacity"
	_, _, err = s.acquireUploadJob(1, nil, &p)
	require.ErrorContains(t, err, "capacity")
}
