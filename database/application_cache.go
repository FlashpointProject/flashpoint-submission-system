package database

import "github.com/FlashpointProject/flashpoint-submission-system/appcache"

// changed records application read dependencies in the current transaction.
// Rollback drops these; Commit invalidates even on an ambiguous commit failure.
func changed(session any, dependencies ...appcache.Dependency) {
	switch s := session.(type) {
	case *MysqlSession:
		s.changes = append(s.changes, dependencies...)
	case *PostgresSession:
		s.changes = append(s.changes, dependencies...)
	}
}

// File/comment mutations also affect their parent submission. Resolve the parent
// on the writer transaction; never use a cached lookup here.
func changedFile(s DBSession, id int64) error {
	var sid int64
	if err := s.Tx().QueryRowContext(s.Ctx(), "SELECT fk_submission_id FROM submission_file WHERE id=?", id).Scan(&sid); err != nil {
		return err
	}
	changed(s, appcache.Key("submission", sid), appcache.Key("file", id))
	return nil
}
func changedComment(s DBSession, id int64) error {
	var sid int64
	if err := s.Tx().QueryRowContext(s.Ctx(), "SELECT fk_submission_id FROM comment WHERE id=?", id).Scan(&sid); err != nil {
		return err
	}
	changed(s, appcache.Key("submission", sid))
	return nil
}

// InvalidateSubmissionRead declares the dependency for internal repair callers.
func InvalidateSubmissionRead(s DBSession, id int64) { changed(s, appcache.Key("submission", id)) }
