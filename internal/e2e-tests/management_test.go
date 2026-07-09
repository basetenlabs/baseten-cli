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

// TestE2EManagement exercises the management-API commands (api-key, secret,
// user, team, whoami) against a real backend. Skips when the e2e env vars are
// absent.
func TestE2EManagement(t *testing.T) {
	m := newManagement(t)
	t.Run("APIKey", m.APIKey)
	t.Run("Secret", m.Secret)
	t.Run("User", m.User)
	t.Run("Team", m.Team)
	t.Run("Whoami", m.Whoami)
}

// management gates on the e2e env vars and installs the credential into the
// environment so each sub-test can drive the CLI against a real backend.
type management struct{}

// newManagement runs the env-gate and configures the credential. Skips the
// whole suite when the e2e env vars are absent.
func newManagement(t *testing.T) *management {
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")
	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())
	return &management{}
}

// APIKey creates, lists, and deletes a personal API key.
func (m *management) APIKey(t *testing.T) {
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

	createOut := mustCLI(t, "org", "api-key", "create",
		"--type", "personal", "--name", keyName, "--output", "json")
	var created struct {
		ApiKey string `json:"api_key"`
	}
	require.NoError(t, json.Unmarshal([]byte(createOut), &created))
	require.NotEmpty(t, created.ApiKey, "create response missing api_key")

	listOut := mustCLI(t, "org", "api-key", "list", "--output", "json")
	var listed struct {
		Keys []struct {
			Name   *string `json:"name,omitempty"`
			Prefix string  `json:"prefix"`
		} `json:"keys"`
	}
	require.NoError(t, json.Unmarshal([]byte(listOut), &listed))
	found := false
	for _, k := range listed.Keys {
		if k.Name != nil && *k.Name == keyName {
			found = true
			require.NotEmpty(t, k.Prefix)
			break
		}
	}
	require.True(t, found, "api key %q missing from list", keyName)

	mustCLI(t, "org", "api-key", "delete", "--name", keyName)
	afterOut := mustCLI(t, "org", "api-key", "list", "--output", "json")
	var after struct {
		Keys []struct {
			Name *string `json:"name,omitempty"`
		} `json:"keys"`
	}
	require.NoError(t, json.Unmarshal([]byte(afterOut), &after))
	for _, k := range after.Keys {
		if k.Name != nil {
			require.NotEqual(t, keyName, *k.Name, "api key still present after delete")
		}
	}
}

// Secret sets, lists, and deletes a secret.
func (m *management) Secret(t *testing.T) {
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

	setOut := mustCLIStdin(t, "secret-value-1\n",
		"org", "secret", "set", "--name", secretName, "--output", "json")
	var set struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal([]byte(setOut), &set))
	require.Equal(t, secretName, set.Name)

	listOut := mustCLI(t, "org", "secret", "list", "--output", "json")
	var listed struct {
		Secrets []struct {
			Name string `json:"name"`
		} `json:"secrets"`
	}
	require.NoError(t, json.Unmarshal([]byte(listOut), &listed))
	found := false
	for _, s := range listed.Secrets {
		if s.Name == secretName {
			found = true
			break
		}
	}
	require.True(t, found, "secret %s missing from list", secretName)

	mustCLI(t, "org", "secret", "delete", "--name", secretName)
	afterOut := mustCLI(t, "org", "secret", "list", "--output", "json")
	var after struct {
		Secrets []struct {
			Name string `json:"name"`
		} `json:"secrets"`
	}
	require.NoError(t, json.Unmarshal([]byte(afterOut), &after))
	for _, s := range after.Secrets {
		require.NotEqual(t, secretName, s.Name, "secret still present after delete")
	}
}

// User lists workspace users and describes the caller by "me" and by ID.
func (m *management) User(t *testing.T) {
	meOut := mustCLI(t, "org", "user", "describe", "--user-id", "me", "--output", "json")
	var me struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
	}
	require.NoError(t, json.Unmarshal([]byte(meOut), &me))
	require.NotEmpty(t, me.UserID, "describe me missing user_id")

	listOut := mustCLI(t, "org", "user", "list", "--output", "json")
	var list struct {
		Items []struct {
			UserID string `json:"user_id"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(listOut), &list))
	found := false
	for _, u := range list.Items {
		if u.UserID == me.UserID {
			found = true
			break
		}
	}
	require.True(t, found, "caller %s missing from user list", me.UserID)

	// Describing by explicit ID returns the same user.
	byIDOut := mustCLI(t, "org", "user", "describe", "--user-id", me.UserID, "--output", "json")
	var byID struct {
		UserID string `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(byIDOut), &byID))
	require.Equal(t, me.UserID, byID.UserID)

	// Describing by email (server-side ?email= filter) resolves the same user.
	require.NotEmpty(t, me.Email, "describe me missing email")
	byEmailOut := mustCLI(t, "org", "user", "describe", "--user-email", me.Email, "--output", "json")
	var byEmail struct {
		UserID string `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(byEmailOut), &byEmail))
	require.Equal(t, me.UserID, byEmail.UserID)
}

// Team lists teams and describes the default team by ID.
func (m *management) Team(t *testing.T) {
	listOut := mustCLI(t, "org", "team", "list", "--output", "json")
	var list struct {
		Teams []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Default bool   `json:"default"`
		} `json:"teams"`
	}
	require.NoError(t, json.Unmarshal([]byte(listOut), &list))
	require.NotEmpty(t, list.Teams, "team list is empty")

	// Every org has a default team; describe it by ID.
	team := list.Teams[0]
	for _, tm := range list.Teams {
		if tm.Default {
			team = tm
			break
		}
	}
	descOut := mustCLI(t, "org", "team", "describe", "--team-id", team.ID, "--output", "json")
	var desc struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(descOut), &desc))
	require.Equal(t, team.ID, desc.ID)

	// Describing by name (server-side ?name= filter) resolves the same team.
	require.NotEmpty(t, team.Name, "team missing name")
	byNameOut := mustCLI(t, "org", "team", "describe", "--team-name", team.Name, "--output", "json")
	var byName struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(byNameOut), &byName))
	require.Equal(t, team.ID, byName.ID)
}

// Whoami resolves the authenticated user.
func (m *management) Whoami(t *testing.T) {
	out := mustCLI(t, "whoami", "--output", "json")
	var resp struct {
		UserID string `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &resp))
	require.NotEmpty(t, resp.UserID, "whoami missing user_id")
}
