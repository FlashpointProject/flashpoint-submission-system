package integration_tests

import (
	"sort"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/stretchr/testify/require"
)

// historyOp is a portable replay trace: times are microseconds relative to
// fixtureEpoch. Deletes reference IDs, not slice positions or generated choices.
type historyOp struct {
	Kind       string `json:"kind"`
	ID         int64  `json:"id"`
	Submission int64  `json:"submission"`
	User       int64  `json:"user,omitempty"`
	Micros     int64  `json:"micros,omitempty"`
	Action     string `json:"action,omitempty"`
}
type historyModel struct{ files, comments map[int64]historyOp }

func newHistoryModel() *historyModel {
	return &historyModel{map[int64]historyOp{}, map[int64]historyOp{}}
}
func (m *historyModel) apply(op historyOp) {
	switch op.Kind {
	case "file":
		m.files[op.ID] = op
	case "comment":
		m.comments[op.ID] = op
	case "delete-file":
		delete(m.files, op.ID)
	case "delete-comment":
		delete(m.comments, op.ID)
	default:
		panic("unknown history operation: " + op.Kind)
	}
}

type historyState struct {
	Users                [5][]int64 // testing, verification assignment, changes, approval, verification
	Actions              []string
	Bot                  string
	First, Last, Comment historyOp
	FileCount            int
}

func historyBefore(a, b historyOp) bool {
	return a.Micros < b.Micros || a.Micros == b.Micros && a.ID < b.ID
}

// reduce replays live comments in chronological order. It deliberately has no
// SQL/cache dependency and no latest-enabler/latest-disabler query analogue.
// Cross-table upload boundaries remain strictly timestamp-based; same-table
// events use ID to break timestamp ties. Reject suppresses all reviewer sets
// while ANY live reject remains, even if a later enable action exists.
func (m *historyModel) reduce(sid int64) historyState {
	out := historyState{Actions: []string{}}
	for _, f := range m.files {
		if f.Submission != sid {
			continue
		}
		out.FileCount++
		if out.First.ID == 0 || historyBefore(f, out.First) {
			out.First = f
		}
		if out.Last.ID == 0 || historyBefore(out.Last, f) {
			out.Last = f
		}
	}
	comments := []historyOp{}
	for _, c := range m.comments {
		if c.Submission == sid {
			comments = append(comments, c)
		}
	}
	sort.Slice(comments, func(i, j int) bool { return historyBefore(comments[i], comments[j]) })
	sets := [5]map[int64]bool{}
	for i := range sets {
		sets[i] = map[int64]bool{}
		out.Users[i] = []int64{}
	}
	actions := map[string]bool{}
	for _, c := range comments {
		out.Comment = c
		actions[c.Action] = true
		if c.User == constants.ValidatorID {
			out.Bot = c.Action
			continue
		}
		switch c.Action {
		case "assign-testing":
			sets[0][c.User] = true
		case "unassign-testing":
			delete(sets[0], c.User)
		case "assign-verification":
			sets[1][c.User] = true
		case "unassign-verification":
			delete(sets[1], c.User)
		case "request-changes":
			sets[2][c.User] = true
			delete(sets[3], c.User)
			delete(sets[4], c.User)
		case "approve":
			delete(sets[2], c.User)
			if out.Last.ID != 0 && c.Micros > out.Last.Micros {
				sets[3][c.User] = true
			}
		case "verify":
			delete(sets[2], c.User)
			if out.Last.ID != 0 && c.Micros > out.Last.Micros {
				sets[4][c.User] = true
			}
		}
	}
	if actions["reject"] {
		out.Actions = []string{"reject"}
		return out
	}
	for a := range actions {
		out.Actions = append(out.Actions, a)
	}
	sort.Strings(out.Actions)
	for i, set := range sets {
		for id := range set {
			out.Users[i] = append(out.Users[i], id)
		}
		sort.Slice(out.Users[i], func(a, b int) bool { return out.Users[i][a] < out.Users[i][b] })
	}
	return out
}

func TestSubmissionHistoryModel(t *testing.T) {
	// Focused reducer expectations protect the oracle itself. Broader SQL edge
	// cases already live in submission_cache_test.go and comment_ordering_test.go.
	m := newHistoryModel()
	apply := func(kind string, id, uid, at int64, action string) {
		m.apply(historyOp{Kind: kind, ID: id, Submission: 1, User: uid, Micros: at, Action: action})
	}
	apply("file", 1, 12, 10, "")
	apply("comment", 1, 12, 11, "approve")
	apply("comment", 2, 112, 11, "verify")
	apply("comment", 3, 12, 11, "request-changes")
	require.Equal(t, [5][]int64{{}, {}, {12}, {}, {112}}, m.reduce(1).Users)
	apply("delete-comment", 3, 0, 0, "")
	require.Equal(t, [5][]int64{{}, {}, {}, {12}, {112}}, m.reduce(1).Users)
	apply("comment", 4, 12, 9, "assign-testing")
	apply("comment", 5, 112, 9, "assign-verification")
	apply("file", 2, 112, 11, "") // equal-timestamp approval and verification do not survive
	require.Equal(t, [5][]int64{{12}, {112}, {}, {}, {}}, m.reduce(1).Users)
	apply("delete-file", 2, 0, 0, "")
	require.Equal(t, [5][]int64{{12}, {112}, {}, {12}, {112}}, m.reduce(1).Users)
	apply("comment", 6, constants.ValidatorID, 12, "request-changes")
	require.Equal(t, "request-changes", m.reduce(1).Bot)
	require.Empty(t, m.reduce(1).Users[2])
	apply("comment", 7, 112, 8, "reject") // historical reject suppresses later enables
	require.Equal(t, [5][]int64{{}, {}, {}, {}, {}}, m.reduce(1).Users)
	require.Equal(t, []string{"reject"}, m.reduce(1).Actions)
	apply("delete-comment", 7, 0, 0, "")
	apply("comment", 8, 12, 9, "unassign-testing")
	require.Empty(t, m.reduce(1).Users[0])
	apply("comment", 9, 12, 9, "assign-testing")
	require.Equal(t, []int64{12}, m.reduce(1).Users[0])
	apply("file", 3, 12, 10, "")
	require.EqualValues(t, 1, m.reduce(1).First.ID)
	require.EqualValues(t, 3, m.reduce(1).Last.ID)
	m.apply(historyOp{Kind: "comment", ID: 99, Submission: 2, User: 12, Micros: 999, Action: "reject"})
	require.EqualValues(t, 6, m.reduce(1).Comment.ID)
	require.NotContains(t, m.reduce(1).Actions, "reject")
	apply("delete-file", 1, 0, 0, "")
	apply("delete-file", 3, 0, 0, "")
	require.Empty(t, m.reduce(1).Users[3])
	require.Empty(t, m.reduce(1).Users[4])
	require.Equal(t, []int64{12}, m.reduce(1).Users[0])
}
