// submission-stats-benchmark measures the statistics counters on a disposable
// imported PostgreSQL snapshot. It never updates database records.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/sirupsen/logrus"
)

type workload struct {
	Name   string
	Filter *types.SubmissionsFilter
}

func ptr[T any](v T) *T { return &v }

// These are the seven GetStatisticsPageData and eight GetUserStatistics
// submission counters, including the latter's approved/verified exclusions.
func workloads(uid int64) []workload {
	return []workload{
		{"global/all", nil},
		{"global/bot-happy", &types.SubmissionsFilter{BotActions: []string{"approve"}}},
		{"global/bot-unhappy", &types.SubmissionsFilter{BotActions: []string{"request-changes"}}},
		{"global/approved", &types.SubmissionsFilter{ApprovalsStatus: ptr("approved")}},
		{"global/verified", &types.SubmissionsFilter{VerificationStatus: ptr("verified")}},
		{"global/rejected", &types.SubmissionsFilter{DistinctActions: []string{"reject"}}},
		{"global/imported", &types.SubmissionsFilter{DistinctActions: []string{"mark-added"}}},
		{"user/all", &types.SubmissionsFilter{SubmitterID: &uid}},
		{"user/bot-happy", &types.SubmissionsFilter{SubmitterID: &uid, BotActions: []string{"approve"}}},
		{"user/bot-unhappy", &types.SubmissionsFilter{SubmitterID: &uid, BotActions: []string{"request-changes"}}},
		{"user/changes-requested", &types.SubmissionsFilter{SubmitterID: &uid, RequestedChangedStatus: ptr("ongoing")}},
		{"user/approved", &types.SubmissionsFilter{SubmitterID: &uid, ApprovalsStatus: ptr("approved"), VerificationStatus: ptr("none"), DistinctActionsNot: []string{"reject", "mark-added"}}},
		{"user/verified", &types.SubmissionsFilter{SubmitterID: &uid, VerificationStatus: ptr("verified"), DistinctActionsNot: []string{"reject", "mark-added"}}},
		{"user/imported", &types.SubmissionsFilter{SubmitterID: &uid, DistinctActions: []string{"mark-added"}, DistinctActionsNot: []string{"reject"}}},
		{"user/rejected", &types.SubmissionsFilter{SubmitterID: &uid, DistinctActions: []string{"reject"}}},
	}
}

type result struct {
	Name               string    `json:"name"`
	Count              int64     `json:"count"`
	SearchCount        int64     `json:"search_count"`
	CountMS            []float64 `json:"count_ms_first_then_warm"`
	WarmMedianMS       float64   `json:"count_warm_median_ms"`
	SearchValidationMS float64   `json:"search_validation_ms"`
}
type report struct {
	Complete bool      `json:"complete"`
	UserID   int64     `json:"user_id"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Results  []result  `json:"results"`
}

func median(values []float64) float64 {
	values = slices.Clone(values)
	slices.Sort(values)
	n := len(values)
	if n%2 == 0 {
		return (values[n/2-1] + values[n/2]) / 2
	}
	return values[n/2]
}
func save(path string, r report) error {
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}
func run() error {
	uid := flag.Int64("user-id", 0, "source uploader ID for the eight user counters (required)")
	out := flag.String("out", "", "new report path (required)")
	repetitions := flag.Int("repetitions", 3, "warm repetitions after first execution")
	flag.Parse()
	if *uid <= 0 || *out == "" || *repetitions < 1 {
		return fmt.Errorf("positive user-id, out and repetitions are required")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("POSTGRES_PARITY_DSN"))
	if err != nil {
		return fmt.Errorf("invalid PostgreSQL DSN")
	}
	if cfg.Host != "postgres" || !strings.HasPrefix(cfg.Database, "submission_import_") {
		return fmt.Errorf("only postgres host and submission_import_ databases are permitted")
	}
	cfg.RuntimeParams["timezone"] = "UTC"
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.RuntimeParams["statement_timeout"] = "60000"
	file, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	dal := database.NewPostgresSubmissionDAL(db)
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	ctx := context.WithValue(context.Background(), utils.CtxKeys.Log, logrus.NewEntry(logger))
	session, err := dal.NewSession(ctx)
	if err != nil {
		return err
	}
	defer session.Rollback()
	// Hold one read-only snapshot across count and full-search validation, so
	// observed differences cannot be attributed to concurrent mutations.
	if _, err = session.Tx().ExecContext(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY"); err != nil {
		return err
	}
	r := report{UserID: *uid, Started: time.Now().UTC(), Results: []result{}}
	for _, w := range workloads(*uid) {
		item := result{Name: w.Name}
		for i := 0; i <= *repetitions; i++ {
			start := time.Now()
			count, e := database.CountSubmissions(dal, session, w.Filter)
			elapsed := float64(time.Since(start)) / float64(time.Millisecond)
			if e != nil {
				return fmt.Errorf("%s count: %w", w.Name, e)
			}
			if i > 0 && count != item.Count {
				return fmt.Errorf("%s unstable count", w.Name)
			}
			item.Count = count
			item.CountMS = append(item.CountMS, elapsed)
		}
		item.WarmMedianMS = median(item.CountMS[1:])
		start := time.Now()
		_, item.SearchCount, err = dal.SearchSubmissions(session, w.Filter)
		item.SearchValidationMS = float64(time.Since(start)) / float64(time.Millisecond)
		if err != nil {
			return fmt.Errorf("%s search validation: %w", w.Name, err)
		}
		r.Results = append(r.Results, item)
		if err = save(*out, r); err != nil {
			return err
		}
		if item.SearchCount != item.Count {
			return fmt.Errorf("%s count %d differs from search %d", w.Name, item.Count, item.SearchCount)
		}
	}
	r.Complete = true
	r.Finished = time.Now().UTC()
	return save(*out, r)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
