package appcache

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCommitRejectsOldLoadAndInvalidatesOnlyDependencies(t *testing.T) {
	coord := &Coordinator{}
	c := New[[]string](coord, 10, 10000)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(context.Context) (Snapshot[[]string], error) {
		n := calls.Add(1)
		if n == 1 {
			close(started)
			<-release
			return Snapshot[[]string]{Value: []string{"old"}, Dependencies: []Dependency{"a"}}, nil
		}
		return Snapshot[[]string]{Value: []string{"new"}, Dependencies: []Dependency{"a"}}, nil
	}
	result := make(chan []string, 1)
	go func() { v, _ := c.Get(context.Background(), "a", load); result <- v }()
	<-started
	require.NoError(t, coord.Commit([]Dependency{"a"}, func() error { return nil }))
	close(release)
	require.Equal(t, []string{"new"}, <-result)
	require.EqualValues(t, 2, calls.Load())
	require.NoError(t, coord.Commit([]Dependency{"unrelated"}, func() error { return nil }))
	v, e := c.Get(context.Background(), "a", load)
	require.NoError(t, e)
	require.Equal(t, []string{"new"}, v)
	require.EqualValues(t, 2, calls.Load())
	v[0] = "caller mutation"
	v, e = c.Get(context.Background(), "a", load)
	require.NoError(t, e)
	require.Equal(t, "new", v[0])
	// A commit failure may be an ambiguous success; evict conservatively.
	failure := errors.New("connection lost during commit")
	require.ErrorIs(t, coord.Commit([]Dependency{"a"}, func() error { return failure }), failure)
	_, e = c.Get(context.Background(), "a", load)
	require.NoError(t, e)
	require.EqualValues(t, 3, calls.Load())
}
func TestSharedLoadIndependentCancellationAndFailureRetry(t *testing.T) {
	c := New[int](&Coordinator{}, 10, 100)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(ctx context.Context) (Snapshot[int], error) {
		calls.Add(1)
		close(started)
		<-release
		return Snapshot[int]{Value: 42}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := c.Get(ctx, "a", load); done <- e }()
	<-started
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := c.Get(context.Background(), "a", load)
			require.NoError(t, e)
			require.Equal(t, 42, v)
		}()
	}
	close(release)
	wg.Wait()
	require.EqualValues(t, 1, calls.Load())
	for i := 0; i < 2; i++ {
		_, e := c.Get(context.Background(), "failure", func(context.Context) (Snapshot[int], error) { return Snapshot[int]{}, errors.New("offline") })
		require.Error(t, e)
	}
	require.EqualValues(t, 2, c.Stats().Errors)
}
func TestExpiryAndBoundedEviction(t *testing.T) {
	coord := &Coordinator{}
	c := New[string](coord, 2, 10)
	now := time.Now()
	c.now = func() time.Time { return now }
	calls := 0
	load := func(context.Context) (Snapshot[string], error) {
		calls++
		return Snapshot[string]{Value: "abc", Expires: now.Add(time.Second)}, nil
	}
	for _, key := range []string{"a", "b", "c"} {
		_, e := c.Get(context.Background(), key, load)
		require.NoError(t, e)
	}
	require.Equal(t, 2, c.Stats().Entries)
	require.LessOrEqual(t, c.Stats().Bytes, 10)
	coord.mu.Lock()
	now = now.Add(time.Second)
	coord.mu.Unlock()
	_, e := c.Get(context.Background(), "c", load)
	require.NoError(t, e)
	require.Equal(t, 4, calls)
	// Oversized values are usable but never retained.
	_, e = c.Get(context.Background(), "huge", func(context.Context) (Snapshot[string], error) { return Snapshot[string]{Value: "01234567890"}, nil })
	require.NoError(t, e)
	require.EqualValues(t, 1, c.Stats().Evictions)
}

func TestCommitDoesNotBlockUnrelatedWarmReads(t *testing.T) {
	coord := &Coordinator{}
	c := New[int](coord, 3, 100)
	load := func(d Dependency) func(context.Context) (Snapshot[int], error) {
		return func(context.Context) (Snapshot[int], error) {
			return Snapshot[int]{Value: 1, Dependencies: []Dependency{d}}, nil
		}
	}
	_, e := c.Get(context.Background(), "a", load("a"))
	require.NoError(t, e)
	_, e = c.Get(context.Background(), "b", load("b"))
	require.NoError(t, e)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_ = coord.Commit([]Dependency{"a"}, func() error { close(started); <-release; return nil })
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	v, e := c.Get(ctx, "b", load("b"))
	require.NoError(t, e)
	require.Equal(t, 1, v)
	_, e = c.Get(ctx, "a", load("a"))
	require.ErrorIs(t, e, context.DeadlineExceeded)
	close(release)
	<-done
	_, e = c.Get(context.Background(), "a", load("a"))
	require.NoError(t, e)
}

func TestTenMinuteTTLExpiresWithoutSlidingOnHits(t *testing.T) {
	coord := &Coordinator{}
	cache := New[int](coord, 1, 100)
	now := time.Now()
	cache.now = func() time.Time { return now }
	calls := 0
	load := func(context.Context) (Snapshot[int], error) {
		calls++
		return Snapshot[int]{Value: calls, Expires: now.Add(10 * time.Minute)}, nil
	}
	value, err := cache.Get(context.Background(), "stats", load)
	require.NoError(t, err)
	require.Equal(t, 1, value)
	for _, elapsed := range []time.Duration{9 * time.Minute, time.Minute - time.Nanosecond, time.Nanosecond} {
		coord.mu.Lock()
		now = now.Add(elapsed)
		coord.mu.Unlock()
		value, err = cache.Get(context.Background(), "stats", load)
		require.NoError(t, err)
		if elapsed == time.Nanosecond {
			require.Equal(t, 2, value)
		} else {
			require.Equal(t, 1, value)
		}
	}
}
