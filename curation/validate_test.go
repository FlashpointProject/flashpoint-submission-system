package curation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLookups answers from fixed lists, like the mocks of the Python tests.
type fakeLookups struct {
	tags           []string
	launchCommands []string
	err            error
}

// pythonLookups returns the lists curation_validator_test.py mocked, plus
// extra tags.
func pythonLookups(extraTags ...string) *fakeLookups {
	return &fakeLookups{
		tags: append([]string{"A", "B", "E"}, extraTags...),
		launchCommands: []string{
			"http://www.bluemaxima.org/a.html",
			"http://www.bluemaxima.org/b.html",
			"http://localflash/lab/c.html",
		},
	}
}

func (f *fakeLookups) TagExists(_ context.Context, name string) (bool, error) {
	return slices.ContainsFunc(f.tags, func(tag string) bool { return strings.EqualFold(tag, name) }), f.err
}

func (f *fakeLookups) LaunchCommandExists(_ context.Context, launchCommand string) (bool, error) {
	return slices.Contains(f.launchCommands, launchCommand), f.err
}

// testNow is the validator's clock in tests.
var testNow = time.Date(2026, time.November, 9, 12, 0, 0, 0, time.UTC)

func testValidator(t *testing.T, lookups Lookups) *Validator {
	logger, _ := logtest.NewNullLogger()
	v := New(lookups, t.TempDir(), logger)
	v.now = func() time.Time { return testNow }
	return v
}

func validate(t *testing.T, lookups Lookups, path string) *types.ValidatorResponse {
	t.Helper()
	r, err := testValidator(t, lookups).Validate(context.Background(), path)
	require.NoError(t, err)
	return r
}

// The Python test suite's fixture tests, for both archive formats. Errors
// and warnings are compared in order.
func TestValidateFixtures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		errors   []string
		warnings []string
		extreme  bool
	}{
		{name: "valid"},
		{name: "invalid_extreme", errors: []string{"Curation is extreme but lacks extreme tags."}, extreme: true},
		// Python counted the test tag "E" as extreme; it was removed from
		// extreme_tags.json, so this fixture now lacks extreme tags too.
		// TestValidateExtremeAndBlacklistedTags has the valid cases.
		{name: "valid_extreme", errors: []string{"Curation is extreme but lacks extreme tags."}, extreme: true},
		// Python accepted meta.txt.
		{name: "valid_legacy", errors: []string{msgMetaTxt}},
		{name: "valid_legacy_genre", errors: []string{msgMetaTxt}},
		{name: "empty_meta", errors: []string{"The meta file seems to be empty."}},
		{name: "duplicate_launch_command", errors: []string{
			"Identical launch command already present in the master database. Is your curation a duplicate?"}},
		{name: "capital_extension_logo", errors: []string{"Logo file extension must be lowercase."}},
		{name: "capital_extension_screenshot", errors: []string{"Screenshot file extension must be lowercase."}},
		{name: "nul_languages", errors: []string{"The `Languages` property in the meta file is mandatory."}},
		{name: "empty_content", errors: []string{"No files found in content folder."}},
		{name: "missing_content", errors: []string{"Content folder not found."}},
		{name: "missing_logo", errors: []string{"Logo file is either missing or its filename is incorrect."}},
		{name: "missing_meta", errors: []string{
			"Meta file is either missing or its filename is incorrect. Are you using Flashpoint Core for curating?"}},
		{name: "missing_root_folder", errors: []string{
			"Logo, screenshot, content folder and meta not found. Is your curation structured properly?"}},
		{name: "missing_ss", errors: []string{"Screenshot file is either missing or its filename is incorrect."}},
		{name: "unknown_tag", warnings: []string{
			"Tag `Unknown Tag` is not a known tag, please verify (did you write it correctly?).",
			"Tag `Another Unknown Tag` is not a known tag, please verify (did you write it correctly?)."}},
		{name: "missing_tags", errors: []string{"Missing tags. At least one tag must be specified."}},
		{name: "missing_title", errors: []string{"The `Title` property in the meta file is mandatory."}},
		{name: "missing_application_path", errors: []string{
			"The `Application Path` property in the meta file is mandatory."}},
		{name: "missing_launch_command", errors: []string{
			"The `Launch Command` property in the meta file is mandatory."}},
		{name: "missing_languages", errors: []string{"The `Languages` property in the meta file is mandatory."}},
		{name: "comma_in_languages", errors: []string{"Languages should be separated with semicolons, not commas."}},
		{name: "common_bad_language", errors: []string{
			"The correct ISO 639-1 language code for Japanese is `ja`, not `jp`."}},
		{name: "language_name", errors: []string{
			"Languages must be in ISO 639-1 format, so please use `ja` instead of `Japanese`"}},
		{name: "missing_source", errors: []string{"The `Source` property in the meta file is mandatory."}},
		{name: "missing_status", errors: []string{"The `Status` property in the meta file is mandatory."}},
		{name: "Norwegian"},
		{name: "languages_semicolon"},
		{name: "invalid_date", errors: []string{"Invalid release date. Ensure entered date is valid."}},
		{name: "localflash_too_many_files", errors: []string{
			"Content must be in additional folder in localflash rather than in localflash directly."}},
		{name: "localflash_no_folder", errors: []string{
			"Content must be in additional folder in localflash rather than in localflash directly."}},
		{name: "localflash_bad_name", errors: []string{
			"Extremely common localflash containing folder name, please change."}},
		{name: "none_library"},
		{name: "invalid_ruffle", errors: []string{"Ruffle Support must be '' or a value in '['standalone']'"}},
		{name: "primary_platform"},
		{name: "valid_addapps"},
	} {
		for _, ext := range []string{"zip", "7z"} {
			t.Run(tc.name+"."+ext, func(t *testing.T) {
				r := validate(t, pythonLookups(), fixture("test_curation_"+tc.name+"."+ext))
				assert.Equal(t, append([]string{}, tc.errors...), r.CurationErrors)
				assert.Equal(t, append([]string{}, tc.warnings...), r.CurationWarnings)
				assert.Equal(t, tc.extreme, r.IsExtreme)
			})
		}
	}
}

