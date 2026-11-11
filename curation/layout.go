package curation

import (
	"fmt"
	"strings"
)

// Messages shared with the Python validator.
const (
	msgNotStructured   = "Logo, screenshot, content folder and meta not found. Is your curation structured properly?"
	msgLogoCase        = "Logo file extension must be lowercase."
	msgLogoMissing     = "Logo file is either missing or its filename is incorrect."
	msgScreenshotCase  = "Screenshot file extension must be lowercase."
	msgScreenshotMiss  = "Screenshot file is either missing or its filename is incorrect."
	msgContentMissing  = "Content folder not found."
	msgContentEmpty    = "No files found in content folder."
	msgMetaMissing     = "Meta file is either missing or its filename is incorrect. Are you using Flashpoint Core for curating?"
	msgMetaTxt         = "meta.txt is no longer supported. Open the curation in the Flashpoint launcher and save it again to get a meta.yaml."
	msgMetaYAMLAndYML  = "The curation contains both meta.yaml and meta.yml. Keep only one of them."
	msgTopLevelFiles   = "Everything must be inside the curation folder `%s`, but the archive also contains `%s` at the top level."
	msgSeveralTopLevel = "The archive must contain exactly one folder at the top level (the curation folder), but it contains %d: %s."
)

// Layout is where the parts of a curation are in its archive. The Entry
// pointers point into Archive.Files.
type Layout struct {
	// Root is the name of the single top-level folder. For a current
	// curation this is the curation UUID; older curations may use any name.
	Root string
	// Meta is <Root>/meta.yaml or <Root>/meta.yml, nil if missing (or if
	// both exist).
	Meta *Entry
	// Logo is <Root>/logo.png, nil if missing.
	Logo *Entry
	// Screenshot is <Root>/ss.png, nil if missing.
	Screenshot *Entry
	// Content lists the files under <Root>/content/, in archive order.
	Content []*Entry
	// ContentDirs lists the directory entries under <Root>/content/ (full
	// archive paths), in archive order. Empty folders only show up here.
	ContentDirs []string
	// HasContentDir reports whether <Root>/content/ exists, as a directory
	// entry or as the folder of a file.
	HasContentDir bool

	// Curation errors found in the layout, each empty if there is none.
	// Python reported them in this order (with image size errors between
	// ScreenshotError and ContentError, and meta parsing errors after
	// MetaError).
	LogoError       string
	ScreenshotError string
	ContentError    string
	MetaError       string
}

// FindLayout finds the root folder, meta file, logo, screenshot and content
// of the curation in a, from the entry names only (nothing is decompressed).
//
// Problems that leave nothing to validate are returned as a *CurationError:
// no top-level folder, several top-level folders, files next to the root
// folder, or none of logo, screenshot, content folder and meta found. Other
// problems are reported in the Layout's error fields.
func FindLayout(a *Archive) (*Layout, error) {
	var roots, topFiles []string
	seen := make(map[string]bool)
	for i := range a.Files {
		name := a.Files[i].Name
		top, _, nested := strings.Cut(name, "/")
		if !nested {
			topFiles = append(topFiles, name)
		} else if !seen[top] {
			seen[top] = true
			roots = append(roots, top)
		}
	}
	switch {
	case len(roots) == 0:
		return nil, &CurationError{Message: msgNotStructured}
	case len(roots) > 1:
		return nil, &CurationError{Message: fmt.Sprintf(msgSeveralTopLevel, len(roots), backtickList(roots))}
	}

	l := &Layout{Root: roots[0]}
	rootPrefix := l.Root + "/"
	contentDir := rootPrefix + "content"
	contentPrefix := contentDir + "/"

	var metas []*Entry
	var hasMetaTxt bool
	logoLike, screenshotLike := 0, 0 // names equal to logo.png / ss.png ignoring case
	for i := range a.Files {
		e := &a.Files[i]
		if strings.HasPrefix(e.Name, contentPrefix) {
			l.Content = append(l.Content, e)
			continue
		}
		name, ok := strings.CutPrefix(e.Name, rootPrefix)
		if !ok || strings.Contains(name, "/") {
			continue
		}
		switch name {
		case "meta.yaml", "meta.yml":
			metas = append(metas, e)
		case "meta.txt":
			hasMetaTxt = true
		case "logo.png":
			l.Logo = e
		case "ss.png":
			l.Screenshot = e
		}
		if strings.EqualFold(name, "logo.png") {
			logoLike++
		}
		if strings.EqualFold(name, "ss.png") {
			screenshotLike++
		}
	}

	l.HasContentDir = len(l.Content) > 0
	for _, dir := range a.Dirs {
		if dir == contentDir {
			l.HasContentDir = true
		} else if strings.HasPrefix(dir, contentPrefix) {
			l.HasContentDir = true
			l.ContentDirs = append(l.ContentDirs, dir)
		}
	}

	if l.Logo == nil && l.Screenshot == nil && !l.HasContentDir && len(metas) == 0 && !hasMetaTxt {
		return nil, &CurationError{Message: msgNotStructured}
	}
	if len(topFiles) > 0 {
		return nil, &CurationError{Message: fmt.Sprintf(msgTopLevelFiles, l.Root, topFiles[0])}
	}

	l.LogoError = imageError(l.Logo, logoLike, msgLogoCase, msgLogoMissing)
	l.ScreenshotError = imageError(l.Screenshot, screenshotLike, msgScreenshotCase, msgScreenshotMiss)

	switch {
	case !l.HasContentDir:
		l.ContentError = msgContentMissing
	case len(l.Content) == 0:
		l.ContentError = msgContentEmpty
	}

	switch {
	case len(metas) == 1:
		l.Meta = metas[0]
	case len(metas) > 1:
		l.MetaError = msgMetaYAMLAndYML
	case hasMetaTxt:
		l.MetaError = msgMetaTxt
	default:
		l.MetaError = msgMetaMissing
	}
	return l, nil
}

// imageError reproduces Python's logo/screenshot check: any name that only
// matches ignoring case is an extension error, otherwise a missing exact
// match is a missing-file error.
func imageError(exact *Entry, likeCount int, caseMsg, missingMsg string) string {
	exactCount := 0
	if exact != nil {
		exactCount = 1
	}
	switch {
	case likeCount > exactCount:
		return caseMsg
	case exact == nil:
		return missingMsg
	}
	return ""
}

// IsContent reports whether e is a file under <Root>/content/.
func (l *Layout) IsContent(e *Entry) bool {
	return strings.HasPrefix(e.Name, l.Root+"/content/")
}

// RelPath returns e's path relative to the root folder, for example
// "content/www.example.com/game.swf" or "meta.yaml".
func (l *Layout) RelPath(e *Entry) string {
	return strings.TrimPrefix(e.Name, l.Root+"/")
}

func backtickList(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = "`" + name + "`"
	}
	return strings.Join(quoted, ", ")
}
