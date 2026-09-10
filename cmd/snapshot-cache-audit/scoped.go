package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/go-sql-driver/mysql"
)

type scopedPool struct {
	databases []*sql.DB
	available chan *sql.DB
	source    string
}

func newScopedPool(cfg *mysql.Config, workers int) (*scopedPool, error) {
	p := &scopedPool{source: cfg.DBName, available: make(chan *sql.DB, workers)}
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	defer admin.Close()
	for i := 0; i < workers; i++ {
		name := fmt.Sprintf("scoped_worker_%d", i)
		// Never touch a pre-existing schema: it may contain evidence from another run.
		if _, err = admin.Exec("CREATE DATABASE " + name); err != nil {
			return nil, fmt.Errorf("create scratch schema %s failed; use a clean audit environment", name)
		}
		c := *cfg
		c.DBName = name
		db, e := sql.Open("mysql", c.FormatDSN())
		if e != nil {
			return nil, e
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		p.databases = append(p.databases, db)
		for _, table := range []string{"submission", "comment", "submission_file"} {
			if _, e = db.Exec("CREATE TABLE " + table + " LIKE " + p.source + "." + table); e != nil {
				return nil, fmt.Errorf("create scratch table failed")
			}
		}
		for _, table := range []string{"action", "submission_cache"} {
			if _, e = db.Exec("CREATE VIEW " + table + " AS SELECT * FROM " + p.source + "." + table); e != nil {
				return nil, fmt.Errorf("create scratch view failed")
			}
		}
		p.available <- db
	}
	return p, nil
}
func (p *scopedPool) close() {
	for _, db := range p.databases {
		db.Close()
	}
}
func (p *scopedPool) prepare(ids []int64) error {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	for _, db := range p.databases {
		for _, table := range []string{"comment", "submission_file", "submission"} {
			if _, e := db.Exec("TRUNCATE TABLE " + table); e != nil {
				return fmt.Errorf("clear scratch source failed")
			}
		}
		if _, e := db.Exec("INSERT INTO submission SELECT * FROM "+p.source+".submission WHERE id IN ("+marks+")", args...); e != nil {
			return fmt.Errorf("copy scratch submissions failed")
		}
		for _, table := range []string{"comment", "submission_file"} {
			if _, e := db.Exec("INSERT INTO " + table + " SELECT src.* FROM " + p.source + "." + table + " src JOIN submission s ON s.id=src.fk_submission_id"); e != nil {
				return fmt.Errorf("copy scratch history failed")
			}
		}
	}
	return nil
}
func (p *scopedPool) rebuild(ctx context.Context, id int64) *failure {
	db := <-p.available
	defer func() { p.available <- db }()
	return rebuildOne(ctx, db, id)
}
func rebuildOne(ctx context.Context, db *sql.DB, id int64) *failure {
	dal := database.NewMysqlDAL(db)
	s, e := dal.NewSession(ctx)
	stage := "begin"
	if e == nil {
		stage = "rebuild"
		e = dal.RebuildSubmissionCacheTable(s, id)
		if e == nil {
			stage = "commit"
			e = s.Commit()
		}
		_ = s.Rollback()
	}
	if e == nil {
		return nil
	}
	f := &failure{ID: id, Stage: stage}
	if me, ok := e.(*mysql.MySQLError); ok {
		f.Code = me.Number
	}
	return f
}

// Samples cover source-ID extremes, highest file/comment counts and deleted
// histories. They test the scope transformation, not representativeness of timing.
func scopeSamples(db *sql.DB) ([]int64, error) {
	queries := []string{
		"SELECT id FROM submission WHERE deleted_at IS NULL ORDER BY id LIMIT 3",
		"SELECT id FROM submission WHERE deleted_at IS NULL ORDER BY id DESC LIMIT 3",
		"SELECT id FROM submission WHERE deleted_at IS NULL AND id >= (SELECT MAX(id)/4 FROM submission) ORDER BY id LIMIT 1",
		"SELECT id FROM submission WHERE deleted_at IS NULL AND id >= (SELECT MAX(id)/2 FROM submission) ORDER BY id LIMIT 1",
		"SELECT id FROM submission WHERE deleted_at IS NULL AND id >= (SELECT MAX(id)*3/4 FROM submission) ORDER BY id LIMIT 1",
		"SELECT c.fk_submission_id FROM comment c JOIN submission s ON s.id=c.fk_submission_id WHERE s.deleted_at IS NULL AND c.deleted_at IS NULL GROUP BY c.fk_submission_id,c.fk_user_id,c.created_at HAVING COUNT(*)>1 ORDER BY c.fk_submission_id LIMIT 3",
		"SELECT s.id FROM submission s JOIN comment c ON c.fk_submission_id=s.id WHERE s.deleted_at IS NULL GROUP BY s.id ORDER BY COUNT(*) DESC,s.id LIMIT 3",
		"SELECT s.id FROM submission s JOIN submission_file f ON f.fk_submission_id=s.id WHERE s.deleted_at IS NULL GROUP BY s.id ORDER BY COUNT(*) DESC,s.id LIMIT 3",
		"SELECT s.id FROM submission s JOIN comment c ON c.fk_submission_id=s.id WHERE s.deleted_at IS NULL AND c.deleted_at IS NOT NULL GROUP BY s.id ORDER BY s.id LIMIT 3",
		"SELECT s.id FROM submission s JOIN submission_file f ON f.fk_submission_id=s.id WHERE s.deleted_at IS NULL AND f.deleted_at IS NOT NULL GROUP BY s.id ORDER BY s.id LIMIT 3",
	}
	seen := map[int64]bool{}
	var ids []int64
	for _, q := range queries {
		rows, e := db.Query(q)
		if e != nil {
			return nil, fmt.Errorf("sample selection failed")
		}
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, e
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	return ids, nil
}
func onlyIDs(s snapshot, ids []int64) snapshot {
	r := snapshot{}
	for _, id := range ids {
		r[id] = s[id]
	}
	return r
}
