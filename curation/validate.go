package curation

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
)

// Lookups is what validation needs from FPFSS's database.
type Lookups interface {
	// TagExists reports whether a tag name or alias exists (case-insensitive).
	TagExists(ctx context.Context, name string) (bool, error)
	// LaunchCommandExists reports whether a game already uses this launch command.
	LaunchCommandExists(ctx context.Context, launchCommand string) (bool, error)
}

// Curation types reported in ValidatorResponse.CurationType, with the
// values of Python's CurationType enum.
const (
	CurationTypeFlashGame = 1
	CurationTypeOtherGame = 2
	CurationTypeAnimation = 3
)

// Messages of the validation rules, as Python gave them.
const (
	msgImageTooLarge        = "Image `%s` exceeds the 16MB validation limit."
	msgMetaTooLarge         = "Metadata file `%s` exceeds the 16MB validation limit."
	msgLocalflashDirectly   = "Content must be in additional folder in localflash rather than in localflash directly."
	msgLocalflashCommonName = "Extremely common localflash containing folder name, please change."
	msgReleaseDateFormat    = "Release date %s is incorrect. Release dates should always be in `YYYY-MM-DD` format."
	msgReleaseDateInvalid   = "Invalid release date. Ensure entered date is valid."
	msgReleaseDateRecent    = "Release date %s is less than 3 years ago. Curation should be frozen."
	msgLanguageCommas       = "Languages should be separated with semicolons, not commas."
	msgLanguageName         = "Languages must be in ISO 639-1 format, so please use `%s` instead of `%s`"
	msgLanguageReplacement  = "The correct ISO 639-1 language code for %s is `%s`, not `%s`."
	msgLanguageInvalid      = "Code `%s` is not a valid ISO 639-1 language code."
	msgMandatory            = "The `%s` property in the meta file is mandatory."
	msgHTTPS                = "Found `https` in launch command. All launch commands must use `http` instead of `https`."
	msgDuplicateLaunch      = "Identical launch command already present in the master database. Is your curation a duplicate?"
	msgMissingTags          = "Missing tags. At least one tag must be specified."
	msgUnknownTag           = "Tag `%s` is not a known tag, please verify (did you write it correctly?)."
	msgExtremeWithoutTags   = "Curation is extreme but lacks extreme tags."
	msgBlacklistedTags      = "Contains blacklisted tags"
	msgRuffleSupport        = "Ruffle Support must be '' or a value in '['standalone']'"
)

// Notes: information for reviewers, not problems with the curation.
const (
	msgArchiveSize      = "The archive is %.1f GB."
	msgUncompressedSize = "The curation is %.1f GB uncompressed."
)