func TestValidateArchiveProblems(t *testing.T) {
	for _, tc := range []struct {
		file string
		err  string
	}{
		{"test_curation_rar.rar", "Curations must be either .zip or .7z, not .rar."},
		{"test_curation_invalid_archive.zip", "There seems to a problem with your zip file."},
		{"test_curation_invalid_archive.7z", "There seems to a problem with your 7z file."},
		{"test_curation_missing_root_folder.zip", msgNotStructured},
	} {
		t.Run(tc.file, func(t *testing.T) {
			r := validate(t, pythonLookups(), fixture(tc.file))
			assert.Equal(t, []string{tc.err}, r.CurationErrors)
			assert.Equal(t, []string{}, r.CurationWarnings)
			// As in Python, nothing else is reported.
			assert.Equal(t, types.CurationMeta{}, r.Meta)
			assert.Nil(t, r.Images)
			assert.Zero(t, r.CurationType)
			assert.False(t, r.IsExtreme)
		})
	}
}

func TestValidateMissingFileIsAnError(t *testing.T) {
	_, err := testValidator(t, pythonLookups()).Validate(context.Background(), fixture("does_not_exist.zip"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestValidateValidResponse(t *testing.T) {
	for _, ext := range []string{"zip", "7z"} {
		t.Run(ext, func(t *testing.T) {
			path := fixture("test_curation_valid." + ext)
			r := validate(t, pythonLookups(), path)

			assert.Equal(t, "test_curation_valid."+ext, r.Filename)
			assert.Equal(t, path, r.Path)
			assert.Equal(t, CurationTypeFlashGame, r.CurationType)
			assert.Equal(t, types.CurationMeta{
				Title:           strPtr("Sesame Street: Where Is Thumbkin?"),
				AlternateTitles: strPtr("Where Is Thumbkin?; Sesame Street: Toddler: Where Is Thumbkin?; Toddler: Where Is Thumbkin?"),
				Library:         strPtr("arcade"),
				Developer:       strPtr("Sesame Workshop"),
				Publisher:       strPtr("Sesame Workshop"),
				PlayMode:        strPtr("Single Player"),
				Languages:       strPtr("en"),
				Extreme:         strPtr("No"),
				Tags:            strPtr("A; B"),
				TagCategories:   strPtr("default; default; default"),
				Source:          strPtr("http://sesamestreet.org/"),
				Platform:        strPtr("Flash"), // from the legacy Platform key
				PrimaryPlatform: strPtr("Flash"), // defaulted
				Status:          strPtr("Playable"),
				ApplicationPath: strPtr(`FPSoftware\Basilisk-Portable\Basilisk-Portable.exe`),
				LaunchCommand:   strPtr("http://www.sesamestreet.org/ssgames/flash/129.html"),
				AdditionalApps:  []*types.CurationAdditionalApp{},
				// Series, Release Date, Version, Game Notes, Original
				// Description and Curation Notes are null.
			}, r.Meta)

			a := openFixture(t, "test_curation_valid."+ext)
			files, err := a.ReadFiles(map[string]int64{
				validRoot + "/logo.png": MaxImageSize,
				validRoot + "/ss.png":   MaxImageSize,
			})
			require.NoError(t, err)
			assert.Equal(t, []types.ValidatorResponseImage{
				{Type: "logo", Data: base64.StdEncoding.EncodeToString(files[validRoot+"/logo.png"])},
				{Type: "screenshot", Data: base64.StdEncoding.EncodeToString(files[validRoot+"/ss.png"])},
			}, r.Images)
		})
	}
}

// FPFSS and JSON consumers get empty lists, not null.
func TestValidateResponseJSONHasEmptyLists(t *testing.T) {
	r := validate(t, pythonLookups(), fixture("test_curation_valid.zip"))
	data, err := json.Marshal(r)
	require.NoError(t, err)
	s := string(data)
	assert.Contains(t, s, `"curation_errors":[]`)
	assert.Contains(t, s, `"curation_warnings":[]`)
	assert.Contains(t, s, `"curation_notes":[]`)
	assert.Contains(t, s, `"Additional Applications":[]`)
	assert.Contains(t, s, `"curation_type":1`)
}

func TestValidateCurationType(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int
	}{
		{"none_library", CurationTypeFlashGame},
		{"localflash_bad_name", CurationTypeAnimation}, // Library: theatre
		{"primary_platform", CurationTypeFlashGame},    // Platforms: Flash; HTML5
	} {
		r := validate(t, pythonLookups(), fixture("test_curation_"+tc.name+".zip"))
		assert.Equal(t, tc.want, r.CurationType, tc.name)
	}

	r := validateMeta(t, pythonLookups(), withMeta("-Platform", "Platforms: HTML5"))
	assert.Equal(t, CurationTypeOtherGame, r.CurationType)
	r = validateMeta(t, pythonLookups(), withMeta("-Platform"))
	assert.Equal(t, CurationTypeFlashGame, r.CurationType)
}

func TestValidatePlatforms(t *testing.T) {
	for _, ext := range []string{"zip", "7z"} {
		r := validate(t, pythonLookups(), fixture("test_curation_valid."+ext))
		assert.Equal(t, strPtr("Flash"), r.Meta.Platform)
		assert.Equal(t, strPtr("Flash"), r.Meta.PrimaryPlatform)

		r = validate(t, pythonLookups(), fixture("test_curation_primary_platform."+ext))
		assert.Equal(t, strPtr("Flash; HTML5"), r.Meta.Platform)
		assert.Equal(t, strPtr("HTML5"), r.Meta.PrimaryPlatform)
	}
}

func TestValidateNorwegianStaysText(t *testing.T) {
	for _, ext := range []string{"zip", "7z"} {
		r := validate(t, pythonLookups(), fixture("test_curation_Norwegian."+ext))
		assert.Equal(t, strPtr("no"), r.Meta.Languages)
		assert.Equal(t, strPtr("No"), r.Meta.Extreme)
	}
}

func TestValidateAdditionalAppsFixture(t *testing.T) {
	for _, ext := range []string{"zip", "7z"} {
		r := validate(t, pythonLookups(), fixture("test_curation_valid_addapps."+ext))
		assert.Equal(t, strPtr("test"), r.Meta.Extras)
		assert.Equal(t, strPtr("test"), r.Meta.Message)
		assert.Equal(t, []*types.CurationAdditionalApp{
			{Heading: strPtr("Test"), ApplicationPath: strPtr("test"), LaunchCommand: strPtr("test")},
		}, r.Meta.AdditionalApps)
	}
}

// The content of test_curation_2GB is never decompressed.
func TestValidateLargeContent(t *testing.T) {
	r := validate(t, pythonLookups(), fixture("test_curation_2GB.zip"))
	assert.Equal(t, []string{}, r.CurationErrors)
	assert.Equal(t, []string{}, r.CurationWarnings)
	assert.Len(t, r.Images, 2)
}

func TestValidateEntryLimit(t *testing.T) {
	for _, ext := range []string{"zip", "7z"} {
		v := testValidator(t, pythonLookups())
		v.maxEntries = 1
		r, err := v.Validate(context.Background(), fixture("test_curation_valid."+ext))
		require.NoError(t, err)
		assert.Equal(t, []string{"The archive contains too many members (`14/1`)."}, r.CurationErrors)
	}
}

// Large archives get a note for the archive size and one for the
// uncompressed size, each from NoteSize on; notes change nothing else.
func TestSizeNotes(t *testing.T) {
	for _, ext := range []string{"zip", "7z"} {
		path := fixture("test_curation_valid." + ext)
		a, err := Open(path, MaxEntries)
		require.NoError(t, err)
		archiveSize, uncompressedSize := a.Size, a.UncompressedSize()
		require.NoError(t, a.Close())
		require.Less(t, archiveSize, uncompressedSize)

		v := testValidator(t, pythonLookups())
		plain, err := v.Validate(context.Background(), path)
		require.NoError(t, err)
		assert.Empty(t, plain.CurationNotes)

		v.noteSize = archiveSize
		r, err := v.Validate(context.Background(), path)
		require.NoError(t, err)
		assert.Equal(t, []string{"The archive is 0.0 GB.", "The curation is 0.0 GB uncompressed."}, r.CurationNotes)
		assert.Equal(t, plain.CurationErrors, r.CurationErrors)
		assert.Equal(t, plain.CurationWarnings, r.CurationWarnings)

		v.noteSize = archiveSize + 1
		r, err = v.Validate(context.Background(), path)
		require.NoError(t, err)
		assert.Equal(t, []string{"The curation is 0.0 GB uncompressed."}, r.CurationNotes)

		v.noteSize = uncompressedSize + 1
		r, err = v.Validate(context.Background(), path)
		require.NoError(t, err)
		assert.Empty(t, r.CurationNotes)
	}
	assert.Equal(t, "The archive is 44.3 GB.", fmt.Sprintf(msgArchiveSize, 44.3e9/1e9))
	assert.Equal(t, "The curation is 103.6 GB uncompressed.", fmt.Sprintf(msgUncompressedSize, 103.6e9/1e9))
}

func TestValidateOversizedImage(t *testing.T) {
	path := writeZip(t,
		zipEntry{name: validRoot + "/content/example.com/a.swf", content: "swf"},
		zipEntry{name: validRoot + "/logo.png", content: strings.Repeat("\x00", MaxImageSize+1)},
		zipEntry{name: validRoot + "/ss.png", content: "ss"},
		zipEntry{name: validRoot + "/meta.yaml", content: baseMeta},
	)
	r := validate(t, pythonLookups(), path)
	assert.Equal(t, []string{"Image `" + validRoot + "/logo.png` exceeds the 16MB validation limit."}, r.CurationErrors)
	assert.Equal(t, []types.ValidatorResponseImage{
		{Type: "screenshot", Data: base64.StdEncoding.EncodeToString([]byte("ss"))},
	}, r.Images)
	assert.Equal(t, strPtr("Example"), r.Meta.Title)
}

func TestValidateOversizedMeta(t *testing.T) {
	path := writeZip(t,
		zipEntry{name: validRoot + "/content/example.com/a.swf", content: "swf"},
		zipEntry{name: validRoot + "/logo.png", content: "logo"},
		zipEntry{name: validRoot + "/ss.png", content: "ss"},
		zipEntry{name: validRoot + "/meta.yaml", content: baseMeta + "# " + strings.Repeat("x", MaxMetaSize)},
	)
	r := validate(t, pythonLookups(), path)
	assert.Equal(t, []string{"Metadata file `" + validRoot + "/meta.yaml` exceeds the 16MB validation limit."}, r.CurationErrors)
	// As in Python, the meta is skipped without further errors.
	assert.Equal(t, types.CurationMeta{AdditionalApps: []*types.CurationAdditionalApp{}}, r.Meta)
	assert.Len(t, r.Images, 2)
}

// Python encoded a duplicated ss.png once; the archive is now rejected.
func TestValidateDuplicateScreenshot(t *testing.T) {
	path := writeZip(t,
		zipEntry{name: validRoot + "/content/example.com/a.swf", content: "swf"},
		zipEntry{name: validRoot + "/logo.png", content: "logo"},
		zipEntry{name: validRoot + "/ss.png", content: "ss"},
		zipEntry{name: validRoot + "/ss.png", content: "ss"},
		zipEntry{name: validRoot + "/meta.yaml", content: baseMeta},
	)
	r := validate(t, pythonLookups(), path)
	assert.Equal(t, []string{"The archive contains `" + validRoot + "/ss.png` more than once."}, r.CurationErrors)
}

func TestValidateRootRules(t *testing.T) {
	complete := []zipEntry{
		{name: validRoot + "/content/example.com/a.swf", content: "swf"},
		{name: validRoot + "/logo.png", content: "logo"},
		{name: validRoot + "/ss.png", content: "ss"},
		{name: validRoot + "/meta.yaml", content: baseMeta},
	}
	for name, tc := range map[string]struct {
		extra  []zipEntry
		errors []string
	}{
		"non-UUID root": {
			// Python accepted any root folder name.
		},
		"second folder": {
			extra: []zipEntry{{name: "__MACOSX/._logo.png", content: "x"}},
			errors: []string{"The archive must contain exactly one folder at the top level (the curation folder), " +
				"but it contains 2: `" + validRoot + "`, `__MACOSX`."},
		},
		"file next to the root": {
			extra: []zipEntry{{name: "readme.txt", content: "x"}},
			errors: []string{"Everything must be inside the curation folder `" + validRoot +
				"`, but the archive also contains `readme.txt` at the top level."},
		},
		"meta.yaml and meta.yml": {
			extra:  []zipEntry{{name: validRoot + "/meta.yml", content: baseMeta}},
			errors: []string{msgMetaYAMLAndYML},
		},
	} {
		t.Run(name, func(t *testing.T) {
			entries := append(slices.Clone(complete), tc.extra...)
			if name == "non-UUID root" {
				entries = nil
				for _, e := range complete {
					e.name = "My Game" + strings.TrimPrefix(e.name, validRoot)
					entries = append(entries, e)
				}
			}
			r := validate(t, pythonLookups(), writeZip(t, entries...))
			assert.Equal(t, append([]string{}, tc.errors...), r.CurationErrors)
		})
	}
}

// Python reported errors in this order; FPFSS shows them as a list.
func TestValidateErrorOrder(t *testing.T) {
	meta := withMeta(
		"Release Date: 2004/01/01",
		"Languages: en, de",
		"Title:",
		"Launch Command: https://www.bluemaxima.org/a.html",
		"Tags: Lolicon",
		"Ruffle Support: bad",
	)
	path := writeZip(t,
		zipEntry{name: validRoot + "/content/localflash/a.swf", content: "swf"},
		zipEntry{name: validRoot + "/Logo.png", content: "logo"},
		zipEntry{name: validRoot + "/meta.yaml", content: meta},
	)
	r := validate(t, pythonLookups("Lolicon"), path)
	assert.Equal(t, []string{
		msgLogoCase,
		msgScreenshotMiss,
		msgLocalflashDirectly,
		"Release date 2004/01/01 is incorrect. Release dates should always be in `YYYY-MM-DD` format.",
		msgLanguageCommas,
		"The `Title` property in the meta file is mandatory.",
		msgHTTPS,
		msgExtremeWithoutTags,
		msgBlacklistedTags,
		msgRuffleSupport,
	}, r.CurationErrors)
}

func TestValidateLookupErrors(t *testing.T) {
	lookups := pythonLookups()
	lookups.err = errors.New("database down")
	_, err := testValidator(t, lookups).Validate(context.Background(), fixture("test_curation_valid.zip"))
	require.ErrorIs(t, err, lookups.err)
}

func TestValidateLaunchCommand(t *testing.T) {
	for _, tc := range []struct {
		launchCommand string
		errors        []string
	}{
		{"http://www.example.com/game.swf", nil},
		{"https://www.example.com/game.swf", []string{msgHTTPS}},
		// Python matched "https" anywhere in the launch command.
		{"http://www.example.com/https.swf", nil},
		{"http://www.bluemaxima.org/a.html", []string{msgDuplicateLaunch}},
		{"http://localflash/lab/c.html", []string{msgDuplicateLaunch}},
	} {
		r := validateMeta(t, pythonLookups(), withMeta("Launch Command: "+tc.launchCommand))
		assert.Equal(t, append([]string{}, tc.errors...), r.CurationErrors, tc.launchCommand)
	}
}

func TestValidateExtremeAndBlacklistedTags(t *testing.T) {
	lookups := pythonLookups("Gore", "Lolicon", "LEGACY-Extreme")
	for _, tc := range []struct {
		name    string
		meta    []string
		extreme bool
		errors  []string
	}{
		{"not extreme", []string{"Extreme: No", "Tags: A"}, false, nil},
		{"extreme tag", []string{"Extreme: Yes", "Tags: A; Gore"}, true, nil},
		{"extreme tag without the flag", []string{"Extreme: No", "Tags: Gore"}, true, nil},
		{"extreme tag in lower case", []string{"Extreme: No", "Tags: gore"}, true, nil},
		{"flag without extreme tags", []string{"Extreme: Yes", "Tags: A"}, true, []string{msgExtremeWithoutTags}},
		{"flag true", []string{"Extreme: true", "Tags: A"}, true, []string{msgExtremeWithoutTags}},
		{"flag YES", []string{"Extreme: YES", "Tags: Gore"}, true, nil},
		{"flag no tags", []string{"Extreme: Yes", "Tags:"}, true, []string{msgMissingTags}},
		{"blacklisted tag", []string{"Extreme: No", "Tags: Lolicon"}, true,
			[]string{msgExtremeWithoutTags, msgBlacklistedTags}},
		{"blacklisted and extreme tag", []string{"Extreme: Yes", "Tags: Gore; Lolicon"}, true,
			[]string{msgBlacklistedTags}},
		{"legacy extreme", []string{"Extreme: No", "Tags: LEGACY-Extreme"}, true, []string{msgExtremeWithoutTags}},
		// The test tag was removed from the data file.
		{"tag E", []string{"Extreme: No", "Tags: E"}, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := validateMeta(t, lookups, withMeta(tc.meta...))
			assert.Equal(t, tc.extreme, r.IsExtreme)
			assert.Equal(t, append([]string{}, tc.errors...), r.CurationErrors)
			assert.Equal(t, []string{}, r.CurationWarnings)
		})
	}
}

func TestValidateTags(t *testing.T) {
	for _, tc := range []struct {
		tags     string
		errors   []string
		warnings []string
	}{
		{"A; B", nil, nil},
		{"a;b;", nil, nil}, // case-insensitive, empty entries dropped
		{" ; ", []string{msgMissingTags}, nil},
		{"null", nil, []string{"Tag `null` is not a known tag, please verify (did you write it correctly?)."}},
	} {
		r := validateMeta(t, pythonLookups(), withMeta("Tags: "+quote(tc.tags)))
		assert.Equal(t, append([]string{}, tc.errors...), r.CurationErrors, tc.tags)
		assert.Equal(t, append([]string{}, tc.warnings...), r.CurationWarnings, tc.tags)
	}
	r := validateMeta(t, pythonLookups(), withMeta("-Tags"))
	assert.Equal(t, []string{msgMissingTags}, r.CurationErrors)
}

func TestValidateMandatoryProperties(t *testing.T) {
	for _, key := range []string{"Title", "Languages", "Source", "Launch Command", "Status", "Application Path"} {
		want := []string{"The `" + key + "` property in the meta file is mandatory."}
		for _, change := range []string{"-" + key, key + ":", key + ": ''"} {
			r := validateMeta(t, pythonLookups(), withMeta(change))
			assert.Equal(t, want, r.CurationErrors, change)
		}
	}
	// All missing: one error each, in Python's order.
	r := validateMeta(t, pythonLookups(), withMeta("-Title", "-Languages", "-Source", "-Launch Command", "-Status", "-Application Path"))
	assert.Equal(t, []string{
		"The `Title` property in the meta file is mandatory.",
		"The `Languages` property in the meta file is mandatory.",
		"The `Source` property in the meta file is mandatory.",
		"The `Launch Command` property in the meta file is mandatory.",
		"The `Status` property in the meta file is mandatory.",
		"The `Application Path` property in the meta file is mandatory.",
	}, r.CurationErrors)
}

func TestValidateRuffleSupport(t *testing.T) {
	for _, tc := range []struct {
		value  string
		errors []string
	}{
		{"Ruffle Support: standalone", nil},
		{"Ruffle Support: ''", nil},
		{"Ruffle Support: ' '", nil},
		{"Ruffle Support:", nil},
		{"Ruffle Support: Standalone", []string{msgRuffleSupport}},
		{"Ruffle Support: bad", []string{msgRuffleSupport}},
	} {
		r := validateMeta(t, pythonLookups(), withMeta(tc.value))
		assert.Equal(t, append([]string{}, tc.errors...), r.CurationErrors, tc.value)
	}
}

func TestValidateNonStringScalars(t *testing.T) {
	r := validateMeta(t, pythonLookups(), withMeta(
		"Version: 1.10",
		"Release Date: 2004",
		"Languages: no",
		"Title: 123",
		"Extreme: true",
		"Tags: Gore",
	))
	assert.Equal(t, []string{}, r.CurationErrors)
	assert.Equal(t, strPtr("1.10"), r.Meta.Version)
	assert.Equal(t, strPtr("2004"), r.Meta.ReleaseDate)
	assert.Equal(t, strPtr("no"), r.Meta.Languages)
	assert.Equal(t, strPtr("123"), r.Meta.Title)
	assert.Equal(t, strPtr("true"), r.Meta.Extreme)
	assert.True(t, r.IsExtreme)
}

func TestValidateBadMeta(t *testing.T) {
	for _, tc := range []struct {
		name   string
		meta   string
		errors []string
	}{
		{"syntax error", "Title: [unclosed", []string{msgMetaYAML}},
		{"list", "- Title: x", []string{msgMetaYAML}},
		{"text", "just text", []string{msgMetaYAML}},
		{"not UTF-8", "Title: \xff\xfe", []string{msgMetaYAML}},
		{"duplicate key", baseMeta + "Title: again\n", []string{msgMetaYAML}},
		{"empty", "", []string{msgMetaEmpty}},
		{"only a comment", "# nothing here\n", []string{msgMetaEmpty}},
		{"null", "null\n", []string{msgMetaEmpty}},
		{"tags as a list", withMeta("Tags: [A, B]"), []string{
			"The `Tags` property in the meta file must be text, not a list or mapping. Separate several values with semicolons."}},
		{"several fields not text", withMeta("Languages:\n  - en", "Platform: {a: b}"), []string{
			"The `Languages` property in the meta file must be text, not a list or mapping. Separate several values with semicolons.",
			"The `Platform` property in the meta file must be text, not a list or mapping. Separate several values with semicolons."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := validateMeta(t, pythonLookups(), tc.meta)
			assert.Equal(t, tc.errors, r.CurationErrors)
			// As in Python, a meta that cannot be read stops validation.
			assert.Equal(t, types.CurationMeta{}, r.Meta)
			assert.Nil(t, r.Images)
		})
	}
}

func TestValidateAdditionalApps(t *testing.T) {
	app := func(heading, path, launch string) *types.CurationAdditionalApp {
		return &types.CurationAdditionalApp{Heading: &heading, ApplicationPath: &path, LaunchCommand: &launch}
	}
	for _, tc := range []struct {
		name    string
		addApps string
		apps    []*types.CurationAdditionalApp
		extras  *string
		message *string
		errors  []string
	}{
		{name: "null", addApps: "Additional Applications:", apps: []*types.CurationAdditionalApp{}},
		{name: "missing", addApps: "-Additional Applications", apps: []*types.CurationAdditionalApp{}},
		{name: "empty", addApps: "Additional Applications: {}", apps: []*types.CurationAdditionalApp{}},
		{name: "extras only", addApps: "Additional Applications:\n  Extras: extras", extras: strPtr("extras"),
			apps: []*types.CurationAdditionalApp{}},
		{name: "message only", addApps: "Additional Applications:\n  Message: hello", message: strPtr("hello"),
			apps: []*types.CurationAdditionalApp{}},
		{name: "apps in order", addApps: `Additional Applications:
  Second:
    Application Path: b.exe
    Launch Command: b
  Message: hello
  First:
    Launch Command: a
    Application Path: a.exe
    Wait For Exit: true`,
			message: strPtr("hello"),
			apps:    []*types.CurationAdditionalApp{app("Second", "b.exe", "b"), app("First", "a.exe", "a")}},
		{name: "empty values", addApps: "Additional Applications:\n  App:\n    Application Path: ''\n    Launch Command: ''",
			apps: []*types.CurationAdditionalApp{app("App", "", "")}},
		{name: "missing launch command", addApps: "Additional Applications:\n  App:\n    Application Path: a.exe",
			errors: []string{"The additional application `App` in the meta file must have an `Application Path` and a `Launch Command`."}},
		{name: "null launch command", addApps: "Additional Applications:\n  App:\n    Application Path: a.exe\n    Launch Command:",
			errors: []string{"The additional application `App` in the meta file must have an `Application Path` and a `Launch Command`."}},
		{name: "app is text", addApps: "Additional Applications:\n  App: a.exe",
			errors: []string{"The additional application `App` in the meta file must have an `Application Path` and a `Launch Command`."}},
		{name: "no heading", addApps: "Additional Applications:\n  '':\n    Application Path: a.exe\n    Launch Command: a",
			errors: []string{msgAddAppNoHeading}},
		{name: "list", addApps: "Additional Applications:\n  - Extras: x",
			errors: []string{msgAddAppsNotMapping}},
		{name: "extras is a mapping", addApps: "Additional Applications:\n  Extras: {a: b}",
			errors: []string{"The `Extras` property in the meta file must be text, not a list or mapping. Separate several values with semicolons."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := validateMeta(t, pythonLookups(), withMeta(tc.addApps))
			assert.Equal(t, append([]string{}, tc.errors...), r.CurationErrors)
			assert.Equal(t, tc.apps, r.Meta.AdditionalApps)
			assert.Equal(t, tc.extras, r.Meta.Extras)
			assert.Equal(t, tc.message, r.Meta.Message)
		})
	}
}

func TestValidateLocalflash(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string // under <root>/content/; a name ending in "/" is a directory
		errors  []string
	}{
		{"one folder", []string{"localflash/mygame/a.swf", "localflash/mygame/data/b.xml"}, nil},
		{"no localflash", []string{"example.com/a.swf"}, nil},
		{"localflash next to a domain", []string{"example.com/a.swf", "localflash/mygame/a.swf"}, nil},
		{"empty localflash folder", []string{"example.com/a.swf", "localflash/"}, nil},
		{"file in localflash", []string{"localflash/a.swf"}, []string{msgLocalflashDirectly}},
		{"two folders", []string{"localflash/one/a.swf", "localflash/two/b.swf"}, []string{msgLocalflashDirectly}},
		{"folder and empty folder", []string{"localflash/one/a.swf", "localflash/two/"}, []string{msgLocalflashDirectly}},
		{"file named localflash", []string{"localflash"}, []string{msgLocalflashDirectly}},
		{"common name", []string{"localflash/games/a.swf"}, []string{msgLocalflashCommonName}},
		{"common name differs in case", []string{"localflash/Games/a.swf"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := []zipEntry{
				{name: validRoot + "/logo.png", content: "logo"},
				{name: validRoot + "/ss.png", content: "ss"},
				{name: validRoot + "/meta.yaml", content: baseMeta},
			}
			for _, name := range tc.entries {
				e := zipEntry{name: validRoot + "/content/" + name}
				if !strings.HasSuffix(name, "/") {
					e.content = "x"
				}
				entries = append(entries, e)
			}
			r := validate(t, pythonLookups(), writeZip(t, entries...))
			assert.Equal(t, append([]string{}, tc.errors...), r.CurationErrors)
		})
	}
}

