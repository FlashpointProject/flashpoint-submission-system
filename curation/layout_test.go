package curation

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// layoutErrors holds the error fields of a Layout, for comparing them in
// one assertion.
type layoutErrors struct{ Logo, Screenshot, Content, Meta string }

func errorsOf(l *Layout) layoutErrors {
	return layoutErrors{l.LogoError, l.ScreenshotError, l.ContentError, l.MetaError}
}

func findLayout(t *testing.T, path string) (*Layout, error) {
	t.Helper()
	a, err := Open(path, MaxEntries)
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })
	return FindLayout(a)
}

// The Python fixtures, as far as the layout is concerned. Expectations come
// from curation_validator_test.py.
func TestFindLayoutFixtures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		errors layoutErrors
	}{
		{"valid", layoutErrors{}},
		{"valid_addapps", layoutErrors{}}, // zip has no directory entry for the root
		{"valid_extreme", layoutErrors{}},
		{"Norwegian", layoutErrors{}}, // root is not a UUID
		{"none_library", layoutErrors{}},
		{"localflash_bad_name", layoutErrors{}},
		{"localflash_no_folder", layoutErrors{}},
		{"localflash_too_many_files", layoutErrors{}},
		{"empty_meta", layoutErrors{}},
		{"capital_extension_logo", layoutErrors{Logo: "Logo file extension must be lowercase."}},
		{"capital_extension_screenshot", layoutErrors{Screenshot: "Screenshot file extension must be lowercase."}},
		{"missing_logo", layoutErrors{Logo: "Logo file is either missing or its filename is incorrect."}},
		{"missing_ss", layoutErrors{Screenshot: "Screenshot file is either missing or its filename is incorrect."}},
		{"missing_content", layoutErrors{Content: "Content folder not found."}},
		{"empty_content", layoutErrors{Content: "No files found in content folder."}},
		{"missing_meta", layoutErrors{
			Meta: "Meta file is either missing or its filename is incorrect. Are you using Flashpoint Core for curating?"}},
		// Python accepted meta.txt; it is now rejected.
		{"valid_legacy", layoutErrors{Meta: msgMetaTxt}},
		{"valid_legacy_genre", layoutErrors{Meta: msgMetaTxt}},
	} {
		for _, ext := range []string{".zip", ".7z"} {
			t.Run(tc.name+ext, func(t *testing.T) {
				l, err := findLayout(t, fixture("test_curation_"+tc.name+ext))
				require.NoError(t, err)
				assert.Equal(t, tc.errors, errorsOf(l))
			})
		}
	}
}

func TestFindLayoutMissingRootFolder(t *testing.T) {
	for _, ext := range []string{".zip", ".7z"} {
		_, err := findLayout(t, fixture("test_curation_missing_root_folder"+ext))
		assert.Equal(t, "Logo, screenshot, content folder and meta not found. Is your curation structured properly?",
			curationErrorMessage(t, err))
	}
}

func TestFindLayoutValid(t *testing.T) {
	for _, ext := range []string{".zip", ".7z"} {
		t.Run(ext, func(t *testing.T) {
			l, err := findLayout(t, fixture("test_curation_valid"+ext))
			require.NoError(t, err)
			assert.Equal(t, validRoot, l.Root)
			require.NotNil(t, l.Meta)
			require.NotNil(t, l.Logo)
			require.NotNil(t, l.Screenshot)
			assert.Equal(t, validRoot+"/meta.yaml", l.Meta.Name)
			assert.Equal(t, validRoot+"/logo.png", l.Logo.Name)
			assert.Equal(t, validRoot+"/ss.png", l.Screenshot.Name)
			assert.Equal(t, "meta.yaml", l.RelPath(l.Meta))

			var content []string
			for _, e := range l.Content {
				assert.True(t, l.IsContent(e))
				content = append(content, l.RelPath(e))
			}
			assert.ElementsMatch(t, []string{
				"content/www.sesamestreet.org/sites/all/libraries/swfobject/swfobject.js",
				"content/www.sesamestreet.org/ssgames/flash/129.html",
			}, content)
			assert.False(t, l.IsContent(l.Meta))
			assert.Len(t, l.ContentDirs, 7)
		})
	}
}

func TestFindLayoutNonUUIDRoot(t *testing.T) {
	l, err := findLayout(t, fixture("test_curation_none_library.7z"))
	require.NoError(t, err)
	assert.Equal(t, "Wacked Mission Mode", l.Root)
}

func TestFindLayoutContentDirs(t *testing.T) {
	// Python's localflash check needs the empty folder2.
	l, err := findLayout(t, fixture("test_curation_localflash_too_many_files.zip"))
	require.NoError(t, err)
	root := "c2dc8da2-6700-4686-a600-df8866a6e12e"
	assert.Equal(t, []string{
		root + "/content/localflash",
		root + "/content/localflash/folder1",
		root + "/content/localflash/folder2",
	}, l.ContentDirs)
	require.Len(t, l.Content, 1)
	assert.Equal(t, root+"/content/localflash/folder1/TVNoise.swf", l.Content[0].Name)
}

// The 2 GB entry is listed but never decompressed by FindLayout.
func TestFindLayoutLargeContent(t *testing.T) {
	for _, ext := range []string{".zip", ".7z"} {
		t.Run(ext, func(t *testing.T) {
			l, err := findLayout(t, fixture("test_curation_2GB"+ext))
			require.NoError(t, err)
			assert.Equal(t, layoutErrors{}, errorsOf(l))
			var bomb *Entry
			for _, e := range l.Content {
				if e.Name == validRoot+"/content/bomb" {
					bomb = e
				}
			}
			require.NotNil(t, bomb)
			assert.Equal(t, int64(2_000_000_000), bomb.Size)
		})
	}
}

