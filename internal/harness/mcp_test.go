//go:build !windows

package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func mcpFixture() []MCPServer {
	return []MCPServer{
		{Name: "github", URL: "https://api.githubcopilot.com/mcp/", AuthorizationToken: "mcp-secret-token"},
		{Name: "docs", URL: "https://docs.example.com/mcp"},
	}
}

func mcpHarness(t *testing.T, name string) Harness {
	t.Helper()
	for _, h := range All() {
		if h.Name() == name {
			return h
		}
	}
	t.Fatalf("unknown harness %s", name)
	return nil
}

func mcpSettingsPath(name, dir string) string {
	filename := "settings.json"
	if name == "codex" {
		filename = "config.toml"
	}
	if name == "opencode" {
		filename = "opencode.json"
	}
	return filepath.Join(dir, filename)
}

func mcpConfigPath(name, dir, settingsPath string) string {
	if name == "claude-code" {
		return filepath.Join(dir, ".claude.json")
	}
	return settingsPath
}

func mcpServerKey(name, server string, leaf ...string) []string {
	base := []string{"mcpServers", server}
	if name == "codex" {
		base = []string{"mcp_servers", server}
	}
	if name == "opencode" {
		base = []string{"mcp", server}
	}
	return append(base, leaf...)
}

func mcpUserServerConfig(name string) []byte {
	switch name {
	case "codex":
		return []byte("theme = \"dark\"\n[mcp_servers.user-server]\nurl = \"https://user.example.com/mcp\"\n")
	case "opencode":
		return []byte("{\"theme\":\"dark\",\"mcp\":{\"user-server\":{\"type\":\"remote\",\"url\":\"https://user.example.com/mcp\"}}}\n")
	default:
		return []byte("{\n  \"theme\": \"dark\", \"mcpServers\": {\"user-server\": {\"type\": \"http\", \"url\": \"https://user.example.com/mcp\"}}\n}\n")
	}
}

func mcpUserServerKey(name string, leaf ...string) []string {
	return mcpServerKey(name, "user-server", leaf...)
}

func TestMCPServerWriterShapes(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			path := mcpSettingsPath(name, dir)
			h := mcpHarness(t, name)
			plans, err := h.Prepare(path, testRoutes(), mcpFixture(), Selection{Primary: "acme/primary"}, testEndpoint, testToken)
			require.NoError(t, err)
			if name == "claude-code" {
				require.Len(t, plans, 2)
			}
			require.NoError(t, ApplyPlans(plans, testToken))
			d := load(t, mcpConfigPath(name, dir, path))
			require.Equal(t, "https://api.githubcopilot.com/mcp/", get(d, mcpServerKey(name, "github", "url")).Data)
			require.Equal(t, "https://docs.example.com/mcp", get(d, mcpServerKey(name, "docs", "url")).Data)
			headerKey := "headers"
			if name == "codex" {
				headerKey = "http_headers"
			}
			require.Equal(t, "Bearer mcp-secret-token", get(d, mcpServerKey(name, "github", headerKey, "Authorization")).Data)
			require.False(t, get(d, mcpServerKey(name, "docs", headerKey)).Exists)
			if name == "claude-code" {
				require.Equal(t, "http", get(d, mcpServerKey(name, "github", "type")).Data)
			}
			if name == "opencode" {
				require.Equal(t, "remote", get(d, mcpServerKey(name, "github", "type")).Data)
			}
		})
	}
}

func TestMCPServerRerunKeepsUserServers(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			path := mcpSettingsPath(name, dir)
			require.NoError(t, os.WriteFile(mcpConfigPath(name, dir, path), mcpUserServerConfig(name), 0o600))
			h := mcpHarness(t, name)
			plans, err := h.Prepare(path, testRoutes(), mcpFixture(), Selection{Primary: "acme/primary"}, testEndpoint, testToken)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, testToken))
			d := load(t, mcpConfigPath(name, dir, path))
			require.Equal(t, "https://user.example.com/mcp", get(d, mcpUserServerKey(name, "url")).Data)
			require.Equal(t, "https://api.githubcopilot.com/mcp/", get(d, mcpServerKey(name, "github", "url")).Data)
			plans, err = h.Prepare(path, testRoutes(), mcpFixture(), Selection{Primary: "acme/primary"}, testEndpoint, testToken)
			require.NoError(t, err)
			for _, p := range plans {
				require.False(t, p.Changed)
			}
			require.NoError(t, ApplyPlans(plans, testToken))
			plans, err = h.Teardown(path)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, ""))
			d = load(t, mcpConfigPath(name, dir, path))
			require.Equal(t, "https://user.example.com/mcp", get(d, mcpUserServerKey(name, "url")).Data)
		})
	}
}

func TestMCPServerOverwriteOnRerun(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			path := mcpSettingsPath(name, dir)
			h := mcpHarness(t, name)
			plans, err := h.Prepare(path, testRoutes(), mcpFixture(), Selection{Primary: "acme/primary"}, testEndpoint, testToken)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, testToken))
			mcpPath := mcpConfigPath(name, dir, path)
			d := load(t, mcpPath)
			key := mcpServerKey(name, "github", "url")
			require.NoError(t, put(d, key, value{Exists: true, Data: "https://edited.example.com/mcp"}))
			save(t, mcpPath, d)
			plans, err = h.Prepare(path, testRoutes(), mcpFixture(), Selection{Primary: "acme/primary"}, testEndpoint, testToken)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, testToken))
			d = load(t, mcpPath)
			require.Equal(t, "https://api.githubcopilot.com/mcp/", get(d, key).Data)
		})
	}
}

func TestNoMCPServersLeavesNoTrace(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			path := mcpSettingsPath(name, dir)
			h := mcpHarness(t, name)
			plans, err := h.Prepare(path, testRoutes(), nil, Selection{Primary: "acme/primary"}, testEndpoint, testToken)
			require.NoError(t, err)
			if name == "claude-code" {
				require.Len(t, plans, 1)
			}
			require.NoError(t, ApplyPlans(plans, testToken))
			mcpPath := mcpConfigPath(name, dir, path)
			if name == "claude-code" {
				_, err = os.Stat(mcpPath)
				require.True(t, os.IsNotExist(err))
				return
			}
			d := load(t, mcpPath)
			root := "mcp_servers"
			if name == "opencode" {
				root = "mcp"
			}
			require.False(t, get(d, []string{root}).Exists)
		})
	}
}

func TestMCPTokenNeverInPlanOutput(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := mcpSettingsPath("claude-code", dir)
	plans, err := claudeCodeHarness{}.Prepare(path, testRoutes(), mcpFixture(), Selection{Primary: "acme/primary"}, testEndpoint, testToken)
	require.NoError(t, err)
	for _, p := range plans {
		b, err := json.Marshal(p)
		require.NoError(t, err)
		require.NotContains(t, string(b), "mcp-secret-token")
	}
	require.NoError(t, ApplyPlans(plans, testToken))
	info, err := os.Stat(filepath.Join(dir, ".claude.json"))
	require.NoError(t, err)
	require.Zero(t, info.Mode().Perm()&0077)
}
