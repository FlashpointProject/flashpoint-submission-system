package curation

import (
	"archive/zip"
	"bytes"
	"errors"
	"hash/crc32"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validRoot = "e647a839-c4d8-4c51-8f04-4cf142f1718c"

// testdata/sevenzip/ holds test archives from github.com/bodgit/sevenzip
// (BSD 3-Clause, see testdata/sevenzip/LICENSE) that use the coders we disable.

func fixture(name string) string {
	return filepath.Join("testdata", name)
}

// curationErrorMessage returns the curator-facing message of err, failing
// the test if err is not a *CurationError.
func curationErrorMessage(t *testing.T, err error) string {
	t.Helper()
	var ce *CurationError
	require.ErrorAs(t, err, &ce)
	return ce.Message
}

func openFixture(t *testing.T, name string) *Archive {
	t.Helper()
	a, err := Open(fixture(name), MaxEntries)
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })
	return a
}

// zipEntry describes an entry of a zip built by writeZip. A name ending in
// "/" is a directory.
type zipEntry struct {
	name      string
	content   string
	mode      fs.FileMode // 0 means default
	store     bool        // Store instead of Deflate
	rawMethod uint16      // if non-zero, write the content raw with this method
	flags     uint16      // if non-zero, write the content raw with these flags
}

// writeZip writes a zip with the given entries to a temp file and returns
// its path.
func writeZip(t *testing.T, entries ...zipEntry) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Flags: e.flags}
		if e.store || strings.HasSuffix(e.name, "/") {
			h.Method = zip.Store
		}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		var err error
		var fw io.Writer
		if e.rawMethod != 0 || e.flags != 0 {
			h.Method = e.rawMethod
			h.CRC32 = crc32.ChecksumIEEE([]byte(e.content))
			h.CompressedSize64 = uint64(len(e.content))
			h.UncompressedSize64 = uint64(len(e.content))
			fw, err = w.CreateRaw(h)
		} else {
			fw, err = w.CreateHeader(h)
		}
		require.NoError(t, err)
		_, err = io.WriteString(fw, e.content)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	path := filepath.Join(t.TempDir(), "curation.zip")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
	return path
}

// files returns the names of a's file entries.
func files(a *Archive) []string {
	var names []string
	for _, e := range a.Files {
		names = append(names, e.Name)
	}
	return names
}

func TestOpenValidFixtures(t *testing.T) {
	want := []string{
		validRoot + "/content/www.sesamestreet.org/sites/all/libraries/swfobject/swfobject.js",
		validRoot + "/content/www.sesamestreet.org/ssgames/flash/129.html",
		validRoot + "/logo.png",
		validRoot + "/meta.yaml",
		validRoot + "/ss.png",
	}
	for _, tc := range []struct {
		file   string
		format Format
	}{
		{"test_curation_valid.zip", FormatZip},
		{"test_curation_valid.7z", Format7z},
	} {
		t.Run(tc.file, func(t *testing.T) {
			a := openFixture(t, tc.file)
			assert.Equal(t, tc.format, a.Format)
			assert.ElementsMatch(t, want, files(a))
			assert.Len(t, a.Dirs, 9)
			assert.Contains(t, a.Dirs, validRoot)
			assert.Contains(t, a.Dirs, validRoot+"/content")
			for _, e := range a.Files {
				assert.False(t, e.Modified.IsZero(), e.Name)
			}
		})
	}
}

func TestOpenUnsupported(t *testing.T) {
	for file, want := range map[string]string{
		// Python: test_curation_invalid_archive, test_rar.
		"test_curation_invalid_archive.zip": "There seems to a problem with your zip file.",
		"test_curation_invalid_archive.7z":  "There seems to a problem with your 7z file.",
		"test_curation_rar.rar":             "Curations must be either .zip or .7z, not .rar.",
		// Encrypted header: the AES coder is disabled.
		"sevenzip/aes7z.7z": "Encrypted archives are not supported.",
	} {
		t.Run(file, func(t *testing.T) {
			_, err := Open(fixture(file), MaxEntries)
			assert.Equal(t, want, curationErrorMessage(t, err))
		})
	}
}

