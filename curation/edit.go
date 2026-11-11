package curation

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"go.yaml.in/yaml/v3"
)

// editMetaYAML returns src with the edited values (the non-nil fields of
// edit) replaced. Keys that exist get their value replaced, keeping the
// comments around it; missing keys are appended at the end of the mapping.
// The key names are the ones Python's update_meta wrote. With nothing
// edited, src is returned unchanged.
//
// New values are written as strings: the encoder quotes them where plain
// text would read back as something else ("1.10", "2004", "null", ...) and
// uses a block or double-quoted scalar for multi-line text, so every value
// reads back as exactly the string given.
//
// The rest of the document keeps its order, comments and values, but it is
// re-encoded, so the encoder normalizes its formatting: indentation (set to
// 2 spaces, as the launcher writes), blank lines and "---" are dropped,
// long or folded scalars are re-flowed (same value), and characters outside
// the Basic Multilingual Plane (emoji) are written as \U escapes in a
// double-quoted scalar.
func editMetaYAML(src []byte, edit *types.EditCurationMeta) ([]byte, error) {
	if edit == nil {
		return src, nil
	}
	values := []struct {
		key   string
		value *string
	}{
		{"Title", edit.Title},
		{"Alternate Titles", edit.AlternateTitles},
		{"Version", edit.Version},
		{"Developer", edit.Developer},
		{"Publisher", edit.Publisher},
		{"Release Date", edit.ReleaseDate},
		{"Series", edit.Series},
		{"Source", edit.Source},
		{"Status", edit.Status},
		{"Tags", edit.Tags},
		{"Languages", edit.Languages},
		{"Original Description", edit.OriginalDescription},
		{"Game Notes", edit.GameNotes},
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("meta is not a YAML mapping")
	}
	mapping := doc.Content[0]

	edited := false
	for _, v := range values {
		if v.value != nil {
			setValue(mapping, v.key, *v.value)
			edited = true
		}
	}
	if !edited {
		return src, nil
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// setValue sets key to the string value in mapping. An existing value node
// is changed in place so its comments stay; a missing key is appended.
func setValue(mapping *yaml.Node, key, value string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			n := mapping.Content[i+1]
			n.Kind = yaml.ScalarNode
			n.Tag = "!!str"
			n.Value = value
			n.Style = 0 // let the encoder pick a style that fits the value
			n.Content = nil
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

// writeEditedArchive writes a copy of the curation archive to w as a zip:
// the directory entries of a.Dirs (so empty folders survive), then every
// file in archive order with its path and modification time, except that
// the meta file's content is replaced by meta, and logo and screenshot
// replace <root>/logo.png and <root>/ss.png when non-nil. An image the
// archive does not have is added at the end. Replaced and added files get
// the current time. Files are deflated at the default level.
//
// l.Meta must not be nil. Errors from reading the archive are returned
// unchanged.
func writeEditedArchive(w io.Writer, a *Archive, l *Layout, meta, logo, screenshot []byte) error {
	replace := map[string][]byte{l.Meta.Name: meta}
	if logo != nil {
		replace[l.Root+"/logo.png"] = logo
	}
	if screenshot != nil {
		replace[l.Root+"/ss.png"] = screenshot
	}
	now := time.Now()

	zw := zip.NewWriter(w)
	for _, dir := range a.Dirs {
		// archive/zip stores a name ending in "/" as an empty directory entry.
		if _, err := zw.Create(dir + "/"); err != nil {
			return err
		}
	}
	err := a.Stream(nil, func(e *Entry, r io.Reader) error {
		if data, ok := replace[e.Name]; ok {
			delete(replace, e.Name)
			return writeFile(zw, e.Name, now, data)
		}
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: e.Name, Method: zip.Deflate, Modified: e.Modified})
		if err != nil {
			return err
		}
		_, err = io.Copy(fw, r)
		return err
	})
	if err != nil {
		return err
	}
	// Images the archive did not have, in a fixed order.
	for _, name := range []string{l.Root + "/logo.png", l.Root + "/ss.png"} {
		if data, ok := replace[name]; ok {
			if err := writeFile(zw, name, now, data); err != nil {
				return err
			}
		}
	}
	return zw.Close()
}

// writeFile adds a deflated file entry with the given content.
func writeFile(zw *zip.Writer, name string, modified time.Time, data []byte) error {
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: modified})
	if err != nil {
		return err
	}
	_, err = fw.Write(data)
	return err
}
