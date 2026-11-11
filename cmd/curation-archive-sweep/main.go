// Command curation-archive-sweep reads every curation archive in a directory
// tree and prints what the Go readers in package curation will meet in
// production: per format, the number of archives, the 7z coder chains,
// solid or not, the largest entry count, the largest total declared
// uncompressed size and declared-size/file-size ratio, entry names with
// backslashes or invalid UTF-8, and the archives the readers (or the layout
// check) reject.
//
// The report goes to stdout; progress goes to stderr every 10 seconds.
//
// It only reads. Run it on the server against the submissions directory
// before deploying the Go validator; its output decides the final 7z coder
// allowlist.
//
//	curation-archive-sweep [-read] DIR
package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	_ "unsafe" // for go:linkname

	"github.com/FlashpointProject/flashpoint-submission-system/curation"
	"github.com/bodgit/sevenzip"
)

// sevenzipDecompressor is the library's (unexported) decompressor lookup. The
// library does not expose the coders of an archive, so the sweep wraps every
// registered decompressor to record which ones an archive uses.
//
// This depends on the internals of the sevenzip pseudo-version pinned in
// go.mod and may stop building (or break at run time) when it is upgraded.
//
//go:linkname sevenzipDecompressor github.com/bodgit/sevenzip.decompressor
func sevenzipDecompressor(method []byte) sevenzip.Decompressor

// coderNames are the 7z method IDs the library knows (see its register.go).
var coderNames = map[string]string{
	"\x00":             "Copy",
	"\x03":             "Delta",
	"\x03\x01\x01":     "LZMA",
	"\x03\x03\x01\x03": "BCJ",
	"\x03\x03\x01\x1b": "BCJ2",
	"\x03\x03\x02\x05": "PPC",
	"\x03\x03\x05\x01": "ARM",
	"\x03\x03\x08\x05": "SPARC",
	"\x04\x01\x08":     "Deflate",
	"\x04\x02\x02":     "BZip2",
	"\x04\xf7\x11\x01": "Zstandard",
	"\x04\xf7\x11\x02": "Brotli",
	"\x04\xf7\x11\x04": "LZ4",
	"\x06\xf1\x07\x01": "AES",
	"\x0a":             "ARM64",
	"\x21":             "LZMA2",
	"\x03\x04\x01":     "PPMd",
}

// usedCoders collects the coders built since the last reset.
var usedCoders []string

func recordCoders() {
	for method, name := range coderNames {
		original := sevenzipDecompressor([]byte(method))
		sevenzip.RegisterDecompressor([]byte(method), func(p []byte, size uint64, rs []io.ReadCloser) (io.ReadCloser, error) {
			usedCoders = append(usedCoders, name)
			return original(p, size, rs)
		})
	}
}

// formatStats is what the sweep found for one format.
type formatStats struct {
	count        int
	maxEntries   int
	maxPath      string
	maxSize      int64 // largest total declared uncompressed size
	maxSizePath  string
	maxRatio     float64 // largest declared uncompressed size / file size
	maxRatioPath string
	backslashes  []string         // archives with at least one backslash in a name
	badUTF8      []string         // archives with at least one name that is not valid UTF-8
	methods      map[string]int   // zip methods or 7z coder chains, by number of archives
	headers      map[string]int   // 7z: coder chains of encoded headers
	solid        int              // 7z: archives with a folder holding more than one file
	failures     map[string]error // path -> error from curation.Open, FindLayout or reading
}

func main() {
	read := flag.Bool("read", false, "also decompress every entry (checks sizes and CRCs; slow)")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: curation-archive-sweep [-read] DIR")
		os.Exit(2)
	}
	recordCoders()

	var paths []string
	err := filepath.WalkDir(flag.Arg(0), func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			paths = append(paths, path)
		}
		return err
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	p := &progress{total: len(paths), start: time.Now()}
	go p.report(10 * time.Second)

	stats := map[string]*formatStats{}
	for i, path := range paths {
		p.set(i, path)
		format := detect(path)
		s := stats[format]
		if s == nil {
			s = &formatStats{methods: map[string]int{}, headers: map[string]int{}, failures: map[string]error{}}
			stats[format] = s
		}
		s.count++
		if err := sweep(path, format, s, *read); err != nil {
			s.failures[path] = err
		}
	}
	p.set(len(paths), "")
	p.print()

	for _, format := range slices.Sorted(maps.Keys(stats)) {
		stats[format].print(format)
	}
}

// progress prints to stderr, every interval, how many archives are done and
// which one is being read, so a slow archive shows up while it is slow.
type progress struct {
	mu    sync.Mutex
	done  int
	total int
	path  string
	start time.Time
}

func (p *progress) set(done int, path string) {
	p.mu.Lock()
	p.done, p.path = done, path
	p.mu.Unlock()
}

func (p *progress) report(interval time.Duration) {
	for range time.Tick(interval) {
		p.print()
	}
}

func (p *progress) print() {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(os.Stderr, "%d/%d archives, %s elapsed", p.done, p.total, time.Since(p.start).Round(time.Second))
	if p.path != "" {
		fmt.Fprintf(os.Stderr, ", reading %s", p.path)
	}
	fmt.Fprintln(os.Stderr)
}

