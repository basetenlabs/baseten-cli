package cmd_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	public "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/zalando/go-keyring"
)

func routesAuthHarness(t *testing.T) (*CommandHarness, *MockManagementAPI) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-a"})
	api.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []map[string]string{{"id": "team-a", "name": "Engineering"}, {"id": "team-b", "name": "Research"}}})
	api.SetRoute("POST", "/v1/api_keys", 200, map[string]string{"api_key": "created-routes-secret"})
	return h, api
}

func Test_Harness_Setup_CredentialsCreateAndReuse(t *testing.T) {
	h, api := productionHarness(t)
	var headers []string
	api.SetRouteFunc("POST", "/v1/api_keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		headers = append(headers, r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]string{"api_key": "created-routes-secret"})
	})
	args := []string{"--team", "team-a", "--key-name", "laptop", "--output", "json"}
	h.Require.NoError(executeHarnessSetup(t, h, args...))
	h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "created-routes-secret")
	call := api.FindCall("POST", "/v1/api_keys")
	h.Require.NotNil(call)
	h.Require.Equal(map[string]any{"type": "ROUTES", "name": "laptop", "team_id": "team-a"}, call.BodyJSON(t))
	h.Require.Equal([]string{"Bearer test-key"}, headers)
	store := configDirStore(t)
	saved, err := store.GetRoutesKey(auth.RoutesKeyScope{ManagementURL: api.URL, UserID: "user-a", TeamID: "team-a", Name: "laptop"})
	h.Require.NoError(err)
	h.Require.Equal("created-routes-secret", saved)
	h.Require.NoError(executeHarnessSetup(t, h, args...))
	h.Require.Len(headers, 1)
	h.Require.NoError(filepath.WalkDir(os.Getenv("BASETEN_CONFIG_DIR"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		h.Require.NotContains(string(data), "created-routes-secret")
		return err
	}))
}

func Test_Harness_Setup_CredentialsTeamNameReusesIDKey(t *testing.T) {
	h, api := productionHarness(t)
	h.Require.NoError(executeHarnessSetup(t, h, "--team", "Engineering", "--key-name", "laptop"))
	h.Require.Equal("team-a", api.FindCall("POST", "/v1/api_keys").BodyJSON(t)["team_id"])
	h.Require.NoError(executeHarnessSetup(t, h, "--team", "team-a", "--key-name", "laptop", "--output", "json"))
	count := 0
	for _, call := range api.Calls() {
		if call.Method == "POST" {
			count++
		}
	}
	h.Require.Equal(1, count)
}

func Test_Harness_Setup_CredentialsDryRunDoesNotCreate(t *testing.T) {
	h, api := productionHarness(t)
	h.Require.NoError(executeHarnessSetup(t, h, "--team", "team-a", "--key-name", "preview", "--output", "json", "--dry-run"))
	h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
	entries, err := os.ReadDir(os.Getenv("BASETEN_CONFIG_DIR"))
	h.Require.NoError(err)
	h.Require.Empty(entries)
}

func Test_Harness_Setup_CredentialsAutomaticTeamSelection(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			h, api := productionHarness(t)
			teams := []map[string]string{}
			for i := 0; i < count; i++ {
				teams = append(teams, map[string]string{"id": fmt.Sprintf("team-%d", i), "name": fmt.Sprintf("Team %d", i)})
			}
			api.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": teams})
			err := executeHarnessSetup(t, h, "--key-name", "automatic", "--output", "json")
			if count == 1 {
				h.Require.NoError(err)
				h.Require.Equal("team-0", api.FindCall("POST", "/v1/api_keys").BodyJSON(t)["team_id"])
			} else {
				h.Require.Error(err)
				h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
				if count == 0 {
					h.Require.Contains(h.Stderr.String(), "no teams")
				} else {
					h.Require.Contains(h.Stderr.String(), "pass --team")
					h.Require.Contains(h.Stderr.String(), "Team 0 (team-0)")
					h.Require.Contains(h.Stderr.String(), "Team 1 (team-1)")
					h.Require.Contains(h.Stderr.String(), "baseten harness setup --team team-0")
					h.Require.Nil(api.FindCall("GET", "/v1/routes"))
				}
			}
		})
	}
}

