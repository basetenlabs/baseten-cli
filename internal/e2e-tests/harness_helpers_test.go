//go:build e2e

package e2etests

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/require"
)

const (
	harnessProcessTimeout = 150 * time.Second
	harnessProviderID     = "baseten-harness"

	harnessThinkingMarker   = "THINKING-MARKER-391"
	harnessClaudeToolWord   = "CLAUDE-TOOL-CALL-OK"
	harnessCodexToolWord    = "CODEX-TOOL-CALL-OK"
	harnessClaudeSwitchWord = "SWITCH-CLAUDE-STAGE-OK"
	harnessCodexSwitchWord  = "SWITCH-CODEX-STAGE-OK"
	harnessPiSwitchWord     = "SWITCH-PI-STAGE-OK"
	harnessClaudeRecallWord = "RECALL-CLAUDE-9"
	harnessCodexRecallWord  = "RECALL-CODEX-7"
	harnessPiRecallWord     = "RECALL-PI-3"
)

type harnessModelOption struct {
	id    string
	label string
}

type harnessPiModel struct {
	id        string
	api       string
	reasoning bool
	levels    []string
}

type harnessTest struct {
	name      string
	binary    string
	root      string
	configDir string
	endpoint  string

	claudeDefault string
	claudeOptions []harnessModelOption
	claudeEfforts map[string]string

	piModels []harnessPiModel

	codexDefault   string
	codexSlugs     []string
	codexWebSearch bool
}

func newHarnessTest(t *testing.T, name, binary string) *harnessTest {
	t.Helper()
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")
	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())

	found, err := exec.LookPath(binary)
	if err != nil {
		t.Skipf("%s is not on PATH", binary)
	}

	root, err := os.MkdirTemp("", "baseten-cli-e2e-")
	require.NoError(t, err)
	t.Cleanup(func() {
		err := os.RemoveAll(root)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			err = os.RemoveAll(root)
		}
		if err != nil {
			t.Logf("harness root %s not fully removed: %v", root, err)
		}
	})
	h := &harnessTest{name: name, binary: found, root: root}
	switch name {
	case "claude-code":
		h.configDir = filepath.Join(h.root, ".claude")
	case "codex":
		h.configDir = filepath.Join(h.root, ".codex")
	case "pi":
		h.configDir = filepath.Join(h.root, ".pi", "agent")
	default:
		t.Fatalf("unsupported harness %q", name)
	}
	teamArgs := harnessTeamArgs(t, name)
	out, errOut, err := cli(t, append([]string{"grid", "setup", "--harness", name, "--config-dir", h.configDir, "--use-session-credential", "--yes"}, teamArgs...)...)
	if err != nil {
		msg := strings.TrimSpace(errOut + out)
		switch {
		case strings.Contains(msg, "/v1/grid/routes was not found"):
			t.Skipf("the grid routes API (/v1/grid/routes) is not deployed on this remote (%s); harness e2e needs a remote with the grid routes feature enabled", remoteURL)
		case harnessEnvironmentalSetupError(msg):
			t.Skipf("harness setup cannot run against this org: %s", msg)
		}
		t.Fatalf("harness setup failed: %v\nstderr: %s", err, errOut)
	}
	step(t, "%s harness setup complete (config %s)", name, h.configDir)
	h.loadConfig(t)
	require.NotEmpty(t, h.endpoint, "harness setup wrote no endpoint for %s", name)
	return h
}

