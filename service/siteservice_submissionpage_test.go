package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/appcache"
	"github.com/FlashpointProject/flashpoint-submission-system/database"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

type submissionPageSession struct{ database.DBSession }

func (submissionPageSession) Rollback() error { return nil }

type submissionPageDAL struct {
	database.DAL
	sessions int
}

func (d *submissionPageDAL) NewSession(context.Context) (database.DBSession, error) {
	d.sessions++
	return submissionPageSession{}, nil
}

func (*submissionPageDAL) SearchSubmissions(database.DBSession, *types.SubmissionsFilter) ([]*types.ExtendedSubmission, int64, error) {
	return nil, 0, nil
}

type submissionPagePGDAL struct {
	database.PGDAL
	opened bool
}

func (d *submissionPagePGDAL) NewSession(context.Context) (database.PGDBSession, error) {
	d.opened = true
	return nil, errors.New("unexpected PostgreSQL transaction")
}

type unavailableTagValidator struct{ Validator }

func (unavailableTagValidator) GetTags(context.Context) ([]types.Tag, error) {
	return nil, errors.New("validator unavailable")
}

func TestSubmissionPageDoesNotOpenPostgresWhenValidatorFails(t *testing.T) {
	dal := &submissionPageDAL{}
	pgdal := &submissionPagePGDAL{}
	s := &SiteService{dal: dal, pgdal: pgdal, validator: unavailableTagValidator{}}

	_, err := s.GetViewSubmissionPageData(context.Background(), 0, 123)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "submission not found"), err)
	require.Equal(t, 1, dal.sessions, "missing submission stops before viewer data or enrichment")
	require.False(t, pgdal.opened)
}

type submissionTagValidator struct {
	Validator
	tags []types.Tag
}

func (v submissionTagValidator) GetTags(context.Context) ([]types.Tag, error) {
	return v.tags, nil
}

func TestSubmissionPageTagListContainsOnlySubmissionTags(t *testing.T) {
	ctx := context.Background()
	catalogue := []types.Tag{
		{ID: 1, Name: "Action", Description: "Action description", Category: "genre"},
		{ID: 2, Name: "Puzzle", Description: "Puzzle description"},
		{ID: 3, Name: " Role-Playing ", Description: "RPG description"},
	}
	originalCatalogue := append([]types.Tag(nil), catalogue...)
	s := &SiteService{validator: submissionTagValidator{tags: catalogue}}
	meta := func(tags string) *types.CurationMeta { return &types.CurationMeta{Tags: &tags} }
	cases := []struct {
		name string
		meta *types.CurationMeta
		want []types.Tag
	}{
		{"normalized exact matches", meta(" role-playing ; ACTION ; action ;; Unknown ; Act "), []types.Tag{catalogue[0], catalogue[2]}},
		{"different submission", meta("Puzzle"), []types.Tag{catalogue[1]}},
		{"unknown tag", meta("Unknown"), []types.Tag{}},
		{"empty tags", meta(" ; ; "), []types.Tag{}},
		{"missing tags", &types.CurationMeta{}, []types.Tag{}},
		{"missing metadata", nil, []types.Tag{}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sid := int64(i + 1)
			key := strconv.FormatInt(sid, 10)
			// Seed the database snapshots to exercise the real page-data and JSON
			// path, including repeated reads, without external databases.
			_, err := s.readCaches().submissions.Get(ctx, key, func(context.Context) (appcache.Snapshot[submissionSnapshot], error) {
				return appcache.Snapshot[submissionSnapshot]{Value: submissionSnapshot{Meta: tc.meta}}, nil
			})
			require.NoError(t, err)
			_, err = s.readCaches().subscriptions.Get(ctx, subscriptionKey(0, sid), func(context.Context) (appcache.Snapshot[bool], error) {
				return appcache.Snapshot[bool]{}, nil
			})
			require.NoError(t, err)
			_, err = s.readCaches().navigation.Get(ctx, key, func(context.Context) (appcache.Snapshot[submissionNavigation], error) {
				return appcache.Snapshot[submissionNavigation]{}, nil
			})
			require.NoError(t, err)
			for range 2 {
				page, err := s.GetViewSubmissionPageData(ctx, 0, sid)
				require.NoError(t, err)
				require.Equal(t, tc.want, page.TagList)
				require.Equal(t, tc.meta, page.CurationMeta)
				encoded, err := json.Marshal(page)
				require.NoError(t, err)
				var response map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(encoded, &response))
				var tags []types.Tag
				require.NoError(t, json.Unmarshal(response["TagList"], &tags))
				require.Equal(t, tc.want, tags, "TagList remains a JSON array, including when empty")
				require.Equal(t, originalCatalogue, catalogue, "filtering must not mutate the validator catalogue")
			}
		})
	}
}

// The file summary must not use either the original owner or latest commenter.
type submissionUploaderDAL struct {
	database.DAL
	username  string
	requested []int64
}

func (d *submissionUploaderDAL) NewSession(context.Context) (database.DBSession, error) {
	return submissionPageSession{}, nil
}
func (d *submissionUploaderDAL) GetDiscordUser(_ database.DBSession, uid int64) (*types.DiscordUser, error) {
	d.requested = append(d.requested, uid)
	return &types.DiscordUser{ID: uid, Username: d.username}, nil
}
func TestSubmissionPageCurrentFileUploaderAndProfileRefresh(t *testing.T) {
	ctx := context.Background()
	dal := &submissionUploaderDAL{username: "Current uploader"}
	s := &SiteService{dal: dal, validator: submissionTagValidator{}}
	_, err := s.readCaches().submissions.Get(ctx, "42", func(context.Context) (appcache.Snapshot[submissionSnapshot], error) {
		return appcache.Snapshot[submissionSnapshot]{Value: submissionSnapshot{Submissions: []*types.ExtendedSubmission{{SubmissionID: 42, SubmitterID: 1, UpdaterID: 3, LastUploaderID: 2}}}}, nil
	})
	require.NoError(t, err)
	_, err = s.readCaches().subscriptions.Get(ctx, subscriptionKey(0, 42), func(context.Context) (appcache.Snapshot[bool], error) { return appcache.Snapshot[bool]{}, nil })
	require.NoError(t, err)
	_, err = s.readCaches().navigation.Get(ctx, "42", func(context.Context) (appcache.Snapshot[submissionNavigation], error) {
		return appcache.Snapshot[submissionNavigation]{}, nil
	})
	require.NoError(t, err)
	for range 2 {
		page, err := s.GetViewSubmissionPageData(ctx, 0, 42)
		require.NoError(t, err)
		require.Equal(t, "Current uploader", page.CurrentFileUploader)
	}
	require.Equal(t, []int64{2}, dal.requested, "uploader lookup should use the existing user cache")
	dal.username = "Renamed uploader"
	require.NoError(t, s.cacheCoordinator.Commit([]appcache.Dependency{appcache.Key("user", 2)}, func() error { return nil }))
	page, err := s.GetViewSubmissionPageData(ctx, 0, 42)
	require.NoError(t, err)
	require.Equal(t, "Renamed uploader", page.CurrentFileUploader)
	require.Equal(t, []int64{2, 2}, dal.requested)
}
