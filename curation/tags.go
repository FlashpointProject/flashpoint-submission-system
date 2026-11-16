package curation

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	// WikiTagsURL is the wiki page whose tables list the known tags.
	WikiTagsURL = "https://flashpointarchive.org/datahub/Tags"

	// wikiTagsTTL is how long a fetched (or failed) wiki tag list is reused.
	// GetTags runs on every submission page view and waits for a fetch, so
	// the wiki is asked at most every 10 minutes, as FPFSS's tag cache did.
	wikiTagsTTL = 10 * time.Minute
	// wikiRequestTimeout bounds how long a page view waits for the wiki, as
	// FPFSS's tag fetch did.
	wikiRequestTimeout = 5 * time.Second
)

//go:embed data/category_tags.json
var categoryTagsJSON []byte

// categoryTags are the tags from data/category_tags.json, in file order.
var categoryTags = func() []types.Tag {
	var file struct {
		Tags []types.Tag `json:"tags"`
	}
	if err := json.Unmarshal(categoryTagsJSON, &file); err != nil {
		panic(fmt.Sprintf("parsing embedded category_tags.json: %v", err))
	}
	return file.Tags
}()

// WikiTags supplies the tag list with descriptions: the category tags from
// the embedded file plus the tags scraped from the wiki. The wiki list is
// cached for wikiTagsTTL, and when the wiki cannot be fetched or parsed the
// last good list is used. Create it with NewWikiTags; it is safe for
// concurrent use.
type WikiTags struct {
	log    logrus.FieldLogger
	url    string
	client *http.Client
	now    func() time.Time

	// mu is held during a fetch, so concurrent callers share one request.
	mu       sync.Mutex
	lastGood []types.Tag
	expires  time.Time
}

// NewWikiTags returns a WikiTags that scrapes WikiTagsURL and logs fetch
// failures to log.
func NewWikiTags(log logrus.FieldLogger) *WikiTags {
	return &WikiTags{
		log:    log,
		url:    WikiTagsURL,
		client: &http.Client{Timeout: wikiRequestTimeout},
		now:    time.Now,
	}
}

// FetchInBackground starts fetching the wiki tags without waiting for the
// result, so the list is ready (and remembered in case the wiki goes down)
// before the first caller needs it. Call it once at startup.
func (w *WikiTags) FetchInBackground() {
	go w.wikiTags()
}

// GetTags returns the category tags followed by the wiki tags. Only Name and
// Description are set. A name can occur in both parts; consumers let the later
// (wiki) entry win.
//
// The error is always nil: a wiki failure falls back to the last good list (or
// to no wiki tags before the first success). ctx is not used for the fetch,
// which is shared by all callers and bounded by its own timeout.
func (w *WikiTags) GetTags(ctx context.Context) ([]types.Tag, error) {
	return append(slices.Clone(categoryTags), w.wikiTags()...), nil
}

// wikiTags returns the cached wiki tag list, refetching it when it expired.
func (w *WikiTags) wikiTags() []types.Tag {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.now().Before(w.expires) {
		return w.lastGood
	}
	tags, err := w.fetch()
	if err != nil {
		w.log.Warnf("failed to get tags from wiki, using last good list of %d tags: %v", len(w.lastGood), err)
	} else {
		w.log.Debugf("got %d tags from wiki", len(tags))
		w.lastGood = tags
	}
	w.expires = w.now().Add(wikiTagsTTL)
	return w.lastGood
}

func (w *WikiTags) fetch() ([]types.Tag, error) {
	resp, err := w.client.Get(w.url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wiki returned HTTP %d", resp.StatusCode)
	}
	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseWikiTags(doc)
}

// parseWikiTags extracts the tags from the wiki page's tables. A table
// contributes rows once a header row containing a "Tag" column has been seen;
// the "Description" column, when present, gives the description. Rows that do
// not map unambiguously onto the header columns are skipped.
func parseWikiTags(doc *html.Node) ([]types.Tag, error) {
	var tags []types.Tag
	index := map[string]int{} // tag name -> position in tags
	for table := range doc.Descendants() {
		if table.DataAtom != atom.Table {
			continue
		}
		var headers []string
		for _, row := range tableRows(table) {
			cells := rowCells(row)
			if len(cells) == 0 {
				continue
			}
			if !slices.ContainsFunc(cells, isTd) { // only th cells: a header row
				candidate := make([]string, len(cells))
				for i, cell := range cells {
					candidate[i] = strings.ToLower(cellText(cell))
				}
				// Section labels such as Species' "Specific Types" are not
				// replacement column headers.
				if slices.Contains(candidate, "tag") {
					headers = candidate
				}
				continue
			}
			nameIndex := slices.Index(headers, "tag")
			if nameIndex < 0 || len(cells) > len(headers) {
				continue
			}
			// Merged cells do not have an unambiguous column mapping.
			if slices.ContainsFunc(cells, isMerged) {
				continue
			}
			// Franchise tables have an Example column, not a Description.
			descriptionIndex := slices.Index(headers, "description")
			// Some wiki rows omit trailing Example/Specific Types cells.
			if nameIndex >= len(cells) || descriptionIndex >= len(cells) {
				continue
			}
			name := cellText(cells[nameIndex])
			if name == "" {
				continue
			}
			description := ""
			if descriptionIndex >= 0 {
				description = cellText(cells[descriptionIndex])
			}
			// A tag can occur in several sections. Keep its first nonempty
			// description so later combination/example rows cannot replace it.
			if i, ok := index[name]; !ok {
				index[name] = len(tags)
				tags = append(tags, types.Tag{Name: name, Description: description})
			} else if tags[i].Description == "" {
				tags[i].Description = description
			}
		}
	}
	if len(tags) == 0 {
		return nil, errors.New("no tags found in wiki tag tables")
	}
	return tags, nil
}

// tableRows returns the rows of table itself, not of tables nested in it. The
// HTML parser always puts rows directly in the table or in its row groups.
func tableRows(table *html.Node) []*html.Node {
	var rows []*html.Node
	for child := range table.ChildNodes() {
		switch child.DataAtom {
		case atom.Tr:
			rows = append(rows, child)
		case atom.Thead, atom.Tbody, atom.Tfoot:
			for row := range child.ChildNodes() {
				if row.DataAtom == atom.Tr {
					rows = append(rows, row)
				}
			}
		}
	}
	return rows
}

// rowCells returns the th and td cells of row.
func rowCells(row *html.Node) []*html.Node {
	var cells []*html.Node
	for cell := range row.ChildNodes() {
		if cell.DataAtom == atom.Th || cell.DataAtom == atom.Td {
			cells = append(cells, cell)
		}
	}
	return cells
}

func isTd(cell *html.Node) bool { return cell.DataAtom == atom.Td }

// isMerged reports whether cell spans more than one column or row.
func isMerged(cell *html.Node) bool {
	for _, attr := range cell.Attr {
		if (attr.Key == "colspan" || attr.Key == "rowspan") && attr.Val != "1" {
			return true
		}
	}
	return false
}

// cellText returns the visible text of cell with whitespace collapsed.
// Punctuation around inline markup is kept as is, but line breaks and
// paragraph, div and list item boundaries separate words.
func cellText(cell *html.Node) string {
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		block := n.DataAtom == atom.P || n.DataAtom == atom.Div || n.DataAtom == atom.Li
		if block || n.DataAtom == atom.Br {
			b.WriteByte(' ')
		}
		for child := range n.ChildNodes() {
			walk(child)
		}
		if block {
			b.WriteByte(' ')
		}
	}
	for child := range cell.ChildNodes() {
		walk(child)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
