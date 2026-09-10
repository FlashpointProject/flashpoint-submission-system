package main

import (
	"database/sql/driver"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

func TestRecordingDriverCannotBypassOpen(t *testing.T) {
	// Embedding MySQLDriver promotes OpenConnector and bypasses our Open wrapper.
	if _, ok := any(recordingDriver{}).(driver.DriverContext); ok {
		t.Fatal("DriverContext bypasses recording connections")
	}
}
func TestPageCardinality(t *testing.T) {
	for _, s := range []struct {
		name  string
		f     types.SubmissionsFilter
		total int64
		got   int
		valid bool
	}{
		{"full", types.SubmissionsFilter{}, 101, 100, true},
		{"truncated", types.SubmissionsFilter{}, 101, 99, false},
		{"last", types.SubmissionsFilter{Page: ptr(int64(2))}, 101, 1, true},
		{"past-end", types.SubmissionsFilter{Page: ptr(int64(3))}, 101, 0, true},
		{"empty", types.SubmissionsFilter{}, 0, 0, true},
		{"custom-page-size", types.SubmissionsFilter{ResultsPerPage: ptr(int64(3)), Page: ptr(int64(2))}, 5, 2, true},
	} {
		t.Run(s.name, func(t *testing.T) {
			if valid := checkPage(s.f, s.total, s.got) == nil; valid != s.valid {
				t.Fatal("incorrect page validation")
			}
		})
	}
}
func TestCanonicalRetainsDuplicatesNullsAndResultOrder(t *testing.T) {
	rows := []*types.ExtendedSubmission{{SubmissionID: 2, ApprovedUserIDs: []int64{3, 1, 3}}, {SubmissionID: 1, CurationTitle: ptr("")}}
	canonical(rows)
	if rows[0].SubmissionID != 2 || len(rows[0].ApprovedUserIDs) != 3 || rows[0].ApprovedUserIDs[0] != 1 {
		t.Fatal("order/duplicate contract broken")
	}
	if rows[0].CurationTitle != nil || rows[1].CurationTitle == nil {
		t.Fatal("NULL/empty contract broken")
	}
	before := digest(corpus{2, rows})
	canonical(rows)
	if digest(corpus{2, rows}) != before {
		t.Fatal("canonicalization not repeatable")
	}
	rows[0], rows[1] = rows[1], rows[0]
	if digest(corpus{2, rows}) == before {
		t.Fatal("result ordering must affect corpus digest")
	}
}
