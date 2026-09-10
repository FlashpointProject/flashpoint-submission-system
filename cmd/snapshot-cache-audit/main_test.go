package main

import (
	"database/sql"
	"sync/atomic"
	"testing"
)

func value(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
func TestComparisonPreservesMeaning(t *testing.T) {
	a := snapshot{7: {{"active_approved_ids": value("12,112"), "original_filename_sequence": value("a,b")}}}
	b := snapshot{7: {{"active_approved_ids": value("112,12"), "original_filename_sequence": value("a,b")}}}
	if compare(a, b, false).Rows != 1 || compare(a, b, true).Rows != 0 {
		t.Fatal("only known collection ordering may normalize")
	}
	b[7][0]["active_approved_ids"] = value("12,12,112")
	if compare(a, b, true).Rows != 1 {
		t.Fatal("duplicates must survive")
	}
	b[7][0]["active_approved_ids"] = value("12,112")
	b[7][0]["original_filename_sequence"] = value("b,a")
	if compare(a, b, true).Rows != 1 {
		t.Fatal("filenames cannot safely tokenize")
	}
	a[7][0]["active_approved_ids"] = sql.NullString{}
	b[7][0]["active_approved_ids"] = value("")
	if compare(a, b, true).Fields["active_approved_ids"] != 1 {
		t.Fatal("NULL differs from empty")
	}
}
func TestComparisonMissingDuplicateAndSamples(t *testing.T) {
	a := snapshot{3: {{"bot_action": value("approve")}}}
	b := snapshot{3: {{"bot_action": value("approve")}, {"bot_action": value("approve")}}, 4: {{"bot_action": sql.NullString{}}}}
	d := compare(a, b, true)
	if d.Rows != 2 || d.Fields["cache_row_count"] != 2 || len(d.SampleIDs) != 2 || d.SampleIDs[0] != 3 {
		t.Fatalf("unexpected diff: %+v", d)
	}
	if compare(b, b, false).Rows != 0 {
		t.Fatal("snapshot must equal itself")
	}
}

func TestRebuildBatchVisitsOnceAndPreservesFailureOrder(t *testing.T) {
	ids := make([]int64, 1003)
	counts := make([]atomic.Int64, len(ids))
	for i := range ids {
		ids[i] = int64(i)
	}
	outcomes := rebuildBatch(ids, 4, func(id int64) *failure {
		counts[id].Add(1)
		if id%7 == 0 {
			return &failure{ID: id, Stage: "test"}
		}
		return nil
	})
	for i, f := range outcomes {
		if counts[i].Load() != 1 {
			t.Fatalf("id %d visited %d times", i, counts[i].Load())
		}
		if (f != nil) != (i%7 == 0) || f != nil && f.ID != int64(i) {
			t.Fatalf("wrong outcome for id %d", i)
		}
	}
	if got := rebuildBatch(nil, 4, func(int64) *failure { t.Error("empty batch visited"); return nil }); len(got) != 0 {
		t.Fatal("empty batch results")
	}
}
