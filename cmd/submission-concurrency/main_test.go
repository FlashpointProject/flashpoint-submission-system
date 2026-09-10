package main

import "testing"

func TestRejectNonSnapshotTargets(t *testing.T) {
	for _, dsn := range []string{"postgres://u:p@localhost/submission_import_probe", "postgres://u:p@postgres/fpfss", "postgres://u:p@postgres/postgres"} {
		db, err := connect(dsn, "test")
		if db != nil {
			db.Close()
		}
		if err == nil {
			t.Fatalf("accepted non-snapshot target %q", dsn)
		}
	}
}

func TestSummarySmallSamples(t *testing.T) {
	if s := summarize(nil); s.N != 0 {
		t.Fatal(s)
	}
	if s := summarize([]float64{7}); s.N != 1 || s.P50 != 7 || s.P95 != 7 || s.P99 != 7 {
		t.Fatal(s)
	}
	if s := summarize([]float64{9, 1, 5}); s.P50 != 5 || s.P95 != 9 || s.P99 != 9 {
		t.Fatal(s)
	}
}
