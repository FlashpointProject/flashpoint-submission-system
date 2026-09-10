package database

import (
	"database/sql"
	"testing"
)

func TestSubmissionBackendSelection(t *testing.T) {
	mysqlDB, err := sql.Open("mysql", "user:pass@tcp(localhost:3306)/test")
	if err != nil {
		t.Fatal(err)
	}
	defer mysqlDB.Close()
	pgDB, err := sql.Open("pgx", "postgres://user:pass@localhost/test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer pgDB.Close()
	if _, ok := NewSubmissionDAL(mysqlDB).(*mysqlDAL); !ok {
		t.Fatal("MariaDB baseline selected wrong DAL")
	}
	if _, ok := NewSubmissionDAL(pgDB).(*postgresSubmissionDAL); !ok {
		t.Fatal("PostgreSQL mode selected wrong DAL")
	}
	if SubmissionPlaceholder(&MysqlSession{}, 2) != "?" || SubmissionPlaceholder(&PostgresSubmissionSession{}, 2) != "$2" {
		t.Fatal("locking/liveness placeholder uses wrong backend")
	}
}
