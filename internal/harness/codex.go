package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	d, err := detect(ctx, execer, h.Name(), "codex", path)
	if err != nil || d.Installed {
		return d, err
	}
	for _, binary := range desktopCodexBinaries() {
		d, err = detect(ctx, execer, h.Name(), binary, path)
		if err != nil || d.Installed {
			return d, err
		}
	}
	return d, nil
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
				binaries = append(binaries, filepath.Join(root, app, "Contents", "Resources", "codex"))
			}
		}
		return binaries
	case "linux":
		return []string{linuxDesktopCodex}
	}
	return nil
}

func (codexHarness) BackgroundRoute(Selection) string { return "" }

var codexCredentialPath = []string{"model_providers", providerID, "experimental_bearer_token"}

func (codexHarness) Credential(path string) (string, error) {
	return credential(path, codexCredentialPath)
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
		}),
	}
	p, err := prepareSettings(path, codexCredentialPath, func(map[string]any) ([]setting, error) { return values, nil })
	if err != nil {
		return nil, err
	}
	models := []any{}
	for i, r := range routes {
		// Codex requires every field. Model-specific capabilities and limits are
		// follow-up work, so these are conservative defaults.
		models = append(models, map[string]any{
			"slug":                         r.Name,
			"display_name":                 r.DisplayName,
			"description":                  "Baseten route",
			"base_instructions":            "",
			"default_reasoning_level":      nil,
			"supported_reasoning_levels":   []any{},
			"shell_type":                   "shell_command",
			"visibility":                   "list",
			"supported_in_api":             true,
			"priority":                     i,
			"supports_reasoning_summaries": false,
			"support_verbosity":            false,
			"default_verbosity":            nil,
			"apply_patch_tool_type":        nil,
			"truncation_policy":            map[string]any{"mode": "tokens", "limit": 10000},
			"supports_parallel_tool_calls": false,
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
