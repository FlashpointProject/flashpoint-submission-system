package curation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"golang.org/x/net/html"
)

func parseWikiTagsString(t *testing.T, page string) ([]types.Tag, error) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	return parseWikiTags(doc)
}

func assertWikiTags(t *testing.T, page string, want []types.Tag) {
	t.Helper()
	got, err := parseWikiTagsString(t, page)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestWikiTagNamesAndDescriptionsStayInTheirOwnCells(t *testing.T) {
	assertWikiTags(t, `
		<table><tr><th>Tag</th><th>Description</th><th>Example</th></tr>
		<tr><td>Newgrounds Obituary</td>
			<td>Removed from Newgrounds; see the <a href="/obituaries">Obituaries page</a>.</td>
			<td><a href="/game">Create a Smiley</a></td></tr>
		<tr><td>Emulated</td>
			<td>Please note <a href="/rules">Not Accepted Curations</a>.
				Not to be confused with <code>Emulator</code>.</td><td></td></tr>
		<tr><td><a href="/future"><strong>Future</strong> Tag</a></td>
			<td><code>Formatted</code> description with <i>emphasis</i>.</td>
			<td><a href="/example">Unrelated example</a></td></tr>
		</table>`,
		[]types.Tag{
			{Name: "Newgrounds Obituary", Description: "Removed from Newgrounds; see the Obituaries page."},
			{Name: "Emulated", Description: "Please note Not Accepted Curations. Not to be confused with Emulator."},
			{Name: "Future Tag", Description: "Formatted description with emphasis."},
		})
}

func TestWikiTagColumnsAreIdentifiedByHeadersAndExamplesAreNotDescriptions(t *testing.T) {
	assertWikiTags(t, `
		<table><thead><tr><th> Example </th><th><b>TAG</b></th><th>Description</th></tr></thead>
		<tbody><tr><td>Example game</td><td>Reordered</td><td>Actual description.</td></tr></tbody></table>
		<table><tr><th>Tag</th><th>Example</th></tr>
		<tr><td>30 Rock</td><td>30 Rock: The Boardroom</td></tr></table>`,
		[]types.Tag{
			{Name: "Reordered", Description: "Actual description."},
			{Name: "30 Rock", Description: ""},
		})
}

func TestWikiTagWhitespaceBlocksEntitiesAndInlinePunctuation(t *testing.T) {
	assertWikiTags(t, `
		<table><tr><th>Tag</th><th>Description</th></tr>
		<tr><td> Future&nbsp; Tag
		</td><td><p>Use <code>this</code>, then<br>that.</p><p>
		Rock &amp; roll.</p><ul><li>One</li><li>Two</li></ul></td></tr></table>`,
		[]types.Tag{{Name: "Future Tag", Description: "Use this, then that. Rock & roll. One Two"}})
}

func TestWikiTagOptionalTrailingCellsCanBeOmitted(t *testing.T) {
	assertWikiTags(t, `
		<table><tr><th>Tag</th><th>Description</th><th>Specific Types</th></tr>
		<tr><td>Canine</td><td>A dog-like character.</td></tr></table>
		<table><tr><th>Tag</th><th>Example</th></tr>
		<tr><td>Future Franchise</td></tr></table>`,
		[]types.Tag{
			{Name: "Canine", Description: "A dog-like character."},
			{Name: "Future Franchise", Description: ""},
		})
}

func TestWikiTagSectionHeadingInsideTablePreservesColumnMapping(t *testing.T) {
	assertWikiTags(t, `
		<table><tr><th>Tag</th><th>Description</th></tr>
		<tr><td>Animal Hybrid</td><td>Animal-like features.</td></tr>
		<tr><th colspan="2"><i>Specific Types</i></th></tr>
		<tr><td>Canine</td><td>A dog-like character.</td></tr></table>`,
		[]types.Tag{
			{Name: "Animal Hybrid", Description: "Animal-like features."},
			{Name: "Canine", Description: "A dog-like character."},
		})
}

func TestWikiTagDuplicatesKeepFirstNonemptyDescription(t *testing.T) {
	assertWikiTags(t, `
		<table><tr><th>Tag</th><th>Example</th></tr>
		<tr><td>Repeated</td><td>Example only</td></tr></table>
		<table><tr><th>Tag</th><th>Description</th></tr>
		<tr><td>Repeated</td><td>Full definition.</td></tr>
		<tr><td>Repeated</td><td><code>Combination</code> + <code>Tag</code></td></tr>
		<tr><td>Repeated</td><td></td></tr></table>`,
		[]types.Tag{{Name: "Repeated", Description: "Full definition."}})
}

func TestWikiTagUnrelatedTablesHeadersAndMalformedRowsAreNotTags(t *testing.T) {
	assertWikiTags(t, `
		<table><tr><td>Navigation</td><td>Help</td></tr></table>
		<table><tr><th>Name</th><th>Description</th></tr>
		<tr><td>Unrelated</td><td>Not a tag table</td></tr></table>
		<table><tr><th>Tag</th><th>Description</th></tr>
		<tr><td></td><td>Empty name</td></tr>
		<tr><td>Missing cell</td></tr>
		<tr><td colspan="2">Section label</td><td>Invalid</td></tr>
		<tr><td>Extra</td><td>Invalid</td><td>cell</td></tr>
		<tr><th>Tag</th><th>Description</th></tr>
		<tr><th>Valid</th><td>Row header works too.</td></tr></table>`,
		[]types.Tag{{Name: "Valid", Description: "Row header works too."}})
}

// Rows with merged cells are skipped even when their cell count would fit the
// header columns.
func TestWikiTagMergedCellsAreSkipped(t *testing.T) {
	assertWikiTags(t, `
		<table><tr><th>Tag</th><th>Description</th><th>Example</th></tr>
		<tr><td>Wide</td><td colspan="2">Description and example</td></tr>
		<tr><td>Bare</td><td colspan>Empty colspan</td></tr>
		<tr><td rowspan="2">Tall</td><td>Spans two rows.</td><td></td></tr>
		<tr><td colspan="1">Single</td><td rowspan="1">One column is not merged.</td></tr></table>`,
		[]types.Tag{{Name: "Single", Description: "One column is not merged."}})
}

func TestWikiTagMissingTagTablesFail(t *testing.T) {
	for _, page := range []string{"", "<html>Unavailable</html>", "<table><tr><th>Tag</th></tr></table>"} {
		if tags, err := parseWikiTagsString(t, page); err == nil || !strings.Contains(err.Error(), "no tags found") {
			t.Errorf("%q: got %v, %v; want no tags found error", page, tags, err)
		}
	}
}

func TestCategoryTagsComeFromTheEmbeddedFile(t *testing.T) {
	if len(categoryTags) != 13 {
		t.Fatalf("got %d category tags, want 13", len(categoryTags))
	}
	for _, tag := range categoryTags {
		if tag.Name == "" || tag.Description == "" {
			t.Errorf("category tag without name or description: %+v", tag)
		}
	}
	want := types.Tag{Name: "Action", Description: "These games are active and action-packed, whether they involve running, jumping, shooting, or something else."}
	if categoryTags[0] != want {
		t.Errorf("first category tag %+v, want %+v", categoryTags[0], want)
	}
}

// testWikiTags returns a WikiTags that fetches url with a fake clock, and the
// hook that records its log entries.
func testWikiTags(url string) (*WikiTags, *time.Time, *logtest.Hook) {
	log, hook := logtest.NewNullLogger()
	w := NewWikiTags(log)
	w.url = url
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return now }
	return w, &now, hook
}

