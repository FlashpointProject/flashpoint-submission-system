package curation

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/json"
	"io"
	"time"
)

// packModified is the modification time of every data pack entry: the fixed
// timestamp of the torrentzip format, which bluezip produced via trrntzip.
var packModified = time.Date(1996, time.December, 24, 23, 32, 0, 0, time.UTC)

// contentJSON returns the content.json of a data pack, byte for byte as
// bluezip writes it (json.dump with indent=4 to a file opened with
// newline='\r\n', no trailing newline). uniqueID is written as a JSON
// string; HTML characters are not escaped.
func contentJSON(uniqueID string) []byte {
	var id bytes.Buffer
	enc := json.NewEncoder(&id)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(uniqueID) // encoding a string cannot fail
	quoted := bytes.TrimSuffix(id.Bytes(), []byte("\n"))
	return []byte("{\r\n    \"version\": 1,\r\n    \"uniqueId\": " + string(quoted) + "\r\n}")
}

// writeDataPack writes the data pack of the curation to w: a zip with
// content.json first, then every file under the root's content/ folder as
// "content/...", in archive order. There are no directory entries; meta,
// logo and screenshot are not included. Entries are deflated at
// flate.BestCompression and carry a fixed modification time, so the same
// archive always gives the same bytes.
//
// Errors from reading the archive (a *CurationError for a disabled 7z coder
// or a CRC mismatch, for example) are returned unchanged.
func writeDataPack(w io.Writer, a *Archive, l *Layout) error {
	zw := zip.NewWriter(w)
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestCompression)
	})

	fw, err := createPackEntry(zw, "content.json")
	if err != nil {
		return err
	}
	// The root folder name is the uniqueId, as in bluezip.
	if _, err := fw.Write(contentJSON(l.Root)); err != nil {
		return err
	}

	err = a.Stream(l.IsContent, func(e *Entry, r io.Reader) error {
		fw, err := createPackEntry(zw, l.RelPath(e))
		if err != nil {
			return err
		}
		_, err = io.Copy(fw, r)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

// createPackEntry starts a deflated data pack entry with the fixed
// modification time.
func createPackEntry(zw *zip.Writer, name string) (io.Writer, error) {
	return zw.CreateHeader(&zip.FileHeader{
		Name:     name,
		Method:   zip.Deflate,
		Modified: packModified,
	})
}
