package database

import (
	"errors"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

type searchCountFallback struct {
	DAL
	called bool
	filter *types.SubmissionsFilter
	err    error
}

func (d *searchCountFallback) SearchSubmissions(_ DBSession, f *types.SubmissionsFilter) ([]*types.ExtendedSubmission, int64, error) {
	d.called = true
	d.filter = f
	return nil, 42, d.err
}

type dedicatedCount struct{ searchCountFallback }

func (d *dedicatedCount) CountSubmissions(_ DBSession, f *types.SubmissionsFilter) (int64, error) {
	d.filter = f
	return 73, d.err
}

func TestCountSubmissionsDispatch(t *testing.T) {
	filter := &types.SubmissionsFilter{ExcludeLegacy: true}
	fallback := &searchCountFallback{}
	count, err := CountSubmissions(fallback, nil, filter)
	require.NoError(t, err)
	require.EqualValues(t, 42, count)
	require.True(t, fallback.called)
	require.Same(t, filter, fallback.filter)
	dedicated := &dedicatedCount{}
	count, err = CountSubmissions(dedicated, nil, filter)
	require.NoError(t, err)
	require.EqualValues(t, 73, count)
	require.False(t, dedicated.called)
	require.Same(t, filter, dedicated.filter)
	sentinel := errors.New("database failure")
	fallback.err, dedicated.err = sentinel, sentinel
	_, err = CountSubmissions(fallback, nil, filter)
	require.ErrorIs(t, err, sentinel)
	_, err = CountSubmissions(dedicated, nil, filter)
	require.ErrorIs(t, err, sentinel)
}

type actionCountFallback struct {
	DAL
	called bool
	uid    int64
	action string
	err    error
}

func (d *actionCountFallback) GetCommentsByUserIDAndAction(_ DBSession, uid int64, action string) ([]*types.Comment, error) {
	d.called, d.uid, d.action = true, uid, action
	return []*types.Comment{{ID: 1}, {ID: 2}}, d.err
}

type dedicatedActionCount struct{ actionCountFallback }

func (d *dedicatedActionCount) CountCommentsByUserIDAndAction(_ DBSession, uid int64, action string) (int64, error) {
	d.uid, d.action = uid, action
	return 73, d.err
}
func TestCountCommentsByUserIDAndActionDispatch(t *testing.T) {
	fallback := &actionCountFallback{}
	count, err := CountCommentsByUserIDAndAction(fallback, nil, 123, "approve")
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	require.True(t, fallback.called)
	require.EqualValues(t, 123, fallback.uid)
	require.Equal(t, "approve", fallback.action)
	dedicated := &dedicatedActionCount{}
	count, err = CountCommentsByUserIDAndAction(dedicated, nil, 456, "verify")
	require.NoError(t, err)
	require.EqualValues(t, 73, count)
	require.False(t, dedicated.called)
	require.EqualValues(t, 456, dedicated.uid)
	require.Equal(t, "verify", dedicated.action)
	sentinel := errors.New("database failure")
	fallback.err, dedicated.err = sentinel, sentinel
	_, err = CountCommentsByUserIDAndAction(fallback, nil, 123, "approve")
	require.ErrorIs(t, err, sentinel)
	_, err = CountCommentsByUserIDAndAction(dedicated, nil, 123, "approve")
	require.ErrorIs(t, err, sentinel)
}
