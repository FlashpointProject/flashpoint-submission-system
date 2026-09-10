package integration_tests

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func postgresSubmissionTests() bool { return os.Getenv("SUBMISSION_DB_ENGINE") == "postgres" }

// testSQL translates only fixture parameter markers, never application SQL.
// Application queries must execute their own PostgreSQL implementation.
func testSQL(query string) string {
	if !postgresSubmissionTests() {
		return query
	}
	var out strings.Builder
	parameter := 0
	quoted := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		if c == '\'' {
			if quoted && i+1 < len(query) && query[i+1] == '\'' {
				out.WriteString("''")
				i++
				continue
			}
			quoted = !quoted
		}
		if c == '?' && !quoted {
			parameter++
			fmt.Fprintf(&out, "$%d", parameter)
		} else {
			out.WriteByte(c)
		}
	}
	result := out.String()
	if strings.HasPrefix(result, "INSERT IGNORE INTO ") {
		result = strings.Replace(result, "INSERT IGNORE INTO ", "INSERT INTO ", 1) + " ON CONFLICT DO NOTHING"
	}
	return result
}

func openSubmissionTestDB(conf *config.Config) (*sql.DB, error) {
	if !postgresSubmissionTests() {
		return openMariaTestDB(conf)
	}
	return sql.Open("pgx", fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable&timezone=UTC", conf.PostgresUser, conf.PostgresPassword, conf.PostgresHost, conf.PostgresPort, conf.PostgresUser))
}

// MariaDB advances AUTO_INCREMENT after explicit fixture IDs. PostgreSQL identity
// sequences require the fixture to do that explicitly before later service writes.
func syncFixtureSequence(t *testing.T, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, table string) {
	t.Helper()
	if !postgresSubmissionTests() {
		return
	}
	_, err := db.ExecContext(context.Background(), fmt.Sprintf("SELECT setval(pg_get_serial_sequence('%s','id'),GREATEST(COALESCE((SELECT MAX(id) FROM %s),0),1),COALESCE((SELECT MAX(id) FROM %s),0)>0)", table, table, table))
	require.NoError(t, err)
}

func TestFixtureSQLDialect(t *testing.T) {
	t.Setenv("SUBMISSION_DB_ENGINE", "mariadb")
	source := "SELECT '?' AS literal, 'it''s ?' AS escaped FROM submission WHERE id=? AND deleted_reason=?"
	require.Equal(t, source, testSQL(source))
	t.Setenv("SUBMISSION_DB_ENGINE", "postgres")
	require.Equal(t, "SELECT '?' AS literal, 'it''s ?' AS escaped FROM submission WHERE id=$1 AND deleted_reason=$2", testSQL(source))
	require.Equal(t, "INSERT INTO action(id) VALUES ($1) ON CONFLICT DO NOTHING", testSQL("INSERT IGNORE INTO action(id) VALUES (?)"))
}
