package cmd_test

import (
	"encoding/json"
	"testing"

	public "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
)

func Test_Route_APIKey_List(t *testing.T) {
	h, api := harnessAPI(t, "")
	h.Require.NoError(h.Execute("route", "api-key", "list"))
	for _, want := range []string{"NAME", "PREFIX", "LAST USED", "secret-team-a", "Engineering", "2026-09-01T00:00:00Z", "never"} {
		h.Require.Contains(h.Stdout.String(), want)
	}
	h.Require.Equal("ROUTES", api.FindCall("GET", "/v1/api_keys").Query().Get("type"))
	h.Require.Equal("true", api.FindCall("GET", "/v1/api_keys").Query().Get("created_by_me"))

	api.SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": []any{}})
	h.Require.NoError(h.Execute("route", "api-key", "list"))
	h.Require.Contains(h.Stderr.String(), "No routes API keys found.")
}

func Test_Route_APIKey_Delete(t *testing.T) {
	h, api := harnessAPI(t, "")
	api.SetRoute("DELETE", "/v1/api_keys/secret-team-a", 200, map[string]string{"prefix": "secret-team-a"})
	api.SetRoute("DELETE", "/v1/api_keys/secret-team-b", 200, map[string]string{"prefix": "secret-team-b"})

	h.Require.Error(h.Execute("route", "api-key", "delete"))
	h.Require.Equal(int(public.ExitUsage), h.ExitCode)
	h.Require.Error(h.Execute("route", "api-key", "delete", "--prefix", "secret-team-a"))
	h.Require.Contains(h.Stderr.String(), "pass --yes")
	h.Require.Error(h.Execute("route", "api-key", "delete", "--prefix", "unknown", "--yes"))
	h.Require.Contains(h.Stderr.String(), `no routes API key with prefix "unknown"`)
	h.Require.Nil(api.FindCall("DELETE", "/v1/api_keys/secret-team-a"))

	h.Require.NoError(h.Execute("route", "api-key", "delete", "--prefix", "secret-team-a", "--yes"))
	h.Require.Contains(h.Stderr.String(), "Deleted routes API key secret-team-a")
	h.Require.NotNil(api.FindCall("DELETE", "/v1/api_keys/secret-team-a"))
	h.Require.Nil(api.FindCall("DELETE", "/v1/api_keys/secret-team-b"))
}

func Test_Route_APIKey_DeleteAllReportsPartialFailure(t *testing.T) {
	h, api := harnessAPI(t, "")
	api.SetRoute("DELETE", "/v1/api_keys/secret-team-a", 500, map[string]string{"message": "unavailable"})
	api.SetRoute("DELETE", "/v1/api_keys/secret-team-b", 200, map[string]string{"prefix": "secret-team-b"})
	h.Require.Error(h.Execute("route", "api-key", "delete", "--all", "--yes", "--output", "json"))
	var result public.RouteAPIKeyDeleteResult
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Equal(public.RouteAPIKeyDeleteResult{Deleted: []string{"secret-team-b"}, Failed: []string{"secret-team-a"}}, result)
}

func Test_Harness_Setup_ReplacesDeletedKey(t *testing.T) {
	skipUnlessMacOS(t)
	h, api := fakeHarnessAPI(t)
	dir := t.TempDir()
	args := []string{"harness", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"}
	h.Require.NoError(h.Execute(args...))
	h.Require.NoError(h.Execute(args...))
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "an active key is reused")

	// The key was deleted on another machine.
	api.SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": []any{}})
	api.SetRoute("POST", "/v1/teams/team-a/api_keys", 200, map[string]string{"api_key": "secret-team-a-2"})
	h.Require.NoError(h.Execute(args...))
	h.Require.Contains(h.Stderr.String(), "Created routes API key")
	h.Require.Equal(2, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
	h.Require.Equal("secret-team-a-2", readHarnessSettings(t, "claude-code", harnessSettingsFile("claude-code", dir))["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"])

	// A failed check leaves the saved key and configuration alone.
	api.SetRoute("GET", "/v1/api_keys", 500, map[string]string{"message": "unavailable"})
	h.Require.Error(h.Execute(args...))
	h.Require.Contains(h.Stderr.String(), "listing routes API keys")
	h.Require.Equal(2, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
}

func Test_Route_APIKey_DeleteForgetsSavedKey(t *testing.T) {
	skipUnlessMacOS(t)
	h, api := fakeHarnessAPI(t)
	api.SetRoute("DELETE", "/v1/api_keys/secret-team-a", 200, map[string]string{"prefix": "secret-team-a"})
	setup := []string{"harness", "setup", "--harness", "claude-code", "--config-dir", t.TempDir(), "--key-name", "laptop", "--yes"}
	h.Require.NoError(h.Execute(setup...))
	scope := auth.RoutesKeyScope{ManagementURL: api.URL, UserID: "user-a", TeamID: "team-a", Name: "laptop"}
	key, err := configDirStore(t).GetRoutesKey(scope)
	h.Require.NoError(err)
	h.Require.Equal("secret-team-a", key)

	h.Require.NoError(h.Execute("route", "api-key", "delete", "--prefix", "secret-team-a", "--yes"))
	key, err = configDirStore(t).GetRoutesKey(scope)
	h.Require.NoError(err)
	h.Require.Empty(key, "delete removes this machine's saved copy")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal(2, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "setup creates a new key")
}