// detect names the format from the magic bytes.
func detect(path string) string {
	head := make([]byte, 6)
	f, err := os.Open(path)
	if err == nil {
		_, _ = io.ReadFull(f, head)
		f.Close()
	}
	switch {
	case bytes.HasPrefix(head, []byte("PK")):
		return "zip"
	case bytes.HasPrefix(head, []byte("7z\xBC\xAF\x27\x1C")):
		return "7z"
	}
	return "other"
}

// sweep collects the raw statistics of one archive and returns the error
// the curation package gives for it, if any.
func sweep(path, format string, s *formatStats, read bool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	var names []string
	switch format {
	case "zip":
		zr, err := zip.OpenReader(path)
		if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
			break // reported by curation.Open below
		}
		methods := map[string]bool{}
		for _, f := range zr.File {
			names = append(names, f.Name)
			methods[fmt.Sprint(f.Method)] = true
		}
		zr.Close()
		s.methods["method "+strings.Join(slices.Sorted(maps.Keys(methods)), ",")]++
	case "7z":
		names = sweep7z(path, s)
	}

	if len(names) > s.maxEntries {
		s.maxEntries, s.maxPath = len(names), path
	}
	if slices.ContainsFunc(names, func(n string) bool { return strings.Contains(n, `\`) }) {
		s.backslashes = append(s.backslashes, path)
	}
	if slices.ContainsFunc(names, func(n string) bool { return !utf8.ValidString(n) || strings.ContainsRune(n, utf8.RuneError) }) {
		s.badUTF8 = append(s.badUTF8, path)
	}

	a, err := curation.Open(path, curation.MaxEntries)
	if err != nil {
		return err
	}
	var errs []error
	defer a.Close()
	size := a.UncompressedSize()
	if size > s.maxSize {
		s.maxSize, s.maxSizePath = size, path
	}
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		if ratio := float64(size) / float64(info.Size()); ratio > s.maxRatio {
			s.maxRatio, s.maxRatioPath = ratio, path
		}
	}
	if _, err := curation.FindLayout(a); err != nil {
		errs = append(errs, fmt.Errorf("layout: %w", err))
	}
	if read {
		errs = append(errs, a.Stream(nil, func(_ *curation.Entry, r io.Reader) error {
			_, err := io.Copy(io.Discard, r)
			return err
		}))
	}
	return errors.Join(errs...)
}

// sweep7z records the coder chains of a 7z archive (opening the first file
// of each folder builds its chain without decompressing it) and returns the
// entry names.
func sweep7z(path string, s *formatStats) []string {
	usedCoders = nil
	r, err := sevenzip.OpenReader(path)
	if usedCoders != nil {
		s.headers[strings.Join(usedCoders, "+")]++
	}
	if err != nil {
		return nil // reported by curation.Open
	}
	defer r.Close()

	var names []string
	chains := map[string]bool{}
	filesPerFolder := map[int]int{}
	for _, f := range r.File {
		names = append(names, f.Name)
		if f.UncompressedSize == 0 {
			continue
		}
		filesPerFolder[f.Stream]++
		if filesPerFolder[f.Stream] > 1 {
			continue
		}
		usedCoders = nil
		rc, err := f.Open()
		if err == nil {
			rc.Close()
		}
		chains[strings.Join(usedCoders, "+")] = true
	}
	for _, n := range filesPerFolder {
		if n > 1 {
			s.solid++
			break
		}
	}
	s.methods[strings.Join(slices.Sorted(maps.Keys(chains)), " | ")]++
	return names
}

func (s *formatStats) print(format string) {
	fmt.Printf("== %s: %d archives\n", format, s.count)
	fmt.Printf("max entries: %d (%s)\n", s.maxEntries, s.maxPath)
	fmt.Printf("max declared uncompressed size: %dMB (%s)\n", s.maxSize/1_000_000, s.maxSizePath)
	fmt.Printf("max declared uncompressed size / file size: %.1f (%s)\n", s.maxRatio, s.maxRatioPath)
	if format == "7z" {
		fmt.Printf("solid: %d, not solid: %d\n", s.solid, s.count-s.solid)
		printCounts("encoded header coders", s.headers)
		printCounts("coder chains (per archive, folders separated by |)", s.methods)
	} else if format == "zip" {
		printCounts("compression methods", s.methods)
	}
	printPaths("archives with backslashes in names", s.backslashes)
	printPaths("archives with names that are not valid UTF-8", s.badUTF8)
	fmt.Printf("rejected by package curation: %d\n", len(s.failures))
	for _, path := range slices.Sorted(maps.Keys(s.failures)) {
		fmt.Printf("  %s: %s\n", path, strings.ReplaceAll(s.failures[path].Error(), "\n", "; "))
	}
	fmt.Println()
}

func printCounts(title string, counts map[string]int) {
	fmt.Println(title + ":")
	for _, key := range slices.Sorted(maps.Keys(counts)) {
		fmt.Printf("  %6d  %s\n", counts[key], key)
	}
}

func printPaths(title string, paths []string) {
	fmt.Printf("%s: %d\n", title, len(paths))
	for _, path := range paths[:min(len(paths), 10)] {
		fmt.Println("  " + path)
	}
}
