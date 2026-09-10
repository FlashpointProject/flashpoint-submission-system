package service

import (
	"sync"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

func TestSubmissionStatusKeeperReturnsIndependentSnapshot(t *testing.T) {
	s := SubmissionStatusKeeper{m: map[string]*types.SubmissionStatus{}}
	require.Nil(t, s.Get("missing"))
	s.SetReceived("upload")
	s.SetCopying("upload", "copy progress")
	before := s.Get("upload")
	s.SetFailed("upload", "quota failure")
	require.Equal(t, constants.SubmissionStatusCopying, before.Status)
	require.Equal(t, "copy progress", *before.Message)
	failure := s.Get("upload")
	*failure.Message = "caller modification"
	failure.Status = "caller modification"
	require.Equal(t, "quota failure", *s.Get("upload").Message)
	require.Equal(t, constants.SubmissionStatusFailed, s.Get("upload").Status)
	stableFailure := s.Get("upload")
	s.SetSuccess("upload", 42)
	success := s.Get("upload")
	*success.SubmissionID = 99
	require.EqualValues(t, 42, *s.Get("upload").SubmissionID)
	require.Equal(t, constants.SubmissionStatusFailed, stableFailure.Status)
	require.Equal(t, "quota failure", *stableFailure.Message)
}

func TestSubmissionStatusKeeperConcurrentPolling(t *testing.T) {
	s := SubmissionStatusKeeper{m: map[string]*types.SubmissionStatus{}}
	s.SetReceived("upload")
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for i := 0; i < 1000; i++ {
			s.SetReceived("upload")
			s.SetCopying("upload", "progress")
			s.SetValidating("upload")
			s.SetFinalizing("upload")
			s.SetFailed("upload", "failure")
			s.SetSuccess("upload", 42)
		}
	}()
	defer workers.Wait()
	for i := 0; i < 1000; i++ {
		status := s.Get("upload")
		if status.Status == constants.SubmissionStatusSuccess {
			require.NotNil(t, status.SubmissionID)
			require.EqualValues(t, 42, *status.SubmissionID)
			require.Nil(t, status.Message)
		}
		if status.Message != nil {
			_ = *status.Message
		}
	}
}
