package curation

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bodgit/sevenzip"
)

// Limits carried over from the Python validator.
const (
	// MaxEntries is the maximum number of entries (files and directories) in
	// a curation archive.
	MaxEntries = 10_000_000
	// MaxImageSize is the maximum size of a logo or screenshot read into memory.
	MaxImageSize = 16 * 1024 * 1024
	// MaxMetaSize is the maximum size of a meta file read into memory.
	MaxMetaSize = 16 * 1024 * 1024
)

// CurationError is a problem with the curation archive that the curator has
// to fix. Message is shown to the curator as is; Err, when set, is the
// underlying cause, for logs.
type CurationError struct {
	Message string
	Err     error
}

func (e *CurationError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *CurationError) Unwrap() error { return e.Err }

// ErrEntryTooLarge is returned by ReadFiles when an entry is larger than its
// size limit. The message is the one Python gave.
var ErrEntryTooLarge = &CurationError{Message: "An archive member exceeded its validation limit while being read."}

// Format is the container format of an archive.
type Format int

// The supported archive formats.
const (
	FormatZip Format = iota + 1
	Format7z
)

// String returns "zip" or "7z", as used in curator-facing messages.
func (f Format) String() string {
	if f == Format7z {
		return "7z"
	}
	return "zip"
}

// Entry is a file in an archive.
type Entry struct {
	// Name is the normalized path: relative, slash-separated, without
	// empty, "." or ".." components.
	Name string
	// Size is the uncompressed size declared by the archive header. Reading
	// the entry checks that the content has this size.
	Size int64
	// Modified is the modification time recorded in the archive.
	Modified time.Time

	index int    // position in the zip or 7z reader's file list
	crc   uint32 // 7z only: CRC32 from the header, 0 if the header has none
}

// Archive is a zip or 7z curation archive opened read-only.
//
// An Archive is not safe for concurrent use. Nothing in it starts goroutines,
// so a panic from a decoder (the caller is expected to recover) happens on
// the caller's goroutine.
type Archive struct {
	// Format is the detected container format.
	Format Format
	// Size is the size of the archive file in bytes.
	Size int64
	// Files lists the file entries in archive order. Directory entries are
	// not in this list.
	Files []Entry
	// Dirs lists the normalized names of the directory entries, in archive
	// order and without duplicates. Folders are normally implied by file
	// paths; this list is only needed to tell an empty folder from a
	// missing one.
	Dirs []string

	file *os.File
	zip  *zip.Reader
	sz   *sevenzip.Reader
}

// Open opens the zip or 7z archive at path read-only and lists its entries.
// The format is detected from the magic bytes, not from the extension.
//
// Problems with the archive (unsupported format, corrupt headers, more than
// maxEntries entries, invalid or duplicate names, special files, unsupported
// compression methods, encryption) are returned as a *CurationError. Other
// errors (the file cannot be opened or read) are returned as is.
func Open(path string, maxEntries int) (*Archive, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	a, err := newArchive(f, info.Size(), path, maxEntries)
	if err != nil {
		f.Close()
		return nil, err
	}
	a.file = f
	return a, nil
}

// UncompressedSize returns the total size the file entries declare. A total
// that does not fit in an int64 (only possible with forged headers) is
// returned as math.MaxInt64.
func (a *Archive) UncompressedSize() int64 {
	var total int64
	for _, e := range a.Files {
		if e.Size < 0 || e.Size > math.MaxInt64-total {
			return math.MaxInt64
		}
		total += e.Size
	}
	return total
}

// Close closes the archive file.
func (a *Archive) Close() error {
	if a.file == nil {
		return nil
	}
	return a.file.Close()
}

// Stream calls fn for every file entry for which want returns true (every
// file if want is nil), in archive order, with a reader for its content.
// Entries that are not wanted are not decompressed, except where a 7z solid
// block has to be decoded past them to reach a later wanted entry.
//
// The reader is only valid during the call. Reading it to EOF checks the
// content against the header (size and CRC32); a mismatch or any
// decompression error is returned by Read as a *CurationError, which fn
// should pass back. An error returned by fn stops the walk and is returned
// unchanged.
//
// A 7z coder that is disabled (see registerDisabledCoders) is only noticed
// when an entry that uses it is opened.
func (a *Archive) Stream(want func(e *Entry) bool, fn func(e *Entry, r io.Reader) error) error {
	for i := range a.Files {
		e := &a.Files[i]
		if want != nil && !want(e) {
			continue
		}
		if err := a.streamEntry(e, fn); err != nil {
			return err
		}
	}
	return nil
}

