package database

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/sirupsen/logrus"
)

// NewSubmissionDAL selects the submission implementation without changing the
// existing launcher PostgreSQL DAL or merging its tables/behavior.
func NewSubmissionDAL(db *sql.DB) DAL {
	if _, ok := db.Driver().(*stdlib.Driver); ok {
		return NewPostgresSubmissionDAL(db)
	}
	if d, ok := db.Driver().(interface{ PostgreSQL() bool }); ok && d.PostgreSQL() {
		return NewPostgresSubmissionDAL(db)
	}
	return NewMysqlDAL(db)
}

func IsPostgresSubmissionSession(s DBSession) bool {
	p, ok := s.(interface{ PostgreSQL() bool })
	return ok && p.PostgreSQL()
}

// SubmissionPlaceholder is for the few service-owned liveness/locking queries.
func SubmissionPlaceholder(s DBSession, n int) string {
	if IsPostgresSubmissionSession(s) {
		return "$" + strconv.Itoa(n)
	}
	return "?"
}

func openPostgresSubmissionDB(l *logrus.Entry, c *config.Config) *sql.DB {
	// The existing launcher connection uses its PostgreSQL role name as database
	// name. Submission mode targets that same database, without MariaDB credentials.
	u := url.URL{Scheme: "postgres", User: url.UserPassword(c.PostgresUser, c.PostgresPassword), Host: net.JoinHostPort(c.PostgresHost, strconv.FormatInt(c.PostgresPort, 10)), Path: "/" + c.PostgresUser}
	q := u.Query()
	q.Set("sslmode", "disable")
	q.Set("timezone", "UTC")
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		l.Fatal("open PostgreSQL submission connection: ", err)
	}
	if err = db.PingContext(context.Background()); err != nil {
		l.Fatal(fmt.Errorf("connect PostgreSQL submission database: %w", err))
	}
	return db
}