func TestCheckReleaseDate(t *testing.T) {
	recent := func(date string) []string {
		return []string{"Release date " + date + " is less than 3 years ago. Curation should be frozen."}
	}
	format := func(date string) []string {
		return []string{"Release date " + date + " is incorrect. Release dates should always be in `YYYY-MM-DD` format."}
	}
	// 3×365 days before testNow (2026-11-09 12:00) is 2023-11-10 12:00.
	for _, tc := range []struct {
		date     string
		errors   []string
		warnings []string
	}{
		{date: ""},
		{date: "   "},
		{date: "2004"},
		{date: "2004-10"},
		{date: "2004-10-23"},
		{date: " 2004-10-23 "},
		{date: "2008-02-29"},
		{date: "2004-1-1", errors: format("2004-1-1")},
		{date: "2004/10/23", errors: format("2004/10/23")},
		{date: "23.10.2004", errors: format("23.10.2004")},
		{date: "2004-10-23 12:00:00", errors: format("2004-10-23 12:00:00")},
		{date: "Oct 2004", errors: format("Oct 2004")},
		{date: "20041", errors: format("20041")},
		{date: "2008-06-31", errors: []string{msgReleaseDateInvalid}},
		{date: "2007-02-29", errors: []string{msgReleaseDateInvalid}},
		{date: "2008-13", errors: []string{msgReleaseDateInvalid}},
		{date: "2008-00-10", errors: []string{msgReleaseDateInvalid}},
		// Freezer boundary: a year or month counts as its last day.
		{date: "2023-11-10"},
		{date: "2023-11-11", warnings: recent("2023-11-11")},
		{date: "2023-10"},
		{date: "2023-11", warnings: recent("2023-11")},
		{date: "2022"},
		{date: "2023", warnings: recent("2023")},
		{date: "2026-11-09", warnings: recent("2026-11-09")},
		{date: "2030", warnings: recent("2030")},
	} {
		errs, warnings := checkReleaseDate(testNow, tc.date)
		assert.Equal(t, tc.errors, errs, tc.date)
		assert.Equal(t, tc.warnings, warnings, tc.date)
	}
}

