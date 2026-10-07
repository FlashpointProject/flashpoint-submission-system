package integration_tests

import (
	"context"
	"github.com/stretchr/testify/require"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestUploadJobBrowserRetryAndAttemptIdentity(t *testing.T) {
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "node", filepath.Join(root, "integration_tests/browser/upload-jobs.cjs")).CombinedOutput()
	require.NoError(t, err, string(output))
}
