package main

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"math"
	"strings"
	"testing"
	"time"
)

func TestNormalizePreservesData(t *testing.T) {
	for _, s := range []string{"", "a,b.zip", "résumé_汉字%\\", `{"x":1,"x":2}`, "null"} {
		v, e := normalize([]byte(s), column{name: "title"})
		if e != nil || v != s {
			t.Fatalf("%q: %v %v", s, v, e)
		}
	}
	if v, e := normalize(nil, column{name: "title"}); e != nil || v != nil {
		t.Fatal(v, e)
	}
	for _, s := range []string{"0", "9007199254740993", "9223372036854775807"} {
		v, e := normalize([]byte(s), column{name: "id", kind: "bigint"})
		if e != nil {
			t.Fatal(e)
		}
		if _, ok := v.(int64); !ok {
			t.Fatalf("ID remained %T", v)
		}
	}
	if _, e := normalize([]byte("9223372036854775808"), column{name: "id", kind: "bigint"}); e == nil {
		t.Fatal("accepted overflowing ID")
	}
	v, err := normalize([]byte("1"), column{name: "mfa_enabled", kind: "bigint"})
	if err != nil || v != true {
		t.Fatal("MFA source bytes conversion", v, err)
	}
	for _, n := range []int64{0, 1} {
		v, e := normalize(n, column{name: "game_exists"})
		if e != nil || v != (n == 1) {
			t.Fatal(v, e)
		}
	}
	for _, n := range []int64{-1, 2} {
		if _, e := normalize(n, column{name: "should_autofreeze"}); e == nil {
			t.Fatal("accepted invalid boolean")
		}
	}
	for _, s := range []string{"embedded\x00nul", string([]byte{255})} {
		if _, e := normalize(s, column{name: "title"}); e == nil {
			t.Fatal("accepted invalid text")
		}
	}
	if _, e := normalize("invalid", column{name: "additional_applications"}); e == nil {
		t.Fatal("accepted invalid JSON")
	}
	// JSON text is not normalized: preserve duplicate keys, whitespace and null.
	for _, s := range []string{`{"x":1,"x":2}`, "  null  "} {
		v, e := normalize(s, column{name: "additional_applications"})
		if e != nil || v != s {
			t.Fatal(v, e)
		}
	}
	ts := time.Date(1000, 1, 1, 0, 0, 0, 123456000, time.UTC)
	v, e := normalize(ts, column{name: "created_at"})
	if e != nil || v != ts {
		t.Fatal(v, e)
	}
	if _, e := normalize(time.Time{}, column{}); e == nil {
		t.Fatal("accepted zero date")
	}
}
func TestCanonicalDigestDistinguishesNullEmptyAndMicros(t *testing.T) {
	digest := func(v []any) string {
		h := sha256.New()
		if e := hashRow(h, v); e != nil {
			t.Fatal(e)
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	if digest([]any{nil}) == digest([]any{""}) {
		t.Fatal("NULL conflated with empty")
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 123456000, time.UTC)
	if digest([]any{now}) == digest([]any{now.Add(time.Microsecond)}) {
		t.Fatal("microseconds lost")
	}
	if digest([]any{int64(9007199254740992)}) == digest([]any{int64(9007199254740993)}) {
		t.Fatal("integer precision lost")
	}
	if digest([]any{now}) != digest([]any{now.In(time.FixedZone("offset", 3600))}) {
		t.Fatal("UTC instant depends on location")
	}
}

func TestImporterRejectsReferenceOrUnisolatedTargets(t *testing.T) {
	for _, dsn := range []string{"postgres://u:p@postgres/fpfss", "postgres://u:p@production/submission_import_test", "postgres://u:p@localhost/submission_import_test"} {
		c, e := pgx.ParseConfig(dsn)
		if e != nil {
			t.Fatal(e)
		}
		if validateTarget(c) == nil {
			t.Fatalf("accepted forbidden target %s", c.Database)
		}
	}
	c, e := pgx.ParseConfig("postgres://u:p@postgres/submission_import_test")
	if e != nil || validateTarget(c) != nil {
		t.Fatal(e)
	}
	for _, dsn := range []string{"u:p@tcp(production:3306)/fpfss", "u:p@tcp(mariadb:3306)/live", "u:p@unix(/var/run/mysql.sock)/fpfss"} {
		c, e := mysql.ParseDSN(dsn)
		if e != nil {
			t.Fatal(e)
		}
		if validateSource(c) == nil {
			t.Fatal("accepted forbidden source")
		}
	}
}

func TestIdentityReseedPreservesHighWaterAndCannotCollide(t *testing.T) {
	for _, tc := range []struct{ source, max, want int64 }{{1, 0, 1}, {500, 12, 500}, {2, 12, 13}, {0, 0, 1}} {
		n, e := nextIdentity(tc.source, tc.max)
		if e != nil || n != tc.want {
			t.Fatal(tc, n, e)
		}
	}
	if _, e := nextIdentity(math.MaxInt64, math.MaxInt64); e == nil {
		t.Fatal("wrapped exhausted sequence")
	}
}

// Row checksums cannot detect changes to equality/LIKE semantics. In particular,
// a database-wide conversion to unicode_ci would silently break OAuth behavior.
func TestSourceCollationGuard(t *testing.T) {
	valid := []sourceTextColumn{
		{"discord_user", "username", "utf8mb4_unicode_ci"},
		{"submission_file", "current_filename", "utf8mb4_unicode_ci"},
		{"session", "secret", "utf8mb4_unicode_ci"},
		{"curation_meta", "title", "utf8mb4_unicode_ci"},
		{"curation_meta", "additional_applications", "utf8mb4_bin"},
		{"oauth_client", "client_id", "utf8mb4_general_ci"},
		{"oauth_client", "client_secret", "utf8mb4_general_ci"},
	}
	if err := validateSourceTextCollations(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []sourceTextColumn{
		{"curation_meta", "title", "utf8mb4_general_ci"},
		{"submission_file", "current_filename", "utf8mb4_bin"},
		{"session", "secret", "utf8mb4_general_ci"},
		{"oauth_client", "client_id", "utf8mb4_unicode_ci"},
		{"oauth_client", "client_secret", "utf8mb4_unicode_ci"},
		{"curation_meta", "additional_applications", "utf8mb4_unicode_ci"},
		{"masterdb_game", "uuid", "utf8mb4_uca1400_ai_ci"},
	} {
		t.Run(tc.table+"/"+tc.name, func(t *testing.T) {
			columns := append(append([]sourceTextColumn{}, valid...), tc)
			err := validateSourceTextCollations(columns)
			if err == nil || !strings.Contains(err.Error(), tc.table+"."+tc.name) || !strings.Contains(err.Error(), "collation="+tc.collation) {
				t.Fatalf("expected actionable collation rejection, got %v", err)
			}
		})
	}
}

func TestImporterColumnCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name           string
		source, target []string
		want           string
	}{
		{"reordered columns allowed", []string{"id", "deleted_at", "title"}, []string{"id", "title", "deleted_at"}, ""},
		{"missing nullable field", []string{"id", "title"}, []string{"id", "title", "deleted_at"}, "missing source columns=[deleted_at]"},
		{"missing defaulted field", []string{"id"}, []string{"id", "should_autofreeze"}, "missing source columns=[should_autofreeze]"},
		{"extra source field", []string{"id", "unknown"}, []string{"id"}, "extra source columns=[unknown]"},
		{"renamed field", []string{"id", "old_name"}, []string{"id", "new_name"}, "missing source columns=[new_name]; extra source columns=[old_name]"},
		{"stable diagnostics", []string{"id"}, []string{"z", "id", "a"}, "missing source columns=[a z]"},
		{"missing target table", []string{"id"}, nil, "extra source columns=[id]"},
		{"both absent", nil, nil, "source=0 target=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var source []column
			for _, name := range tc.source {
				source = append(source, column{name: name})
			}
			err := compareColumnNames("submission", source, tc.target)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "submission") {
				t.Fatalf("want diagnostic %q, got %v", tc.want, err)
			}
		})
	}
}