func harnessEnvironmentalSetupError(msg string) bool {
	for _, marker := range []string{
		"/v1/grid/routes was not found",
		"list routes: baseten API error (HTTP 403)",
		"no routes",
		"none of the team's routes have usable model metadata",
		"different invoke URLs",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

type harnessTeamProbeFailure struct {
	team   string
	err    error
	out    string
	errOut string
}

func harnessTeamArgs(t *testing.T, name string) []string {
	t.Helper()
	probeDir := t.TempDir()
	_, _, probeErr := cli(t, "grid", "setup", "--harness", name, "--config-dir", probeDir,
		"--use-session-credential", "--yes", "--dry-run")
	if probeErr == nil {
		return nil
	}
	out, errOut, err := cli(t, "org", "team", "list", "--output", "json")
	if err != nil {
		t.Skipf("org team list failed; cannot discover teams on this remote: %v\nstderr: %s", err, errOut)
	}
	var listed struct {
		Teams []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"teams"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &listed))
	if len(listed.Teams) == 0 {
		t.Skipf("the e2e org has no teams; %s needs a team with routes", name)
	}
	if len(listed.Teams) == 1 {
		return nil
	}
	lastErr := probeErr
	var failures []harnessTeamProbeFailure
	for _, team := range listed.Teams {
		if team.Name == "" {
			continue
		}
		out, errOut, err := cli(t, "grid", "setup", "--harness", name, "--config-dir", probeDir,
			"--team", team.Name, "--use-session-credential", "--yes", "--dry-run")
		if err == nil {
			return []string{"--team", team.Name}
		}
		lastErr = err
		failures = append(failures, harnessTeamProbeFailure{team: team.Name, err: err, out: out, errOut: errOut})
	}
	for _, failure := range failures {
		if !harnessEnvironmentalSetupError(failure.errOut + failure.out) {
			t.Fatalf("team %s probe failed with an unrecognized error: %v\nstdout:\n%s\nstderr:\n%s", failure.team, failure.err, failure.out, failure.errOut)
		}
	}
	t.Skipf("none of the org's %d teams have routes %s can call; last probe error: %v", len(listed.Teams), name, lastErr)
	return nil
}

func (h *harnessTest) loadConfig(t *testing.T) {
	t.Helper()
	switch h.name {
	case "claude-code":
		h.loadClaudeConfig(t)
	case "codex":
		h.loadCodexConfig(t)
	case "pi":
		h.loadPiConfig(t)
	default:
		t.Fatalf("unsupported harness %q", h.name)
	}
}

func (h *harnessTest) loadClaudeConfig(t *testing.T) {
	t.Helper()
	data := harnessReadJSON(t, filepath.Join(h.configDir, "settings.json"))
	h.claudeDefault, _ = data["model"].(string)
	h.claudeEfforts = map[string]string{}
	if settings, ok := data["modelSettings"].(map[string]any); ok {
		for id, value := range settings {
			row, ok := value.(map[string]any)
			if !ok {
				continue
			}
			effort, _ := row["maxEffortLevel"].(string)
			h.claudeEfforts[id] = effort
		}
	}
	if picker, ok := data["modelPicker"].(map[string]any); ok {
		options, _ := picker["options"].([]any)
		for _, entry := range options {
			row, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			id, _ := row["model"].(string)
			label, _ := row["label"].(string)
			if id != "" {
				h.claudeOptions = append(h.claudeOptions, harnessModelOption{id: id, label: label})
			}
		}
	}
	if env, ok := data["env"].(map[string]any); ok {
		h.endpoint, _ = env["ANTHROPIC_BASE_URL"].(string)
	}
}

func (h *harnessTest) loadPiConfig(t *testing.T) {
	t.Helper()
	data := harnessReadJSON(t, filepath.Join(h.configDir, "models.json"))
	providers, _ := data["providers"].(map[string]any)
	provider, _ := providers[harnessProviderID].(map[string]any)
	require.NotNil(t, provider, "pi models.json has no %s provider", harnessProviderID)
	baseURL, _ := provider["baseUrl"].(string)
	h.endpoint = strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
	entries, _ := provider["models"].([]any)
	for _, entry := range entries {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		id, _ := row["id"].(string)
		if id == "" {
			continue
		}
		model := harnessPiModel{id: id}
		model.api, _ = row["api"].(string)
		model.reasoning, _ = row["reasoning"].(bool)
		if levels, ok := row["thinkingLevelMap"].(map[string]any); ok {
			for _, level := range []string{"max", "xhigh", "high", "medium", "low", "minimal"} {
				if value, ok := levels[level]; ok && value != nil {
					model.levels = append(model.levels, level)
				}
			}
		}
		h.piModels = append(h.piModels, model)
	}
}

func (h *harnessTest) loadCodexConfig(t *testing.T) {
	t.Helper()
	data := harnessReadTOML(t, filepath.Join(h.configDir, "config.toml"))
	h.codexDefault, _ = data["model"].(string)
	providers, _ := data["model_providers"].(map[string]any)
	provider, _ := providers[harnessProviderID].(map[string]any)
	require.NotNil(t, provider, "codex config.toml has no %s provider", harnessProviderID)
	baseURL, _ := provider["base_url"].(string)
	h.endpoint = strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
	if value, ok := data["web_search"]; ok {
		h.codexWebSearch = harnessCodexWebSearchEnabled(value)
	} else if tools, ok := data["tools"].(map[string]any); ok {
		if value, ok := tools["web_search"]; ok {
			h.codexWebSearch = harnessCodexWebSearchEnabled(value)
		}
	}
	catalogPath, _ := data["model_catalog_json"].(string)
	require.NotEmpty(t, catalogPath, "codex config.toml has no model_catalog_json")
	catalog := harnessReadJSON(t, catalogPath)
	models, _ := catalog["models"].([]any)
	for _, entry := range models {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		slug, _ := row["slug"].(string)
		if slug != "" {
			h.codexSlugs = append(h.codexSlugs, slug)
		}
	}
}

func harnessCodexWebSearchEnabled(value any) bool {
	switch search := value.(type) {
	case bool:
		return search
	case string:
		return search != "" && search != "disabled" && search != "false"
	}
	return false
}

func harnessReadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s", path)
	parsed := map[string]any{}
	require.NoError(t, json.Unmarshal(data, &parsed), "parsing %s", path)
	return parsed
}