// validate checks the curation archive at path. Problems with the curation
// are reported in the response; an error means the archive could not be
// processed (I/O, lookups). metaParsed reports whether the meta file was
// read and parsed, so that the response carries the curation's meta.
func (v *Validator) validate(ctx context.Context, path string) (r *types.ValidatorResponse, metaParsed bool, err error) {
	r = &types.ValidatorResponse{
		Filename:         filepath.Base(path),
		Path:             path,
		CurationErrors:   []string{},
		CurationWarnings: []string{},
		CurationNotes:    []string{},
	}
	// fail reports err as the only finding if it is a curation error. As in
	// Python, the response then has no meta and no images.
	fail := func(err error) (*types.ValidatorResponse, bool, error) {
		var ce *CurationError
		if !errors.As(err, &ce) {
			return nil, false, err
		}
		v.logCause(path, ce)
		r.CurationErrors = append(r.CurationErrors, ce.Message)
		return r, false, nil
	}

	a, l, err := v.openCuration(path)
	if err != nil {
		return fail(err)
	}
	defer a.Close()

	if a.Size >= v.noteSize {
		r.CurationNotes = append(r.CurationNotes, fmt.Sprintf(msgArchiveSize, float64(a.Size)/1e9))
	}
	if size := a.UncompressedSize(); size >= v.noteSize {
		r.CurationNotes = append(r.CurationNotes, fmt.Sprintf(msgUncompressedSize, float64(size)/1e9))
	}

	addError := func(msg string) { r.CurationErrors = append(r.CurationErrors, msg) }
	if l.LogoError != "" {
		addError(l.LogoError)
	}
	if l.ScreenshotError != "" {
		addError(l.ScreenshotError)
	}

	// Read meta, logo and screenshot, skipping any over the size limits.
	images := []struct {
		typ   string
		entry *Entry
	}{{"logo", l.Logo}, {"screenshot", l.Screenshot}}
	limits := make(map[string]int64)
	for _, image := range images {
		if image.entry == nil {
			continue
		}
		if image.entry.Size > MaxImageSize {
			addError(fmt.Sprintf(msgImageTooLarge, image.entry.Name))
			continue
		}
		limits[image.entry.Name] = MaxImageSize
	}
	if l.Meta != nil {
		if l.Meta.Size > MaxMetaSize {
			addError(fmt.Sprintf(msgMetaTooLarge, l.Meta.Name))
		} else {
			limits[l.Meta.Name] = MaxMetaSize
		}
	}
	var data map[string][]byte
	if len(limits) > 0 {
		data, err = a.ReadFiles(limits)
		if errors.Is(err, ErrEntryTooLarge) {
			addError(ErrEntryTooLarge.Message)
		} else if err != nil {
			return fail(err)
		}
	}
	responseImages := []types.ValidatorResponseImage{}
	for _, image := range images {
		if image.entry == nil {
			continue
		}
		if content, ok := data[image.entry.Name]; ok {
			responseImages = append(responseImages, types.ValidatorResponseImage{
				Type: image.typ,
				Data: base64.StdEncoding.EncodeToString(content),
			})
		}
	}

	if l.ContentError != "" {
		addError(l.ContentError)
	}
	if l.HasContentDir {
		r.CurationErrors = append(r.CurationErrors, checkLocalflash(l)...)
	}
	if l.MetaError != "" {
		addError(l.MetaError)
	}

	meta := &types.CurationMeta{AdditionalApps: []*types.CurationAdditionalApp{}}
	if l.Meta != nil {
		if content, ok := data[l.Meta.Name]; ok {
			var errs []string
			meta, errs = parseMeta(content)
			if len(errs) > 0 {
				r.CurationErrors = append(r.CurationErrors, errs...)
				return r, false, nil
			}
			if err := v.checkMeta(ctx, meta, r); err != nil {
				return nil, false, err
			}
			metaParsed = true
		}
	}
	if meta.RuffleSupport != nil && *meta.RuffleSupport != "standalone" && strings.TrimSpace(*meta.RuffleSupport) != "" {
		addError(msgRuffleSupport)
	}

	r.Meta = *meta
	r.Images = responseImages
	return r, metaParsed, nil
}

// checkLocalflash checks that files under content/localflash are in exactly
// one folder whose name is not a common one.
func checkLocalflash(l *Layout) []string {
	localflash := l.Root + "/content/localflash"
	// children maps each name directly inside localflash to whether it is a
	// file. A file named localflash counts as a file inside it.
	children := make(map[string]bool)
	found := false
	add := func(name string, isFile bool) {
		if name == localflash {
			found = true
			if isFile {
				children["localflash"] = true
			}
			return
		}
		rest, ok := strings.CutPrefix(name, localflash+"/")
		if !ok {
			return
		}
		found = true
		child, _, nested := strings.Cut(rest, "/")
		children[child] = children[child] || (isFile && !nested)
	}
	for _, e := range l.Content {
		add(e.Name, true)
	}
	for _, dir := range l.ContentDirs {
		add(dir, false)
	}

	if !found {
		return nil
	}
	if len(children) > 1 {
		return []string{msgLocalflashDirectly}
	}
	for child, isFile := range children { // at most one
		if isFile {
			return []string{msgLocalflashDirectly}
		}
		if commonLocalflashNames[child] {
			return []string{msgLocalflashCommonName}
		}
	}
	return nil
}

