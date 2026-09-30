package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

type codexHarness struct{}

// catalogPath is the model catalog Baseten owns next to the Codex settings.
func catalogPath(path string) string {
	return filepath.Join(filepath.Dir(path), "baseten-models.json")
}

func (codexHarness) Name() string { return Codex }

func (h codexHarness) Detect(ctx context.Context, execer Execer, dir string) (Detection, error) {
	dir, err := configDir(dir, "CODEX_HOME", ".codex")
	if err != nil {
		return Detection{}, err
	}
	path := filepath.Join(dir, "config.toml")
	return detect(ctx, execer, h.Name(), codexBinary(execer), path)
}

func codexBinary(execer Execer) string {
	for _, binary := range append([]string{"codex"}, desktopCodexBinaries()...) {
		if _, err := execer.LookPath(binary); err == nil {
			return binary
		}
	}
	return "codex"
}

// linuxDesktopCodex is where the ChatGPT desktop app's x64 .deb package
// installs its bundled Codex.
const linuxDesktopCodex = "/usr/lib/chatgpt/resources/codex"

// CodexDesktopLocations describes where Detect looks for a desktop-bundled
// Codex on this platform, or "" if it looks only on PATH.
func CodexDesktopLocations() string {
	switch runtime.GOOS {
	case "darwin":
		return "ChatGPT.app or Codex.app"
	case "linux":
		return filepath.Dir(filepath.Dir(linuxDesktopCodex))
	}
	return ""
}

// desktopCodexBinaries lists where ChatGPT and Codex desktop installs bundle
// Codex without adding it to PATH. The bundled Codex shares the CLI config.
func desktopCodexBinaries() []string {
	switch runtime.GOOS {
	case "darwin":
		roots := []string{"/Applications"}
		if home, err := os.UserHomeDir(); err == nil {
			roots = append(roots, filepath.Join(home, "Applications"))
		}
		var binaries []string
		for _, root := range roots {
			for _, app := range []string{"ChatGPT.app", "Codex.app"} {
				resources := filepath.Join(root, app, "Contents", "Resources")
				binaries = append(binaries,
					filepath.Join(resources, "codex"),
					filepath.Join(resources, "codex-cli", "CodexCLI.app", "Contents", "MacOS", "codex"),
				)
			}
		}
		return binaries
	case "linux":
		return []string{linuxDesktopCodex}
	}
	return nil
}

func codexCommand(ctx context.Context, execer Execer, dir string, args ...string) (string, string, error) {
	bin, err := execer.LookPath(codexBinary(execer))
	if err != nil {
		return "", "", err
	}
	var out, errOut bytes.Buffer
	command := exec.CommandContext(ctx, bin, args...)
	command.Env = append(os.Environ(), "CODEX_HOME="+dir)
	command.Stdout, command.Stderr = &out, &errOut
	if err := execer.Exec(command); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w: %v", ctx.Err(), err)
		}
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return "", "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", "", err
	}
	return out.String(), errOut.String(), nil
}

func codexDaemonCommand(ctx context.Context, execer Execer, dir string, args ...string) (map[string]any, error) {
	out, _, err := codexCommand(ctx, execer, dir, append([]string{"app-server", "daemon"}, args...)...)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return nil, err
	}
	return result, nil
}

const codexChatGPTLogin = "Logged in using ChatGPT"

func CodexChatGPTLogin(ctx context.Context, execer Execer, dir string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stdout, stderr, err := codexCommand(ctx, execer, dir, "login", "status")
	return err == nil && strings.Contains(stdout+stderr, codexChatGPTLogin)
}

func CodexLogout(ctx context.Context, execer Execer, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, _, err := codexCommand(ctx, execer, dir, "logout")
	return err
}

func CodexDaemonSocket(ctx context.Context, execer Execer, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := codexDaemonCommand(ctx, execer, dir, "version")
	if err != nil || result["status"] != "running" {
		return ""
	}
	socket, _ := result["socketPath"].(string)
	return socket
}

func CodexDaemonClients(ctx context.Context, execer Execer, socket string) (int, error) {
	socket, err := filepath.EvalSymlinks(socket)
	if err != nil {
		return 0, err
	}
	lsof, err := execer.LookPath("lsof")
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var out bytes.Buffer
	command := exec.CommandContext(ctx, lsof, "-U", "-F", "n")
	command.Stdout = &out
	if err := execer.Exec(command); err != nil {
		return 0, err
	}
	return codexSocketClients(out.String(), socket)
}

func codexSocketClients(lsof, socket string) (int, error) {
	sockets := 0
	for _, line := range strings.Split(lsof, "\n") {
		name, _, _ := strings.Cut(strings.TrimPrefix(line, "n"), " type=")
		if strings.HasPrefix(line, "n") && name == socket {
			sockets++
		}
	}
	if sockets == 0 {
		return 0, errors.New("the daemon socket is not listed by lsof")
	}
	return sockets - 1, nil
}

func RestartCodexDaemon(ctx context.Context, execer Execer, dir string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := codexDaemonCommand(ctx, execer, dir, "restart")
	if err != nil {
		return 0, err
	}
	pid, _ := result["pid"].(float64)
	return int(pid), nil
}

