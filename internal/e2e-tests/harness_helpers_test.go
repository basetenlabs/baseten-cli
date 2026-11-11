//go:build e2e && (linux || darwin)

package e2etests

import (
	"bytes"
	"cmp"
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
	harnessLongContextSuffix = "[1m]"

	harnessRouter    = "router"
	harnessBaseten   = "baseten"
	harnessAnthropic = "anthropic"
	harnessOpenAI    = "openai"
	harnessOther     = "other"

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

var harnessTargetClasses = map[string]string{
	"BASETEN_MODEL_API": harnessBaseten,
	"CLASSIFIER":        harnessRouter,
	"INTELLIGENT":       harnessRouter,
	"ANTHROPIC":         harnessAnthropic,
	"OPENAI":            harnessOpenAI,
}

type harnessModel struct {
	id     string
	route  string
	class  string
	levels []string
}

type harnessNeed struct {
	name string
	ok   func(harnessModel) bool
}

var (
	harnessAnyModel   = harnessNeed{"no extra requirement", func(harnessModel) bool { return true }}
	harnessReasoning  = harnessNeed{"reasoning levels", func(model harnessModel) bool { return len(model.levels) > 0 }}
	harnessHighEffort = harnessNeed{"effort high", func(model harnessModel) bool { return slices.Contains(model.levels, "high") }}
)

type harnessTest struct {
	name      string
	binary    string
	prefix    []string
	opencode2 bool
	root      string
	configDir string
	endpoint  string
	models    []harnessModel
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
	{label: "opencode1"},
	{label: "opencode2/v1-config", opencode2: true},
	{label: "opencode2/v2-config", opencode2: true, v2Config: true},
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
	h := newHarnessVariantTest(t, "opencode", spec)
	h.opencode2 = true
	return h
}

func newHarnessVariantTest(t *testing.T, name string, variant harnessVariant) *harnessTest {
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
	case "claude":
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
	out, errOut, err := cli(t, "grid", "setup", "--harness", name, "--config-dir", h.configDir, "--use-session-credential", "--yes")
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
	classes := harnessRouteClasses(t)
	for i := range h.models {
		h.models[i].class = cmp.Or(classes[h.models[i].route], harnessOther)
	}
	return h
}

func harnessRouteClasses(t *testing.T) map[string]string {
	t.Helper()
	providers := map[string]string{}
	for _, connection := range harnessCLIItems(t, "grid", "connection", "list", "--output", "json") {
		id, _ := connection["id"].(string)
		config, _ := connection["config"].(map[string]any)
		providers[id], _ = config["provider"].(string)
	}
	classes := map[string]string{}
	for _, route := range harnessCLIItems(t, "grid", "route", "list", "--output", "json") {
		name, _ := route["name"].(string)
		target, _ := route["target"].(map[string]any)
		kind, _ := target["type"].(string)
		if connection, ok := target["connection_id"].(string); ok {
			kind = providers[connection]
		}
		classes[name] = harnessTargetClasses[kind]
	}
	return classes
}

func harnessCLIItems(t *testing.T, args ...string) []map[string]any {
	t.Helper()
	out, errOut, err := cli(t, args...)
	require.NoError(t, err, "baseten %s: %s", strings.Join(args, " "), errOut)
	var list struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &list), "parsing the output of baseten %s", strings.Join(args, " "))
	return list.Items
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
	case "claude":
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
	settings, _ := data["modelSettings"].(map[string]any)
	if picker, ok := data["modelPicker"].(map[string]any); ok {
		options, _ := picker["options"].([]any)
		for _, entry := range options {
			row, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			id, _ := row["model"].(string)
			if id == "" {
				continue
			}
			effort, _ := settings[id].(map[string]any)
			ceiling, _ := effort["maxEffortLevel"].(string)
			h.models = append(h.models, harnessModel{id: id, route: strings.TrimSuffix(id, harnessLongContextSuffix), levels: harnessLevels(ceiling)})
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
		var levels []string
		thinking, _ := row["thinkingLevelMap"].(map[string]any)
		for level, value := range thinking {
			if value != nil {
				levels = append(levels, level)
			}
		}
		h.models = append(h.models, harnessModel{id: id, route: id, levels: harnessLevels(levels...)})
	}
}

func (h *harnessTest) loadCodexConfig(t *testing.T) {
	t.Helper()
	data := harnessReadTOML(t, filepath.Join(h.configDir, "config.toml"))
	providers, _ := data["model_providers"].(map[string]any)
	provider, _ := providers[harnessProviderID].(map[string]any)
	require.NotNil(t, provider, "codex config.toml has no %s provider", harnessProviderID)
	baseURL, _ := provider["base_url"].(string)
	h.endpoint = strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
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
		if slug == "" {
			continue
		}
		var efforts []string
		levels, _ := row["supported_reasoning_levels"].([]any)
		for _, level := range levels {
			option, _ := level.(map[string]any)
			effort, _ := option["effort"].(string)
			efforts = append(efforts, effort)
		}
		h.models = append(h.models, harnessModel{id: slug, route: slug, levels: harnessLevels(efforts...)})
	}
}