// checkMeta applies the rules on the meta values, in Python's order, and
// sets the extreme flag and curation type of r.
func (v *Validator) checkMeta(ctx context.Context, m *types.CurationMeta, r *types.ValidatorResponse) error {
	addError := func(msg string) { r.CurationErrors = append(r.CurationErrors, msg) }

	if m.ReleaseDate != nil {
		errs, warnings := checkReleaseDate(v.now(), *m.ReleaseDate)
		r.CurationErrors = append(r.CurationErrors, errs...)
		r.CurationWarnings = append(r.CurationWarnings, warnings...)
	}
	if isSet(m.Languages) {
		r.CurationErrors = append(r.CurationErrors, checkLanguages(*m.Languages)...)
	}

	for _, field := range []struct {
		name  string
		value *string
	}{
		{"Title", m.Title},
		{"Languages", m.Languages},
		{"Source", m.Source},
		{"Launch Command", m.LaunchCommand},
		{"Status", m.Status},
		{"Application Path", m.ApplicationPath},
	} {
		if !isSet(field.value) {
			addError(fmt.Sprintf(msgMandatory, field.name))
		}
	}

	if isSet(m.LaunchCommand) {
		if strings.HasPrefix(*m.LaunchCommand, "https://") {
			addError(msgHTTPS)
		}
		exists, err := v.lookups.LaunchCommandExists(ctx, *m.LaunchCommand)
		if err != nil {
			return fmt.Errorf("looking up launch command: %w", err)
		}
		if exists {
			addError(msgDuplicateLaunch)
		}
	}

	tags := splitList(m.Tags)
	if len(tags) == 0 {
		addError(msgMissingTags)
	}
	for _, tag := range tags {
		exists, err := v.lookups.TagExists(ctx, tag)
		if err != nil {
			return fmt.Errorf("looking up tag: %w", err)
		}
		if !exists {
			r.CurationWarnings = append(r.CurationWarnings, fmt.Sprintf(msgUnknownTag, tag))
		}
	}

	r.IsExtreme = m.Extreme != nil && (strings.EqualFold(*m.Extreme, "yes") || strings.EqualFold(*m.Extreme, "true"))
	if len(tags) > 0 {
		var hasExtreme, hasBlacklisted, hasLegacyExtreme bool
		for _, tag := range tags {
			tag = strings.ToLower(tag)
			hasExtreme = hasExtreme || extremeTags[tag]
			hasBlacklisted = hasBlacklisted || blacklistedTags[tag]
			hasLegacyExtreme = hasLegacyExtreme || tag == "legacy-extreme"
		}
		if hasExtreme || hasBlacklisted || hasLegacyExtreme {
			r.IsExtreme = true
		}
		if r.IsExtreme && !hasExtreme {
			addError(msgExtremeWithoutTags)
		}
		if hasBlacklisted {
			addError(msgBlacklistedTags)
		}
	}

	switch {
	case m.Library != nil && strings.Contains(*m.Library, "theatre"):
		r.CurationType = CurationTypeAnimation
	case m.Platform == nil || strings.Contains(*m.Platform, "Flash"):
		r.CurationType = CurationTypeFlashGame
	default:
		r.CurationType = CurationTypeOtherGame
	}
	return nil
}

var releaseDateFormat = regexp.MustCompile(`^[0-9]{4}(-[0-9]{2}){0,2}$`)

// checkReleaseDate checks the format of a release date (YYYY, YYYY-MM or
// YYYY-MM-DD) and warns when it may be less than 3×365 days before now,
// counting a year or month as its last day.
func checkReleaseDate(now time.Time, date string) (errs, warnings []string) {
	date = strings.TrimSpace(date)
	if date == "" {
		return nil, nil
	}
	if !releaseDateFormat.MatchString(date) {
		return []string{fmt.Sprintf(msgReleaseDateFormat, date)}, nil
	}
	layout := map[int]string{4: "2006", 7: "2006-01", 10: "2006-01-02"}[len(date)]
	t, err := time.Parse(layout, date)
	if err != nil {
		return []string{msgReleaseDateInvalid}, nil
	}
	last := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
	switch len(date) {
	case 4:
		last = last.AddDate(1, 0, -1) // December 31
	case 7:
		last = last.AddDate(0, 1, -1) // last day of the month
	}
	if !last.Before(now.AddDate(0, 0, -3*365)) {
		return nil, []string{fmt.Sprintf(msgReleaseDateRecent, date)}
	}
	return nil, nil
}

