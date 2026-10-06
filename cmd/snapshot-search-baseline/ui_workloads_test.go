package main

import (
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/gorilla/schema"
)

// Keep the benchmark tied to the UI: reconstruct each button's checked form
// controls from the JS and HTML, then decode them exactly like the HTTP handler.
func TestUIPresetsMatchActualButtons(t *testing.T) {
	js, err := os.ReadFile("../../static/js.js")
	if err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile("../../templates/submission-filter.gohtml")
	if err != nil {
		t.Fatal(err)
	}
	attr := regexp.MustCompile(`\b(id|name|value)="([^"]*)"`)
	inputs := map[string][2]string{}
	for _, input := range regexp.MustCompile(`(?s)<input\b[^>]*>`).FindAllString(string(html), -1) {
		attrs := map[string]string{}
		for _, m := range attr.FindAllStringSubmatch(input, -1) {
			attrs[m[1]] = m[2]
		}
		if attrs["id"] != "" {
			inputs[attrs["id"]] = [2]string{attrs["name"], attrs["value"]}
		}
	}
	functions := map[string]string{
		"ui-preset-ready-testing":           "filterReadyForTesting",
		"ui-preset-ready-verification":      "filterReadyForVerification",
		"ui-preset-ready-fp":                "filterReadyForFlashpoint",
		"ui-preset-me-testing":              "filterAssignedToMeForTesting",
		"ui-preset-me-verification":         "filterAssignedToMeForVerification",
		"ui-preset-me-testing-changes":      "filterIHaveRequestedChangesAfterTesting",
		"ui-preset-me-verification-changes": "filterIHaveRequestedChangesVerification",
	}
	members := map[string]int64{"testing": 101, "verification": 102, "testing-changes": 103, "verification-changes": 104}
	for _, w := range buildUIWorkloads(200, "uploader", members) {
		fn, ok := functions[w.Name]
		if !ok {
			continue
		}
		t.Run(w.Name, func(t *testing.T) {
			body := regexp.MustCompile(`(?s)function ` + fn + `\(\) \{(.*?)\n\}`).FindSubmatch(js)
			if body == nil {
				t.Fatalf("missing UI function %s", fn)
			}
			values := url.Values{}
			for _, m := range regexp.MustCompile(`getElementById\("([^"]+)"\)\.checked = true`).FindAllSubmatch(body[1], -1) {
				control, ok := inputs[string(m[1])]
				if !ok {
					t.Fatalf("missing form control %s", m[1])
				}
				values.Add(control[0], control[1])
			}
			if strings.Contains(string(body[1]), `getElementsByClassName("bot-action-approve")`) {
				values.Add("bot-action", "approve")
			}
			var got types.SubmissionsFilter
			if err := schema.NewDecoder().Decode(&got, values); err != nil {
				t.Fatal(err)
			}
			if err := got.Validate(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, w.Filter) {
				t.Fatalf("UI form differs from benchmark: form=%+v benchmark=%+v", got, w.Filter)
			}
		})
	}
}

func TestParseVariants(t *testing.T) {
	for _, value := range []string{"original", "rebuilt", "original,rebuilt", "rebuilt,original"} {
		if _, err := parseVariants(value); err != nil {
			t.Errorf("%s: %v", value, err)
		}
	}
	for _, value := range []string{"", "rebuilt,rebuilt", "original,", "original, rebuilt", "missing"} {
		if _, err := parseVariants(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}
