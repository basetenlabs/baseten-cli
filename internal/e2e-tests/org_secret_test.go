//go:build e2e

package e2etests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestE2EOrgSecret exercises `org secret set/list/delete` against a real
// management API. Skips when the e2e env vars are absent.
func TestE2EOrgSecret(t *testing.T) {
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")
	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())

	secretName := fmt.Sprintf("cli-e2e-secret-%s", randomSuffix(t))
	// Cleanup runs only if the test failed before its own delete step.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, errOut, err := cliCtx(t, ctx, "org", "secret", "delete", "--name", secretName); err != nil {
			t.Logf("cleanup delete failed (may already be gone): %v\nstderr: %s", err, errOut)
		}
	})

	t.Run("Create", func(t *testing.T) {
		setOut := mustCLIStdin(t, "secret-value-1\n",
			"org", "secret", "set", "--name", secretName, "--output", "json")
		var resp struct {
			Name string `json:"name"`
		}
		require.NoError(t, json.Unmarshal([]byte(setOut), &resp))
		require.Equal(t, secretName, resp.Name)
	})

	t.Run("List", func(t *testing.T) {
		listOut := mustCLI(t, "org", "secret", "list", "--output", "json")
		var resp struct {
			Secrets []struct {
				Name string `json:"name"`
			} `json:"secrets"`
		}
		require.NoError(t, json.Unmarshal([]byte(listOut), &resp))
		found := false
		for _, s := range resp.Secrets {
			if s.Name == secretName {
				found = true
				break
			}
		}
		require.True(t, found, "secret %s missing from list", secretName)
	})

	t.Run("Delete", func(t *testing.T) {
		mustCLI(t, "org", "secret", "delete", "--name", secretName)

		listOut := mustCLI(t, "org", "secret", "list", "--output", "json")
		var resp struct {
			Secrets []struct {
				Name string `json:"name"`
			} `json:"secrets"`
		}
		require.NoError(t, json.Unmarshal([]byte(listOut), &resp))
		for _, s := range resp.Secrets {
			require.NotEqual(t, secretName, s.Name, "secret still present after delete")
		}
	})
}