func (codexHarness) BackgroundRoute(Selection) string { return "" }

var codexCredentialPath = []string{"model_providers", providerID, "experimental_bearer_token"}

func (codexHarness) Credential(path string) (string, error) {
	return credential(path, codexCredentialPath)
}

// Routes keeps the routes that serve the OpenAI Responses API, which Codex uses
// for every route.
func (codexHarness) Routes(routes []Route) []Route {
	return slices.DeleteFunc(slices.Clone(routes), func(r Route) bool { return !r.Responses })
}

func (codexHarness) Prepare(path string, routes []Route, s Selection, endpoint string) ([]*Plan, error) {
	switch {
	case s.Background != "":
		return nil, errors.New("--background-route is supported only for Claude Code and OpenCode")
	case s.Subagent != "":
		return nil, errors.New("--subagent-route is supported only for Claude Code and OpenCode")
	case s.Fallback != "":
		return nil, errors.New("--fallback-route is supported only for Claude Code")
	}
	s, err := s.resolve(routes)
	if err != nil {
		return nil, err
	}
	values := []setting{
		desired([]string{"model"}, s.Primary),
		desired([]string{"model_provider"}, providerID),
		desired([]string{"model_catalog_json"}, catalogPath(path)),
		desired([]string{"review_model"}, s.Primary),
		desired([]string{"model_providers", providerID}, map[string]any{
			"name":                      "Baseten",
			"base_url":                  strings.TrimRight(endpoint, "/") + "/v1",
			"wire_api":                  "responses",
			"requires_openai_auth":      false,
			"experimental_bearer_token": "",
			"http_headers":              map[string]any{clientHeader: Codex},
		}),
	}
	p, err := prepareSettings(path, codexCredentialPath, func(map[string]any) ([]setting, error) { return values, nil })
	if err != nil {
		return nil, err
	}
	models := []any{}
	for i, r := range routes {
		efforts := xhighReasoningLevels(r.ReasoningLevels)
		levels := []any{}
		for _, level := range efforts {
			levels = append(levels, map[string]any{"effort": level, "description": level})
		}
		low, _ := reasoningBounds(efforts)
		// Codex requires every field.
		models = append(models, map[string]any{
			"slug":                         r.Name,
			"display_name":                 r.DisplayName,
			"description":                  "Baseten route",
			"base_instructions":            "",
			"default_reasoning_level":      low,
			"supported_reasoning_levels":   levels,
			"shell_type":                   "shell_command",
			"visibility":                   "list",
			"supported_in_api":             true,
			"priority":                     i,
			"supports_reasoning_summaries": false,
			"support_verbosity":            false,
			"default_verbosity":            nil,
			"apply_patch_tool_type":        nil,
			"truncation_policy":            map[string]any{"mode": "tokens", "limit": 10000},
			"context_window":               r.ContextWindow,
			"input_modalities":             r.InputModalities,
			"supports_parallel_tool_calls": r.ParallelTools,
			"experimental_supported_tools": []any{},
		})
	}
	catalog, err := prepareSettings(catalogPath(path), nil, func(map[string]any) ([]setting, error) {
		return []setting{desired([]string{"models"}, models)}, nil
	})
	if err != nil {
		return nil, err
	}
	// The catalog precedes the settings that reference it.
	return []*Plan{catalog, p}, nil
}

func codexTeardownPaths(data map[string]any, catalog string) [][]string {
	paths := [][]string{{"model_providers", providerID}}
	// Only reset shared defaults while Baseten is the selected provider.
	if data["model_provider"] == providerID {
		paths = append(paths, []string{"model_provider"}, []string{"model"}, []string{"review_model"})
	}
	if data["model_catalog_json"] == catalog {
		paths = append(paths, []string{"model_catalog_json"})
	}
	return paths
}

func (codexHarness) Teardown(path string) ([]*Plan, error) {
	config, err := prepareTeardown(path, func(data map[string]any) [][]string {
		return codexTeardownPaths(data, catalogPath(path))
	})
	if err != nil {
		return nil, err
	}
	removal, err := prepareRemoval(catalogPath(path))
	if err != nil {
		return nil, err
	}
	return []*Plan{config, removal}, nil
}

func (codexHarness) Inspect(d Detection) (Status, error) {
	r, data, err := inspectConfig(d)
	if err != nil {
		return r, err
	}
	r.managed(data, codexTeardownPaths(data, catalogPath(d.Path)))
	f, models, err := readConfig(catalogPath(d.Path))
	switch {
	case err != nil:
		// Setup rewrites a malformed catalog and teardown deletes it.
		r.State = StateIncomplete
		return r, nil
	case r.State == StateNotConfigured:
		if f.exists {
			r.State = StateIncomplete
		}
		return r, nil
	case !get(data, []string{"model_providers", providerID}).Exists || !f.exists:
		r.State = StateIncomplete
	case data["model_provider"] != providerID:
		r.State = StateInactive
	}
	labels := map[string]string{}
	entries, _ := models["models"].([]any)
	for _, entry := range entries {
		if row, ok := entry.(map[string]any); ok {
			name, _ := row["slug"].(string)
			labels[name], _ = row["display_name"].(string)
		}
	}
	r.addRoutes(labels)
	return r, nil
}
