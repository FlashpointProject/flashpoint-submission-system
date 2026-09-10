package main

import (
	"testing"
	"time"
)

func TestEndpointGuards(t *testing.T) {
	for _, tc := range []struct{ engine, dsn string }{
		{"mariadb", "root:p@tcp(mariadb:3306)/fpfss"},
		{"mariadb", "root:p@tcp(localhost:3306)/snapshot_workflow_test"},
		{"postgres", "postgres://u:p@postgres:5432/fpfss?sslmode=disable"},
		{"postgres", "postgres://u:p@localhost:5432/submission_import_test?sslmode=disable"},
		{"postgres", "postgres://u:p@postgres:5433/submission_import_test?sslmode=disable"},
		{"invalid", ""},
	} {
		t.Run(tc.engine+tc.dsn, func(t *testing.T) {
			db, e := openSubmission(tc.engine, tc.dsn)
			if db != nil {
				db.Close()
			}
			if e == nil {
				t.Fatal("unsafe DSN accepted")
			}
		})
	}
}
func TestSequenceRegistration(t *testing.T) {
	r := runner{out: &report{Sequences: map[string]*sequenceCheck{"comment": {BeforeMax: 100}}}}
	if r.register("comment", 102) != 1 || r.register("comment", 102) != 1 || r.register("comment", 105) != 2 {
		t.Fatal("logical identity mapping")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("reused source identity accepted")
		}
	}()
	r.register("comment", 100)
}
func TestFixedClockExactMicroseconds(t *testing.T) {
	c := fixedClock{epoch}
	if !c.Now().Equal(epoch) || c.Now().Nanosecond()%1000 != 0 {
		t.Fatal("clock loses precision")
	}
	if !c.Unix(1, 1000).Equal(time.Unix(1, 1000)) {
		t.Fatal("Unix")
	}
}
