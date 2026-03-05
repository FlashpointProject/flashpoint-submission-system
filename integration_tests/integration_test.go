package integration_tests

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// TestPrintSchema prints the MariaDB schema for all tables with timestamp columns.
func TestPrintSchema(t *testing.T) {
	_, _, _, _, _, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	rows, err := maria.Query(`
		SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE()
		AND (COLUMN_NAME LIKE '%_at' OR COLUMN_NAME LIKE 'date_%')
		ORDER BY TABLE_NAME, ORDINAL_POSITION`)
	require.NoError(t, err)
	defer rows.Close()

	t.Log("=== MariaDB Timestamp Columns ===")
	for rows.Next() {
		var tableName, colName, colType, nullable string
		var colDefault *string
		err := rows.Scan(&tableName, &colName, &colType, &nullable, &colDefault)
		require.NoError(t, err)
		def := "NULL"
		if colDefault != nil {
			def = *colDefault
		}
		t.Logf("%-45s %-15s nullable=%-3s default=%s", tableName+"."+colName, colType, nullable, def)
	}
}

// TestFileSubmission verifies submission upload through resumable upload service.
func TestFileSubmission(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	// Prepare user with curator role
	const SubmitterID = 42
	authToken := createTestUser(t, ctx, l, app, db, pgdb, SubmitterID, []int64{442665038642413569})

	// Upload submission
	cookie := createTestCookie(t, l, authToken)
	submissionFilename := "./test_files/Warpstar4K.7z"
	submissionID := uploadTestSubmission(t, l, app, submissionFilename, cookie, nil)

	// Verify submission
	verifyTestSubmissionExists(t, ctx, l, app, SubmitterID, submissionID)

	// Check comments
	rctx := addContextValues(ctx, l, SubmitterID, fmt.Sprintf("req_GetViewSubmissionPageData_%d", submissionID))
	viewData, err := app.Service.GetViewSubmissionPageData(rctx, SubmitterID, submissionID)
	require.NoError(t, err)

	comments := viewData.Comments
	c1 := comments[0]
	c2 := comments[1]
	if !(c1.AuthorID == SubmitterID && c1.Action == constants.ActionUpload) {
		require.Fail(t, "upload comment not found")
	}
	if !(c2.AuthorID == constants.ValidatorID && c2.Action == constants.ActionApprove) {
		require.Fail(t, "validator comment not found")
	}
}

// TestApproveVerifySubmission verifies basic happy path of submission flow.
func TestApproveVerifySubmission(t *testing.T) {
	app, l, ctx, db, pgdb, maria, postgres := setupIntegrationTest(t)
	defer maria.Close()
	defer postgres.Close()

	ctx = context.WithValue(ctx, utils.CtxKeys.Log, l)

	const SubmitterID = 100000101
	const TesterID = 100000102
	const VerifierID = 100000103
	const AdderID = 100000104

	// Create users
	// Submitter: Curator
	submitterAuth := createTestUser(t, ctx, l, app, db, pgdb, SubmitterID, []int64{442665038642413569})
	// Tester: Tester
	_ = createTestUser(t, ctx, l, app, db, pgdb, TesterID, []int64{442988314480476170})
	// Verifier: Tester
	_ = createTestUser(t, ctx, l, app, db, pgdb, VerifierID, []int64{442988314480476170})
	// Adder: Moderator
	_ = createTestUser(t, ctx, l, app, db, pgdb, AdderID, []int64{442462642599231499})

	// 1. Submitter uploads
	submitterCookie := createTestCookie(t, l, submitterAuth)
	submissionFilename := "./test_files/Warpstar4K.7z"
	submissionID := uploadTestSubmission(t, l, app, submissionFilename, submitterCookie, nil)
	l.Infof("submission %d uploaded", submissionID)

	// 2. Tester assigns and approves
	conf := app.Conf
	req := httptest.NewRequest("POST", "/", nil) // Dummy request

	// Tester assigns
	err := app.Service.ReceiveComments(ctx, TesterID, []int64{submissionID}, constants.ActionAssignTesting, "", "false",
		conf.SubmissionsDirFullPath, conf.DataPacksDir, conf.FrozenPacksDir, conf.ImagesDir, req)
	require.NoError(t, err)

	// Tester approves
	err = app.Service.ReceiveComments(ctx, TesterID, []int64{submissionID}, constants.ActionApprove, "Looks good", "false",
		conf.SubmissionsDirFullPath, conf.DataPacksDir, conf.FrozenPacksDir, conf.ImagesDir, req)
	require.NoError(t, err)

	// 3. Verifier assigns and verifies
	// Verifier assigns
	err = app.Service.ReceiveComments(ctx, VerifierID, []int64{submissionID}, constants.ActionAssignVerification, "", "false",
		conf.SubmissionsDirFullPath, conf.DataPacksDir, conf.FrozenPacksDir, conf.ImagesDir, req)
	require.NoError(t, err)

	// Verifier verifies
	err = app.Service.ReceiveComments(ctx, VerifierID, []int64{submissionID}, constants.ActionVerify, "", "false",
		conf.SubmissionsDirFullPath, conf.DataPacksDir, conf.FrozenPacksDir, conf.ImagesDir, req)
	require.NoError(t, err)

	// 4. Adder adds
	// Adder marks as added
	err = app.Service.ReceiveComments(ctx, AdderID, []int64{submissionID}, constants.ActionMarkAdded, "Added to Flashpoint", "false",
		conf.SubmissionsDirFullPath, conf.DataPacksDir, conf.FrozenPacksDir, conf.ImagesDir, req)
	require.NoError(t, err)

	// Verify final state
	viewData, err := app.Service.GetViewSubmissionPageData(ctx, SubmitterID, submissionID)
	require.NoError(t, err)

	require.NotEmpty(t, viewData.Submissions)
	sub := viewData.Submissions[0]
	hasMarkAdded := false
	for _, action := range sub.DistinctActions {
		if action == constants.ActionMarkAdded {
			hasMarkAdded = true
			break
		}
	}
	require.True(t, hasMarkAdded, "submission should be marked as added")
}
