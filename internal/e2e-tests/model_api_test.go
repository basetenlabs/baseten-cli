//go:build e2e

package e2etests

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// e2eModelAPI is a Model API that is always available to the e2e workspace.
const e2eModelAPI = "labs/standard-testing"

// TestE2EModelAPI exercises the model-api commands against a real backend.
func TestE2EModelAPI(t *testing.T) {
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")
	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())

	t.Run("Predict", func(t *testing.T) {
		out := mustCLI(t, "model-api", "predict", "--model", e2eModelAPI, "--content", "hi")
		require.NotEmpty(t, strings.TrimSpace(out), "predict returned no reply")
	})

	// Intentionally asserts nothing about the Predict above; this is wholly
	// independent, just a sanity check that the command works and the shape is
	// right.
	t.Run("Usage", func(t *testing.T) {
		out := mustCLI(t, "model-api", "usage", "--since", "1h", "--bucket-width", "1m",
			"--group-by", "model", "--group-by", "team", "--output", "json")
		var buckets []struct {
			StartTime time.Time `json:"start_time"`
			EndTime   time.Time `json:"end_time"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &buckets))
		// Buckets are gapless, so a 1h window always returns buckets even with no usage.
		require.NotEmpty(t, buckets)
		for _, b := range buckets {
			require.True(t, b.StartTime.Before(b.EndTime), "bucket start %v not before end %v", b.StartTime, b.EndTime)
		}
	})

	// Intentionally asserts nothing about the Predict above; this is wholly
	// independent, just a sanity check that the command works and the shape is
	// right.
	t.Run("Metrics", func(t *testing.T) {
		type metricsResp struct {
			Mode string `json:"mode"`
		}
		for _, mode := range []string{"current", "series"} {
			args := []string{"model-api", "metrics", "--model", e2eModelAPI, "--mode", mode, "--output", "json"}
			if mode != "current" {
				args = append(args, "--since", "1h")
			}
			var resp metricsResp
			require.NoError(t, json.Unmarshal([]byte(mustCLI(t, args...)), &resp))
			require.Equal(t, strings.ToUpper(mode), resp.Mode)
		}
	})
}
