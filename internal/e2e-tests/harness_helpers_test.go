//go:build e2e

package e2etests

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/require"
)

const (
	harnessProcessTimeout    = 150 * time.Second
	harnessProviderID        = "baseten-harness"
	harnessOpenCodeSettings  = "opencode.json"
	harnessRouterRoutePrefix = "auto-"
	harnessLongContextSuffix = "[1m]"

	harnessThinkingMarker     = "THINKING-MARKER-391"
	harnessClaudeToolWord     = "CLAUDE-TOOL-CALL-OK"
	harnessCodexToolWord      = "CODEX-TOOL-CALL-OK"
	harnessPiToolWord         = "PI-TOOL-CALL-OK"
	harnessOpenCodeToolWord   = "OPENCODE-TOOL-CALL-OK"
	harnessClaudeSwitchWord   = "SWITCH-CLAUDE-STAGE-OK"
	harnessCodexSwitchWord    = "SWITCH-CODEX-STAGE-OK"
	harnessPiSwitchWord       = "SWITCH-PI-STAGE-OK"
	harnessOpenCodeSwitchWord = "SWITCH-OPENCODE-STAGE-OK"
	harnessClaudeRecallWord   = "RECALL-CLAUDE-9"
	harnessCodexRecallWord    = "RECALL-CODEX-7"
	harnessPiRecallWord       = "RECALL-PI-3"
	harnessOpenCodeRecallWord = "RECALL-OPENCODE-5"
)

var harnessEffortLevels = []string{"max", "xhigh", "high", "medium", "low", "minimal"}

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

type harnessOpenCodeModel struct {
	id        string
	anthropic bool
	variants  []string
}

type harnessTest struct {
	name      string
	binary    string
	prefix    []string
	root      string
	configDir string
	endpoint  string

	claudeDefault string
	claudeOptions []harnessModelOption
	claudeEfforts map[string]string

	piModels []harnessPiModel

	codexDefault string
	codexSlugs   []string
	codexEfforts map[string][]string

	openCodeDefault string
	openCodeModels  []harnessOpenCodeModel
}

type harnessVariant struct {
	binary string
	major  string
	prefix []string
	seed   map[string]string
}

type harnessOpenCodeVariant struct {
	label     string
	opencode2 bool
	v2Config  bool
}

var harnessOpenCodeVariants = []harnessOpenCodeVariant{
	{label: "opencode"},
	{label: "opencode2/v1", opencode2: true},
	{label: "opencode2/v2", opencode2: true, v2Config: true},
}

func newHarnessTest(t *testing.T, name, binary string) *harnessTest {
	t.Helper()
	return newHarnessVariantTest(t, name, harnessVariant{binary: binary})
}

func newOpenCodeHarnessTest(t *testing.T, variant harnessOpenCodeVariant) *harnessTest {
	t.Helper()
	if !variant.opencode2 {
		return newHarnessVariantTest(t, "opencode", harnessVariant{binary: "opencode", major: "1"})
	}
	binary := os.Getenv("BASETEN_TEST_OPENCODE2")
	if binary == "" {
		t.Skip("BASETEN_TEST_OPENCODE2 not set")
	}
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	spec := harnessVariant{binary: binary, prefix: []string{"--standalone"}}
	if variant.v2Config {
		spec.seed = map[string]string{harnessOpenCodeSettings: `{"providers": {}}`}
	}
	return newHarnessVariantTest(t, "opencode", spec)
}

