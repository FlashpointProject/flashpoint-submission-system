package curation

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"go.yaml.in/yaml/v3"
)

// Messages for meta parsing problems. The first two are Python's.
const (
	msgMetaEmpty         = "The meta file seems to be empty."
	msgMetaYAML          = "Unable to load meta YAML file"
	msgMetaNotText       = "The `%s` property in the meta file must be text, not a list or mapping. Separate several values with semicolons."
	msgAddAppsNotMapping = "The `Additional Applications` property in the meta file must be a mapping from headings to applications."
	msgAddAppIncomplete  = "The additional application `%s` in the meta file must have an `Application Path` and a `Launch Command`."
	msgAddAppNoHeading   = "An additional application in the meta file has no heading."
)

// parseMeta parses the content of a meta.yaml file. Every scalar is kept as
// its text (Version: 1.0 stays "1.0", dates stay as written); null and empty
// values are nil. Unknown keys are ignored.
//
// As Python did, the legacy Platform key replaces Platforms, Primary
// Platform defaults to the first platform, and Extras and Message are taken
// out of Additional Applications. AdditionalApps is never nil.
//
// Problems are returned as curation errors, and meta is then nil.
func parseMeta(data []byte) (*types.CurationMeta, []string) {
	if !utf8.Valid(data) {
		return nil, []string{msgMetaYAML}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, []string{msgMetaYAML}
	}
	if len(doc.Content) == 0 || isNull(doc.Content[0]) {
		return nil, []string{msgMetaEmpty}
	}
	pairs, ok := mappingPairs(doc.Content[0])
	if !ok {
		return nil, []string{msgMetaYAML}
	}

	meta := &types.CurationMeta{AdditionalApps: []*types.CurationAdditionalApp{}}
	var legacyPlatform *string
	var errs []string
	for _, p := range pairs {
		var field **string
		switch p.key {
		case "Additional Applications":
			errs = append(errs, parseAdditionalApps(meta, p.value)...)
			continue
		case "Platform":
			field = &legacyPlatform
		default:
			field = metaField(meta, p.key)
			if field == nil {
				continue
			}
		}
		value, ok := scalar(p.value)
		if !ok {
			errs = append(errs, fmt.Sprintf(msgMetaNotText, p.key))
			continue
		}
		*field = value
	}
	if len(errs) > 0 {
		return nil, errs
	}

	if legacyPlatform != nil {
		meta.Platform = legacyPlatform
	}
	if meta.PrimaryPlatform == nil && meta.Platform != nil {
		first, _, _ := strings.Cut(*meta.Platform, ";")
		first = strings.TrimSpace(first)
		meta.PrimaryPlatform = &first
	}
	return meta, nil
}

// metaField returns the field of m for a top-level meta key, or nil if the
// key is not one FPFSS stores. The keys are the JSON names of the fields.
func metaField(m *types.CurationMeta, key string) **string {
	switch key {
	case "UUID":
		return &m.UUID
	case "Application Path":
		return &m.ApplicationPath
	case "Developer":
		return &m.Developer
	case "Extreme":
		return &m.Extreme
	case "Game Notes":
		return &m.GameNotes
	case "Languages":
		return &m.Languages
	case "Launch Command":
		return &m.LaunchCommand
	case "Original Description":
		return &m.OriginalDescription
	case "Play Mode":
		return &m.PlayMode
	case "Primary Platform":
		return &m.PrimaryPlatform
	case "Platforms":
		return &m.Platform
	case "Publisher":
		return &m.Publisher
	case "Release Date":
		return &m.ReleaseDate
	case "Series":
		return &m.Series
	case "Source":
		return &m.Source
	case "Status":
		return &m.Status
	case "Tags":
		return &m.Tags
	case "Tag Categories":
		return &m.TagCategories
	case "Title":
		return &m.Title
	case "Alternate Titles":
		return &m.AlternateTitles
	case "Library":
		return &m.Library
	case "Version":
		return &m.Version
	case "Curation Notes":
		return &m.CurationNotes
	case "Mount Parameters":
		return &m.MountParameters
	case "Extras":
		return &m.Extras
	case "Message":
		return &m.Message
	case "Ruffle Support":
		return &m.RuffleSupport
	}
	return nil
}

// parseAdditionalApps reads the Additional Applications mapping into m:
// Extras and Message go to their own fields, every other key is the heading
// of an application with an Application Path and a Launch Command.
func parseAdditionalApps(m *types.CurationMeta, n *yaml.Node) []string {
	if isNull(n) {
		return nil
	}
	if resolve(n).Kind != yaml.MappingNode {
		return []string{msgAddAppsNotMapping}
	}
	pairs, ok := mappingPairs(n)
	if !ok {
		return []string{msgMetaYAML}
	}

	var errs []string
	for _, p := range pairs {
		switch p.key {
		case "Extras", "Message":
			value, ok := scalar(p.value)
			if !ok {
				errs = append(errs, fmt.Sprintf(msgMetaNotText, p.key))
			} else if p.key == "Extras" {
				m.Extras = value
			} else {
				m.Message = value
			}
		case "":
			errs = append(errs, msgAddAppNoHeading)
		default:
			app, err := parseAdditionalApp(p.key, p.value)
			if err != "" {
				errs = append(errs, err)
				continue
			}
			m.AdditionalApps = append(m.AdditionalApps, app)
		}
	}
	return errs
}

func parseAdditionalApp(heading string, n *yaml.Node) (*types.CurationAdditionalApp, string) {
	incomplete := fmt.Sprintf(msgAddAppIncomplete, heading)
	if resolve(n).Kind != yaml.MappingNode {
		return nil, incomplete
	}
	pairs, ok := mappingPairs(n)
	if !ok {
		return nil, msgMetaYAML
	}
	app := &types.CurationAdditionalApp{Heading: &heading}
	for _, p := range pairs {
		var field **string
		switch p.key {
		case "Application Path":
			field = &app.ApplicationPath
		case "Launch Command":
			field = &app.LaunchCommand
		default:
			continue
		}
		value, ok := scalar(p.value)
		if !ok {
			return nil, incomplete
		}
		*field = value
	}
	if app.ApplicationPath == nil || app.LaunchCommand == nil {
		return nil, incomplete
	}
	return app, ""
}

type yamlPair struct {
	key   string
	value *yaml.Node
}

// mappingPairs returns the entries of a mapping node in order. It reports
// false if n is not a mapping, a key is not a scalar or a key occurs twice
// (Python's YAML loader rejected duplicate keys).
func mappingPairs(n *yaml.Node) ([]yamlPair, bool) {
	n = resolve(n)
	if n.Kind != yaml.MappingNode {
		return nil, false
	}
	pairs := make([]yamlPair, 0, len(n.Content)/2)
	seen := make(map[string]bool)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := resolve(n.Content[i])
		if key.Kind != yaml.ScalarNode || seen[key.Value] {
			return nil, false
		}
		seen[key.Value] = true
		pairs = append(pairs, yamlPair{key: key.Value, value: n.Content[i+1]})
	}
	return pairs, true
}

// scalar returns the text of a scalar node, nil for null. It reports false
// for a list or mapping.
func scalar(n *yaml.Node) (*string, bool) {
	n = resolve(n)
	if n.Kind != yaml.ScalarNode {
		return nil, false
	}
	if isNull(n) {
		return nil, true
	}
	value := n.Value
	return &value, true
}

func isNull(n *yaml.Node) bool {
	n = resolve(n)
	return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

// resolve follows an alias to the node it refers to.
func resolve(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}