func TestOpenDetectsFormatFromMagicBytes(t *testing.T) {
	dir := t.TempDir()
	for src, dst := range map[string]string{
		"test_curation_valid.zip": "zip-named.7z",
		"test_curation_valid.7z":  "7z-named.zip",
	} {
		data, err := os.ReadFile(fixture(src))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, dst), data, 0o644))
	}
	a, err := Open(filepath.Join(dir, "zip-named.7z"), MaxEntries)
	require.NoError(t, err)
	assert.Equal(t, FormatZip, a.Format)
	a.Close()
	a, err = Open(filepath.Join(dir, "7z-named.zip"), MaxEntries)
	require.NoError(t, err)
	assert.Equal(t, Format7z, a.Format)
	a.Close()

	// A corrupt archive is reported by its detected format.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.zip"), []byte("7z\xBC\xAF\x27\x1Cgarbage"), 0o644))
	_, err = Open(filepath.Join(dir, "broken.zip"), MaxEntries)
	assert.Equal(t, "There seems to a problem with your 7z file.", curationErrorMessage(t, err))
}

func TestOpenMissingFileIsNotACurationError(t *testing.T) {
	_, err := Open(fixture("does-not-exist.zip"), MaxEntries)
	require.Error(t, err)
	var ce *CurationError
	assert.False(t, errors.As(err, &ce))
}

// Python: test_archive_member_limit. Directories count as members.
func TestOpenEntryLimit(t *testing.T) {
	for _, file := range []string{"test_curation_valid.zip", "test_curation_valid.7z"} {
		t.Run(file, func(t *testing.T) {
			_, err := Open(fixture(file), 1)
			assert.Equal(t, "The archive contains too many members (`14/1`).", curationErrorMessage(t, err))

			a, err := Open(fixture(file), 14)
			require.NoError(t, err)
			a.Close()
		})
	}
}

