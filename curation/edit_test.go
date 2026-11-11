package curation

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// editStr returns a pointer to s, for EditCurationMeta fields.
func editStr(s string) *string { return &s }

// readTopLevelValues returns the top-level values of a YAML mapping as their
// source text, the way meta parsing reads them.
func readTopLevelValues(t *testing.T, src []byte) map[string]string {
	t.Helper()
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(src, &doc))
	m := doc.Content[0]
	values := map[string]string{}
	for i := 0; i < len(m.Content); i += 2 {
		values[m.Content[i].Value] = m.Content[i+1].Value
	}
	return values
}

const editTestMeta = `# Curation meta
Title: "Old: Title"
Alternate Titles: "Where Is Thumbkin?; Sesame Street: Toddler: Where Is
  Thumbkin?"
Library: arcade # line comment
Series: null
Version: null # version comment
# comment before the release date
Release Date: 2004-01-02
Languages: en
Extreme: No
Original Description: |-
  first
  second
Additional Applications:
  Test:
    Application Path: test
    Launch Command: test
  Extras: test
`

func TestEditMetaYAML(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit *types.EditCurationMeta
		want map[string]string // edited keys and their values as read back
		text []string          // lines the output must contain
	}{
		{
			name: "existing key",
			edit: &types.EditCurationMeta{Title: editStr("New Title")},
			want: map[string]string{"Title": "New Title"},
			text: []string{"Title: New Title\n"},
		},
		{
			name: "missing keys appended at the end in field order",
			edit: &types.EditCurationMeta{GameNotes: editStr("notes"), Developer: editStr("dev")},
			want: map[string]string{"Developer": "dev", "Game Notes": "notes"},
			text: []string{"    Launch Command: test\n  Extras: test\nDeveloper: dev\nGame Notes: notes\n"},
		},
		{
			name: "values that look like other types stay strings",
			edit: &types.EditCurationMeta{
				Version: editStr("1.10"), Languages: editStr("no"), ReleaseDate: editStr("2004"),
				Series: editStr("null"), Status: editStr(""), Tags: editStr("A; B # not a comment"),
			},
			want: map[string]string{
				"Version": "1.10", "Languages": "no", "Release Date": "2004",
				"Series": "null", "Status": "", "Tags": "A; B # not a comment",
			},
			text: []string{`Version: "1.10" # version comment`, `Release Date: "2004"`, `Series: "null"`},
		},
		{
			name: "multi-line values",
			edit: &types.EditCurationMeta{
				OriginalDescription: editStr("line one\n  indented: x\n\nlast line\n"),
				GameNotes:           editStr("windows\r\nline endings"),
			},
			want: map[string]string{
				"Original Description": "line one\n  indented: x\n\nlast line\n",
				"Game Notes":           "windows\r\nline endings",
			},
			text: []string{"Original Description: |\n  line one\n    indented: x\n\n  last line\n"},
		},
		{
			name: "all keys use Python's names",
			edit: &types.EditCurationMeta{
				Title: editStr("1"), AlternateTitles: editStr("2"), Version: editStr("3"), Developer: editStr("4"),
				Publisher: editStr("5"), ReleaseDate: editStr("6"), Series: editStr("7"), Source: editStr("8"),
				Status: editStr("9"), Tags: editStr("10"), Languages: editStr("11"),
				OriginalDescription: editStr("12"), GameNotes: editStr("13"),
			},
			want: map[string]string{
				"Title": "1", "Alternate Titles": "2", "Version": "3", "Developer": "4",
				"Publisher": "5", "Release Date": "6", "Series": "7", "Source": "8",
				"Status": "9", "Tags": "10", "Languages": "11",
				"Original Description": "12", "Game Notes": "13",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := editMetaYAML([]byte(editTestMeta), tc.edit)
			require.NoError(t, err)

			before := readTopLevelValues(t, []byte(editTestMeta))
			after := readTopLevelValues(t, out)
			for key, value := range before {
				if _, edited := tc.want[key]; !edited {
					assert.Equal(t, value, after[key], "untouched key %q", key)
				}
			}
			for key, value := range tc.want {
				assert.Equal(t, value, after[key], key)
			}
			assert.Len(t, after, len(before)+countNewKeys(before, tc.want))

			// Decoded as plain YAML (not as source text), edited values are
			// strings too.
			var decoded map[string]any
			require.NoError(t, yaml.Unmarshal(out, &decoded))
			for key, value := range tc.want {
				assert.Equal(t, value, decoded[key], key)
			}

			// Comments and order stay.
			s := string(out)
			assert.Regexp(t, `(?s)^# Curation meta\nTitle: .*\nAlternate Titles: .*\nLibrary: arcade # line comment\n`+
				`Series: .*\nVersion: .*# version comment\n# comment before the release date\nRelease Date: .*\n`+
				`Languages: .*\nExtreme: No\nOriginal Description: .*\nAdditional Applications:\n  Test:\n`, s)
			for _, line := range tc.text {
				assert.Contains(t, s, line)
			}
		})
	}
}

func countNewKeys(before, edited map[string]string) int {
	n := 0
	for key := range edited {
		if _, ok := before[key]; !ok {
			n++
		}
	}
	return n
}

