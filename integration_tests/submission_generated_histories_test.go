package integration_tests

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

var historySeed = flag.String("history-seed", "", "replay one generated history seed instead of the default seeds")
var historyReplay = flag.String("history-replay", "", "replay a JSON array of historyOp from a file (Docker path relative to repo)")

func historyIDs(values []int64) string {
	parts := make([]string, len(values))
	for i, id := range values {
		parts[i] = strconv.FormatInt(id, 10)
	}
	// cacheSnapshot canonicalizes serialized membership lexicographically.
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func generatedHistory(seed int64) []historyOp {
	rng := rand.New(rand.NewSource(seed))
	users := []int64{12, 112, 120, 1200}
	model := newHistoryModel()
	trace := []historyOp{}
	next := int64(100)
	add := func(op historyOp) { trace = append(trace, op); model.apply(op) }
	for sid := int64(1); sid <= 3; sid++ {
		add(historyOp{Kind: "file", ID: sid, Submission: sid, User: users[sid-1], Micros: 0})
		add(historyOp{Kind: "comment", ID: sid, Submission: sid, User: constants.ValidatorID, Micros: 1, Action: "approve"})
	}
	actions := []string{"assign-testing", "unassign-testing", "assign-verification", "unassign-verification", "request-changes", "approve", "verify", "comment", "mark-added", "reject"}
	fileTotals := map[int64]int{1: 1, 2: 1, 3: 1}
	for step := 0; step < 72; step++ {
		sid := int64(1 + rng.Intn(3))
		next++
		// Small time domain deliberately creates ties and backdated inserts. IDs
		// represent insertion order, which differs from chronological event order.
		op := historyOp{Kind: "comment", ID: next, Submission: sid, User: users[rng.Intn(len(users))], Micros: int64(2 + rng.Intn(20)), Action: actions[rng.Intn(len(actions))]}
		choice := rng.Intn(10)
		candidates := []historyOp{}
		if choice < 2 {
			for _, c := range model.comments {
				if c.Submission == sid && c.ID > 3 {
					candidates = append(candidates, c)
				}
			}
			sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
			if len(candidates) > 0 {
				c := candidates[rng.Intn(len(candidates))]
				op = historyOp{Kind: "delete-comment", ID: c.ID, Submission: sid}
			}
		} else if choice == 2 && fileTotals[sid] < 4 {
			op.Kind = "file"
			op.Action = ""
			fileTotals[sid]++
		} else if choice == 3 {
			for _, f := range model.files {
				if f.Submission == sid && f.ID > 3 {
					candidates = append(candidates, f)
				}
			}
			sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
			if len(candidates) > 0 {
				f := candidates[rng.Intn(len(candidates))]
				op = historyOp{Kind: "delete-file", ID: f.ID, Submission: sid}
			}
		} else if choice == 4 {
			op.User = constants.ValidatorID
		}
		// Bound reject lifetime so random reviewer actions are not hidden by a
		// permanent rejection. The next mutation of this submission removes it.
		rejects := []historyOp{}
		for _, c := range model.comments {
			if c.Submission == sid && c.Action == "reject" {
				rejects = append(rejects, c)
			}
		}
		sort.Slice(rejects, func(i, j int) bool { return rejects[i].ID < rejects[j].ID })
		if len(rejects) > 0 {
			op = historyOp{Kind: "delete-comment", ID: rejects[0].ID, Submission: sid}
		}
		add(op)
	}
	return trace
}

// These are DAL characterization histories, not valid service workflows. They
// intentionally mix raw actions that service policy may reject. Real service
// mutation/cache/search behavior has separate mutation_equivalence coverage.
func TestSubmissionGeneratedHistories(t *testing.T) {
	seeds := []int64{7, 42, 20260909}
	if *historySeed != "" {
		seed, err := strconv.ParseInt(*historySeed, 10, 64)
		require.NoError(t, err)
		seeds = []int64{seed}
	}
	if *historyReplay != "" {
		path := *historyReplay
		// Go runs this package from integration_tests; CLI paths are relative
		// to the repository root, matching run.sh and its documentation.
		if !filepath.IsAbs(path) {
			path = filepath.Join("..", path)
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var trace []historyOp
		require.NoError(t, json.Unmarshal(data, &trace))
		require.NotEmpty(t, trace)
		t.Run("replay", func(t *testing.T) { runGeneratedHistory(t, 0, trace) })
		return
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) { runGeneratedHistory(t, seed, generatedHistory(seed)) })
	}
}

