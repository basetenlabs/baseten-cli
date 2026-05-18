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

// TestE2EOrgAPIKey exercises `org api-key create/list/delete` against a real
// management API. Skips when the e2e env vars are absent.
func TestE2EOrgAPIKey(t *testing.T) {
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")
	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())

	keyName := fmt.Sprintf("cli-e2e-apikey-%s", randomSuffix(t))
	// Cleanup runs only if the test failed before its own delete step.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, errOut, err := cliCtx(t, ctx, "org", "api-key", "delete", "--name", keyName); err != nil {
			t.Logf("cleanup delete failed (may already be gone): %v\nstderr: %s", err, errOut)
		}
	})

	t.Run("Create", func(t *testing.T) {
		createOut := mustCLI(t, "org", "api-key", "create",
			"--type", "personal", "--name", keyName, "--output", "json")
		var resp struct {
			ApiKey string `json:"api_key"`
		}
		require.NoError(t, json.Unmarshal([]byte(createOut), &resp))
		require.NotEmpty(t, resp.ApiKey, "create response missing api_key")
	})

	t.Run("List", func(t *testing.T) {
		listOut := mustCLI(t, "org", "api-key", "list", "--output", "json")
		var resp struct {
			Keys []struct {
				Name   *string `json:"name,omitempty"`
				Prefix string  `json:"prefix"`
			} `json:"keys"`
		}
		require.NoError(t, json.Unmarshal([]byte(listOut), &resp))
		found := false
		for _, k := range resp.Keys {
			if k.Name != nil && *k.Name == keyName {
				found = true
				require.NotEmpty(t, k.Prefix)
				break
			}
		}
		require.True(t, found, "api key %q missing from list", keyName)
	})

	t.Run("Delete", func(t *testing.T) {
		mustCLI(t, "org", "api-key", "delete", "--name", keyName)

		listOut := mustCLI(t, "org", "api-key", "list", "--output", "json")
		var resp struct {
			Keys []struct {
				Name *string `json:"name,omitempty"`
			} `json:"keys"`
		}
		require.NoError(t, json.Unmarshal([]byte(listOut), &resp))
		for _, k := range resp.Keys {
			if k.Name != nil {
				require.NotEqual(t, keyName, *k.Name, "api key still present after delete")
			}
		}
	})
}
