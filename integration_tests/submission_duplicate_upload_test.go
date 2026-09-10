package integration_tests

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

// HTTP admission is asynchronous (200). The worker must expose the duplicate
// checksum error, roll back all writes and remove only the rejected archive.
// The synchronous public-error status is covered by the service classifier test.
func TestSubmissionDuplicateUploadRollback(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new submission"
		if existing {
			name = "existing submission"
		}
		t.Run(name, func(t *testing.T) {
			app, l, ctx, db, pgdb, raw, pg := setupIntegrationTest(t)
			ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)
			user := createExtendedTestUser(t, ctx, l, app, db, pgdb, 98009, []int64{roleIDCurator}, "duplicate upload")
			content, err := os.ReadFile("./test_files/Warpstar4K.7z")
			require.NoError(t, err)
			first := quotaWait(t, app, quotaAccepted(t, uploadSubmissionContent(t, l, app, user.Cookie, nil, content)))
			require.Equal(t, "success", first.Status, first.Message)
			require.NotNil(t, first.SubmissionID)
			f := &sqlFixture{DB: db, Maria: raw, Ctx: ctx}
			before := transactionSnapshot(t, f, pg, "curation_meta", "curation_image")
			var sid *int64
			if existing {
				sid = first.SubmissionID
			}
			duplicate := quotaWait(t, app, quotaAccepted(t, uploadSubmissionContent(t, l, app, user.Cookie, sid, content)))
			require.Equal(t, "failed", duplicate.Status)
			require.NotNil(t, duplicate.Message)
			require.Contains(t, *duplicate.Message, "already present in the DB")
			require.Contains(t, *duplicate.Message, "checksums md5:")
			require.Equal(t, before, transactionSnapshot(t, f, pg, "curation_meta", "curation_image"), "duplicate failure must preserve every persisted value")
			require.Eventually(t, func() bool {
				entries, err := os.ReadDir(app.Conf.SubmissionsDirFullPath)
				return err == nil && len(entries) == 1
			}, 5*time.Second, 10*time.Millisecond, "keep original archive and remove rejected duplicate")
		})
	}
}