// checkLanguages checks a semicolon-separated list of ISO 639-1 codes.
func checkLanguages(languages string) []string {
	var errs []string
	for _, code := range strings.Split(languages, ";") {
		code = strings.TrimSpace(code)
		if code == "" || languageNames[code] != "" {
			continue
		}
		if strings.Contains(code, ",") {
			errs = append(errs, msgLanguageCommas)
		} else if alpha2, ok := languageCodes[code]; ok {
			errs = append(errs, fmt.Sprintf(msgLanguageName, alpha2, code))
		} else if replacement, ok := languageReplacements[code]; ok {
			errs = append(errs, fmt.Sprintf(msgLanguageReplacement, languageNames[replacement], replacement, code))
		} else {
			errs = append(errs, fmt.Sprintf(msgLanguageInvalid, code))
		}
	}
	return errs
}

// splitList splits a semicolon-separated meta value into its trimmed,
// non-empty entries.
func splitList(value *string) []string {
	if value == nil {
		return nil
	}
	var entries []string
	for _, entry := range strings.Split(*value, ";") {
		if entry = strings.TrimSpace(entry); entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// isSet reports whether a meta value is present and not empty (Python's
// truthiness of a string).
func isSet(value *string) bool {
	return value != nil && *value != ""
}

// Embedded copies of the Python validator's data files.
var (
	//go:embed data/language-codes.json
	languageCodesJSON []byte
	//go:embed data/lang_replacements.json
	langReplacementsJSON []byte
	//go:embed data/extreme_tags.json
	extremeTagsJSON []byte
	//go:embed data/blacklisted_tags.json
	blacklistedTagsJSON []byte
	//go:embed data/common_localflash_names.json
	commonLocalflashNamesJSON []byte
)

var (
	// languageNames maps ISO 639-1 codes to English names, languageCodes
	// the other way round.
	languageNames, languageCodes = func() (map[string]string, map[string]string) {
		var languages []struct {
			English string `json:"English"`
			Alpha2  string `json:"alpha2"`
		}
		mustUnmarshal("language-codes.json", languageCodesJSON, &languages)
		names := make(map[string]string, len(languages))
		codes := make(map[string]string, len(languages))
		for _, l := range languages {
			names[l.Alpha2] = l.English
			codes[l.English] = l.Alpha2
		}
		return names, codes
	}()

	// languageReplacements maps common wrong codes to the right ones.
	languageReplacements = func() map[string]string {
		var replacements map[string]string
		mustUnmarshal("lang_replacements.json", langReplacementsJSON, &replacements)
		return replacements
	}()

	// extremeTags and blacklistedTags are lower-cased tag names.
	extremeTags     = lowerSet(stringList("extreme_tags.json", extremeTagsJSON, "tags"))
	blacklistedTags = lowerSet(stringList("blacklisted_tags.json", blacklistedTagsJSON, "tags"))

	// commonLocalflashNames are folder names inside localflash that are
	// likely to clash with other games.
	commonLocalflashNames = func() map[string]bool {
		set := make(map[string]bool)
		for _, name := range stringList("common_localflash_names.json", commonLocalflashNamesJSON, "names") {
			set[name] = true
		}
		return set
	}()
)

// stringList reads a JSON object with one list of strings under key.
func stringList(file string, data []byte, key string) []string {
	var lists map[string][]string
	mustUnmarshal(file, data, &lists)
	return lists[key]
}

func lowerSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[strings.ToLower(name)] = true
	}
	return set
}

func mustUnmarshal(file string, data []byte, v any) {
	if err := json.Unmarshal(data, v); err != nil {
		panic(fmt.Sprintf("parsing embedded %s: %v", file, err))
	}
}
