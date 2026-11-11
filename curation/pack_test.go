package curation

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// packArchive writes the data pack of the curation archive at path.
func packArchive(t *testing.T, path string) []byte {
	t.Helper()
	a, err := Open(path, MaxEntries)
	require.NoError(t, err)
	defer a.Close()
	l, err := FindLayout(a)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, writeDataPack(&buf, a, l))
	return buf.Bytes()
}

// readPackZip returns the names and contents of the file entries of a zip, in order.
func readPackZip(t *testing.T, data []byte) ([]string, map[string][]byte, *zip.Reader) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	var names []string
	contents := map[string][]byte{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		rc, err := f.Open()
		require.NoError(t, err)
		content, err := io.ReadAll(rc) // archive/zip checks the CRC at EOF
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		contents[f.Name] = content
	}
	return names, contents, zr
}

func TestContentJSON(t *testing.T) {
	// The bytes Python's json.dump(..., indent=4) writes to a file opened
	// with newline='\r\n', as bluezip does.
	want := "{\r\n    \"version\": 1,\r\n    \"uniqueId\": \"" + validRoot + "\"\r\n}"
	assert.Equal(t, want, string(contentJSON(validRoot)))
}

// Old curations may use any root folder name; it is escaped as a JSON string.
func TestWriteDataPackEscapesTheUniqueID(t *testing.T) {
	root := `say "hi" <&>`
	path := writeZip(t,
		zipEntry{name: root + "/meta.yaml", content: "Title: x"},
		zipEntry{name: root + "/content/a.txt", content: "a"},
	)
	_, contents, _ := readPackZip(t, packArchive(t, path))
	assert.Equal(t, "{\r\n    \"version\": 1,\r\n    \"uniqueId\": \"say \\\"hi\\\" <&>\"\r\n}", string(contents["content.json"]))
}

func TestWriteDataPack(t *testing.T) {
	const content = "content/www.sesamestreet.org/"
	wantNames := []string{
		"content.json",
		content + "sites/all/libraries/swfobject/swfobject.js",
		content + "ssgames/flash/129.html",
	}
	for _, ext := range []string{".zip", ".7z"} {
		t.Run(ext, func(t *testing.T) {
			path := fixture("test_curation_valid" + ext)
			data := packArchive(t, path)
			assert.Equal(t, data, packArchive(t, path), "packing twice gives the same bytes")

			names, contents, zr := readPackZip(t, data)
			assert.Equal(t, wantNames, names)
			assert.Equal(t, string(contentJSON(validRoot)), string(contents["content.json"]))
			for _, f := range zr.File {
				assert.Equal(t, zip.Deflate, f.Method, f.Name)
				assert.True(t, packModified.Equal(f.Modified), "%s modified %v", f.Name, f.Modified)
			}

			// Content is copied unchanged.
			a := openFixture(t, "test_curation_valid"+ext)
			original, err := a.ReadFiles(map[string]int64{
				validRoot + "/" + wantNames[1]: MaxMetaSize,
				validRoot + "/" + wantNames[2]: MaxMetaSize,
			})
			require.NoError(t, err)
			for _, name := range wantNames[1:] {
				assert.Equal(t, original[validRoot+"/"+name], contents[name], name)
			}
		})
	}
}

func TestWriteDataPackSameForZipAnd7z(t *testing.T) {
	// Both fixtures hold the same files in the same order, so the packs are
	// identical: nothing from the container format leaks into the pack.
	assert.Equal(t, packArchive(t, fixture("test_curation_valid.zip")), packArchive(t, fixture("test_curation_valid.7z")))
}

func TestWriteDataPackKeepsArchiveOrderAndSkipsDirs(t *testing.T) {
	path := writeZip(t,
		zipEntry{name: "root/"},
		zipEntry{name: "root/meta.yaml", content: "Title: x"},
		zipEntry{name: "root/content/z.txt", content: "z"},
		zipEntry{name: "root/content/empty/"},
		zipEntry{name: "root/content/A/b.txt", content: "b"},
		zipEntry{name: "root/logo.png", content: "png"},
		zipEntry{name: "root/content/a.txt", content: "a"},
	)
	names, contents, _ := readPackZip(t, packArchive(t, path))
	assert.Equal(t, []string{"content.json", "content/z.txt", "content/A/b.txt", "content/a.txt"}, names)
	assert.Equal(t, "b", string(contents["content/A/b.txt"]))
	assert.Equal(t, "{\r\n    \"version\": 1,\r\n    \"uniqueId\": \"root\"\r\n}", string(contents["content.json"]))
}

func TestWriteDataPackReturnsReadErrors(t *testing.T) {
	path := writeZip(t, zipEntry{name: "root/content/a.txt", content: "hello world", store: true})
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	i := bytes.Index(data, []byte("hello world"))
	require.Positive(t, i)
	data[i] = 'j'
	require.NoError(t, os.WriteFile(path, data, 0o644))

	a, err := Open(path, MaxEntries)
	require.NoError(t, err)
	defer a.Close()
	l, err := FindLayout(a)
	require.NoError(t, err)
	err = writeDataPack(io.Discard, a, l)
	assert.Equal(t, "There seems to a problem with your zip file.", curationErrorMessage(t, err))
	assert.ErrorIs(t, err, zip.ErrChecksum)
}
