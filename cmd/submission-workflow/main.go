// submission-workflow performs committed, isolated post-import acceptance workflows.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/service"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/sirupsen/logrus"
)

const uploader int64 = 910000000000000001
const reviewer int64 = uploader + 1
const verifier int64 = uploader + 2

var epoch = time.Date(2030, 1, 2, 3, 4, 5, 123456000, time.UTC)

type fixedClock struct{ at time.Time }

func (c *fixedClock) Now() time.Time            { return c.at }
func (c *fixedClock) Unix(s, n int64) time.Time { return time.Unix(s, n) }

type roles struct{}

func (roles) GetFlashpointRoles() ([]types.DiscordRole, error)    { return nil, nil }
func (roles) GetFlashpointRoleIDsForUser(int64) ([]string, error) { return nil, nil }
func (roles) GetFlashpointUserInfo(int64, []types.DiscordRole) (*types.FlashpointDiscordUser, error) {
	return nil, nil
}
func (roles) GetJoinedAtForUser(int64) (time.Time, error) { return epoch.AddDate(-1, 0, 0), nil }

type notifications struct{}

func (notifications) SendNotification(string, string) error {
	return errors.New("notification delivery is forbidden in rehearsal")
}
func (notifications) SendCurationFeedMessage(string) error {
	return errors.New("notification delivery is forbidden in rehearsal")
}