func runGeneratedHistory(t *testing.T, seed int64, trace []historyOp) {
	f := newSQLFixture(t)
	for _, uid := range []int64{12, 112, 120, 1200} {
		f.User(t, uid, fmt.Sprintf("history reviewer %d", uid))
	}
	for sid := int64(1); sid <= 3; sid++ {
		f.Submission(t, sid, "staff")
	}
	model := newHistoryModel()
	step := -1
	// Failures include the complete, exact operation prefix. Copy this JSON into
	// integration_tests/testdata/histories/name.json and replay/minimize without RNG changes:
	// bash integration_tests/run.sh -run '^TestSubmissionGeneratedHistories$' -args -history-replay=integration_tests/testdata/histories/name.json
	t.Cleanup(func() {
		if t.Failed() {
			data, _ := json.MarshalIndent(trace[:step+1], "", "  ")
			// The Docker runner mounts /results; local runs still retain the
			// full trace in test output if no artifact directory is available.
			path := fmt.Sprintf("/results/history-failure-seed-%d-step-%d.json", seed, step)
			if err := os.WriteFile(path, data, 0600); err == nil {
				t.Logf("saved replay trace: %s", path)
			}
			t.Logf("history seed=%d step=%d; replay seed: bash integration_tests/run.sh -run '^TestSubmissionGeneratedHistories$' -args -history-seed=%d\nHISTORY_TRACE_JSON\n%s", seed, step, seed, data)
		}
	})
	columns := []string{"active_assigned_testing_ids", "active_assigned_verification_ids", "active_requested_changes_ids", "active_approved_ids", "active_verified_ids"}
	check := func(sid int64) {
		t.Helper()
		want := model.reduce(sid)
		got := cacheSnapshot(t, f, sid)
		for i, col := range columns {
			require.Equal(t, historyIDs(want.Users[i]), got[col], "seed=%d step=%d sid=%d %s", seed, step, sid, col)
			require.Equal(t, strconv.FormatBool(len(want.Users[i]) > 0), got[col+".valid"])
		}
		require.Equal(t, strings.Join(want.Actions, ","), got["distinct_actions"])
		require.Equal(t, want.Bot, got["bot_action"])
		require.Equal(t, strconv.FormatBool(want.Bot != ""), got["bot_action.valid"])
		require.Equal(t, strconv.FormatBool(len(want.Actions) > 0), got["distinct_actions.valid"])
		for col, id := range map[string]int64{"fk_oldest_file_id": want.First.ID, "fk_newest_file_id": want.Last.ID, "fk_newest_comment_id": want.Comment.ID} {
			if id == 0 {
				require.Equal(t, "false", got[col+".valid"])
			} else {
				require.Equal(t, strconv.FormatInt(id, 10), got[col])
				require.Equal(t, "true", got[col+".valid"])
			}
		}
		// Every generated searchable state retains the bootstrap upload and bot
		// comment. Empty-source cache behavior is covered in cache/model unit tests.
		if want.First.ID == 0 || want.Bot == "" {
			return
		}
		rows, count := f.Search(t, 12, &types.SubmissionsFilter{SubmissionIDs: []int64{sid}})
		require.EqualValues(t, 1, count)
		require.Len(t, rows, 1)
		row := rows[0]
		actual := [5][]int64{row.AssignedTestingUserIDs, row.AssignedVerificationUserIDs, row.RequestedChangesUserIDs, row.ApprovedUserIDs, row.VerifiedUserIDs}
		for i := range actual {
			require.ElementsMatch(t, want.Users[i], actual[i], "search set %s", columns[i])
		}
		require.ElementsMatch(t, want.Actions, row.DistinctActions)
		require.Equal(t, want.Bot, row.BotAction)
		require.Equal(t, want.Last.ID, row.FileID)
		require.Equal(t, want.First.User, row.SubmitterID)
		require.Equal(t, want.Last.User, row.LastUploaderID)
		require.EqualValues(t, want.FileCount, row.FileCount)
		require.True(t, fixtureEpoch.Add(time.Duration(want.First.Micros)*time.Microsecond).Equal(row.UploadedAt))
		require.True(t, fixtureEpoch.Add(time.Duration(want.Comment.Micros)*time.Microsecond).Equal(row.UpdatedAt))
	}
	for i, op := range trace {
		step = i
		switch op.Kind {
		case "file":
			f.File(t, fixtureFile{ID: op.ID, SubmissionID: op.Submission, UserID: op.User, At: fixtureEpoch.Add(time.Duration(op.Micros) * time.Microsecond)})
		case "comment":
			f.Comment(t, op.ID, op.Submission, op.User, op.Action, fixtureEpoch.Add(time.Duration(op.Micros)*time.Microsecond), nil)
		case "delete-file":
			_, err := f.Maria.ExecContext(f.Ctx, testSQL("UPDATE submission_file SET deleted_at=? WHERE id=?"), fixtureEpoch.Add(time.Hour), op.ID)
			require.NoError(t, err)
		case "delete-comment":
			deleteFixtureComment(t, f, op.ID)
		default:
			t.Fatalf("unknown operation %q", op.Kind)
		}
		model.apply(op)
		f.Rebuild(t, op.Submission)
		// Checking every submission catches cross-submission leaks in shared user
		// histories, not only errors in the row touched by the current operation.
		for sid := int64(1); sid <= 3; sid++ {
			check(sid)
		}
		if (i+1)%12 == 0 || i == len(trace)-1 {
			before := map[int64]map[string]string{}
			for sid := int64(1); sid <= 3; sid++ {
				before[sid] = cacheSnapshot(t, f, sid)
			}
			_, err := f.Maria.ExecContext(f.Ctx, testSQL("DELETE FROM submission_cache WHERE fk_submission_id IN (1,2,3)"))
			require.NoError(t, err)
			f.InTx(t, func(s database.DBSession) {
				for sid := int64(1); sid <= 3; sid++ {
					require.NoError(t, f.DB.RebuildSubmissionCacheTable(s, sid))
				}
			})
			for sid := int64(1); sid <= 3; sid++ {
				check(sid)
				require.Equal(t, before[sid], cacheSnapshot(t, f, sid), "erased rebuild")
			}
			f.Rebuild(t, 1, 2, 3)
			for sid := int64(1); sid <= 3; sid++ {
				require.Equal(t, before[sid], cacheSnapshot(t, f, sid), "repeated rebuild")
			}
		}
	}
}

