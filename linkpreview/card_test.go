package linkpreview

import (
	"bytes"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/stretchr/testify/require"
)

func TestSubmissionCardPublicFieldsAndStates(t *testing.T) {
	title := "Fusion Rocket"
	platform := "Flash"
	library := "arcade"
	s := &types.ExtendedSubmission{SubmissionID: 1, FileID: 2, CurationTitle: &title, CurationPlatform: &platform, CurationLibrary: &library, SubmissionLevel: "trial", SubmitterUsername: "Original uploader", UpdaterUsername: "Latest reviewer", OriginalFilename: "Fusion Rocket.7z", CurrentFilename: "PRIVATE-path", GameExists: true, BotAction: "approve", ApprovedUserIDs: []int64{1}, VerifiedUserIDs: []int64{2}, DistinctActions: []string{"reject", "mark-added"}, UpdatedAt: time.Date(2026, 10, 7, 15, 4, 36, 0, time.FixedZone("CEST", 7200))}
	card := Submission(s)
	require.Equal(t, "Content Change", card.Kind)
	require.Equal(t, "Original uploader", card.Rows[2][0].Value)
	require.Equal(t, "Latest reviewer", card.Rows[2][1].Value)
	require.Equal(t, "2026-10-07 13:04:36 UTC", card.Rows[6][0].Value)
	require.Equal(t, "1", card.Statuses[3].Value)
	require.Equal(t, "#8011a7", card.Statuses[6].Color)
	require.NotContains(t, card.Description(), "PRIVATE")
	for _, row := range card.Rows {
		for _, field := range row {
			require.NotEqual(t, "Frozen", field.Label)
		}
	}
	s.IsFrozen = true
	require.Nil(t, Submission(s))
	s.IsFrozen = false
	s.FileID = 0
	require.Nil(t, Submission(s))
	require.Nil(t, Tag(&types.Tag{ID: 1, Deleted: true}, 0))
}
func TestPNGLayoutBoundsAndColors(t *testing.T) {
	title := "Fusion Rocket"
	s := &types.ExtendedSubmission{SubmissionID: 1, FileID: 2, CurationTitle: &title, ApprovedUserIDs: []int64{1}}
	for _, card := range []*Card{Submission(s), Tag(&types.Tag{ID: 2, Name: strings.Repeat("Long title & Café Ж ", 100), Description: strings.Repeat("longword", 200)}, 1234)} {
		data, err := PNG(card)
		require.NoError(t, err)
		im, err := png.Decode(bytes.NewReader(data))
		require.NoError(t, err)
		w, h := Dimensions(card)
		require.Equal(t, w, im.Bounds().Dx())
		require.Equal(t, h, im.Bounds().Dy())
		require.Less(t, h, 2000)
		f := newFonts()
		l := makeLayout(card, f)
		for _, r := range l.rows {
			for _, cell := range r.cells {
				if card.Kind == "Tag" && cell.label {
					require.Len(t, cell.lines, 1, "tag labels, including Games using tag, fit on one line")
				}
				face := f.regular
				if cell.label {
					face = f.bold
				}
				for _, line := range cell.lines {
					require.LessOrEqual(t, measure(face, line), cell.width-14)
				}
			}
		}
		if len(card.Statuses) > 0 {
			require.Equal(t, hex("#13842d"), color.RGBAModel.Convert(im.At((padding+3*tableWidth/8+5)*scale, (l.headerBottom+5)*scale)))
		}
		f.close()
	}
}

func TestPNGConcurrentRendering(t *testing.T) {
	card := Tag(&types.Tag{ID: 2, Name: "Platformer", Category: "Genre", Description: "A shared renderer"}, 1234)
	want, err := PNG(card)
	require.NoError(t, err)
	for i := 0; i < 8; i++ {
		t.Run("render", func(t *testing.T) {
			t.Parallel()
			got, err := PNG(card)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}
