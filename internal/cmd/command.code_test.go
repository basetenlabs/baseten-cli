package cmd_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	public "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/basetenlabs/baseten-cli/internal/code"
)

func newCodeHarness(t *testing.T) *CommandHarness {
	h := NewCommandHarness(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "claude"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BASETEN_PROFILE", "")
	// No harness executable is run by these CLI tests. Adapter tests construct
	// their own Harness; protocol tests use an in-memory RoundTripper.
	t.Setenv("PATH", t.TempDir())
	return h
}
func seedCode(t *testing.T, h *CommandHarness) (*code.Store, *code.Installation) {
	t.Helper()
	s, err := code.NewStore()
	h.Require.NoError(err)
	i := &code.Installation{Version: 1, Org: "org-code", Profile: "human", UserID: "human-id", KeyID: "code-key-1", Model: "engineering", Configs: map[string]*code.ManagedConfig{}}
	h.Require.NoError(s.Save(i))
	h.Require.NoError(s.Credentials().SetAPIKeyProfile("baseten-code:"+i.KeyID, "https://app.baseten.co", "code-secret", false, nil))
	return s, i
}
func Test_Code_AllNodesHidden(t *testing.T) {
	var walk func(public.Command)
	walk = func(c public.Command) {
		if !c.Hidden {
			t.Errorf("%s is not hidden", c.Name)
		}
		for _, child := range c.Children {
			walk(child)
		}
	}
	found := false
	for _, c := range public.Root.Children {
		if c.Name == "code" {
			found = true
			walk(c)
		}
	}
	if !found {
		t.Fatal("missing code group")
	}
}
func Test_Code_HelpAndCompletionHideDescendants(t *testing.T) {
	h := newCodeHarness(t)
	for _, args := range [][]string{{"--help"}, {"help"}, {"__complete", ""}, {"__complete", "co"}, {"__complete", "code", ""}, {"__complete", "code", "keys", ""}, {"__complete", "code", "auth", ""}, {"__completeNoDesc", "code", ""}, {"code", "--help"}, {"code", "keys", "--help"}, {"code", "auth", "--help"}} {
		h.Require.NoError(h.Execute(args...))
		out := h.Stdout.String()
		h.Require.NotContains(out, "Preview or configure")
		h.Require.NotContains(out, "List Code key metadata")
		if !(len(args) > 1 && args[1] == "auth") {
			h.Require.NotContains(out, "Internal Code authentication")
		}
		if args[0] != "code" {
			h.Require.NotContains(out, "Configure Baseten Code")
		}
		if strings.HasPrefix(args[0], "__complete") {
			h.Require.NotContains(out, "\ninit")
			h.Require.NotContains(out, "\ncode\t")
			h.Require.NotContains(out, "\nrevoke")
			h.Require.NotContains(out, "\ntoken")
		}
	}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		h.Require.NoError(h.Execute("completion", shell))
		h.Require.NotContains(h.Stdout.String(), "Configure Baseten Code")
		h.Require.NotContains(h.Stdout.String(), "List Code key metadata")
	}
}
func Test_Code_ExplicitCommandsRemainInvocable(t *testing.T) {
	h := newCodeHarness(t)
	for _, leaf := range []string{"init", "status", "models", "spend", "doctor", "sync", "teardown", "logout", "keys list", "keys revoke", "auth token"} {
		args := append([]string{"code"}, strings.Fields(leaf)...)
		args = append(args, "--help")
		h.Require.NoError(h.Execute(args...))
		h.Require.Contains(h.Stdout.String(), "USAGE")
	}
}
func Test_Code_Init_DryRunHasNoSideEffects(t *testing.T) {
	h := newCodeHarness(t)
	h.Require.NoError(h.Execute("code", "init", "--harness", "codex", "--harness", "codex-desktop", "--model", "engineering", "--no-browser", "--no-interactive", "--dry-run", "--json"))
	var r struct {
		Status    string
		Harnesses []code.Harness
		Notes     []string
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &r))
	h.Require.Equal("preview", r.Status)
	h.Require.Len(r.Harnesses, 1)
	h.Require.Contains(h.Stdout.String(), "dedicated backend endpoints")
	s, err := code.NewStore()
	h.Require.NoError(err)
	_, err = os.Stat(s.Dir)
	h.Require.True(os.IsNotExist(err))
	_, err = os.Stat(r.Harnesses[0].Path)
	h.Require.True(os.IsNotExist(err))
}
func Test_Code_BackendDependenciesNeverFallBackToRegularKeys(t *testing.T) {
	h := newCodeHarness(t)
	for _, args := range [][]string{{"init", "--harness", "codex", "--no-interactive"}, {"models"}, {"keys", "list", "--include-revoked"}, {"keys", "revoke", "id", "--yes"}, {"keys", "revoke", "--all", "--yes"}, {"logout", "--yes"}, {"spend"}} {
		h.Require.Error(h.Execute(append([]string{"code"}, args...)...))
		h.Require.NotContains(h.Stdout.String(), "test-key")
		h.Require.NotContains(h.Stderr.String(), "test-key")
	}
	s, err := code.NewStore()
	h.Require.NoError(err)
	_, err = os.Stat(s.Dir)
	h.Require.True(os.IsNotExist(err))
}
func Test_Code_ScopeAndDateValidation(t *testing.T) {
	h := newCodeHarness(t)
	for _, args := range [][]string{{"teardown", "--yes"}, {"teardown", "codex", "--all", "--yes"}, {"keys", "revoke", "--yes"}, {"keys", "revoke", "id", "--all", "--yes"}, {"spend", "--from", "2026-09-01"}, {"spend", "--from", "bad", "--to", "2026-09-03"}, {"spend", "--from", "2026-09-03", "--to", "2026-09-01"}, {"doctor", "codex", "--model", "route"}, {"doctor", "unknown"}} {
		h.Require.Error(h.Execute(append([]string{"code"}, args...)...))
		h.Require.Equal(2, h.ExitCode)
	}
}
func Test_Code_StatusAndTokenIsolation(t *testing.T) {
	h := newCodeHarness(t)
	s, i := seedCode(t, h)
	regular := auth.NewStore(auth.StoreOptions{Dir: os.Getenv("BASETEN_CONFIG_DIR")})
	h.Require.NoError(regular.SetAPIKeyProfile("other", "https://app.baseten.co", "regular-secret", true, nil))
	h.Require.NoError(h.Execute("code", "status", "--json"))
	h.Require.Contains(h.Stdout.String(), "stored_unverified")
	h.Require.Contains(h.Stdout.String(), "human-id")
	h.Require.NotContains(h.Stdout.String(), "code-secret")
	h.Require.NotContains(h.Stdout.String(), "regular-secret")
	h.Require.NoError(h.Execute("code", "auth", "token"))
	h.Require.Equal("code-secret\n", h.Stdout.String())
	h.Require.Empty(h.Stderr.String())
	h.Require.Error(h.Execute("code", "auth", "token", "--org", "other"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Error(h.Execute("code", "auth", "token", "--output", "json"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Error(h.Execute("code", "logout", "--yes"))
	h.Require.NotContains(h.Stderr.String(), "code-secret")
	h.Require.NoError(h.Execute("code", "auth", "token"))
	h.Require.Equal("code-secret\n", h.Stdout.String())
	h.Require.Error(h.Execute("code", "status", "--org", "other"))
	h.Require.Equal(2, h.ExitCode)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())
	h.Require.NoError(h.Execute("code", "auth", "token", "--config-dir", s.Dir, "--org", i.Org))
	h.Require.Equal("code-secret\n", h.Stdout.String())
}
func Test_Code_JSONAliasAndJQ(t *testing.T) {
	h := newCodeHarness(t)
	h.Require.NoError(h.Execute("code", "status", "--json", "--jq", ".status"))
	h.Require.JSONEq(`"not_initialized"`, h.Stdout.String())
	h.Require.Error(h.Execute("code", "status", "--json", "--output", "text"))
	h.Require.Equal(2, h.ExitCode)
	h.Require.Error(h.Execute("code", "models", "--json"))
	var envelope public.JSONErrorEnvelope
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &envelope))
	h.Require.Equal(public.ExitAuth, envelope.Error.ExitCode)
}

type codeRoundTrip func(*http.Request) (*http.Response, error)

func (f codeRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func Test_Code_ModelsUsesOnlyCodeCredentialAndDoesNotFilter(t *testing.T) {
	h := newCodeHarness(t)
	seedCode(t, h)
	calls := 0
	h.Context = cmd.WithHTTPClient(h.Context, &http.Client{Transport: codeRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		h.Require.Equal("https://coding.baseten.co/v1/models", r.URL.String())
		h.Require.Equal("Bearer code-secret", r.Header.Get("Authorization"))
		h.Require.Empty(r.Header.Get("x-baseten-web-search-provider-priority"))
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"engineering","name":"Engineering"},{"id":"plain-route"}]}`))}, nil
	})})
	h.Require.NoError(h.Execute("code", "models", "--harness", "claude-code", "--json"))
	h.Require.Equal(1, calls)
	h.Require.Contains(h.Stdout.String(), "plain-route")
	h.Require.Contains(h.Stdout.String(), "unverified")
	h.Require.NotContains(h.Stdout.String(), "code-secret")
}
func Test_Code_DoctorWithoutTestNeverSendsInference(t *testing.T) {
	h := newCodeHarness(t)
	seedCode(t, h)
	h.Context = cmd.WithHTTPClient(h.Context, &http.Client{Transport: codeRoundTrip(func(r *http.Request) (*http.Response, error) {
		h.Require.Equal("GET", r.Method)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"engineering"}]}`))}, nil
	})})
	h.Require.Error(h.Execute("code", "doctor", "codex", "--json"))
	var report map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &report))
	h.Require.Equal("failed", report["status"])
}
func Test_Code_TeardownRequiresExplicitScopeAndRetainsCredential(t *testing.T) {
	h := newCodeHarness(t)
	s, i := seedCode(t, h)
	path := filepath.Join(t.TempDir(), "settings.json")
	h.Require.NoError(os.WriteFile(path, []byte(`{"model":"engineering","extra":"keep"}`), 0600))
	i.Configs["claude-code"] = &code.ManagedConfig{Harness: "claude-code", Path: path, Format: "json", Original: []byte(`{"model":"old"}`), Existed: true, Settings: []code.Setting{{Path: []string{"model"}, Before: code.Value{Exists: true, Data: "old"}, Installed: code.Value{Exists: true, Data: "engineering"}}}}
	h.Require.NoError(s.Save(i))
	h.Require.NoError(h.Execute("code", "teardown", "claude-code", "--dry-run", "--json"))
	raw, err := os.ReadFile(path)
	h.Require.NoError(err)
	h.Require.Contains(string(raw), "engineering")
	h.Require.Error(h.Execute("code", "teardown", "claude-code", "--no-interactive"))
	h.Require.Equal(2, h.ExitCode)
	h.Require.NoError(h.Execute("code", "teardown", "claude-code", "--yes", "--json"))
	raw, err = os.ReadFile(path)
	h.Require.NoError(err)
	h.Require.JSONEq(`{"model":"old","extra":"keep"}`, string(raw))
	h.Require.NoError(h.Execute("code", "auth", "token"))
	h.Require.Equal("code-secret\n", h.Stdout.String())
}

func Test_Code_TokenFailuresNeverWriteStdout(t *testing.T) {
	h := newCodeHarness(t)
	for _, args := range [][]string{{"--jq", "["}, {"--output", "json", "--unknown"}, {"--output", "jsonl", "extra"}, {"--json"}} {
		h.Require.Error(h.Execute(append([]string{"code", "auth", "token"}, args...)...))
		h.Require.Empty(h.Stdout.String())
	}
	h.Require.Error(h.Execute("code", "status", "--json", "--unknown"))
	var e public.JSONErrorEnvelope
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &e))
	h.Require.Equal(public.ExitUsage, e.Error.ExitCode)
}