func TestSubmissionGeneratedHistoryCorpus(t *testing.T) {
	kinds, actions, users := map[string]bool{}, map[string]bool{}, map[int64]bool{}
	tied, backdated := false, false
	observed := [5]bool{}
	restored := false
	for _, seed := range []int64{7, 42, 20260909} {
		trace := generatedHistory(seed)
		model := newHistoryModel()
		require.Equal(t, trace, generatedHistory(seed), "map traversal must not make seeded replay nondeterministic")
		files, comments := map[int64]int{}, map[int64]int{}
		times := map[int64]map[int64]bool{}
		latest := map[int64]int64{}
		for _, op := range trace {
			before := model.reduce(op.Submission)
			model.apply(op)
			after := model.reduce(op.Submission)
			active := false
			for i, ids := range after.Users {
				observed[i] = observed[i] || len(ids) > 0
				active = active || len(ids) > 0
			}
			restored = restored || (len(before.Actions) == 1 && before.Actions[0] == "reject" && active)
			kinds[op.Kind] = true
			if op.Kind == "file" {
				files[op.Submission]++
			}
			if op.Kind == "comment" {
				comments[op.Submission]++
				actions[op.Action] = true
				users[op.User] = true
				if times[op.Submission] == nil {
					times[op.Submission] = map[int64]bool{}
				}
				tied = tied || times[op.Submission][op.Micros]
				backdated = backdated || op.Micros < latest[op.Submission]
				times[op.Submission][op.Micros] = true
				if op.Micros > latest[op.Submission] {
					latest[op.Submission] = op.Micros
				}
			}
		}
		for sid := int64(1); sid <= 3; sid++ {
			require.LessOrEqual(t, files[sid], 4)
			require.LessOrEqual(t, comments[sid], 50)
		}
	}
	for _, kind := range []string{"file", "comment", "delete-file", "delete-comment"} {
		require.True(t, kinds[kind], kind)
	}
	for _, action := range []string{"assign-testing", "unassign-testing", "assign-verification", "unassign-verification", "request-changes", "approve", "verify", "comment", "mark-added", "reject"} {
		require.True(t, actions[action], action)
	}
	for _, user := range []int64{12, 112, 120, 1200, constants.ValidatorID} {
		require.True(t, users[user], "user %d", user)
	}
	for i, seen := range observed {
		require.True(t, seen, "corpus must observe nonempty reviewer set %d", i)
	}
	require.True(t, restored, "corpus must restore reviewer state after deleting reject")
	require.True(t, tied, "corpus must contain same-table timestamp ties")
	require.True(t, backdated, "corpus must differ in insertion vs chronological order")
}