func TestOpenNames(t *testing.T) {
	t.Run("backslashes are converted", func(t *testing.T) {
		a, err := Open(writeZip(t,
			zipEntry{name: `root\content\`},
			zipEntry{name: `root\content\a.swf`, content: "x"},
		), MaxEntries)
		require.NoError(t, err)
		defer a.Close()
		assert.Equal(t, []string{"root/content/a.swf"}, files(a))
		assert.Equal(t, []string{"root/content"}, a.Dirs)
	})

	t.Run("repeated directories are listed once", func(t *testing.T) {
		a, err := Open(writeZip(t, zipEntry{name: "root/"}, zipEntry{name: `root\`}), MaxEntries)
		require.NoError(t, err)
		defer a.Close()
		assert.Equal(t, []string{"root"}, a.Dirs)
		assert.Empty(t, a.Files)
	})

	for name, tc := range map[string]struct {
		entries []zipEntry
		want    string
	}{
		"parent component": {
			[]zipEntry{{name: "root/../../evil.txt"}},
			"Archive entry `root/../../evil.txt` has an invalid path.",
		},
		"absolute": {
			[]zipEntry{{name: "/etc/passwd"}},
			"Archive entry `/etc/passwd` has an invalid path.",
		},
		"absolute with backslash": {
			[]zipEntry{{name: `\root\a`}},
			"Archive entry `\\root\\a` has an invalid path.",
		},
		"drive letter": {
			[]zipEntry{{name: `C:\root\a`}},
			"Archive entry `C:\\root\\a` has an invalid path.",
		},
		"dot component": {
			[]zipEntry{{name: "./root/a"}},
			"Archive entry `./root/a` has an invalid path.",
		},
		"empty component": {
			[]zipEntry{{name: "root//a"}},
			"Archive entry `root//a` has an invalid path.",
		},
		"duplicate file": {
			[]zipEntry{{name: "root/a"}, {name: `root\a`}},
			"The archive contains `root/a` more than once.",
		},
		"symlink": {
			[]zipEntry{{name: "root/link", content: "/etc/passwd", mode: fs.ModeSymlink | 0o777}},
			"Archive entry `root/link` is not a regular file or folder (link, device, pipe or socket).",
		},
		"named pipe": {
			[]zipEntry{{name: "root/fifo", mode: fs.ModeNamedPipe | 0o644}},
			"Archive entry `root/fifo` is not a regular file or folder (link, device, pipe or socket).",
		},
		"unsupported method": {
			[]zipEntry{{name: "root/a.swf", content: "x", rawMethod: 12}}, // bzip2
			"Archive entry `root/a.swf` uses an unsupported compression method. Zip files may only use Store or Deflate.",
		},
		"encrypted": {
			[]zipEntry{{name: "root/a.swf", content: "x", flags: 0x1}},
			"Encrypted archives are not supported.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Open(writeZip(t, tc.entries...), MaxEntries)
			assert.Equal(t, tc.want, curationErrorMessage(t, err))
		})
	}
}

func TestNormalizeName(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dirMode   bool
		want      string
		wantDir   bool
		wantValid bool
	}{
		{"a/b.txt", false, "a/b.txt", false, true},
		{`a\b.txt`, false, "a/b.txt", false, true},
		{"a/b", true, "a/b", true, true},
		{"a/b/", true, "a/b", true, true},
		{"a/b/", false, "a/b", true, true},
		{`a\b\`, false, "a/b", true, true},
		{"a/www.example.com:8080/x", false, "a/www.example.com:8080/x", false, true},
		{"a/..b/c..", false, "a/..b/c..", false, true},
		{"a//", false, "", false, false},
		{"", false, "", false, false},
		{"/", true, "", false, false},
		{"/a", false, "", false, false},
		{"../a", false, "", false, false},
		{"a/..", true, "", false, false},
		{"a/./b", false, "", false, false},
		{"C:/a", false, "", false, false},
	} {
		got, isDir, ok := normalizeName(tc.name, tc.dirMode)
		assert.Equal(t, tc.wantValid, ok, tc.name)
		assert.Equal(t, tc.want, got, tc.name)
		assert.Equal(t, tc.wantDir, isDir, tc.name)
	}
}

func TestReadFiles(t *testing.T) {
	meta := validRoot + "/meta.yaml"
	logo := validRoot + "/logo.png"
	ss := validRoot + "/ss.png"

	var logos [][]byte
	for _, file := range []string{"test_curation_valid.zip", "test_curation_valid.7z"} {
		t.Run(file, func(t *testing.T) {
			a := openFixture(t, file)
			got, err := a.ReadFiles(map[string]int64{
				meta:                 MaxMetaSize,
				logo:                 MaxImageSize,
				ss:                   MaxImageSize,
				validRoot + "/other": MaxImageSize,
			})
			require.NoError(t, err)
			assert.Len(t, got, 3)
			assert.Contains(t, string(got[meta]), "Title:")
			assert.Len(t, got[logo], 1407)
			assert.Len(t, got[ss], 2954)
			assert.True(t, bytes.HasPrefix(got[logo], []byte("\x89PNG")))
			logos = append(logos, got[logo])

			// Exactly at the limit is fine; one byte less is not.
			_, err = a.ReadFiles(map[string]int64{logo: 1407})
			require.NoError(t, err)
			_, err = a.ReadFiles(map[string]int64{meta: MaxMetaSize, logo: 1406})
			assert.Same(t, ErrEntryTooLarge, err)
		})
	}
	require.Len(t, logos, 2)
	assert.Equal(t, logos[0], logos[1])
}

func TestReadFilesActualSizeOverLimit(t *testing.T) {
	// The declared size is checked first; this checks the limit on what is
	// actually read, using a header that lies about the size.
	a := &Archive{Format: Format7z}
	e := &Entry{Name: "x", Size: 2}
	r := &entryReader{rc: io.NopCloser(strings.NewReader("abcdef")), archive: a, entry: e, crc: crc32.NewIEEE()}
	data, err := io.ReadAll(io.LimitReader(r, 3))
	require.NoError(t, err)
	assert.Len(t, data, 3) // ReadFiles turns this into ErrEntryTooLarge
}

func TestStreamChecks7zSizeAndCRC(t *testing.T) {
	a := &Archive{Format: Format7z}
	read := func(content string, e *Entry) error {
		r := &entryReader{rc: io.NopCloser(strings.NewReader(content)), archive: a, entry: e, crc: crc32.NewIEEE()}
		_, err := io.Copy(io.Discard, r)
		return err
	}
	sum := crc32.ChecksumIEEE([]byte("abc"))

	assert.NoError(t, read("abc", &Entry{Name: "ok", Size: 3, crc: sum}))
	assert.NoError(t, read("abc", &Entry{Name: "no crc in header", Size: 3}))

	err := read("abc", &Entry{Name: "short", Size: 4, crc: sum})
	assert.Equal(t, "There seems to a problem with your 7z file.", curationErrorMessage(t, err))
	assert.ErrorContains(t, err, "read 3 bytes, header says 4")

	err = read("abd", &Entry{Name: "corrupt", Size: 3, crc: sum})
	assert.Equal(t, "There seems to a problem with your 7z file.", curationErrorMessage(t, err))
	assert.ErrorContains(t, err, "CRC32 mismatch")
}

func TestStreamDetectsCorrupt7zData(t *testing.T) {
	// copy.7z stores its files uncompressed (Copy coder), so flipping a
	// content byte leaves the archive readable but breaks a CRC.
	data, err := os.ReadFile(fixture("sevenzip/copy.7z"))
	require.NoError(t, err)
	data[100] ^= 0xff
	path := filepath.Join(t.TempDir(), "corrupt.7z")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	a, err := Open(path, MaxEntries)
	require.NoError(t, err)
	defer a.Close()
	err = a.Stream(nil, func(e *Entry, r io.Reader) error {
		_, err := io.Copy(io.Discard, r)
		return err
	})
	assert.Equal(t, "There seems to a problem with your 7z file.", curationErrorMessage(t, err))
	assert.ErrorContains(t, err, "CRC32 mismatch")
}

func TestStreamDetectsCorruptZipData(t *testing.T) {
	path := writeZip(t, zipEntry{name: "root/a.txt", content: "hello world", store: true})
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	i := bytes.Index(data, []byte("hello world"))
	require.Positive(t, i)
	data[i] = 'j'
	require.NoError(t, os.WriteFile(path, data, 0o644))

	a, err := Open(path, MaxEntries)
	require.NoError(t, err)
	defer a.Close()
	err = a.Stream(nil, func(e *Entry, r io.Reader) error {
		_, err := io.Copy(io.Discard, r)
		return err
	})
	assert.Equal(t, "There seems to a problem with your zip file.", curationErrorMessage(t, err))
	assert.ErrorIs(t, err, zip.ErrChecksum)
}

func TestStreamDisabledCoders(t *testing.T) {
	for file, name := range map[string]string{
		"sevenzip/zstd.7z":   "Zstandard",
		"sevenzip/brotli.7z": "Brotli",
		"sevenzip/lz4.7z":    "LZ4",
	} {
		t.Run(file, func(t *testing.T) {
			// Headers are readable; the coder is only used for content.
			a := openFixture(t, file)
			err := a.Stream(nil, func(e *Entry, r io.Reader) error {
				_, err := io.Copy(io.Discard, r)
				return err
			})
			assert.Equal(t, "The 7z file uses an unsupported compression method ("+name+"). Use LZMA or LZMA2.",
				curationErrorMessage(t, err))
		})
	}
}

func TestStreamOrderAndSelection(t *testing.T) {
	for _, file := range []string{"test_curation_valid.zip", "test_curation_valid.7z"} {
		t.Run(file, func(t *testing.T) {
			a := openFixture(t, file)
			var all []string
			var total int64
			require.NoError(t, a.Stream(nil, func(e *Entry, r io.Reader) error {
				all = append(all, e.Name)
				n, err := io.Copy(io.Discard, r)
				assert.Equal(t, e.Size, n, e.Name)
				total += n
				return err
			}))
			assert.Equal(t, files(a), all)
			assert.Equal(t, int64(21759), total)

			var picked []string
			require.NoError(t, a.Stream(
				func(e *Entry) bool { return strings.HasSuffix(e.Name, ".png") },
				func(e *Entry, r io.Reader) error {
					picked = append(picked, e.Name)
					return nil // not reading is fine
				}))
			assert.Equal(t, []string{validRoot + "/logo.png", validRoot + "/ss.png"}, picked)

			stop := errors.New("stop")
			calls := 0
			err := a.Stream(nil, func(*Entry, io.Reader) error { calls++; return stop })
			assert.Same(t, stop, err)
			assert.Equal(t, 1, calls)
		})
	}
}

// Python: test_large_zip_content_is_not_extracted. Reading the meta and
// images of the 2 GB fixture must not decompress the 2 GB entry.
func TestReadFilesSkipsLargeContent(t *testing.T) {
	limits := map[string]int64{
		validRoot + "/meta.yaml": MaxMetaSize,
		validRoot + "/logo.png":  MaxImageSize,
		validRoot + "/ss.png":    MaxImageSize,
	}
	a := openFixture(t, "test_curation_2GB.zip")
	got, err := a.ReadFiles(limits)
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestUncompressedSize(t *testing.T) {
	a := &Archive{Files: []Entry{{Size: 40e9}, {Size: 20e9}}}
	assert.Equal(t, int64(60e9), a.UncompressedSize())

	// Sizes from forged headers cannot wrap the total around.
	a = &Archive{Files: []Entry{{Size: math.MaxInt64}, {Size: math.MaxInt64}, {Size: 1}}}
	assert.Equal(t, int64(math.MaxInt64), a.UncompressedSize())
	a = &Archive{Files: []Entry{{Size: 60e9}, {Size: -60e9}}}
	assert.Equal(t, int64(math.MaxInt64), a.UncompressedSize())
}