func newHarnessVariantTest(t *testing.T, name string, variant harnessVariant) *harnessTest {
	t.Helper()
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")
	team := os.Getenv("BASETEN_E2E_TEST_TEAM")
	require.NotEmpty(t, team, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_TEAM is missing")
	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())

	found, err := exec.LookPath(variant.binary)
	if err != nil {
		t.Skipf("%s is not on PATH", variant.binary)
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
	h := &harnessTest{name: name, binary: found, prefix: variant.prefix, root: root}
	switch name {
	case "claude-code":
		h.configDir = filepath.Join(h.root, ".claude")
	case "codex":
		h.configDir = filepath.Join(h.root, ".codex")
	case "pi":
		h.configDir = filepath.Join(h.root, ".pi", "agent")
	case "opencode":
		h.configDir = filepath.Join(h.root, ".config", "opencode")
	default:
		t.Fatalf("unsupported harness %q", name)
	}
	for _, entry := range harnessHomeEnv(t, h.root, h.configDir) {
		key, value, _ := strings.Cut(entry, "=")
		t.Setenv(key, value)
	}
	if variant.major != "" {
		stdout, _ := h.run(t, t.TempDir(), "--version")
		if version := strings.TrimSpace(stdout); !strings.HasPrefix(version, variant.major+".") {
			t.Skipf("%s on PATH is %s, not v%s", variant.binary, version, variant.major)
		}
	}
	for file, content := range variant.seed {
		require.NoError(t, os.MkdirAll(h.configDir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(h.configDir, file), []byte(content), 0o600))
	}
	out, errOut, err := cli(t, "grid", "setup", "--harness", name, "--config-dir", h.configDir, "--use-session-credential", "--yes", "--team", team)
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

func (h *harnessTest) loadConfig(t *testing.T) {
	t.Helper()
	switch h.name {
	case "claude-code":
		h.loadClaudeConfig(t)
	case "codex":
		h.loadCodexConfig(t)
	case "pi":
		h.loadPiConfig(t)
	case "opencode":
		h.loadOpenCodeConfig(t)
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
			for _, level := range harnessEffortLevels {
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
	catalogPath, _ := data["model_catalog_json"].(string)
	require.NotEmpty(t, catalogPath, "codex config.toml has no model_catalog_json")
	catalog := harnessReadJSON(t, catalogPath)
	models, _ := catalog["models"].([]any)
	h.codexEfforts = map[string][]string{}
	for _, entry := range models {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		slug, _ := row["slug"].(string)
		if slug == "" {
			continue
		}
		h.codexSlugs = append(h.codexSlugs, slug)
		levels, _ := row["supported_reasoning_levels"].([]any)
		for _, level := range levels {
			option, _ := level.(map[string]any)
			if effort, _ := option["effort"].(string); effort != "" {
				h.codexEfforts[slug] = append(h.codexEfforts[slug], effort)
			}
		}
	}
}

func (h *harnessTest) loadOpenCodeConfig(t *testing.T) {
	t.Helper()
	data := harnessReadJSON(t, filepath.Join(h.configDir, harnessOpenCodeSettings))
	h.openCodeDefault, _ = data["model"].(string)
	providers, v2 := data["providers"].(map[string]any)
	if !v2 {
		providers, _ = data["provider"].(map[string]any)
	}
	provider, _ := providers[harnessProviderID].(map[string]any)
	require.NotNil(t, provider, "opencode %s has no %s provider", harnessOpenCodeSettings, harnessProviderID)
	optionsKey := "options"
	if v2 {
		optionsKey = "settings"
	}
	options, _ := provider[optionsKey].(map[string]any)
	baseURL, _ := options["baseURL"].(string)
	h.endpoint = strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
	models, _ := provider["models"].(map[string]any)
	for _, key := range slices.Sorted(maps.Keys(models)) {
		row, _ := models[key].(map[string]any)
		model := harnessOpenCodeModel{id: harnessProviderID + "/" + key}
		if v2 {
			pkg, _ := row["package"].(string)
			model.anthropic = strings.Contains(pkg, "anthropic")
			variants, _ := row["variants"].([]any)
			for _, entry := range variants {
				variant, _ := entry.(map[string]any)
				if id, _ := variant["id"].(string); id != "" {
					model.variants = append(model.variants, id)
				}
			}
		} else {
			override, _ := row["provider"].(map[string]any)
			npm, _ := override["npm"].(string)
			model.anthropic = npm == "@ai-sdk/anthropic"
			variants, _ := row["variants"].(map[string]any)
			model.variants = slices.Sorted(maps.Keys(variants))
		}
		h.openCodeModels = append(h.openCodeModels, model)
	}
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

func harnessHomeEnv(t *testing.T, root, configDir string) []string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tmp"), 0o700))
	return []string{
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
	}
}

func harnessProcessEnv(t *testing.T, root, configDir string) []string {
	t.Helper()
	env := append(harnessHomeEnv(t, root, configDir), "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
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
	args = append(slices.Clone(h.prefix), args...)
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

func harnessEventsOf(objects []map[string]any, kind string) []map[string]any {
	var events []map[string]any
	for _, object := range objects {
		if event, _ := object["type"].(string); event == kind {
			events = append(events, object)
		}
	}
	return events
}

func piToolErrors(objects []map[string]any) int {
	count := 0
	for _, event := range harnessEventsOf(objects, "tool_execution_end") {
		if failed, _ := event["isError"].(bool); failed {
			count++
		}
	}
	return count
}

func codexCompletedItems(objects []map[string]any, kind string) []map[string]any {
	var items []map[string]any
	for _, event := range harnessEventsOf(objects, "item.completed") {
		item, _ := event["item"].(map[string]any)
		if itemKind, _ := item["type"].(string); itemKind == kind {
			items = append(items, item)
		}
	}
	return items
}

func codexCompletedItemTypes(objects []map[string]any) []string {
	var kinds []string
	for _, event := range harnessEventsOf(objects, "item.completed") {
		item, _ := event["item"].(map[string]any)
		kind, _ := item["type"].(string)
		kinds = append(kinds, kind)
	}
	return kinds
}

func codexCompletedTools(objects []map[string]any) []string {
	return slices.DeleteFunc(codexCompletedItemTypes(objects), func(kind string) bool {
		return kind != "command_execution" && kind != "file_change"
	})
}

func codexLastAgentMessage(objects []map[string]any) string {
	messages := codexCompletedItems(objects, "agent_message")
	if len(messages) == 0 {
		return ""
	}
	text, _ := messages[len(messages)-1]["text"].(string)
	return text
}

func codexReasoningTokens(objects []map[string]any) float64 {
	total := 0.0
	for _, event := range harnessEventsOf(objects, "turn.completed") {
		usage, _ := event["usage"].(map[string]any)
		tokens, _ := usage["reasoning_output_tokens"].(float64)
		total += tokens
	}
	return total
}

func openCodePart(event map[string]any) map[string]any {
	part, _ := event["part"].(map[string]any)
	return part
}

func openCodeEventText(objects []map[string]any, kind string) string {
	var text strings.Builder
	for _, event := range harnessEventsOf(objects, kind) {
		value, _ := openCodePart(event)["text"].(string)
		text.WriteString(value)
	}
	return text.String()
}

func openCodeReasoningTokens(objects []map[string]any) float64 {
	total := 0.0
	for _, event := range harnessEventsOf(objects, "step_finish") {
		tokens, _ := openCodePart(event)["tokens"].(map[string]any)
		reasoning, _ := tokens["reasoning"].(float64)
		total += reasoning
	}
	return total
}

func openCodeCompletedTools(objects []map[string]any) []string {
	var tools []string
	for _, event := range harnessEventsOf(objects, "tool_use") {
		part := openCodePart(event)
		state, _ := part["state"].(map[string]any)
		if status, _ := state["status"].(string); status == "completed" {
			tool, _ := part["tool"].(string)
			tools = append(tools, tool)
		}
	}
	return tools
}

func openCodeSessionID(objects []map[string]any) string {
	for _, object := range objects {
		if id, _ := object["sessionID"].(string); id != "" {
			return id
		}
	}
	return ""
}

func harnessRouterRoute(model string) bool {
	model = strings.TrimSuffix(model, harnessLongContextSuffix)
	return strings.HasPrefix(model[strings.LastIndex(model, "/")+1:], harnessRouterRoutePrefix)
}

func harnessModelPair(t *testing.T, harness string, ids []string) (string, string) {
	t.Helper()
	ids = slices.DeleteFunc(slices.Clone(ids), harnessRouterRoute)
	if len(ids) < 2 {
		t.Skipf("the team has %d non-router route(s) %s can call; model switching needs at least 2", len(ids), harness)
	}
	return ids[0], ids[1]
}

func harnessDefaultModelArgs(t *testing.T, flag, defaultModel string, models []string) []string {
	t.Helper()
	if !harnessRouterRoute(defaultModel) {
		return nil
	}
	for _, model := range models {
		if !harnessRouterRoute(model) {
			step(t, "default model %s is a router route; using %s", defaultModel, model)
			return []string{flag, model}
		}
	}
	t.Skipf("the default model %s is a router route and the team has no other route (models: %s)", defaultModel, strings.Join(models, ", "))
	return nil
}

func (h *harnessTest) claudeIDs() []string {
	ids := make([]string, 0, len(h.claudeOptions))
	for _, option := range h.claudeOptions {
		ids = append(ids, option.id)
	}
	return ids
}

func (h *harnessTest) claudePair(t *testing.T) (string, string) {
	t.Helper()
	return harnessModelPair(t, "Claude Code", h.claudeIDs())
}

func (h *harnessTest) codexPair(t *testing.T) (string, string) {
	t.Helper()
	return harnessModelPair(t, "Codex", h.codexSlugs)
}

func (h *harnessTest) piPair(t *testing.T) (string, string) {
	t.Helper()
	ids := make([]string, 0, len(h.piModels))
	for _, model := range h.piModels {
		ids = append(ids, model.id)
	}
	return harnessModelPair(t, "Pi", ids)
}

func (h *harnessTest) claudeThinkingModel(t *testing.T) string {
	t.Helper()
	names := make([]string, 0, len(h.claudeOptions))
	for _, option := range h.claudeOptions {
		if _, ok := h.claudeEfforts[option.id]; ok && !harnessRouterRoute(option.id) {
			return option.id
		}
		names = append(names, option.id)
	}
	t.Skipf("the team's routes include no non-router model with an effort ceiling for Claude Code to think with (models: %s)", strings.Join(names, ", "))
	return ""
}

func (h *harnessTest) piAnthropicThinkingModel(t *testing.T) (string, string) {
	t.Helper()
	for _, model := range h.piModels {
		if model.api == "anthropic-messages" && model.reasoning && len(model.levels) > 0 && !harnessRouterRoute(model.id) {
			return model.id, model.levels[0]
		}
	}
	t.Skipf("the team's routes include no non-router Anthropic-format model with reasoning for pi to think with (models: %s)", harnessPiModelNames(h.piModels))
	return "", ""
}

func (h *harnessTest) piNonAnthropicModel(t *testing.T) string {
	t.Helper()
	for _, model := range h.piModels {
		if model.api != "anthropic-messages" && !harnessRouterRoute(model.id) {
			return model.id
		}
	}
	t.Skipf("the team's routes include no non-router, non-Anthropic-format model for pi; Anthropic routes intermittently refuse this prompt with a content filter while pi exits 0 (models: %s)", harnessPiModelNames(h.piModels))
	return ""
}

func harnessPiModelNames(models []harnessPiModel) string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		names = append(names, model.id+"("+model.api+")")
	}
	return strings.Join(names, ", ")
}

func (h *harnessTest) codexThinkingModel(t *testing.T) string {
	t.Helper()
	if slices.Contains(h.codexEfforts[h.codexDefault], "high") && !harnessRouterRoute(h.codexDefault) {
		return h.codexDefault
	}
	for _, slug := range h.codexSlugs {
		if slices.Contains(h.codexEfforts[slug], "high") && !harnessRouterRoute(slug) {
			return slug
		}
	}
	t.Skipf("the team's routes include no non-router model Codex can run at high reasoning effort (models: %s)", strings.Join(h.codexSlugs, ", "))
	return ""
}

func (h *harnessTest) openCodeThinkingModel(t *testing.T) (string, string) {
	t.Helper()
	for _, pick := range []func(harnessOpenCodeModel) bool{
		func(model harnessOpenCodeModel) bool { return model.id == h.openCodeDefault },
		func(model harnessOpenCodeModel) bool { return model.anthropic },
		func(harnessOpenCodeModel) bool { return true },
	} {
		for _, model := range h.openCodeModels {
			if !pick(model) || harnessRouterRoute(model.id) {
				continue
			}
			for _, level := range harnessEffortLevels {
				if slices.Contains(model.variants, level) {
					return model.id, level
				}
			}
		}
	}
	t.Skipf("the team's routes include no non-router model with a reasoning variant for opencode to think with (models: %s)", harnessOpenCodeModelNames(h.openCodeModels))
	return "", ""
}

func (h *harnessTest) openCodeFirstModel(anthropic bool) string {
	for _, model := range h.openCodeModels {
		if model.anthropic == anthropic && !harnessRouterRoute(model.id) {
			return model.id
		}
	}
	return ""
}

func (h *harnessTest) openCodeToolModel(t *testing.T) string {
	t.Helper()
	model := h.openCodeFirstModel(false)
	if model == "" {
		t.Skipf("the team's routes include no non-router, non-Anthropic model for opencode's tool call (models: %s)", harnessOpenCodeModelNames(h.openCodeModels))
	}
	return model
}

func (h *harnessTest) openCodePair(t *testing.T) (string, string) {
	t.Helper()
	modelA, modelB := h.openCodeFirstModel(false), h.openCodeFirstModel(true)
	if modelA != "" && modelB != "" {
		return modelA, modelB
	}
	ids := make([]string, 0, len(h.openCodeModels))
	for _, model := range h.openCodeModels {
		ids = append(ids, model.id)
	}
	return harnessModelPair(t, "opencode", ids)
}

func harnessOpenCodeModelNames(models []harnessOpenCodeModel) string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		name := model.id
		if model.anthropic {
			name += "(anthropic)"
		}
		names = append(names, name)
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