func Test_Harness_Setup_CredentialsProfileAndIdentityIsolation(t *testing.T) {
	h, api := productionHarness(t)
	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile("alice-routes-test", "https://app.example.com", "alice-login", false, nil))
	args := []string{"--profile", "alice-routes-test", "--team", "team-a", "--key-name", "laptop"}
	var headers []string
	api.SetRouteFunc("POST", "/v1/api_keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		headers = append(headers, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"api_key":"routes-key"}`))
	})
	h.Require.NoError(executeHarnessSetup(t, h, args...))
	h.Require.Equal([]string{"Bearer alice-login"}, headers)
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-b"})
	h.Require.NoError(executeHarnessSetup(t, h, args...))
	h.Require.Len(headers, 2)
	h.Require.NoError(executeHarnessSetup(t, h, "--profile", "alice-routes-test", "--team", "team-b", "--key-name", "laptop"))
	h.Require.Len(headers, 3)
	profile, ok := store.GetProfile("alice-routes-test")
	h.Require.True(ok)
	h.Require.Equal(auth.AuthTypeAPIKey, profile.AuthType)
	original, err := store.GetAPIKey("alice-routes-test")
	h.Require.NoError(err)
	h.Require.Equal("alice-login", original)
}

func Test_Harness_Setup_CredentialsFailureRedactionAndRetry(t *testing.T) {
	for _, status := range []int{403, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h, api := productionHarness(t)
			api.SetRoute("POST", "/v1/api_keys", status, map[string]string{"api_key": "must-not-leak"})
			args := []string{"--team", "team-a", "--key-name", "failed"}
			h.Require.Error(executeHarnessSetup(t, h, args...))
			h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "must-not-leak")
			api.SetRoute("POST", "/v1/api_keys", 200, map[string]string{"api_key": "valid-key"})
			if status == 403 {
				h.Require.NoError(executeHarnessSetup(t, h, args...))
			} else {
				h.Require.Error(executeHarnessSetup(t, h, args...))
				h.Require.Contains(h.Stderr.String(), "unresolved")
				count := 0
				for _, call := range api.Calls() {
					if call.Method == "POST" {
						count++
					}
				}
				h.Require.Equal(1, count)
			}
		})
	}
}

func Test_Harness_Setup_CredentialsKeyringUnavailable(t *testing.T) {
	h, api := productionHarness(t)
	keyring.MockInitWithError(errors.New("keyring failure with sensitive details"))
	t.Cleanup(keyring.MockInit)
	h.Require.Error(executeHarnessSetup(t, h, "--team", "team-a"))
	h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
	h.Require.NotContains(h.Stderr.String(), "sensitive details")
}

func Test_Harness_Setup_CredentialsOAuthProfile(t *testing.T) {
	h, api := productionHarness(t)
	store := configDirStore(t)
	h.Require.NoError(store.SetOAuthProfile("oauth-routes-test", "https://app.example.com", auth.OAuthCredential{AccessToken: "oauth-access", RefreshToken: "oauth-refresh", Expiry: time.Now().Add(time.Hour)}, false, nil))
	var authorization string
	api.SetRouteFunc("POST", "/v1/api_keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"api_key":"routes-from-oauth"}`))
	})
	h.Require.NoError(executeHarnessSetup(t, h, "--profile", "oauth-routes-test", "--team", "team-a"))
	h.Require.Equal("Bearer oauth-access", authorization)
	h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "routes-from-oauth")
}

func Test_Harness_Setup_CredentialsReportsAPIValidationMessage(t *testing.T) {
	h, api := productionHarness(t)
	api.SetRoute("POST", "/v1/api_keys", 400, map[string]any{
		"code":    "VALIDATION_ERROR",
		"message": "routes keys require membership in the selected team.",
		"details": map[string]string{"api_key": "secret-in-details"},
		"api_key": "secret-in-body",
	})
	h.Require.Error(executeHarnessSetup(t, h, "--team", "Engineering"))
	h.Require.Contains(h.Stderr.String(), "HTTP 400")
	h.Require.Contains(h.Stderr.String(), "routes keys require membership in the selected team.")
	h.Require.NotContains(h.Stderr.String()+h.Stdout.String(), "secret-in-")
	api.SetRoute("POST", "/v1/api_keys", 400, map[string]string{"message": "Invalid credential test-key"})
	h.Require.Error(executeHarnessSetup(t, h, "--team", "Engineering"))
	h.Require.Contains(h.Stderr.String(), "[REDACTED]")
	h.Require.NotContains(h.Stderr.String(), "test-key")
}

