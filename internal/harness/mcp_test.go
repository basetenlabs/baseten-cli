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

func TestMCPServerWriterShapes(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			filename := "settings.json"
			if name == "codex" {
				filename = "config.toml"
			}
			if name == "opencode" {
				filename = "opencode.json"
			}
			path := filepath.Join(dir, filename)
			plans, e := PrepareHarness(name, path, fixture(t), mcpFixture(), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
			require.NoError(t, e)
			if name == "claude-code" {
				require.Len(t, plans, 2)
			}
			require.NoError(t, ApplyPlans(plans))
			_, d, _, _, e := Read(mcpConfigPath(name, dir, path))
			require.NoError(t, e)
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

func TestMCPServerRerunAndTeardownRestoresUserServers(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			filename := "settings.json"
			original := []byte("{\n  \"theme\": \"dark\", \"mcpServers\": {\"user-server\": {\"type\": \"http\", \"url\": \"https://user.example.com/mcp\"}}\n}\n")
			if name == "codex" {
				filename = "config.toml"
				original = []byte("theme = \"dark\"\n[mcp_servers.user-server]\nurl = \"https://user.example.com/mcp\"\n")
			}
			if name == "opencode" {
				filename = "opencode.json"
				original = []byte("{\"theme\":\"dark\",\"mcp\":{\"user-server\":{\"type\":\"remote\",\"url\":\"https://user.example.com/mcp\"}}}\n")
			}
			path := filepath.Join(dir, filename)
			require.NoError(t, os.WriteFile(mcpConfigPath(name, dir, path), original, 0600))
			plans, e := PrepareHarness(name, path, fixture(t), mcpFixture(), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
			require.NoError(t, e)
			require.NoError(t, ApplyPlans(plans))
			plans, e = PrepareHarness(name, path, fixture(t), mcpFixture(), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
			require.NoError(t, e)
			for _, p := range plans {
				require.False(t, p.Changed)
			}
			require.NoError(t, ApplyPlans(plans))
			plans, e = PrepareHarnessTeardown(name, path)
			require.NoError(t, e)
			require.NoError(t, ApplyPlans(plans))
			b, e := os.ReadFile(mcpConfigPath(name, dir, path))
			require.NoError(t, e)
			require.Equal(t, original, b)
		})
	}
}

func TestMCPServerDriftBlocksRerun(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			filename := "settings.json"
			if name == "codex" {
				filename = "config.toml"
			}
			if name == "opencode" {
				filename = "opencode.json"
			}
			path := filepath.Join(dir, filename)
			plans, e := PrepareHarness(name, path, fixture(t), mcpFixture(), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
			require.NoError(t, e)
			require.NoError(t, ApplyPlans(plans))
			mcpPath := mcpConfigPath(name, dir, path)
			_, d, _, _, e := Read(mcpPath)
			require.NoError(t, e)
			key := mcpServerKey(name, "github", "url")
			require.NoError(t, put(d, key, Value{Exists: true, Data: "https://edited.example.com/mcp"}))
			b, e := encodeConfig(mcpPath, d)
			require.NoError(t, e)
			require.NoError(t, os.WriteFile(mcpPath, b, 0600))
			_, e = PrepareHarness(name, path, fixture(t), mcpFixture(), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
			require.ErrorContains(t, e, "user changed "+pathKey(key))
			require.NotContains(t, e.Error(), "mcp-secret-token")
		})
	}
}

func TestNoMCPServersLeavesNoTrace(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			filename := "settings.json"
			if name == "codex" {
				filename = "config.toml"
			}
			if name == "opencode" {
				filename = "opencode.json"
			}
			path := filepath.Join(dir, filename)
			plans, e := PrepareHarness(name, path, fixture(t), nil, Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
			require.NoError(t, e)
			if name == "claude-code" {
				require.Len(t, plans, 1)
			}
			require.NoError(t, ApplyPlans(plans))
			mcpPath := mcpConfigPath(name, dir, path)
			if name == "claude-code" {
				_, e = os.Stat(mcpPath)
				require.True(t, os.IsNotExist(e))
				_, e = os.Stat(JournalPath(mcpPath))
				require.True(t, os.IsNotExist(e))
				return
			}
			_, d, _, _, e := Read(mcpPath)
			require.NoError(t, e)
			root := "mcp_servers"
			if name == "opencode" {
				root = "mcp"
			}
			require.False(t, get(d, []string{root}).Exists)
		})
	}
}

func TestMCPTokenNeverLeaks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")
	plans, e := PrepareHarness("claude-code", path, fixture(t), mcpFixture(), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
	require.NoError(t, e)
	for _, p := range plans {
		b, e := json.Marshal(p)
		require.NoError(t, e)
		require.NotContains(t, string(b), "mcp-secret-token")
	}
	require.NoError(t, ApplyPlans(plans))
	mcpPath := filepath.Join(dir, ".claude.json")
	info, e := os.Stat(JournalPath(mcpPath))
	require.NoError(t, e)
	require.Zero(t, info.Mode().Perm()&0077)
	_, d, _, _, e := Read(mcpPath)
	require.NoError(t, e)
	require.NoError(t, put(d, []string{"mcpServers", "github", "headers", "Authorization"}, Value{Exists: true, Data: "Bearer user-edit"}))
	b, e := encodeConfig(mcpPath, d)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(mcpPath, b, 0600))
	_, e = PrepareHarness("claude-code", path, fixture(t), mcpFixture(), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
	require.Error(t, e)
	require.NotContains(t, e.Error(), "mcp-secret-token")
	require.NotContains(t, e.Error(), "user-edit")
	conflictDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", conflictDir)
	conflict := []byte("{\"mcpServers\":{\"github\":{\"type\":\"http\",\"url\":\"https://user-owned.example.com/mcp\"}}}\n")
	require.NoError(t, os.WriteFile(filepath.Join(conflictDir, ".claude.json"), conflict, 0600))
	_, e = PrepareHarness("claude-code", filepath.Join(conflictDir, "settings.json"), fixture(t), mcpFixture(), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
	require.ErrorContains(t, e, "existing mcpServers.github.url conflicts")
	require.NotContains(t, e.Error(), "mcp-secret-token")
}