func TestEditMetaYAMLNothingEdited(t *testing.T) {
	src := []byte("Title:   spacing kept\n\n\nVersion: 1.0\n")
	for _, edit := range []*types.EditCurationMeta{nil, {}} {
		out, err := editMetaYAML(src, edit)
		require.NoError(t, err)
		assert.Equal(t, src, out)
	}
}

func TestEditMetaYAMLRejectsNonMapping(t *testing.T) {
	edit := &types.EditCurationMeta{Title: editStr("x")}
	for _, src := range []string{"", "- a\n- b\n", "just text\n", "a: [\n"} {
		_, err := editMetaYAML([]byte(src), edit)
		assert.Error(t, err, "%q", src)
	}
}

// editArchive writes the edited copy of the archive at path and opens it.
func editArchive(t *testing.T, path string, meta, logo, screenshot []byte) (*Archive, *Layout) {
	t.Helper()
	a, err := Open(path, MaxEntries)
	require.NoError(t, err)
	defer a.Close()
	l, err := FindLayout(a)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, writeEditedArchive(&buf, a, l, meta, logo, screenshot))
	out := filepath.Join(t.TempDir(), "edited.zip")
	require.NoError(t, os.WriteFile(out, buf.Bytes(), 0o644))

	edited, err := Open(out, MaxEntries)
	require.NoError(t, err)
	t.Cleanup(func() { edited.Close() })
	el, err := FindLayout(edited)
	require.NoError(t, err)
	return edited, el
}

func TestWriteEditedArchive(t *testing.T) {
	for _, ext := range []string{".zip", ".7z"} {
		t.Run(ext, func(t *testing.T) {
			path := fixture("test_curation_valid" + ext)
			original := openFixture(t, "test_curation_valid"+ext)
			newMeta := []byte("Title: edited\n")

			// Meta only: everything else is copied with its path, content and
			// time (zip keeps whole seconds; 7z has 100 ns resolution).
			edited, l := editArchive(t, path, newMeta, nil, nil)
			assert.Equal(t, layoutErrors{}, errorsOf(l))
			assert.Equal(t, files(original), files(edited))
			assert.Equal(t, original.Dirs, edited.Dirs)
			for i, e := range original.Files {
				if e.Name != l.Meta.Name {
					assert.True(t, e.Modified.Truncate(time.Second).Equal(edited.Files[i].Modified), e.Name)
				}
			}
			assert.Equal(t, newMeta, readArchiveFiles(t, edited)[l.Meta.Name])
			assert.Equal(t, withoutEntry(readArchiveFiles(t, original), l.Meta.Name), withoutEntry(readArchiveFiles(t, edited), l.Meta.Name))

			// Images replaced in place.
			edited, l = editArchive(t, path, newMeta, []byte("new logo"), []byte("new ss"))
			assert.Equal(t, files(original), files(edited))
			contents := readArchiveFiles(t, edited)
			assert.Equal(t, "new logo", string(contents[l.Logo.Name]))
			assert.Equal(t, "new ss", string(contents[l.Screenshot.Name]))
		})
	}
}

func TestWriteEditedArchiveAddsMissingImages(t *testing.T) {
	for _, name := range []string{"missing_logo", "missing_ss"} {
		t.Run(name, func(t *testing.T) {
			path := fixture("test_curation_" + name + ".zip")
			edited, l := editArchive(t, path, []byte("Title: x\n"), []byte("logo"), []byte("ss"))
			assert.Equal(t, layoutErrors{}, errorsOf(l))
			contents := readArchiveFiles(t, edited)
			assert.Equal(t, "logo", string(contents[l.Root+"/logo.png"]))
			assert.Equal(t, "ss", string(contents[l.Root+"/ss.png"]))
			// The added image comes last.
			last := edited.Files[len(edited.Files)-1].Name
			assert.Contains(t, []string{l.Root + "/logo.png", l.Root + "/ss.png"}, last)
		})
	}
}

func TestWriteEditedArchiveKeepsEmptyFolders(t *testing.T) {
	path := writeZip(t,
		zipEntry{name: "root/meta.yaml", content: "Title: x\n"},
		zipEntry{name: "root/content/folder1/a.swf", content: "swf"},
		zipEntry{name: "root/content/folder2/"},
	)
	edited, l := editArchive(t, path, []byte("Title: y\n"), nil, nil)
	assert.Equal(t, []string{"root/content/folder2"}, edited.Dirs)
	assert.Equal(t, []string{"root/content/folder2"}, l.ContentDirs)
	assert.Equal(t, []string{"root/meta.yaml", "root/content/folder1/a.swf"}, files(edited))
	assert.Equal(t, "Title: y\n", string(readArchiveFiles(t, edited)["root/meta.yaml"]))
}

// readArchiveFiles returns the content of every file in a.
func readArchiveFiles(t *testing.T, a *Archive) map[string][]byte {
	t.Helper()
	limits := map[string]int64{}
	for _, e := range a.Files {
		limits[e.Name] = MaxMetaSize
	}
	contents, err := a.ReadFiles(limits)
	require.NoError(t, err)
	return contents
}

func withoutEntry(m map[string][]byte, key string) map[string][]byte {
	delete(m, key)
	return m
}
