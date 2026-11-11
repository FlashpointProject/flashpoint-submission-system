package curation

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// FuzzOpenLayout opens arbitrary bytes as an archive, finds the layout and
// reads the selected entries. It must not panic or hang.
func FuzzOpenLayout(f *testing.F) {
	for _, name := range []string{
		"test_curation_valid.zip",
		"test_curation_valid.7z",
		"test_curation_empty_content.7z",
		"test_curation_missing_root_folder.zip",
		"sevenzip/copy.7z",
	} {
		data, err := os.ReadFile(fixture(name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		a, err := newArchive(bytes.NewReader(data), int64(len(data)), "fuzz.zip", 1000)
		if err != nil {
			return
		}
		l, err := FindLayout(a)
		if err != nil {
			return
		}
		limits := make(map[string]int64)
		for _, e := range []*Entry{l.Meta, l.Logo, l.Screenshot} {
			if e != nil {
				limits[e.Name] = 1 << 20
			}
		}
		_, _ = a.ReadFiles(limits)
		// Read at most 1 MiB per entry so that small decompression bombs do
		// not slow the fuzzer down.
		_ = a.Stream(l.IsContent, func(e *Entry, r io.Reader) error {
			_, err := io.CopyN(io.Discard, r, 1<<20)
			if err == io.EOF {
				err = nil
			}
			return err
		})
	})
}