func (h *harnessTest) loadOpenCodeConfig(t *testing.T) {
	t.Helper()
	data := harnessReadJSON(t, filepath.Join(h.configDir, harnessOpenCodeSettings))
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
		var variants []string
		if v2 {
			entries, _ := row["variants"].([]any)
			for _, entry := range entries {
				variant, _ := entry.(map[string]any)
				id, _ := variant["id"].(string)
				variants = append(variants, id)
			}
		} else {
			entries, _ := row["variants"].(map[string]any)
			variants = slices.Collect(maps.Keys(entries))
		}
		h.models = append(h.models, harnessModel{id: harnessProviderID + "/" + key, route: key, levels: harnessLevels(variants...)})
	}
}

func harnessLevels(levels ...string) []string {
	return slices.DeleteFunc(slices.Clone(harnessEffortLevels), func(level string) bool {
		return !slices.Contains(levels, level)
	})
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

func codexSearched(objects []map[string]any) bool {
	searched := false
	harnessWalkObjects(objects, func(object map[string]any) {
		kind, _ := object["type"].(string)
		if strings.Contains(kind, "web_search") {
			searched = true
		}
	})
	return searched
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

func (h *harnessTest) pickModel(t *testing.T, class string, need harnessNeed) harnessModel {
	t.Helper()
	for _, model := range h.models {
		if model.class == class && need.ok(model) {
			step(t, "%s picked %s model %s (%s)", h.name, class, model.id, need.name)
			return model
		}
	}
	names := make([]string, 0, len(h.models))
	for _, model := range h.models {
		names = append(names, model.id+"("+model.class+")")
	}
	t.Skipf("the team has no %s route %s can call with %s (models: %s)", class, h.name, need.name, strings.Join(names, ", "))
	return harnessModel{}
}

func (h *harnessTest) thinkingNeed() harnessNeed {
	if h.name == "codex" {
		return harnessHighEffort
	}
	return harnessReasoning
}

func (h *harnessTest) thinkingArgs(model harnessModel) []string {
	switch {
	case h.name == "codex":
		return []string{"-m", model.id, "-c", "model_reasoning_effort=high"}
	case h.name == "pi":
		return []string{"--thinking", model.levels[0], "--model", model.id}
	case h.opencode2:
		return []string{"--thinking", "-m", model.id + "#" + model.levels[0]}
	case h.name == "opencode":
		return []string{"--thinking", "--variant", model.levels[0], "-m", model.id}
	}
	return []string{"--model", model.id}
}

func (h *harnessTest) searches() bool {
	return h.name == "claude" || h.name == "codex"
}

func claudeAssertThinking(t *testing.T, model harnessModel, events []claudeStreamEvent, stdout string) {
	t.Helper()
	blocks := claudeThinkingBlocks(events)
	step(t, "claude %s: thinking blocks %d", model.id, blocks)
	require.Greater(t, blocks, 0, "stream-json shows no thinking block under %s, so thinking never engaged; stdout:\n%s", model.id, stdout)
}

func codexAssertThinking(t *testing.T, model harnessModel, objects []map[string]any, stdout string) {
	t.Helper()
	tokens := codexReasoningTokens(objects)
	step(t, "codex %s: reasoning items %d, reasoning_output_tokens %v", model.id, len(codexCompletedItems(objects, "reasoning")), tokens)
	require.Greater(t, tokens, 0.0, "codex reports no reasoning_output_tokens under %s at high effort, so thinking never engaged; stdout:\n%s", model.id, stdout)
}

func piAssertThinking(t *testing.T, model harnessModel, objects []map[string]any, stdout string) {
	t.Helper()
	seen := piThinkingSeen(objects)
	step(t, "pi %s: thinking seen %v", model.id, seen)
	require.True(t, seen, "pi's stream shows no thinking or reasoning event under %s; stdout:\n%s", model.id, stdout)
}

func openCodeAssertThinking(t *testing.T, model harnessModel, objects []map[string]any, stdout string) {
	t.Helper()
	events, reasoning, tokens := len(harnessEventsOf(objects, "reasoning")), openCodeEventText(objects, "reasoning"), openCodeReasoningTokens(objects)
	step(t, "opencode %s: reasoning events %d, reasoning text %d chars, step_finish reasoning tokens %v", model.id, events, len(reasoning), tokens)
	if model.class == harnessOpenAI {
		require.Greater(t, tokens, 0.0, "opencode reports no reasoning tokens under %s (reasoning events %d); stdout:\n%s", model.id, events, stdout)
		return
	}
	require.NotEmpty(t, reasoning, "opencode streamed no reasoning text under %s (reasoning events %d, step_finish reasoning tokens %v); stdout:\n%s", model.id, events, tokens, stdout)
}

func (h *harnessTest) switchTurn(t *testing.T, workspace string, model harnessModel, session, prompt string) string {
	t.Helper()
	switch h.name {
	case "claude":
		return h.claudeTurn(t, workspace, model, session, prompt)
	case "codex":
		return h.codexTurn(t, workspace, model, session, prompt)
	case "pi":
		return h.piTurn(t, workspace, model, session, prompt)
	}
	return h.openCodeTurn(t, workspace, model, session, prompt)
}

func (h *harnessTest) claudeTurn(t *testing.T, workspace string, model harnessModel, session, prompt string) string {
	t.Helper()
	args := append([]string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", "--allowedTools", "WebSearch"}, h.thinkingArgs(model)...)
	if session != "" {
		args = append(args, "--resume", session)
	}
	stdout, stderr := h.run(t, workspace, append(args, prompt)...)
	events := parseClaudeStream(stdout)
	harnessAssertNoAPIError(t, stdout+stderr)
	sessionID, _, ok := claudeResult(events)
	require.True(t, ok, "stream-json has no result event; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	uses := claudeToolUses(events)
	require.Contains(t, uses, "WebSearch", "claude never invoked WebSearch; tool uses: %v; stdout:\n%s", uses, stdout)
	require.True(t, slices.ContainsFunc(uses, func(use string) bool { return use != "WebSearch" }), "claude ran no tool besides WebSearch; tool uses: %v; stdout:\n%s", uses, stdout)
	claudeAssertThinking(t, model, events, stdout)
	require.NotEmpty(t, sessionID, "claude's result event has no session_id; stdout:\n%s", stdout)
	return sessionID
}

func (h *harnessTest) codexTurn(t *testing.T, workspace string, model harnessModel, session, prompt string) string {
	t.Helper()
	args := []string{"exec"}
	if session != "" {
		args = append(args, "resume", session)
	}
	args = append(append(args, "--json", "--skip-git-repo-check", "-c", "sandbox_mode=danger-full-access", "-c", "web_search=live"), h.thinkingArgs(model)...)
	stdout, stderr := h.run(t, workspace, append(args, prompt)...)
	harnessAssertNoAPIError(t, stdout+stderr)
	objects := harnessJSONObjects(stdout)
	step(t, "codex completed items: %v", codexCompletedItemTypes(objects))
	for _, item := range codexCompletedItems(objects, "error") {
		step(t, "codex error item: %v", item["message"])
	}
	require.NotEmpty(t, codexCompletedTools(objects), "codex completed no command_execution or file_change item; stdout:\n%s", stdout)
	require.True(t, codexSearched(objects), "codex emitted no web_search event; stdout:\n%s", stdout)
	codexAssertThinking(t, model, objects, stdout)
	threadID := cmp.Or(session, codexThreadID(objects))
	require.NotEmpty(t, threadID, "codex emitted no thread.started thread_id; stdout:\n%s", stdout)
	return threadID
}

func (h *harnessTest) piTurn(t *testing.T, workspace string, model harnessModel, session, prompt string) string {
	t.Helper()
	sessionDir := cmp.Or(session, t.TempDir())
	args := []string{"-p", "--mode", "json", "--offline", "--session-dir", sessionDir}
	if session != "" {
		args = append(args, "--continue")
	}
	stdout, stderr := h.run(t, workspace, append(append(args, h.thinkingArgs(model)...), prompt)...)
	harnessAssertNoAPIError(t, stdout+stderr)
	objects := harnessJSONObjects(stdout)
	require.Zero(t, piErrorTurns(objects), "pi ended a turn with an API error; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	require.NotEmpty(t, harnessEventsOf(objects, "tool_execution_start"), "pi made no tool calls; stdout:\n%s", stdout)
	step(t, "pi %s: tool errors %d", model.id, piToolErrors(objects))
	piAssertThinking(t, model, objects, stdout)
	return sessionDir
}

func (h *harnessTest) openCodeTurn(t *testing.T, workspace string, model harnessModel, session, prompt string) string {
	t.Helper()
	args := append([]string{"run", "--format", "json", "--auto"}, h.thinkingArgs(model)...)
	if session != "" {
		args = append(args, "-s", session)
	}
	stdout, stderr := h.run(t, workspace, append(args, prompt)...)
	harnessAssertNoAPIError(t, stdout+stderr)
	objects := harnessJSONObjects(stdout)
	require.Empty(t, harnessEventsOf(objects, "error"), "opencode emitted error events; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	require.NotEmpty(t, openCodeCompletedTools(objects), "opencode completed no tool calls; stdout:\n%s", stdout)
	openCodeAssertThinking(t, model, objects, stdout)
	sessionID := cmp.Or(session, openCodeSessionID(objects))
	require.NotEmpty(t, sessionID, "opencode emitted no sessionID; stdout:\n%s", stdout)
	return sessionID
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
