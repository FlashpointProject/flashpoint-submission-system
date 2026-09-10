package service

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestUniqueConstraintError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"mysql duplicate", &mysql.MySQLError{Number: 1062}, true},
		{"postgres duplicate", &pgconn.PgError{Code: "23505"}, true},
		{"wrapped mysql duplicate", fmt.Errorf("insert: %w", &mysql.MySQLError{Number: 1062}), true},
		{"wrapped postgres duplicate", fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505"}), true},
		{"mysql foreign key", &mysql.MySQLError{Number: 1452}, false},
		{"postgres foreign key", &pgconn.PgError{Code: "23503"}, false},
		{"postgres not null", &pgconn.PgError{Code: "23502"}, false},
		{"message alone", errors.New("duplicate key 23505 1062"), false},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, isUniqueConstraintError(tc.err)) })
	}
}

type duplicateFileDAL struct {
	database.DAL
	err error
}

func (d duplicateFileDAL) SubscribeUserToSubmission(database.DBSession, int64, int64) error {
	return nil
}
func (d duplicateFileDAL) SearchSubmissions(database.DBSession, *types.SubmissionsFilter) ([]*types.ExtendedSubmission, int64, error) {
	return []*types.ExtendedSubmission{{}}, 1, nil
}
func (d duplicateFileDAL) StoreSubmissionFile(database.DBSession, *types.SubmissionFile) (int64, error) {
	return 0, d.err
}

func TestSubmissionFileUpdateDuplicateReturnsConflict(t *testing.T) {
	for _, backend := range []struct {
		name string
		err  error
	}{
		{"mariadb", &mysql.MySQLError{Number: 1062}},
		{"postgres", &pgconn.PgError{Code: "23505"}},
	} {
		t.Run(backend.name, func(t *testing.T) {
			s := &SiteService{dal: duplicateFileDAL{err: fmt.Errorf("store file: %w", backend.err)}, clock: &RealClock{}}
			_, err := s.handleSubmissionFileUpdate(context.Background(), nil, nil, "file.pack", "stored.pack", 10, 11, 101, "staff", nil, md5.New(), sha256.New(), false)
			var public constants.PublicError
			require.ErrorAs(t, err, &public)
			require.Equal(t, http.StatusConflict, public.Status)
			require.Contains(t, public.Msg, "already present in the DB")
		})
	}
}