// Python's test_is_date_more_than_three_years_ago.
func TestCheckReleaseDatePythonCases(t *testing.T) {
	now := time.Date(2003, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		date   string
		frozen bool // more than three years ago, no warning
	}{
		{"2000-01-01", true},
		{"2000-01-02", false},
		{"2002-02-20", false},
		{"2002", false},
		{"2002-01", false},
	} {
		_, warnings := checkReleaseDate(now, tc.date)
		assert.Equal(t, tc.frozen, len(warnings) == 0, tc.date)
	}
}

func TestValidateReleaseDateWarning(t *testing.T) {
	r := validateMeta(t, pythonLookups(), withMeta("Release Date: 2026-01-01"))
	assert.Equal(t, []string{}, r.CurationErrors)
	assert.Equal(t, []string{"Release date 2026-01-01 is less than 3 years ago. Curation should be frozen."}, r.CurationWarnings)
}

func TestCheckLanguages(t *testing.T) {
	invalid := func(code string) string { return "Code `" + code + "` is not a valid ISO 639-1 language code." }
	for _, tc := range []struct {
		languages string
		errors    []string
	}{
		{"en", nil},
		{"en; ja;", nil},
		{" en ;; ja ", nil},
		{"no", nil},
		{"en, es", []string{msgLanguageCommas}},
		{"en, es; fr,de", []string{msgLanguageCommas, msgLanguageCommas}},
		{"Japanese", []string{"Languages must be in ISO 639-1 format, so please use `ja` instead of `Japanese`"}},
		// Python matched names as substrings and gave three errors (nb, nn, no).
		{"Norwegian", []string{"Languages must be in ISO 639-1 format, so please use `no` instead of `Norwegian`"}},
		{"japanese", []string{invalid("japanese")}},
		{"Spanish", []string{invalid("Spanish")}}, // the name in the file is "Spanish; Castilian"
		{"EN", []string{invalid("EN")}},
		{"xx", []string{invalid("xx")}},
		{"eng", []string{invalid("eng")}},
		{"en; jp; xx", []string{
			"The correct ISO 639-1 language code for Japanese is `ja`, not `jp`.",
			invalid("xx"),
		}},
	} {
		assert.Equal(t, tc.errors, checkLanguages(tc.languages), tc.languages)
	}
}

