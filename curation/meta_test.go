package curation

import (
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseMetaOK(t *testing.T, src string) *types.CurationMeta {
	t.Helper()
	meta, errs := parseMeta([]byte(src))
	require.Empty(t, errs)
	return meta
}

func TestParseMetaScalarsAreText(t *testing.T) {
	meta := parseMetaOK(t, `
UUID: 449f1dfa-c592-48e6-9939-de99d829c68b
Version: 1.0
Release Date: 2004-10-23
Languages: no
Extreme: false
Title: "quoted: text"
Series: ~
Developer:
Publisher: ''
Source: null
Original Description: |-
  Line one
  Line two
Game Notes: >
  folded
  text
`)
	assert.Equal(t, strPtr("449f1dfa-c592-48e6-9939-de99d829c68b"), meta.UUID)
	assert.Equal(t, strPtr("1.0"), meta.Version)
	assert.Equal(t, strPtr("2004-10-23"), meta.ReleaseDate)
	assert.Equal(t, strPtr("no"), meta.Languages)
	assert.Equal(t, strPtr("false"), meta.Extreme)
	assert.Equal(t, strPtr("quoted: text"), meta.Title)
	assert.Nil(t, meta.Series)
	assert.Nil(t, meta.Developer)
	assert.Equal(t, strPtr(""), meta.Publisher)
	assert.Nil(t, meta.Source)
	assert.Equal(t, strPtr("Line one\nLine two"), meta.OriginalDescription)
	assert.Equal(t, strPtr("folded text\n"), meta.GameNotes)
	assert.Equal(t, []*types.CurationAdditionalApp{}, meta.AdditionalApps)
}

func TestParseMetaPlatforms(t *testing.T) {
	for _, tc := range []struct {
		src             string
		platforms       *string
		primaryPlatform *string
	}{
		{"Title: x", nil, nil},
		{"Platforms: ' HTML5 ; Flash'", strPtr(" HTML5 ; Flash"), strPtr("HTML5")},
		{"Platforms: Flash; HTML5\nPrimary Platform: HTML5", strPtr("Flash; HTML5"), strPtr("HTML5")},
		// The legacy key wins, as in Python.
		{"Platforms: HTML5\nPlatform: Flash", strPtr("Flash"), strPtr("Flash")},
		{"Platform: Flash\nPlatforms:", strPtr("Flash"), strPtr("Flash")},
		{"Platforms:", nil, nil}, // Python crashed here
	} {
		meta := parseMetaOK(t, tc.src)
		assert.Equal(t, tc.platforms, meta.Platform, tc.src)
		assert.Equal(t, tc.primaryPlatform, meta.PrimaryPlatform, tc.src)
	}
}

func TestParseMetaIgnoresUnknownKeysAndCase(t *testing.T) {
	meta := parseMetaOK(t, "title: lower\nSome Launcher Field: [1, 2]\ngame_exists: true\nTitle: Real")
	assert.Equal(t, &types.CurationMeta{Title: strPtr("Real"), AdditionalApps: []*types.CurationAdditionalApp{}}, meta)
}

func TestParseMetaAliasesAndLineEndings(t *testing.T) {
	meta := parseMetaOK(t, "\xef\xbb\xbfDeveloper: &dev Someone\r\nPublisher: *dev\r\n")
	assert.Equal(t, strPtr("Someone"), meta.Developer)
	assert.Equal(t, strPtr("Someone"), meta.Publisher)
}

func TestParseMetaErrors(t *testing.T) {
	for _, tc := range []struct {
		src  string
		errs []string
	}{
		{"Title: x\nTitle: y", []string{msgMetaYAML}},
		{"? [a]\n: b", []string{msgMetaYAML}},
		{"Additional Applications:\n  A: {Application Path: a, Launch Command: b}\n  A: {Application Path: a, Launch Command: b}",
			[]string{msgMetaYAML}},
		{"--- \n...\n", []string{msgMetaEmpty}},
	} {
		meta, errs := parseMeta([]byte(tc.src))
		assert.Nil(t, meta, tc.src)
		assert.Equal(t, tc.errs, errs, tc.src)
	}
}
