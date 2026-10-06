package integration_tests

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/stretchr/testify/require"
)

func TestSubmissionConcurrencyRepeatedDeletion(t *testing.T) {
	for _, kind := range []string{"file", "comment"} {
		t.Run(kind, func(t *testing.T) {
			app, f, pg := newCommentTransactionFixture(t)
			f.File(t, fixtureFile{ID: 9001, SubmissionID: 8001, UserID: 8102, At: fixtureEpoch.Add(time.Hour)})
			f.Comment(t, 9003, 8001, 8102, constants.ActionComment, fixtureEpoch.Add(2*time.Hour), nil)
			f.Rebuild(t, 8001)
			b := newCommentWriteBarrier(t, f, 8101)
			first := b.start(func(ctx context.Context) error {
				return receiveTransactionComments(app, ctx, []int64{8001}, constants.ActionAssignTesting, "false")
			})
			b.wait(t, 1)
			remove := func(ctx context.Context) error {
				if kind == "file" {
					return app.Service.SoftDeleteSubmissionFile(ctx, 9001, "concurrent duplicate")
				}
				return app.Service.SoftDeleteComment(ctx, 9003, "concurrent duplicate")
			}
			a, c := b.start(remove), b.start(remove)
			b.waitForBlockedMutationCount(t, 8101, 2)
			b.release(t, 8101)
			awaitMutation(t, first)
			successes := 0
			for _, w := range []*concurrentMutation{a, c} {
				select {
				case <-w.done:
					if w.err == nil {
						successes++
					} else {
						var public constants.PublicError
						require.ErrorAs(t, w.err, &public)
						require.Equal(t, http.StatusNotFound, public.Status)
					}
				case <-time.After(20 * time.Second):
					t.Fatal("duplicate deletion did not finish")
				}
			}
			require.Equal(t, 1, successes)
			var events int
			require.NoError(t, pg.QueryRow(f.Ctx, "SELECT COUNT(*) FROM activity_events").Scan(&events))
			require.Equal(t, 2, events, "one assignment and one deletion; losing deletion writes no event")
			before := cacheSnapshot(t, f, 8001)
			f.Rebuild(t, 8001)
			require.Equal(t, before, cacheSnapshot(t, f, 8001))
		})
	}
}
