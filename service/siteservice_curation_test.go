package service

import (
	"encoding/base64"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/stretchr/testify/require"
)

func TestTakeFreezerWarning(t *testing.T) {
	freezer := "Release date is recent. Curation should be frozen."
	rest, freeze := takeFreezerWarning([]string{"a", freezer, "b"})
	require.Equal(t, []string{"a", "b"}, rest)
	require.True(t, freeze)

	rest, freeze = takeFreezerWarning([]string{"a", "b"})
	require.Equal(t, []string{"a", "b"}, rest)
	require.False(t, freeze)

	rest, freeze = takeFreezerWarning(nil)
	require.Empty(t, rest)
	require.False(t, freeze)
}

// Notes are listed with ℹ️ and don't keep the bot from approving.
func TestValidatorCommentNotes(t *testing.T) {
	s := &SiteService{clock: &RealClock{}}
	notes := []string{"The archive is 44.3 GB.", "The curation is 103.6 GB uncompressed."}

	c := s.convertValidatorResponseToComment(&types.ValidatorResponse{CurationNotes: notes}, false)
	require.Equal(t, constants.ActionApprove, c.Action)
	require.Equal(t, "Looks good to me! 🤖\n\nℹ️ The archive is 44.3 GB.\nℹ️ The curation is 103.6 GB uncompressed.", *c.Message)

	c = s.convertValidatorResponseToComment(&types.ValidatorResponse{CurationNotes: notes[:1]}, true)
	require.Equal(t, constants.ActionApprove, c.Action)
	require.Equal(t, "Looks good to me! 🤖\n\nℹ️ The archive is 44.3 GB.\n\n This submission will be automatically frozen after validation.", *c.Message)

	c = s.convertValidatorResponseToComment(&types.ValidatorResponse{CurationWarnings: []string{"Tag `X` is not a known tag."}, CurationNotes: notes[:1]}, false)
	require.Equal(t, constants.ActionRequestChanges, c.Action)
	require.Equal(t, "Your curation might have some problems:\n⚠️ Tag `X` is not a known tag.\nℹ️ The archive is 44.3 GB.\n", *c.Message)

	c = s.convertValidatorResponseToComment(&types.ValidatorResponse{}, false)
	require.Equal(t, "Looks good to me! 🤖", *c.Message)
}

func TestWithoutIdenticalLaunchCommandError(t *testing.T) {
	errs := []string{"Identical launch command already present in the master database.", "Missing title."}
	require.Equal(t, []string{"Missing title."}, withoutIdenticalLaunchCommandError(errs))
}

func TestCurationAdditionalApps(t *testing.T) {
	other := &types.CurationAdditionalApp{Heading: utils.StrPtr("Other"), ApplicationPath: utils.StrPtr("o.exe"), LaunchCommand: utils.StrPtr("-o")}
	meta := &types.CurationMeta{
		Extras:         utils.StrPtr("extras"),
		Message:        utils.StrPtr("hello"),
		AdditionalApps: []*types.CurationAdditionalApp{other},
	}
	apps := curationAdditionalApps(meta)
	require.Len(t, apps, 3)
	require.Equal(t, "Extras", *apps[0].Heading)
	require.Equal(t, ":extras:", *apps[0].ApplicationPath)
	require.Equal(t, "extras", *apps[0].LaunchCommand)
	require.Equal(t, "Message", *apps[1].Heading)
	require.Equal(t, ":message:", *apps[1].ApplicationPath)
	require.Equal(t, "hello", *apps[1].LaunchCommand)
	require.Same(t, other, apps[2])

	require.Empty(t, curationAdditionalApps(&types.CurationMeta{}))
}

func TestCurationLogoAndScreenshot(t *testing.T) {
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

	// Picked by type, not by position.
	logo, screenshot, err := curationLogoAndScreenshot([]types.ValidatorResponseImage{
		{Type: "screenshot", Data: enc("ss")},
		{Type: "logo", Data: enc("logo")},
	})
	require.NoError(t, err)
	require.Equal(t, []byte("logo"), logo)
	require.Equal(t, []byte("ss"), screenshot)

	_, _, err = curationLogoAndScreenshot([]types.ValidatorResponseImage{{Type: "screenshot", Data: enc("ss")}})
	require.ErrorContains(t, err, "logo")

	_, _, err = curationLogoAndScreenshot([]types.ValidatorResponseImage{
		{Type: "logo", Data: "not base64!"},
		{Type: "screenshot", Data: enc("ss")},
	})
	require.Error(t, err)
}