// One case per entry of lang_replacements.json.
func TestCheckLanguagesReplacements(t *testing.T) {
	for code, want := range map[string]string{
		"sp": "The correct ISO 639-1 language code for Spanish; Castilian is `es`, not `sp`.",
		"ge": "The correct ISO 639-1 language code for German is `de`, not `ge`.",
		"jp": "The correct ISO 639-1 language code for Japanese is `ja`, not `jp`.",
		"cn": "The correct ISO 639-1 language code for Chinese is `zh`, not `cn`.",
		"iw": "The correct ISO 639-1 language code for Hebrew is `he`, not `iw`.",
		"cz": "The correct ISO 639-1 language code for Czech is `cs`, not `cz`.",
		"pe": "The correct ISO 639-1 language code for Persian is `fa`, not `pe`.",
		"gr": "The correct ISO 639-1 language code for Greek, Modern (1453-) is `el`, not `gr`.",
		// kr (Kanuri) and ch (Chamorro) are valid codes, so their
		// replacements never apply, as in Python.
		"kr": "",
		"ch": "",
	} {
		var wantErrs []string
		if want != "" {
			wantErrs = []string{want}
		}
		assert.Equal(t, wantErrs, checkLanguages(code), code)
	}
	assert.Len(t, languageReplacements, 10, "a replacement without a test case")
}

