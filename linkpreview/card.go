// Package linkpreview renders the deliberately small, public summary shared with
// link crawlers. Never put comments, file paths, downloads or review reasons here.
package linkpreview

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
)

const SiteName = "Flashpoint's Fantastic Submission System"

type Field struct{ Label, Value string }
type Status struct {
	Label, Value, Color string
	Dot                 bool
}
type Card struct {
	Title, Kind, Path string
	Rows              [][]Field
	Statuses          []Status
}

func value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func capital(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}

func Submission(s *types.ExtendedSubmission) *Card {
	if s == nil || s.SubmissionID <= 0 || s.FileID <= 0 || s.IsFrozen {
		return nil
	}
	title := value(s.CurationTitle)
	if strings.TrimSpace(title) == "" {
		title = fmt.Sprintf("Submission %d", s.SubmissionID)
	}
	kind := "New Submission"
	if s.GameExists {
		kind = "Content Change"
	}
	extreme := "No"
	if value(s.CurationExtreme) == "Yes" {
		extreme = "Yes"
	}
	bot := "#b8b8b8"
	switch s.BotAction {
	case "approve":
		bot = "#13842d"
	case "request-changes":
		bot = "#af0e0e"
	}
	added := "#b8b8b8"
	for _, action := range s.DistinctActions {
		if action == "reject" {
			added = "#141414"
		}
	}
	for _, action := range s.DistinctActions {
		if action == "mark-added" {
			added = "#8011a7"
		}
	}
	return &Card{
		Title: title, Kind: kind, Path: fmt.Sprintf("/web/submission/%d", s.SubmissionID),
		Statuses: []Status{
			{"Bot", "", bot, true},
			{"AST", strconv.Itoa(len(s.AssignedTestingUserIDs)), "#11418e", false},
			{"RC", strconv.Itoa(len(s.RequestedChangesUserIDs)), "#af0e0e", false},
			{"AP", strconv.Itoa(len(s.ApprovedUserIDs)), "#13842d", false},
			{"ASV", strconv.Itoa(len(s.AssignedVerificationUserIDs)), "#744721", false},
			{"VE", strconv.Itoa(len(s.VerifiedUserIDs)), "#af6414", false},
			{"FP", "", added, true}, {"F", "", "#b8b8b8", true},
		},
		Rows: [][]Field{
			{{"Platform", value(s.CurationPlatform)}, {"Library", capital(value(s.CurationLibrary))}},
			{{"Level", capital(s.SubmissionLevel)}, {"18+", extreme}},
			{{"Uploaded by", s.SubmitterUsername}, {"Updated by", s.UpdaterUsername}},
			{{"Size", utils.SizeToString(s.Size)}},
			{{"Filename", s.OriginalFilename}},
			{{"Uploaded at", s.UploadedAt.UTC().Format("2006-01-02 15:04:05 UTC")}},
			{{"Updated at", s.UpdatedAt.UTC().Format("2006-01-02 15:04:05 UTC")}},
		},
	}
}

func Tag(t *types.Tag, games int64) *Card {
	if t == nil || t.Deleted || t.ID <= 0 {
		return nil
	}
	return &Card{Title: t.Name, Kind: "Tag", Path: fmt.Sprintf("/web/tag/%d", t.ID), Rows: [][]Field{
		{{"Category", t.Category}}, {{"Description", t.Description}},
		{{"Aliases", value(t.Aliases)}}, {{"Games using tag", strconv.FormatInt(games, 10)}},
	}}
}

// Text bounds arbitrary metadata before layout or inclusion in HTML attributes.
func Text(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

func (c *Card) Description() string {
	parts := []string{c.Kind}
	for _, row := range c.Rows {
		for _, f := range row {
			parts = append(parts, f.Label+": "+Text(f.Value, 100))
		}
	}
	return Text(strings.Join(parts, " · "), 300)
}