// wikiServer serves page for every request (or status when it is not 200),
// counting the requests.
func wikiServer(t *testing.T, status int, page string) (*httptest.Server, *atomic.Int32) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		rw.WriteHeader(status)
		rw.Write([]byte(page))
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func getTags(t *testing.T, w *WikiTags) []types.Tag {
	t.Helper()
	tags, err := w.GetTags(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return tags
}

// withCategoryTags returns the category tags followed by wiki.
func withCategoryTags(wiki ...types.Tag) []types.Tag {
	return append(append([]types.Tag{}, categoryTags...), wiki...)
}

func TestWikiTagsFetchParsesAndCaches(t *testing.T) {
	server, requests := wikiServer(t, http.StatusOK, `
		<table><tr><th>Tag</th><th>Description</th></tr>
		<tr><td>Newgrounds Obituary</td><td>See <a>Obituaries page</a>.</td></tr></table>`)
	w, now, _ := testWikiTags(server.URL)
	want := withCategoryTags(types.Tag{Name: "Newgrounds Obituary", Description: "See Obituaries page."})

	for range 2 {
		if got := getTags(t, w); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("%d requests within the cache time, want 1", n)
	}

	*now = now.Add(wikiTagsTTL)
	getTags(t, w)
	if n := requests.Load(); n != 2 {
		t.Errorf("%d requests after the cache expired, want 2", n)
	}
}

func TestWikiTagsFetchFailuresFallBackToLastGoodList(t *testing.T) {
	const goodPage = `
		<table><tr><th>Tag</th><th>Description</th></tr>
		<tr><td>Known</td><td>Fetched before the outage.</td></tr></table>`
	known := withCategoryTags(types.Tag{Name: "Known", Description: "Fetched before the outage."})

	// Each failure is a function that points w at a failing wiki.
	failures := map[string]func(t *testing.T, w *WikiTags){
		"http error": func(t *testing.T, w *WikiTags) {
			server, _ := wikiServer(t, http.StatusServiceUnavailable, goodPage)
			w.url = server.URL
		},
		"connection refused": func(t *testing.T, w *WikiTags) {
			server, _ := wikiServer(t, http.StatusOK, goodPage)
			server.Close()
			w.url = server.URL
		},
		"timeout": func(t *testing.T, w *WikiTags) {
			done := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-done }))
			t.Cleanup(server.Close)
			t.Cleanup(func() { close(done) })
			w.url = server.URL
			w.client.Timeout = 50 * time.Millisecond
		},
		"no tags found": func(t *testing.T, w *WikiTags) {
			server, _ := wikiServer(t, http.StatusOK, "<html>Unavailable</html>")
			w.url = server.URL
		},
	}
	for name, fail := range failures {
		t.Run(name, func(t *testing.T) {
			good, _ := wikiServer(t, http.StatusOK, goodPage)
			w, now, logs := testWikiTags(good.URL)

			// Before any success there are no wiki tags.
			fail(t, w)
			if got := getTags(t, w); !reflect.DeepEqual(got, withCategoryTags()) {
				t.Errorf("got %+v, want only the category tags", got)
			}

			*now = now.Add(wikiTagsTTL)
			w.url = good.URL
			if got := getTags(t, w); !reflect.DeepEqual(got, known) {
				t.Errorf("got %+v, want %+v", got, known)
			}

			*now = now.Add(wikiTagsTTL)
			fail(t, w)
			logs.Reset()
			if got := getTags(t, w); !reflect.DeepEqual(got, known) {
				t.Errorf("got %+v, want the last good list %+v", got, known)
			}
			if entry := logs.LastEntry(); entry == nil || entry.Level != logrus.WarnLevel {
				t.Errorf("failure was not logged as a warning: %+v", entry)
			}

			// The fallback is cached, so the outage is not retried on every
			// call: a recovered wiki is not asked again within the cache time.
			recovered, _ := wikiServer(t, http.StatusOK, `<table><tr><th>Tag</th></tr><tr><td>New</td></tr></table>`)
			w.url = recovered.URL
			if got := getTags(t, w); !reflect.DeepEqual(got, known) {
				t.Errorf("got %+v, want the cached last good list %+v", got, known)
			}
		})
	}
}

func TestWikiTagsFetchInBackground(t *testing.T) {
	var requests atomic.Int32
	fetched := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		rw.Write([]byte(`<table><tr><th>Tag</th><th>Description</th></tr>
			<tr><td>Known</td><td>Fetched on startup.</td></tr></table>`))
		fetched <- struct{}{}
	}))
	t.Cleanup(server.Close)
	w, _, _ := testWikiTags(server.URL)

	w.FetchInBackground()
	<-fetched
	want := withCategoryTags(types.Tag{Name: "Known", Description: "Fetched on startup."})
	if got := getTags(t, w); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("%d requests, want only the startup fetch", n)
	}
}