func harnessReadTOML(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s", path)
	parsed := map[string]any{}
	require.NoError(t, toml.Unmarshal(data, &parsed), "parsing %s", path)
	return parsed
}

func harnessProcessEnv(t *testing.T, root, configDir string) []string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tmp"), 0o700))
	env := []string{
		"HOME=" + root,
		"USERPROFILE=" + root,
		"CLAUDE_CONFIG_DIR=" + configDir,
		"CODEX_HOME=" + configDir,
		"PI_CODING_AGENT_DIR=" + configDir,
		"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(root, ".local", "state"),
		"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		"APPDATA=" + filepath.Join(root, "AppData", "Roaming"),
		"LOCALAPPDATA=" + filepath.Join(root, "AppData", "Local"),
		"TMPDIR=" + filepath.Join(root, "tmp"),
		"DISABLE_TELEMETRY=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	}
	for _, name := range []string{"PATH", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "TEMP", "TMP", "PROGRAMFILES", "PROGRAMDATA"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func (h *harnessTest) run(t *testing.T, dir string, args ...string) (string, string) {
	t.Helper()
	return h.runWithTimeout(t, dir, harnessProcessTimeout, args...)
}

func (h *harnessTest) runWithTimeout(t *testing.T, dir string, timeout time.Duration, args ...string) (string, string) {
	t.Helper()
	step(t, "%s: %s %s", h.name, h.binary, strings.Join(args, " "))
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, h.binary, args...)
	command.Dir = dir
	command.Env = harnessProcessEnv(t, h.root, h.configDir)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	command.WaitDelay = 5 * time.Second
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err := command.Run()
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	if err != nil {
		t.Fatalf("%s %s failed after %s: %v\nstdout:\n%s\nstderr:\n%s",
			h.binary, strings.Join(args, " "), timeout, err, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

func harnessAssertNoAPIError(t *testing.T, output string) {
	t.Helper()
	for _, marker := range []string{"invalid_request_error", "not supported for this model"} {
		require.NotContains(t, output, marker, "harness hit an API contract error; output:\n%s", output)
	}
}

func harnessJSONObjects(out string) []map[string]any {
	decoder := json.NewDecoder(strings.NewReader(out))
	var objects []map[string]any
	for {
		var value any
		if err := decoder.Decode(&value); err != nil {
			return objects
		}
		switch typed := value.(type) {
		case map[string]any:
			objects = append(objects, typed)
		case []any:
			for _, item := range typed {
				if object, ok := item.(map[string]any); ok {
					objects = append(objects, object)
				}
			}
		}
	}
}

func harnessWalkObjects(objects []map[string]any, visit func(map[string]any)) {
	for _, object := range objects {
		harnessWalk(object, visit)
	}
}

func harnessWalk(value any, visit func(map[string]any)) {
	switch typed := value.(type) {
	case map[string]any:
		visit(typed)
		for _, child := range typed {
			harnessWalk(child, visit)
		}
	case []any:
		for _, child := range typed {
			harnessWalk(child, visit)
		}
	}
}

type claudeStreamEvent struct {
	Type      string         `json:"type"`
	Subtype   string         `json:"subtype"`
	SessionID string         `json:"session_id"`
	Result    string         `json:"result"`
	IsError   bool           `json:"is_error"`
	Message   *claudeMessage `json:"message"`
}

type claudeMessage struct {
	Role    string        `json:"role"`
	Content []claudeBlock `json:"content"`
}

type claudeBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
	Name     string `json:"name"`
}

func parseClaudeStream(out string) []claudeStreamEvent {
	var events []claudeStreamEvent
	for _, object := range harnessJSONObjects(out) {
		encoded, err := json.Marshal(object)
		if err != nil {
			continue
		}
		var event claudeStreamEvent
		if err := json.Unmarshal(encoded, &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	return events
}

func claudeResult(events []claudeStreamEvent) (sessionID, result string, ok bool) {
	for _, event := range events {
		if event.Type == "result" {
			return event.SessionID, event.Result, true
		}
	}
	return "", "", false
}

func claudeThinkingBlocks(events []claudeStreamEvent) int {
	count := 0
	for _, event := range events {
		if event.Message == nil {
			continue
		}
		for _, block := range event.Message.Content {
			if block.Type == "thinking" {
				count++
			}
		}
	}
	return count
}

func claudeToolUses(events []claudeStreamEvent) []string {
	var names []string
	for _, event := range events {
		if event.Message == nil {
			continue
		}
		for _, block := range event.Message.Content {
			if block.Type == "tool_use" && block.Name != "" {
				names = append(names, block.Name)
			}
		}
	}
	return names
}

func codexThreadID(objects []map[string]any) string {
	id := ""
	harnessWalkObjects(objects, func(object map[string]any) {
		if id != "" {
			return
		}
		if value, ok := object["thread_id"].(string); ok && value != "" {
			id = value
		}
	})
	return id
}

func piAssistantText(objects []map[string]any) string {
	var texts []string
	harnessWalkObjects(objects, func(object map[string]any) {
		role, _ := object["role"].(string)
		if role != "assistant" {
			return
		}
		content, ok := object["content"].([]any)
		if !ok {
			return
		}
		for _, item := range content {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := block["text"].(string); ok && text != "" {
				texts = append(texts, text)
			}
		}
	})
	return strings.Join(texts, "\n")
}

func piThinkingSeen(objects []map[string]any) bool {
	seen := false
	harnessWalkObjects(objects, func(object map[string]any) {
		kind, _ := object["type"].(string)
		if kind == "thinking" || kind == "reasoning" {
			seen = true
		}
	})
	return seen
}

func piErrorTurns(objects []map[string]any) int {
	count := 0
	harnessWalkObjects(objects, func(object map[string]any) {
		role, _ := object["role"].(string)
		if role == "assistant" {
			if reason, _ := object["stopReason"].(string); reason == "error" {
				count++
			}
		}
	})
	return count
}

func (h *harnessTest) claudePair(t *testing.T) (string, string) {
	t.Helper()
	if len(h.claudeOptions) < 2 {
		t.Skipf("the team has %d route(s) Claude Code can call; model switching needs at least 2", len(h.claudeOptions))
	}
	return h.claudeOptions[0].id, h.claudeOptions[1].id
}

func (h *harnessTest) codexPair(t *testing.T) (string, string) {
	t.Helper()
	if len(h.codexSlugs) < 2 {
		t.Skipf("the team has %d route(s) Codex can call; model switching needs at least 2", len(h.codexSlugs))
	}
	return h.codexSlugs[0], h.codexSlugs[1]
}

func (h *harnessTest) piPair(t *testing.T) (string, string) {
	t.Helper()
	if len(h.piModels) < 2 {
		t.Skipf("the team has %d route(s) Pi can call; model switching needs at least 2", len(h.piModels))
	}
	return h.piModels[0].id, h.piModels[1].id
}

func (h *harnessTest) claudeThinkingModel(t *testing.T) string {
	t.Helper()
	names := make([]string, 0, len(h.claudeOptions))
	for _, option := range h.claudeOptions {
		if _, ok := h.claudeEfforts[option.id]; ok {
			return option.id
		}
		names = append(names, option.id)
	}
	t.Skipf("the team's routes include no model with an effort ceiling for Claude Code to think with (models: %s)", strings.Join(names, ", "))
	return ""
}

func (h *harnessTest) piAnthropicThinkingModel(t *testing.T) (string, string) {
	t.Helper()
	for _, model := range h.piModels {
		if model.api == "anthropic-messages" && model.reasoning && len(model.levels) > 0 {
			return model.id, model.levels[0]
		}
	}
	t.Skipf("the team's routes include no Anthropic-format model with reasoning for pi to think with (models: %s)", harnessPiModelNames(h.piModels))
	return "", ""
}

func harnessPiModelNames(models []harnessPiModel) string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		names = append(names, model.id+"("+model.api+")")
	}
	return strings.Join(names, ", ")
}

func harnessSeedWorkspace(t *testing.T, entries map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range entries {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	return dir
}

func harnessReadWorkspaceFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err, "reading %s, which the harness was asked to produce", name)
	return string(data)
}

func harnessReadWorkspaceFileTrimmed(t *testing.T, dir, name string) string {
	t.Helper()
	return strings.TrimRight(harnessReadWorkspaceFile(t, dir, name), "\n")
}