func TestFindLayoutSynthetic(t *testing.T) {
	complete := func(root string) []zipEntry {
		return []zipEntry{
			{name: root + "/meta.yaml", content: "Title: x"},
			{name: root + "/logo.png", content: "logo"},
			{name: root + "/ss.png", content: "ss"},
			{name: root + "/content/example.com/a.swf", content: "swf"},
		}
	}
	with := func(entries []zipEntry, more ...zipEntry) []zipEntry {
		return append(append([]zipEntry{}, entries...), more...)
	}

	for name, tc := range map[string]struct {
		entries []zipEntry
		fatal   string
		errors  layoutErrors
	}{
		"complete without directory entries": {
			entries: complete("root"),
		},
		"empty archive": {
			entries: nil,
			fatal:   msgNotStructured,
		},
		"only directories": {
			entries: []zipEntry{{name: "root/"}, {name: "root/content/"}},
			fatal:   msgNotStructured,
		},
		"curation one folder too deep": {
			entries: complete("outer/root"),
			fatal:   msgNotStructured,
		},
		"two top-level folders": {
			entries: with(complete("root"), zipEntry{name: "__MACOSX/root/._logo.png"}),
			fatal: "The archive must contain exactly one folder at the top level (the curation folder), " +
				"but it contains 2: `root`, `__MACOSX`.",
		},
		"empty second top-level folder is ignored": {
			entries: with(complete("root"), zipEntry{name: "other/"}),
		},
		"file next to the root folder": {
			entries: with(complete("root"), zipEntry{name: "readme.txt"}),
			fatal:   "Everything must be inside the curation folder `root`, but the archive also contains `readme.txt` at the top level.",
		},
		"meta.yaml and meta.yml": {
			entries: with(complete("root"), zipEntry{name: "root/meta.yml"}),
			errors:  layoutErrors{Meta: msgMetaYAMLAndYML},
		},
		"meta.yml": {
			entries: []zipEntry{
				{name: "root/meta.yml"}, {name: "root/logo.png"}, {name: "root/ss.png"}, {name: "root/content/a"},
			},
		},
		"stray meta.txt next to meta.yaml": {
			entries: with(complete("root"), zipEntry{name: "root/meta.txt"}),
		},
		"meta in a subfolder does not count": {
			entries: []zipEntry{
				{name: "root/sub/meta.yaml"}, {name: "root/logo.png"}, {name: "root/ss.png"}, {name: "root/content/a"},
			},
			errors: layoutErrors{Meta: msgMetaMissing},
		},
		"logo with both cases": {
			entries: with(complete("root"), zipEntry{name: "root/LOGO.PNG"}),
			errors:  layoutErrors{Logo: msgLogoCase},
		},
		"capitalised name only": {
			entries: []zipEntry{
				{name: "root/meta.yaml"}, {name: "root/Logo.png"}, {name: "root/ss.png"}, {name: "root/content/a"},
			},
			errors: layoutErrors{Logo: msgLogoCase},
		},
		"contents folder is not content": {
			entries: []zipEntry{
				{name: "root/meta.yaml"}, {name: "root/logo.png"}, {name: "root/ss.png"}, {name: "root/contents/a"},
			},
			errors: layoutErrors{Content: msgContentMissing},
		},
		"content folder that is a file": {
			entries: []zipEntry{
				{name: "root/meta.yaml"}, {name: "root/logo.png"}, {name: "root/ss.png"}, {name: "root/content"},
			},
			errors: layoutErrors{Content: msgContentMissing},
		},
		"everything missing but one": {
			entries: []zipEntry{{name: "root/ss.png"}},
			errors:  layoutErrors{Logo: msgLogoMissing, Content: msgContentMissing, Meta: msgMetaMissing},
		},
		"only meta.txt": {
			entries: []zipEntry{{name: "root/meta.txt"}},
			errors: layoutErrors{
				Logo: msgLogoMissing, Screenshot: msgScreenshotMiss, Content: msgContentMissing, Meta: msgMetaTxt},
		},
	} {
		t.Run(name, func(t *testing.T) {
			l, err := findLayout(t, writeZip(t, tc.entries...))
			if tc.fatal != "" {
				assert.Equal(t, tc.fatal, curationErrorMessage(t, err))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.errors, errorsOf(l))
		})
	}
}

func TestFindLayoutMetaSelection(t *testing.T) {
	l, err := findLayout(t, writeZip(t,
		zipEntry{name: "root/meta.yml", content: "Title: yml"},
		zipEntry{name: "root/meta.txt"},
		zipEntry{name: "root/logo.png"},
		zipEntry{name: "root/ss.png"},
		zipEntry{name: "root/content/a"},
	))
	require.NoError(t, err)
	require.NotNil(t, l.Meta)
	assert.Equal(t, "root/meta.yml", l.Meta.Name)
	assert.Empty(t, l.MetaError)
}

// Pack streams every content file, in archive order, with its path relative
// to the root.
func TestStreamContent(t *testing.T) {
	a := openFixture(t, "test_curation_valid.7z")
	l, err := FindLayout(a)
	require.NoError(t, err)
	var paths []string
	require.NoError(t, a.Stream(l.IsContent, func(e *Entry, r io.Reader) error {
		paths = append(paths, l.RelPath(e))
		_, err := io.Copy(io.Discard, r)
		return err
	}))
	var want []string
	for _, e := range l.Content {
		want = append(want, l.RelPath(e))
	}
	assert.Equal(t, want, paths)
}