func (a *Archive) streamEntry(e *Entry, fn func(e *Entry, r io.Reader) error) error {
	var rc io.ReadCloser
	var err error
	if a.Format == FormatZip {
		rc, err = a.zip.File[e.index].Open()
	} else {
		rc, err = a.sz.File[e.index].Open()
	}
	if err != nil {
		return a.readError(err)
	}
	defer rc.Close()

	r := &entryReader{rc: rc, archive: a, entry: e}
	if a.Format == Format7z {
		// The 7z library does not check per-file CRCs; archive/zip does.
		r.crc = crc32.NewIEEE()
	}
	return fn(e, r)
}

// ReadFiles reads the named file entries into memory in one pass in archive
// order. limits maps each wanted name to its maximum size in bytes. Names
// that are not in the archive are missing from the result.
//
// An entry whose declared or actual size exceeds its limit stops the read
// with ErrEntryTooLarge. Read errors are returned as by Stream.
func (a *Archive) ReadFiles(limits map[string]int64) (map[string][]byte, error) {
	result := make(map[string][]byte, len(limits))
	want := func(e *Entry) bool {
		_, ok := limits[e.Name]
		return ok
	}
	err := a.Stream(want, func(e *Entry, r io.Reader) error {
		limit := limits[e.Name]
		if e.Size > limit {
			return ErrEntryTooLarge
		}
		data, err := io.ReadAll(io.LimitReader(r, limit+1))
		if err != nil {
			return err
		}
		if int64(len(data)) > limit {
			return ErrEntryTooLarge
		}
		result[e.Name] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// entryReader turns read errors into curation errors and, for 7z, checks
// the size and CRC32 of the content at EOF.
type entryReader struct {
	rc      io.ReadCloser
	archive *Archive
	entry   *Entry
	crc     hash.Hash32 // nil for zip
	n       int64
}

func (r *entryReader) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if r.crc != nil {
		r.n += int64(n)
		r.crc.Write(p[:n])
		if err == io.EOF {
			err = r.check()
		}
	}
	if err != nil && err != io.EOF {
		err = r.archive.readError(err)
	}
	return n, err
}

func (r *entryReader) check() error {
	if r.n != r.entry.Size {
		return fmt.Errorf("entry %q: read %d bytes, header says %d", r.entry.Name, r.n, r.entry.Size)
	}
	if r.entry.crc != 0 && r.crc.Sum32() != r.entry.crc {
		return fmt.Errorf("entry %q: CRC32 mismatch", r.entry.Name)
	}
	return io.EOF
}

// readError turns an error from the archive library into a curation error.
func (a *Archive) readError(err error) error {
	var unsupported *unsupportedCoderError
	if errors.As(err, &unsupported) {
		return unsupported.curationError()
	}
	return problemWithFile(a.Format, err)
}

// problemWithFile is Python's message for an archive that cannot be read.
func problemWithFile(format Format, err error) *CurationError {
	return &CurationError{Message: fmt.Sprintf("There seems to a problem with your %s file.", format), Err: err}
}

var (
	zipMagic      = []byte("PK\x03\x04")
	emptyZipMagic = []byte("PK\x05\x06")
	sevenZipMagic = []byte("7z\xBC\xAF\x27\x1C")
	rarMagic      = []byte("Rar!\x1A\x07")
)

// newArchive opens the archive in r. name is only used to pick the message
// for a file whose format is not recognized.
func newArchive(r io.ReaderAt, size int64, name string, maxEntries int) (*Archive, error) {
	head := make([]byte, len(sevenZipMagic))
	n, err := r.ReadAt(head, 0)
	if err != nil && err != io.EOF {
		return nil, err
	}
	head = head[:n]

	a := &Archive{Size: size}
	switch {
	case bytes.HasPrefix(head, zipMagic), bytes.HasPrefix(head, emptyZipMagic):
		a.Format = FormatZip
		err = a.openZip(r, size, maxEntries)
	case bytes.HasPrefix(head, sevenZipMagic):
		a.Format = Format7z
		err = a.open7z(r, size, maxEntries)
	case bytes.HasPrefix(head, rarMagic):
		err = &CurationError{Message: "Curations must be either .zip or .7z, not .rar."}
	default:
		// FPFSS only accepts .zip and .7z uploads; name the format the
		// curator meant to upload, as Python did.
		format := FormatZip
		if strings.EqualFold(filepath.Ext(name), ".7z") {
			format = Format7z
		}
		err = problemWithFile(format, errors.New("unrecognized archive format"))
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Archive) openZip(r io.ReaderAt, size int64, maxEntries int) error {
	zr, err := zip.NewReader(r, size)
	// ErrInsecurePath (with GODEBUG=zipinsecurepath=0) comes with a usable
	// reader; names are checked below with our own messages.
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return problemWithFile(FormatZip, err)
	}
	if len(zr.File) > maxEntries {
		return tooManyEntries(len(zr.File), maxEntries)
	}
	a.zip = zr

	files := make(map[string]bool)
	dirs := make(map[string]bool)
	for i, f := range zr.File {
		if f.Flags&0x1 != 0 {
			return errEncrypted
		}
		entry := Entry{Size: int64(f.UncompressedSize64), Modified: f.Modified, index: i}
		name, isFile, err := a.addEntry(f.Name, f.Mode(), entry, files, dirs)
		if err != nil {
			return err
		}
		if isFile && f.Method != zip.Store && f.Method != zip.Deflate {
			return &CurationError{Message: fmt.Sprintf(
				"Archive entry `%s` uses an unsupported compression method. Zip files may only use Store or Deflate.", name)}
		}
	}
	return nil
}

func (a *Archive) open7z(r io.ReaderAt, size int64, maxEntries int) error {
	sz, err := sevenzip.NewReader(r, size)
	if err != nil {
		return a.readError(err)
	}
	if len(sz.File) > maxEntries {
		return tooManyEntries(len(sz.File), maxEntries)
	}
	a.sz = sz

	files := make(map[string]bool)
	dirs := make(map[string]bool)
	for i, f := range sz.File {
		entry := Entry{Size: int64(f.UncompressedSize), Modified: f.Modified, index: i, crc: f.CRC32}
		if _, _, err := a.addEntry(f.Name, f.Mode(), entry, files, dirs); err != nil {
			return err
		}
	}
	return nil
}

// addEntry checks the name and mode of one archive entry and returns its
// normalized name. A directory is added to a.Dirs (once); a file is appended
// to a.Files as e with the normalized name, and isFile is true. files and
// dirs hold the names seen so far.
func (a *Archive) addEntry(rawName string, mode fs.FileMode, e Entry, files, dirs map[string]bool) (name string, isFile bool, err error) {
	name, isDir, ok := normalizeName(rawName, mode.IsDir())
	if !ok {
		return "", false, &CurationError{Message: fmt.Sprintf("Archive entry `%s` has an invalid path.", rawName)}
	}
	if t := mode.Type(); t != 0 && t != fs.ModeDir {
		return "", false, &CurationError{Message: fmt.Sprintf(
			"Archive entry `%s` is not a regular file or folder (link, device, pipe or socket).", name)}
	}
	if isDir {
		if !dirs[name] {
			dirs[name] = true
			a.Dirs = append(a.Dirs, name)
		}
		return name, false, nil
	}
	if files[name] {
		return "", false, &CurationError{Message: fmt.Sprintf("The archive contains `%s` more than once.", name)}
	}
	files[name] = true
	e.Name = name
	a.Files = append(a.Files, e)
	return name, true, nil
}

// normalizeName converts backslashes to slashes. A name ending in a slash
// is a directory, as in zip files; the trailing slash of a directory is
// removed. It reports false for an empty or absolute path and for a path
// with an empty, "." or ".." component.
func normalizeName(name string, isDir bool) (string, bool, bool) {
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.HasSuffix(name, "/") {
		isDir = true
		name = strings.TrimSuffix(name, "/")
	}
	if len(name) >= 2 && name[1] == ':' { // Windows drive letter, "C:/..."
		return "", false, false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", false, false
		}
	}
	return name, isDir, true
}

func tooManyEntries(n, max int) *CurationError {
	return &CurationError{Message: fmt.Sprintf("The archive contains too many members (`%d/%d`).", n, max)}
}

var errEncrypted = &CurationError{Message: "Encrypted archives are not supported."}

// unsupportedCoderError is returned by the decompressors registered for
// disabled 7z coders.
type unsupportedCoderError struct {
	name string
}

func (e *unsupportedCoderError) Error() string {
	return "7z coder " + e.name + " is disabled"
}

func (e *unsupportedCoderError) curationError() *CurationError {
	if e.name == "AES" {
		return &CurationError{Message: errEncrypted.Message, Err: e}
	}
	return &CurationError{Message: fmt.Sprintf(
		"The 7z file uses an unsupported compression method (%s). Use LZMA or LZMA2.", e.name), Err: e}
}

// disabledCoders are the 7z coders production submissions do not need, by
// method ID. Other coders the library supports stay enabled until the
// production sweep (cmd/curation-archive-sweep) says otherwise.
var disabledCoders = map[string][]byte{
	"Zstandard": {0x04, 0xf7, 0x11, 0x01},
	"Brotli":    {0x04, 0xf7, 0x11, 0x02},
	"LZ4":       {0x04, 0xf7, 0x11, 0x04},
	"AES":       {0x06, 0xf1, 0x07, 0x01},
}

func init() {
	registerDisabledCoders()
}

// registerDisabledCoders replaces the library's decompressors for
// disabledCoders with one that fails. The registry is global to the process.
func registerDisabledCoders() {
	for name, method := range disabledCoders {
		err := &unsupportedCoderError{name: name}
		sevenzip.RegisterDecompressor(method, func([]byte, uint64, []io.ReadCloser) (io.ReadCloser, error) {
			return nil, err
		})
	}
}