func Test_Harness_Setup_CredentialsInvalidNameRejectedBeforeAPI(t *testing.T) {
	h, api := productionHarness(t)
	h.Require.Error(executeHarnessSetup(t, h, "--team", "Engineering", "--key-name", "My.Laptop"))
	h.Require.Contains(h.Stderr.String(), "lowercase letters, numbers, and hyphens")
	h.Require.Empty(api.Calls())
}

func Test_Harness_Setup_CredentialsStandardHTTPExitCodes(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   public.ExitCode
		kind   string
	}{
		{401, public.ExitAuth, "ErrAuth"}, {403, public.ExitAuth, "ErrAuth"},
		{404, public.ExitNotFound, "ErrNotFound"}, {400, public.ExitValidation, "ErrValidation"},
		{422, public.ExitValidation, "ErrValidation"}, {500, public.ExitServer, "ErrServer"},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			h, api := productionHarness(t)
			api.SetRoute("POST", "/v1/api_keys", tc.status, map[string]string{"message": "failure test-key"})
			h.Require.Error(executeHarnessSetup(t, h, "--team", "team-a", "--key-name", fmt.Sprintf("status-%d", tc.status), "--output", "json"))
			h.Require.Equal(int(tc.code), h.ExitCode)
			var envelope public.JSONErrorEnvelope
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &envelope))
			h.Require.Equal(tc.kind, envelope.Error.Type)
			h.Require.Equal(tc.code, envelope.Error.ExitCode)
			h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "test-key")
		})
	}
}

// Exercise credentials through the public setup path using isolated config files.
func executeHarnessSetup(t *testing.T, h *CommandHarness, flags ...string) error {
	t.Helper()
	args := []string{"harness", "setup", "--harness", "claude-code", "--model", "acme/primary", "--config", filepath.Join(t.TempDir(), "settings.json"), "--yes"}
	return h.Execute(append(args, flags...)...)
}

func Test_Harness_Setup_MultipleTeamsTextRequiresFlag(t *testing.T) {
	h, api := productionHarness(t)
	h.Require.Error(executeHarnessSetup(t, h))
	h.Require.Equal(int(public.ExitUsage), h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "Engineering (team-a)")
	h.Require.Contains(h.Stderr.String(), "Research (team-b)")
	h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
	h.Require.Nil(api.FindCall("GET", "/v1/routes"))
}

func Test_Harness_Setup_TeamSwitchReusesPreviousKey(t *testing.T) {
	h, api := productionHarness(t)
	minted := []string{}
	api.SetRouteFunc("POST", "/v1/api_keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]string
		h.Require.NoError(json.NewDecoder(r.Body).Decode(&body))
		minted = append(minted, body["team_id"])
		json.NewEncoder(w).Encode(map[string]string{"api_key": "secret-" + body["team_id"]})
	})
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, team := range []string{"team-a", "team-b", "team-a"} {
		h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--model", "acme/primary", "--config", path, "--team", team, "--key-name", "laptop", "--yes"))
		data, err := os.ReadFile(path)
		h.Require.NoError(err)
		h.Require.Contains(string(data), "secret-"+team)
		h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "secret-")
	}
	h.Require.Equal([]string{"team-a", "team-b"}, minted)
	h.Require.Contains(h.Stdout.String(), "Reuse saved key")
	for _, team := range []string{"team-a", "team-b"} {
		key, err := configDirStore(t).GetRoutesKey(auth.RoutesKeyScope{ManagementURL: api.URL, UserID: "user-a", TeamID: team, Name: "laptop"})
		h.Require.NoError(err)
		h.Require.Equal("secret-"+team, key)
	}
}

func Test_Harness_Setup_SummaryTeamName(t *testing.T) {
	for _, team := range []string{"", "team-a", "Engineering"} {
		t.Run(team, func(t *testing.T) {
			h, api := productionHarness(t)
			api.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []map[string]string{{"id": "team-a", "name": "Engineering"}}})
			args := []string{"--dry-run"}
			if team != "" {
				args = append(args, "--team", team)
			}
			h.Require.NoError(executeHarnessSetup(t, h, args...))
			h.Require.Contains(h.Stdout.String(), "Team: Engineering")
			h.Require.NotContains(h.Stdout.String(), "\x1b[")
			lookups := 0
			for _, call := range api.Calls() {
				if call.Method == "GET" && call.Path == "/v1/teams" {
					lookups++
				}
			}
			h.Require.Equal(1, lookups)
		})
	}
}