type step struct {
	Name          string
	Search        []*types.ExtendedSubmission
	Comments      []*types.ExtendedComment
	Cache         map[string]sql.NullString
	Filters       map[string]int64
	Subscriptions []int64
	Events        []event
	Notifications []notice
	Deletions     []deletion
}
type event struct {
	User            int64
	Area, Operation string
	Data            map[string]json.RawMessage
}
type notice struct {
	Type, Message string
	Sent          bool
}
type deletion struct {
	Table  string
	ID     int64
	Reason string
}
type sequenceCheck struct {
	BeforeMax  int64
	BeforeNext int64
	Allocated  []int64
}
type report struct {
	Engine           string
	Complete         bool
	Error            string
	Steps            []step
	Sequences        map[string]*sequenceCheck
	TranscriptSHA256 string
	Limitations      []string
}
type runner struct {
	ctx             context.Context
	db              *sql.DB
	dal             database.DAL
	pg              *pgxpool.Pool
	svc             *service.SiteService
	clock           *fixedClock
	pgMode          bool
	sid             int64
	files, comments map[int64]int64
	out             *report
	started         time.Time
	notificationMax int64
}

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func check(ok bool, f string, a ...any) {
	if !ok {
		panic(fmt.Errorf(f, a...))
	}
}
func disposable(name string) bool {
	return strings.HasPrefix(name, "snapshot_workflow_") || strings.HasPrefix(name, "submission_import_")
}
func openSubmission(engine, dsn string) (*sql.DB, error) {
	if engine == "mariadb" {
		c, e := mysql.ParseDSN(dsn)
		if e != nil {
			return nil, errors.New("invalid MariaDB DSN")
		}
		if c.Net != "tcp" || c.Addr != "mariadb:3306" || !strings.HasPrefix(c.DBName, "snapshot_workflow_") {
			return nil, errors.New("MariaDB must be mariadb:3306/snapshot_workflow_*")
		}
		c.ParseTime = true
		c.Loc = time.UTC
		if c.Params == nil {
			c.Params = map[string]string{}
		}
		c.Params["time_zone"] = "'+00:00'"
		return sql.Open("mysql", c.FormatDSN())
	}
	if engine != "postgres" {
		return nil, errors.New("engine must be mariadb or postgres")
	}
	c, e := pgx.ParseConfig(dsn)
	if e != nil {
		return nil, errors.New("invalid PostgreSQL DSN")
	}
	if c.Host != "postgres" || c.Port != 5432 || !disposable(c.Database) {
		return nil, errors.New("PostgreSQL must be postgres:5432/disposable target")
	}
	c.RuntimeParams["timezone"] = "UTC"
	return stdlib.OpenDB(*c), nil
}
func openNative(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	c, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		return nil, errors.New("invalid native PostgreSQL DSN")
	}
	if c.ConnConfig.Host != "postgres" || c.ConnConfig.Port != 5432 || !disposable(c.ConnConfig.Database) {
		return nil, errors.New("native PostgreSQL must be postgres:5432/disposable target")
	}
	c.ConnConfig.RuntimeParams["timezone"] = "UTC"
	return pgxpool.NewWithConfig(ctx, c)
}
func (r *runner) q(q string) string {
	if !r.pgMode {
		return q
	}
	i := 0
	for strings.Contains(q, "?") {
		i++
		q = strings.Replace(q, "?", fmt.Sprintf("$%d", i), 1)
	}
	return q
}
func (r *runner) tx(f func(database.DBSession)) {
	s, e := r.dal.NewSession(r.ctx)
	must(e)
	defer s.Rollback()
	f(s)
	must(s.Commit())
}
func (r *runner) register(table string, id int64) int64 {
	s := r.out.Sequences[table]
	check(id >= s.BeforeNext, "%s generated ID %d below source allocation %d", table, id, s.BeforeNext)
	check(id > s.BeforeMax, "%s generated ID %d must exceed source maximum %d", table, id, s.BeforeMax)
	for i, v := range s.Allocated {
		if v == id {
			return int64(i + 1)
		}
	}
	s.Allocated = append(s.Allocated, id)
	return int64(len(s.Allocated))
}
func (r *runner) setup() {
	r.started = time.Now().UTC().Truncate(time.Microsecond)
	must(r.db.QueryRowContext(r.ctx, "SELECT COALESCE(MAX(id),0) FROM submission_notification").Scan(&r.notificationMax))
	var nativeCount int
	must(r.pg.QueryRow(r.ctx, "SELECT COUNT(*) FROM activity_events WHERE uid IN ($1,$2,$3)", uploader, reviewer, verifier).Scan(&nativeCount))
	check(nativeCount == 0, "workflow actors already have native activity; use fresh clone")

	for _, table := range []string{"submission", "submission_file", "comment"} {
		var n int64
		must(r.db.QueryRowContext(r.ctx, "SELECT COALESCE(MAX(id),0) FROM "+table).Scan(&n))
		var next int64
		if r.pgMode {
			var seq string
			must(r.db.QueryRowContext(r.ctx, "SELECT pg_get_serial_sequence($1,'id')", table).Scan(&seq))
			var called bool
			must(r.db.QueryRowContext(r.ctx, "SELECT last_value,is_called FROM "+pgx.Identifier(strings.Split(seq, ".")).Sanitize()).Scan(&next, &called))
			if called {
				next++
			}
		} else {
			must(r.db.QueryRowContext(r.ctx, "SELECT AUTO_INCREMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", table).Scan(&next))
		}
		r.out.Sequences[table] = &sequenceCheck{BeforeMax: n, BeforeNext: next}
	}
	r.tx(func(s database.DBSession) {
		for i, uid := range []int64{uploader, reviewer, verifier} {
			var n int
			must(s.Tx().QueryRowContext(r.ctx, r.q("SELECT COUNT(*) FROM discord_user WHERE id=?"), uid).Scan(&n))
			check(n == 0, "reserved workflow user already exists; use a fresh clone")
			must(r.dal.StoreDiscordUser(s, &types.DiscordUser{ID: uid, Username: fmt.Sprintf("snapshot-workflow-%d", i)}))
		}
		var e error
		r.sid, e = r.dal.StoreSubmission(s, "staff")
		must(e)
		r.register("submission", r.sid)
	})
	r.file(1)
}
func (r *runner) file(version int) {
	at := epoch.Add(time.Duration(version-1) * time.Hour)
	r.tx(func(s database.DBSession) {
		must(database.LockSubmissions(s, r.sid))
		f := &types.SubmissionFile{SubmitterID: uploader, SubmissionID: r.sid, OriginalFilename: fmt.Sprintf("workflow-%d.7z", version), CurrentFilename: fmt.Sprintf("workflow-%d.7z", version), Size: 12345, UploadedAt: at, MD5Sum: fmt.Sprintf("%032x", version), SHA256Sum: fmt.Sprintf("%064x", version)}
		fid, e := r.dal.StoreSubmissionFile(s, f)
		must(e)
		r.files[fid] = r.register("submission_file", fid)
		must(r.dal.StoreCurationMeta(s, &types.CurationMeta{SubmissionFileID: fid, Title: utils.StrPtr("Workflow café %_ \\ title"), AlternateTitles: utils.StrPtr("Alternate"), Platform: utils.StrPtr("Flash"), Library: utils.StrPtr("arcade"), Extreme: utils.StrPtr("No"), LaunchCommand: utils.StrPtr("content/game.swf")}))
		for i, a := range []string{constants.ActionUpload, constants.ActionApprove} {
			uid := uploader
			if i == 1 {
				uid = constants.ValidatorID
			}
			id, e := r.dal.StoreComment(s, &types.Comment{SubmissionID: r.sid, AuthorID: uid, Action: a, CreatedAt: at.Add(time.Duration(i) * time.Microsecond)})
			must(e)
			r.comments[id] = r.register("comment", id)
		}
		must(r.dal.UpdateSubmissionCacheTable(s, r.sid))
	})
}
func (r *runner) action(uid int64, a string) {
	r.clock.at = r.clock.at.Add(time.Second)
	must(r.svc.ReceiveComments(r.ctx, uid, []int64{r.sid}, a, "workflow message", "false", "", "", "", "", nil))
}
func (r *runner) cache() map[string]sql.NullString {
	rows, e := r.db.QueryContext(r.ctx, r.q("SELECT * FROM submission_cache WHERE fk_submission_id=?"), r.sid)
	must(e)
	defer rows.Close()
	cols, e := rows.Columns()
	must(e)
	check(rows.Next(), "missing cache")
	vs := make([]sql.NullString, len(cols))
	ptr := make([]any, len(cols))
	for i := range vs {
		ptr[i] = &vs[i]
	}
	must(rows.Scan(ptr...))
	out := map[string]sql.NullString{}
	for i, c := range cols {
		v := vs[i]
		if v.Valid {
			switch c {
			case "fk_submission_id":
				v.String = "1"
			case "fk_newest_file_id", "fk_oldest_file_id":
				id, e := strconv.ParseInt(v.String, 10, 64)
				must(e)
				check(r.files[id] != 0, "unknown cache file")
				v.String = strconv.FormatInt(r.files[id], 10)
			case "fk_newest_comment_id":
				id, e := strconv.ParseInt(v.String, 10, 64)
				must(e)
				check(r.comments[id] != 0, "unknown cache comment")
				v.String = strconv.FormatInt(r.comments[id], 10)
			}
			if strings.HasPrefix(c, "active_") || c == "distinct_actions" || c == "md5sum_sequence" || c == "sha256sum_sequence" {
				a := strings.Split(v.String, ",")
				sort.Strings(a)
				v.String = strings.Join(a, ",")
			}
		}
		out[c] = v
	}
	check(!rows.Next(), "duplicate cache")
	must(rows.Err())
	return out
}
func (r *runner) snapshot(name string) step {
	// Register every generated comment, including soft-deleted history, without changing history.
	rows, e := r.db.QueryContext(r.ctx, r.q("SELECT id FROM comment WHERE fk_submission_id=? ORDER BY id"), r.sid)
	must(e)
	for rows.Next() {
		var id int64
		must(rows.Scan(&id))
		r.comments[id] = r.register("comment", id)
	}
	must(rows.Err())
	must(rows.Close())
	out := step{Name: name, Filters: map[string]int64{}}
	r.tx(func(s database.DBSession) {
		var total int64
		out.Search, total, e = r.dal.SearchSubmissions(s, &types.SubmissionsFilter{SubmissionIDs: []int64{r.sid}})
		must(e)
		check(total == 1 && len(out.Search) == 1, "expected one workflow submission")
		out.Comments, e = r.dal.GetExtendedCommentsBySubmissionID(s, r.sid)
		must(e)
		for _, uid := range []int64{uploader, reviewer, verifier} {
			yes, e := r.dal.IsUserSubscribedToSubmission(s, uid, r.sid)
			must(e)
			if yes {
				out.Subscriptions = append(out.Subscriptions, uid)
			}
		}
	})
	for _, sub := range out.Search {
		sub.SubmissionID = 1
		sub.FileID = r.files[sub.FileID]
		sort.Slice(sub.AssignedTestingUserIDs, func(i, j int) bool { return sub.AssignedTestingUserIDs[i] < sub.AssignedTestingUserIDs[j] })
		sort.Slice(sub.AssignedVerificationUserIDs, func(i, j int) bool { return sub.AssignedVerificationUserIDs[i] < sub.AssignedVerificationUserIDs[j] })
		sort.Slice(sub.RequestedChangesUserIDs, func(i, j int) bool { return sub.RequestedChangesUserIDs[i] < sub.RequestedChangesUserIDs[j] })
		sort.Slice(sub.ApprovedUserIDs, func(i, j int) bool { return sub.ApprovedUserIDs[i] < sub.ApprovedUserIDs[j] })
		sort.Slice(sub.VerifiedUserIDs, func(i, j int) bool { return sub.VerifiedUserIDs[i] < sub.VerifiedUserIDs[j] })
		sort.Strings(sub.DistinctActions)
	}
	for _, c := range out.Comments {
		c.SubmissionID = 1
		c.CommentID = r.comments[c.CommentID]
		check(c.CreatedAt.Nanosecond()%1000 == 0, "comment precision exceeded microseconds")
	}
	out.Cache = r.cache()
	r.sideEffects(&out)
	filters := map[string]*types.SubmissionsFilter{
		"platform-flash": {PlatformPartial: utils.StrPtr("Flash")}, "platform-unity": {PlatformPartial: utils.StrPtr("Unity")},
		"title-unicode": {TitlePartial: utils.StrPtr("cafe")}, "uploader": {SubmitterID: utils.Int64Ptr(uploader)},
		"ready":             {BotActions: []string{"approve"}, RequestedChangedStatus: utils.StrPtr("none"), VerificationStatus: utils.StrPtr("verified"), DistinctActionsNot: []string{"mark-added", "reject"}},
		"reviewer-approved": {AssignedStatusUserID: utils.Int64Ptr(reviewer), ApprovalsStatusUser: utils.StrPtr("yes")},
	}
	for n, f := range filters {
		f.SubmissionIDs = []int64{r.sid}
		r.tx(func(s database.DBSession) {
			rs, count, e := r.dal.SearchSubmissions(s, f)
			must(e)
			check(int64(len(rs)) == count, "filtered count mismatch")
			out.Filters[n] = count
		})
	}
	return out
}
func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func (r *runner) checkpoint(name string, approved, verified bool, fileCount uint64) {
	before := r.snapshot(name)
	sub := before.Search[0]
	check((len(sub.ApprovedUserIDs) > 0) == approved, "%s approval state", name)
	check((len(sub.VerifiedUserIDs) > 0) == verified, "%s verification state", name)
	check(sub.FileCount == fileCount, "%s file count", name)
	check(sub.BotAction == constants.ActionApprove, "%s validator action", name)
	check(sub.SubmitterID == uploader && sub.LastUploaderID == uploader, "%s ownership", name)
	check(before.Filters["uploader"] == 1 && before.Filters["title-unicode"] == 1, "%s scoped user/title filters", name)
	var ready, approval int64
	if verified {
		ready = 1
	}
	if approved {
		approval = 1
	}
	check(before.Filters["ready"] == ready && before.Filters["reviewer-approved"] == approval, "%s optimized ready/reviewer filters", name)
	check(len(sub.ApprovedUserIDs) == 0 || reflect.DeepEqual(sub.ApprovedUserIDs, []int64{reviewer}), "%s exact approvers", name)
	check(len(sub.VerifiedUserIDs) == 0 || reflect.DeepEqual(sub.VerifiedUserIDs, []int64{verifier}), "%s exact verifiers", name)
	var testing, verification, changes []int64
	if name == "assign-testing" || name == "request-changes" {
		testing = []int64{reviewer}
	}
	if name == "request-changes" {
		changes = []int64{reviewer}
	}
	if name == "assign-verification" {
		verification = []int64{verifier}
	}
	check(equalIDs(sub.AssignedTestingUserIDs, testing) && equalIDs(sub.AssignedVerificationUserIDs, verification) && equalIDs(sub.RequestedChangesUserIDs, changes), "%s independent reviewer sets", name)
	expectedPlatform := "Flash"
	if name == "metadata-update-SQL" || name == "delete-replacement-service" || name == "delete-verification-service" || name == "new-comment-after-rollback" {
		expectedPlatform = "Unity"
	}
	check(sub.CurationPlatform != nil && *sub.CurationPlatform == expectedPlatform, "%s metadata platform", name)
	check(before.Filters["platform-flash"]+before.Filters["platform-unity"] == 1, "%s platform membership", name)

	r.tx(func(s database.DBSession) {
		must(database.LockSubmissions(s, r.sid))
		must(r.dal.RebuildSubmissionCacheTable(s, r.sid))
	})
	after := r.snapshot(name)
	check(reflect.DeepEqual(before, after), "%s incremental cache/search differs from rebuild", name)
	r.out.Steps = append(r.out.Steps, before)
}
func (r *runner) execute() {
	r.setup()
	r.checkpoint("created-via-DAL", false, false, 1)
	r.action(reviewer, constants.ActionAssignTesting)
	r.checkpoint("assign-testing", false, false, 1)
	r.action(reviewer, constants.ActionRequestChanges)
	r.checkpoint("request-changes", false, false, 1)
	r.action(reviewer, constants.ActionApprove)
	r.checkpoint("approve", true, false, 1)
	r.action(verifier, constants.ActionAssignVerification)
	r.checkpoint("assign-verification", true, false, 1)
	r.action(verifier, constants.ActionVerify)
	r.checkpoint("verify", true, true, 1)
	// Expected rule rejection must preserve full visible history/cache/subscriptions.
	before := r.snapshot("rejected-self-verification")
	e := r.svc.ReceiveComments(r.ctx, uploader, []int64{r.sid}, constants.ActionAssignVerification, "", "false", "", "", "", "", nil)
	check(e != nil, "self verification should reject")
	after := r.snapshot(before.Name)
	check(reflect.DeepEqual(before, after), "rejected action mutated persisted state")
	r.out.Steps = append(r.out.Steps, after)
	var first, verifyID int64
	for id, n := range r.files {
		if n == 1 {
			first = id
		}
	}
	r.tx(func(s database.DBSession) {
		cs, e := r.dal.GetExtendedCommentsBySubmissionID(s, r.sid)
		must(e)
		for _, c := range cs {
			if c.Action == constants.ActionVerify {
				verifyID = c.CommentID
			}
		}
	})
	check(verifyID > 0, "missing verify comment")
	r.tx(func(s database.DBSession) {
		must(database.LockSubmissions(s, r.sid))
		_, e := s.Tx().ExecContext(r.ctx, r.q("UPDATE curation_meta SET platform=?,title=? WHERE fk_submission_file_id=?"), "Unity", "Workflow café metadata revised", first)
		must(e)
	})
	r.checkpoint("metadata-update-SQL", true, true, 1)
	r.file(2)
	r.checkpoint("replacement-file-via-DAL", false, false, 2)
	var second int64
	for id, n := range r.files {
		if n == 2 {
			second = id
		}
	}
	must(r.svc.SoftDeleteSubmissionFile(r.ctx, second, "rehearsal remove replacement"))
	r.checkpoint("delete-replacement-service", true, true, 1)
	must(r.svc.SoftDeleteComment(r.ctx, verifyID, "rehearsal remove verification"))
	r.checkpoint("delete-verification-service", true, false, 1)
	// A real transaction writes a comment, subscription and cache, then rolls back.
	before = r.snapshot("explicit-write-rollback")
	s, e := r.dal.NewSession(r.ctx)
	must(e)
	func() {
		defer s.Rollback()
		must(database.LockSubmissions(s, r.sid))
		_, e = r.dal.StoreComment(s, &types.Comment{SubmissionID: r.sid, AuthorID: reviewer, Action: constants.ActionComment, Message: utils.StrPtr("must roll back"), CreatedAt: epoch.Add(3 * time.Hour)})
		must(e)
		must(r.dal.SubscribeUserToSubmission(s, uploader, r.sid))
		must(r.dal.UpdateSubmissionCacheTable(s, r.sid))
		must(s.Rollback())
	}()
	after = r.snapshot(before.Name)
	check(reflect.DeepEqual(before, after), "rollback leaked persisted changes")
	r.out.Steps = append(r.out.Steps, after)
	r.clock.at = epoch.Add(2 * time.Hour)
	r.action(reviewer, constants.ActionComment)
	r.checkpoint("new-comment-after-rollback", true, false, 1)
	b, e := json.Marshal(r.out.Steps)
	must(e)
	r.out.TranscriptSHA256 = fmt.Sprintf("%x", sha256.Sum256(b))
	r.out.Complete = true
}
func run(engine, dsn, native string, out *report) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", p)
			}
		}
	}()
	ctx := context.Background()
	log := logrus.New()
	log.SetOutput(io.Discard)
	ctx = context.WithValue(ctx, utils.CtxKeys.Log, logrus.NewEntry(log))
	ctx = context.WithValue(ctx, utils.CtxKeys.UserID, reviewer)
	db, e := openSubmission(engine, dsn)
	must(e)
	defer db.Close()
	must(db.PingContext(ctx))
	pg, e := openNative(ctx, native)
	must(e)
	defer pg.Close()
	must(pg.Ping(ctx))
	clock := &fixedClock{at: epoch.Add(time.Minute)}
	svc := service.NewWithMocks(logrus.NewEntry(log), db, pg, roles{}, notifications{}, "", 3600, "", "", true, nil, "", "", clock)
	r := &runner{ctx: ctx, db: db, dal: database.NewSubmissionDAL(db), pg: pg, svc: svc, clock: clock, pgMode: engine == "postgres", files: map[int64]int64{}, comments: map[int64]int64{}, out: out}
	r.execute()
	return nil
}
func main() {
	engine := flag.String("engine", "", "mariadb or postgres")
	path := flag.String("out", "", "new report JSON path")
	flag.Parse()
	if *path == "" {
		fmt.Fprintln(os.Stderr, "--out required")
		os.Exit(2)
	}
	f, e := os.OpenFile(*path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	out := report{Engine: *engine, Sequences: map[string]*sequenceCheck{}, Limitations: []string{"Initial/replacement files and upload/validator comments use the DAL with synthetic metadata; no external archive files are processed.", "Metadata edit uses scoped SQL. Review, verification, comment and deletion operations use actual services and commit.", "Notification senders never run. Exact deterministic history times are retained; raw database-created deletion/notification times are not in the transcript.", "Native activity-event payloads and queued notification text are compared after mapping generated IDs; real wall-clock side-effect timestamps are range-checked then omitted."}}
	e = run(*engine, os.Getenv("WORKFLOW_SUBMISSION_DSN"), os.Getenv("WORKFLOW_NATIVE_PG_DSN"), &out)
	if e != nil {
		out.Error = e.Error()
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if ee := enc.Encode(out); ee != nil {
		e = ee
	}
	if ee := f.Close(); ee != nil {
		e = ee
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Println(out.TranscriptSHA256)
}

func (r *runner) wallTime(at time.Time) {
	check(!at.Before(r.started.Add(-time.Second)) && !at.After(time.Now().UTC().Add(time.Second)), "database side-effect timestamp outside run window: %s", at)
}
func (r *runner) sideEffects(out *step) {
	rows, e := r.pg.Query(r.ctx, "SELECT uid,created_at,event_area,event_operation,event_data FROM activity_events WHERE uid IN ($1,$2,$3) ORDER BY id", uploader, reviewer, verifier)
	must(e)
	for rows.Next() {
		var ev event
		var at time.Time
		var raw []byte
		must(rows.Scan(&ev.User, &at, &ev.Area, &ev.Operation, &raw))
		r.wallTime(at)
		must(json.Unmarshal(raw, &ev.Data))
		for k, v := range ev.Data {
			if string(v) == "null" {
				continue
			}
			var id int64
			switch k {
			case "submission_id", "comment_id", "file_id":
				must(json.Unmarshal(v, &id))
				if k == "submission_id" {
					check(id == r.sid, "unrelated activity submission")
					id = 1
				} else if k == "comment_id" {
					id = r.comments[id]
					check(id != 0, "unknown event comment")
				} else {
					id = r.files[id]
					check(id != 0, "unknown event file")
				}
				ev.Data[k] = json.RawMessage(strconv.FormatInt(id, 10))
			}
		}
		out.Events = append(out.Events, ev)
	}
	must(rows.Err())
	rows.Close()
	nrows, e := r.db.QueryContext(r.ctx, r.q("SELECT t.name,n.message,n.created_at,n.sent_at FROM submission_notification n JOIN submission_notification_type t ON t.id=n.fk_submission_notification_type_id WHERE n.id>? ORDER BY n.id"), r.notificationMax)
	must(e)
	for nrows.Next() {
		var n notice
		var at time.Time
		var sent sql.NullTime
		must(nrows.Scan(&n.Type, &n.Message, &at, &sent))
		r.wallTime(at)
		n.Sent = sent.Valid
		check(!n.Sent, "rehearsal notification unexpectedly sent")
		n.Message = strings.ReplaceAll(n.Message, fmt.Sprintf("/submission/%d>", r.sid), "/submission/1>")
		for id, logical := range r.comments {
			n.Message = strings.ReplaceAll(n.Message, fmt.Sprintf("comment #%d ", id), fmt.Sprintf("comment #%d ", logical))
		}
		for id, logical := range r.files {
			n.Message = strings.ReplaceAll(n.Message, fmt.Sprintf("file #%d ", id), fmt.Sprintf("file #%d ", logical))
		}
		out.Notifications = append(out.Notifications, n)
	}
	must(nrows.Err())
	must(nrows.Close())
	for _, table := range []string{"submission_file", "comment"} {
		dr, e := r.db.QueryContext(r.ctx, r.q("SELECT id,deleted_at,deleted_reason FROM "+table+" WHERE fk_submission_id=? AND deleted_at IS NOT NULL ORDER BY id"), r.sid)
		must(e)
		for dr.Next() {
			var d deletion
			var at time.Time
			must(dr.Scan(&d.ID, &at, &d.Reason))
			r.wallTime(at)
			d.Table = table
			if table == "comment" {
				d.ID = r.comments[d.ID]
			} else {
				d.ID = r.files[d.ID]
			}
			check(d.ID != 0, "unknown deletion identity")
			out.Deletions = append(out.Deletions, d)
		}
		must(dr.Err())
		must(dr.Close())
	}
}
