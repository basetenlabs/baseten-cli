package cmd_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/stretchr/testify/require"
)

func Test_Harness_Setup_MCPServersWritten(t *testing.T) {
	h, api := fakeHarnessAPI(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	api.SetRoute("GET", "/v1/mcp_servers", 200, map[string]any{
		"mcp_servers": []any{
			map[string]any{"name": "github", "url": "https://api.githubcopilot.com/mcp/", "authorization_token": "mcp-secret"},
			map[string]any{"name": "docs", "url": "https://docs.example.com/mcp"},
		},
		"next_cursor": "",
	})
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--route", "acme/primary", "--team", "team-a", "--key-name", "mcp", "--config-dir", dir, "--yes", "--output", "json"))
	h.Require.NotContains(h.Stdout.String(), "mcp-secret")
	call := api.FindCall("GET", "/v1/mcp_servers")
	h.Require.NotNil(call)
	h.Require.Equal("team-a", call.Query().Get("team_id"))
	b, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	h.Require.NoError(err)
	var written map[string]any
	h.Require.NoError(json.Unmarshal(b, &written))
	servers, ok := written["mcpServers"].(map[string]any)
	h.Require.True(ok)
	h.Require.Equal(map[string]any{"type": "http", "url": "https://api.githubcopilot.com/mcp/", "headers": map[string]any{"Authorization": "Bearer mcp-secret"}}, servers["github"])
	h.Require.Equal(map[string]any{"type": "http", "url": "https://docs.example.com/mcp"}, servers["docs"])
	h.Require.NoError(h.Execute("harness", "teardown", "--harness", "claude-code", "--config-dir", dir, "--yes"))
	d, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	h.Require.NoError(err)
	h.Require.NotContains(string(d), "BASETEN_HARNESS")
	b, err = os.ReadFile(filepath.Join(dir, ".claude.json"))
	h.Require.NoError(err)
	h.Require.Contains(string(b), "mcpServers")
}

func Test_Harness_Setup_MCPServers404Proceeds(t *testing.T) {
	h, _ := fakeHarnessAPI(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	settings := filepath.Join(dir, "settings.json")
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--route", "acme/primary", "--team", "team-a", "--key-name", "absent", "--config-dir", dir, "--yes"))
	h.Require.FileExists(settings)
	_, err := os.Stat(filepath.Join(dir, ".claude.json"))
	h.Require.True(os.IsNotExist(err))
}

func Test_Harness_Setup_MCPServersFailureNeverMints(t *testing.T) {
	h, api := fakeHarnessAPI(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	api.SetRoute("GET", "/v1/mcp_servers", 500, map[string]any{"message": "boom"})
	h.Require.Error(h.Execute("harness", "setup", "--harness", "claude-code", "--route", "acme/primary", "--team", "team-a", "--config-dir", dir, "--yes"))
	h.Require.Contains(h.Stderr.String(), "GET /v1/mcp_servers returned HTTP 500")
	h.Require.Nil(api.FindCall("POST", "/v1/teams/team-a/api_keys"))
	_, err := os.Stat(filepath.Join(dir, "settings.json"))
	h.Require.True(os.IsNotExist(err))
}

func Test_Harness_MCPServers_ReadPagination(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/v1/mcp_servers", r.URL.Path)
		require.Equal(t, "team-a", r.URL.Query().Get("team_id"))
		require.Equal(t, "Bearer mock", r.Header.Get("Authorization"))
		if calls == 1 {
			json.NewEncoder(w).Encode(map[string]any{"mcp_servers": []any{map[string]any{"name": "github", "url": "https://api.githubcopilot.com/mcp/", "authorization_token": "mcp-secret"}}, "next_cursor": "next"})
			return
		}
		require.Equal(t, "next", r.URL.Query().Get("cursor"))
		json.NewEncoder(w).Encode(map[string]any{"mcp_servers": []any{map[string]any{"name": "docs", "url": "https://docs.example.com/mcp"}}, "next_cursor": ""})
	}))
	defer server.Close()
	servers, err := internalcmd.ReadHarnessMCPServersForTest(t.Context(), server.Client(), server.URL, http.Header{"Authorization": []string{"Bearer mock"}}, "team-a")
	require.NoError(t, err)
	require.Equal(t, []harness.MCPServer{
		{Name: "github", URL: "https://api.githubcopilot.com/mcp/", AuthorizationToken: "mcp-secret"},
		{Name: "docs", URL: "https://docs.example.com/mcp"},
	}, servers)
	require.Equal(t, 2, calls)
}

func Test_Harness_MCPServers_Read404IsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	servers, err := internalcmd.ReadHarnessMCPServersForTest(t.Context(), server.Client(), server.URL, nil, "team-a")
	require.NoError(t, err)
	require.Empty(t, servers)
}

func Test_Harness_MCPServers_ReadRejectsInvalidPages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"denied", 403, `{}`},
		{"server error", 500, `{}`},
		{"not json", 200, `nope`},
		{"trailing data", 200, `{"mcp_servers":[]} {}`},
		{"missing servers", 200, `{"next_cursor":""}`},
		{"missing name", 200, `{"mcp_servers":[{"url":"https://x.example.com"}]}`},
		{"missing url", 200, `{"mcp_servers":[{"name":"github"}]}`},
		{"duplicate", 200, `{"mcp_servers":[{"name":"github","url":"https://a.example.com"},{"name":"github","url":"https://b.example.com"}]}`},
		{"repeated cursor", 200, `{"mcp_servers":[],"next_cursor":"repeat"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			_, err := internalcmd.ReadHarnessMCPServersForTest(t.Context(), server.Client(), server.URL, nil, "team-a")
			require.Error(t, err)
			require.NotContains(t, err.Error(), "mcp-secret")
		})
	}
}