func TestEmbeddedData(t *testing.T) {
	assert.Len(t, languageNames, 184)
	assert.Len(t, languageCodes, 184)
	assert.True(t, extremeTags["gore"])
	assert.False(t, extremeTags["e"], "test tag E must not be in extreme_tags.json")
	assert.True(t, blacklistedTags["lolicon"])
	assert.True(t, commonLocalflashNames["lab"])
}

// baseMeta is a valid meta file with one line per key; withMeta changes it.
const baseMeta = `Title: Example
Alternate Titles: null
Library: arcade
Series: null
Developer: Someone
Publisher: Someone
Play Mode: Single Player
Release Date: 2004-10-23
Version: null
Languages: en
Extreme: No
Tags: A; B
Tag Categories: default; default
Source: http://www.example.com/
Platform: Flash
Status: Playable
Application Path: FPSoftware\Flash\flashplayer_32_sa.exe
Launch Command: http://www.example.com/game.swf
Game Notes: null
Original Description: null
Curation Notes: null
Additional Applications: {}
`

// withMeta returns baseMeta with changes applied in order. A change
// "Key: value" (possibly several lines) replaces the line of that key or is
// appended; "-Key" removes the key.
func withMeta(changes ...string) string {
	lines := strings.Split(strings.TrimSuffix(baseMeta, "\n"), "\n")
	for _, change := range changes {
		key, remove := strings.CutPrefix(change, "-")
		key, _, _ = strings.Cut(key, ":")
		i := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, key+":") })
		switch {
		case remove:
			lines = slices.Delete(lines, i, i+1)
		case i >= 0:
			lines[i] = change
		default:
			lines = append(lines, change)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// validateMeta validates a small curation with the given meta.yaml.
func validateMeta(t *testing.T, lookups Lookups, meta string) *types.ValidatorResponse {
	t.Helper()
	return validate(t, lookups, writeZip(t,
		zipEntry{name: validRoot + "/content/www.example.com/game.swf", content: "swf"},
		zipEntry{name: validRoot + "/logo.png", content: "logo"},
		zipEntry{name: validRoot + "/ss.png", content: "ss"},
		zipEntry{name: validRoot + "/meta.yaml", content: meta},
	))
}

// quote returns s as a single-quoted YAML scalar.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func strPtr(s string) *string { return &s }
