package main

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestCacheProjectionUsesOnlyRealColumns(t *testing.T) {
	selectList := strings.Split(strings.TrimPrefix(cacheSelectSQL, "SELECT "), "FROM")[0]
	cols := strings.Split(selectList, ",")
	if len(cols) != 15 {
		t.Fatalf("want all 15 cache columns, got %d", len(cols))
	}
	for _, col := range cols {
		if strings.TrimSpace(col) == "id" {
			t.Fatal("submission_cache has no id column")
		}
	}
	if strings.Contains(cacheSelectSQL, "SELECT *") || !strings.HasSuffix(cacheSelectSQL, "ORDER BY fk_submission_id") {
		t.Fatal("projection/order must be explicit and valid")
	}
}

func TestCanonicalCacheIgnoresPhysicalColumnOrder(t *testing.T) {
	a := canonicalCacheColumns([]string{"fk_submission_id", "active_verified_ids", "original_filename_sequence"}, []sql.NullString{{"9007199254740993", true}, {"12,2,12", true}, {"z,a", true}}, true)
	b := canonicalCacheColumns([]string{"original_filename_sequence", "active_verified_ids", "fk_submission_id"}, []sql.NullString{{"z,a", true}, {"12,12,2", true}, {"9007199254740993", true}}, true)
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	if string(aj) != string(bj) {
		t.Fatal("physical column order changed canonical cache")
	}
	if a["active_verified_ids"].String != "12,12,2" {
		t.Fatal("duplicate reviewer removed")
	}
	if a["original_filename_sequence"].String != "z,a" {
		t.Fatal("filename comma sequence was reordered")
	}
}

func TestCanonicalCachePreservesNullEmptyAndHashDuplicates(t *testing.T) {
	row := canonicalCacheColumns([]string{"active_approved_ids", "active_verified_ids", "md5sum_sequence"}, []sql.NullString{{"", false}, {"", true}, {"b,a,b", true}}, true)
	if row["active_approved_ids"].Valid || !row["active_verified_ids"].Valid {
		t.Fatal("NULL and empty collapsed")
	}
	if row["md5sum_sequence"].String != "a,b,b" {
		t.Fatal("hash duplicates changed")
	}
}
