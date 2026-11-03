package harness

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type claudeCodeHarness struct{}

// claudeMarker identifies Baseten's settings, since Claude Code has no named
// provider block to look for.
const claudeMarker = "BASETEN_HARNESS"

var claudeSubagentPath = []string{"env", "CLAUDE_CODE_SUBAGENT_MODEL"}

var claudeCredentialPath = []string{"env", "ANTHROPIC_AUTH_TOKEN"}

// claudeHelperPath is the credential command; a static ANTHROPIC_AUTH_TOKEN
// overrides it.
var claudeHelperPath = []string{"apiKeyHelper"}

var claudeStatusLinePath = []string{"statusLine"}

var claudeStopHooksPath = []string{"hooks", "Stop"}

const claudeServedModelCommand = " grid served-model"

var claudeServedModelPattern = regexp.MustCompile(`^'(?:[^']|'\\'')*'` + claudeServedModelCommand + `(?: --router '(?:[^']|'\\'')*')+(?: --hook)?$`)

const claudeTranscriptWindow = 64 << 10

// extendedContextWindow is the smallest context window Claude Code is told is
// 1M, via a [1m] model ID suffix. Smaller windows get its 200K default.
const extendedContextWindow = 500000

var claudePaths = [][]string{
	{"model"}, {"fallbackModel"}, {"modelPicker", "options"},
	{"modelPicker", "replaceBuiltInOptions"}, {"availableModels"},
	{"modelOverrides"}, {"modelSettings"}, claudeHelperPath,
	{"env", claudeMarker}, {"env", "ANTHROPIC_BASE_URL"},
	{"env", "ANTHROPIC_AUTH_TOKEN"}, {"env", "ANTHROPIC_MODEL"},
	{"env", "ANTHROPIC_DEFAULT_SONNET_MODEL"},
	{"env", "ANTHROPIC_DEFAULT_OPUS_MODEL"}, {"env", "ANTHROPIC_DEFAULT_FABLE_MODEL"},
	{"env", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}, {"env", "ANTHROPIC_SMALL_FAST_MODEL"},
	{"env", "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"}, {"env", "ANTHROPIC_CUSTOM_HEADERS"},
	{"env", "CLAUDE_CODE_GATEWAY_HINT_HEADERS"}, {"env", "ENABLE_TOOL_SEARCH"},
	{"env", "CLAUDE_CODE_MAX_OUTPUT_TOKENS"},
}

func (claudeCodeHarness) Name() string { return ClaudeCode }

func (h claudeCodeHarness) Detect(ctx context.Context, execer Execer, dir string) (Detection, error) {
	dir, err := configDir(dir, "CLAUDE_CONFIG_DIR", ".claude")
	if err != nil {
		return Detection{}, err
	}
	return detect(ctx, execer, h.Name(), []candidate{cliCandidate("claude")}, filepath.Join(dir, "settings.json"))
}

func (claudeCodeHarness) BackgroundRoute(s Selection) string {
	return cmp.Or(s.Background, defaultBackgroundRoute)
}

// Routes keeps the routes that serve the Anthropic Messages API, which Claude
// Code uses for every route.
func (claudeCodeHarness) Routes(routes []Route) []Route {
	return slices.DeleteFunc(slices.Clone(routes), func(r Route) bool { return !r.Messages })
}

func (h claudeCodeHarness) Prepare(path string, routes []Route, s Selection, endpoint string, helper *Helper, cli string) ([]*Plan, error) {
	credentialPath := claudeCredentialPath
	if helper != nil {
		credentialPath = nil
	}
	p, err := prepareSettings(path, credentialPath, func(data map[string]any) ([]setting, error) {
		return claudeSettings(routes, s, endpoint, data, helper, cli)
	})
	if err != nil {
		return nil, err
	}
	p.Replaced = slices.DeleteFunc(p.Replaced, func(key string) bool { return key == pathKey(claudeStopHooksPath) })
	if claudeHasServedModelHook(p.config) {
		p.Notes = append(p.Notes, "Existing statusLine in "+path+" left unchanged; for auto routes, the serving model will be shown after each response through a Stop hook instead.")
	}
	return []*Plan{p}, nil
}

func claudeSettings(routes []Route, selection Selection, endpoint string, current map[string]any, helper *Helper, cli string) ([]setting, error) {
	s, err := selection.Resolve(routes)
	if err != nil {
		return nil, err
	}
	background := cmp.Or(s.Background, defaultBackgroundRoute)
	// Claude Code sizes the context window from the model ID, and
	// modelOverrides sends the plain route name.
	modelID := func(name string) string {
		if r, ok := routeByName(routes, name); ok && r.ContextWindow >= extendedContextWindow {
			return name + "[1m]"
		}
		return name
	}
	options := []any{}
	allowed := []any{}
	overrides := map[string]any{}
	effort := map[string]any{}
	for _, r := range routes {
		id := modelID(r.Name)
		options = append(options, map[string]any{"model": id, "label": r.DisplayName})
		allowed = append(allowed, id)
		if id != r.Name {
			overrides[id] = r.Name
		}
		// Only the ceiling: a default effort would override Claude Code's own.
		if _, high := reasoningBounds(r.ReasoningLevels); high != nil {
			effort[id] = map[string]any{"maxEffortLevel": high}
		}
	}
	if !slices.Contains(allowed, any(modelID(background))) {
		allowed = append(allowed, modelID(background))
	}
	values := []setting{
		desired([]string{"model"}, modelID(s.Primary)),
		desired([]string{"fallbackModel"}, []string{modelID(s.Fallback)}),
		desired([]string{"modelPicker", "options"}, options),
		desired([]string{"modelPicker", "replaceBuiltInOptions"}, true),
		desired([]string{"availableModels"}, allowed),
		desired([]string{"modelOverrides"}, overrides),
		desired([]string{"modelSettings"}, effort),
	}
	for _, kv := range [][2]string{
		{claudeMarker, "1"},
		{"ANTHROPIC_BASE_URL", endpoint},
		{"ANTHROPIC_MODEL", modelID(s.Primary)},
		{"ANTHROPIC_DEFAULT_SONNET_MODEL", modelID(s.Primary)},
		{"ANTHROPIC_DEFAULT_OPUS_MODEL", modelID(s.Primary)},
		{"ANTHROPIC_DEFAULT_FABLE_MODEL", modelID(s.Primary)},
		{"ANTHROPIC_DEFAULT_HAIKU_MODEL", modelID(background)},
		{"ANTHROPIC_SMALL_FAST_MODEL", modelID(background)},
		{"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY", "0"},
		{"ANTHROPIC_CUSTOM_HEADERS", clientHeader + ": " + ClaudeCode},
		{"CLAUDE_CODE_GATEWAY_HINT_HEADERS", "1"},
		{"ENABLE_TOOL_SEARCH", "true"},
	} {
		values = append(values, desired([]string{"env", kv[0]}, kv[1]))
	}
	if helper != nil {
		// Removed, not blanked: a static token overrides the helper.
		values = append(values, setting{Path: claudeCredentialPath}, desired(claudeHelperPath, helper.shellCommand(ClaudeCode)))
	} else {
		// The helper is removed like teardown removes it; a static token
		// overrides it anyway.
		values = append(values, desired(claudeCredentialPath, ""), setting{Path: claudeHelperPath})
	}
	// Output limits have no per-model setting, so the primary route's applies
	// after a /model switch too.
	primary, _ := routeByName(routes, s.Primary)
	values = append(values, desired([]string{"env", "CLAUDE_CODE_MAX_OUTPUT_TOKENS"}, strconv.Itoa(primary.OutputLimit)))
	if selection.Subagent != "" {
		values = append(values, desired(claudeSubagentPath, modelID(s.Subagent)))
	} else if get(current, []string{"env", claudeMarker}).Data == "1" {
		// A refresh may retire the route an earlier --subagent-route-id selected.
		previous, _ := get(current, claudeSubagentPath).Data.(string)
		if _, owned := claudeRoutes(current)[previous]; owned && !slices.Contains(allowed, any(previous)) {
			values = append(values, setting{Path: claudeSubagentPath})
		}
	}
	return append(values, claudeServedModelSettings(routes, current, cli)...), nil
}

// claudeServedModelSettings plans how Claude Code shows which model served a
// router route. With no router routes, it removes whatever setup added before.
// Otherwise, if the status line is unset or already setup's, it writes setup's
// status line there and drops any Stop hook setup added. If the user configured
// their own status line, it leaves that line untouched and keeps exactly one
// Stop hook of setup's instead.
func claudeServedModelSettings(routes []Route, current map[string]any, cli string) []setting {
	routers := claudeRouterFlagValues(routes)
	if len(routers) == 0 {
		return claudeServedModelRemoval(current)
	}
	command := claudeServedModelCommandLine(cli, routers)
	if claudeStatusLineAvailable(current) {
		return append([]setting{desired(claudeStatusLinePath, claudeCommandEntry(command))}, claudeStopHookRemoval(current)...)
	}
	return claudeStopHookInstall(current, command+" --hook")
}

func claudeRouterFlagValues(routes []Route) []string {
	var values []string
	for _, r := range routes {
		switch {
		case r.Router && r.DisplayName != "":
			values = append(values, r.Name+"="+r.DisplayName)
		case r.Router:
			values = append(values, r.Name)
		}
	}
	return values
}

func claudeServedModelCommandLine(cli string, routers []string) string {
	command := shellQuote(cli) + claudeServedModelCommand
	for _, router := range routers {
		command += " --router " + shellQuote(router)
	}
	return command
}

func claudeCommandEntry(command string) map[string]any {
	return map[string]any{"type": "command", "command": command}
}

func claudeStatusLineAvailable(current map[string]any) bool {
	statusLine := get(current, claudeStatusLinePath)
	return !statusLine.Exists || claudeServedModelOwned(statusLine.Data)
}

func claudeServedModelRemoval(current map[string]any) []setting {
	var values []setting
	if claudeServedModelOwned(get(current, claudeStatusLinePath).Data) {
		values = append(values, setting{Path: claudeStatusLinePath})
	}
	return append(values, claudeStopHookRemoval(current)...)
}

func claudeStopHookInstall(current map[string]any, command string) []setting {
	if stop, ok := claudeStopHooksSetting(current, claudeCommandEntry(command)); ok {
		return []setting{stop}
	}
	return nil
}

func claudeStopHookRemoval(current map[string]any) []setting {
	if stop, ok := claudeStopHooksSetting(current, nil); ok {
		return []setting{stop}
	}
	return nil
}

func claudeStopHooksSetting(current, own map[string]any) (setting, bool) {
	stop := get(current, claudeStopHooksPath)
	existing, ok := stop.Data.([]any)
	if (stop.Exists && !ok) || (own == nil && !slices.ContainsFunc(existing, claudeServedModelHook)) {
		return setting{}, false
	}
	var entries []any
	for _, entry := range existing {
		if !claudeServedModelHook(entry) {
			entries = append(entries, entry)
			continue
		}
		group, placed := claudeRewrittenStopGroup(entry, own)
		if placed {
			own = nil
		}
		if group != nil {
			entries = append(entries, group)
		}
	}
	if own != nil {
		entries = append(entries, map[string]any{"hooks": []any{own}})
	}
	if len(entries) == 0 {
		return setting{Path: claudeStopHooksPath}, true
	}
	return desired(claudeStopHooksPath, entries), true
}

func claudeRewrittenStopGroup(entry any, own map[string]any) (map[string]any, bool) {
	group, _ := entry.(map[string]any)
	hooks, _ := group["hooks"].([]any)
	var kept []any
	placed := false
	for _, hook := range hooks {
		switch {
		case !claudeServedModelOwned(hook):
			kept = append(kept, hook)
		case own != nil && !placed:
			kept, placed = append(kept, own), true
		}
	}
	if len(kept) == 0 {
		return nil, placed
	}
	group = maps.Clone(group)
	group["hooks"] = kept
	return group, placed
}

func claudeServedModelOwned(v any) bool {
	entry, _ := v.(map[string]any)
	command, _ := entry["command"].(string)
	return claudeServedModelPattern.MatchString(command)
}

func claudeServedModelHook(entry any) bool {
	group, _ := entry.(map[string]any)
	hooks, _ := group["hooks"].([]any)
	return slices.ContainsFunc(hooks, claudeServedModelOwned)
}

func claudeHasServedModelHook(data map[string]any) bool {
	stop, _ := get(data, claudeStopHooksPath).Data.([]any)
	return slices.ContainsFunc(stop, claudeServedModelHook)
}

func ClaudeServedModel(transcript string) (requested, served string) {
	f, err := os.Open(transcript)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", ""
	}
	size := info.Size()
	for window := min(claudeTranscriptWindow, size); ; window = min(window*4, size) {
		buf := make([]byte, window)
		if _, err := f.ReadAt(buf, size-window); err != nil {
			return "", ""
		}
		var ok bool
		if requested, served, ok = claudeLastServed(buf, window < size); ok || window == size {
			return requested, served
		}
	}
}

func claudeLastServed(window []byte, partial bool) (requested, served string, ok bool) {
	if partial {
		_, window, _ = bytes.Cut(window, []byte("\n"))
	}
	for len(window) > 0 {
		i := bytes.LastIndexByte(window, '\n')
		line := window[i+1:]
		window = window[:max(i, 0)]
		var entry struct {
			Type           string `json:"type"`
			RequestedModel any    `json:"requestedModel"`
			Message        struct {
				Model string `json:"model"`
			} `json:"message"`
		}
		if !bytes.Contains(line, []byte(`"assistant"`)) || json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry.Type == "assistant" && entry.Message.Model != "" && entry.Message.Model != "<synthetic>" {
			alias, _ := entry.RequestedModel.(string)
			return strings.TrimSuffix(alias, "[1m]"), entry.Message.Model, true
		}
	}
	return "", "", false
}

func claudeRoutes(data map[string]any) map[string]string {
	labels := map[string]string{}
	options, _ := get(data, []string{"modelPicker", "options"}).Data.([]any)
	for _, option := range options {
		if row, ok := option.(map[string]any); ok {
			name, _ := row["model"].(string)
			labels[name], _ = row["label"].(string)
		}
	}
	return labels
}

func claudeTeardownPaths(data map[string]any) [][]string {
	if get(data, []string{"env", claudeMarker}).Data != "1" {
		return nil
	}
	paths := slices.Clone(claudePaths)
	// A subagent setting is only Baseten's while it names a configured route.
	subagent, _ := get(data, claudeSubagentPath).Data.(string)
	if _, ok := claudeRoutes(data)[subagent]; ok {
		paths = append(paths, claudeSubagentPath)
	}
	if claudeServedModelOwned(get(data, claudeStatusLinePath).Data) {
		paths = append(paths, claudeStatusLinePath)
	}
	return paths
}

func (claudeCodeHarness) SupportsSubagent() bool { return true }

func (claudeCodeHarness) SupportsHelper() bool { return true }

func (claudeCodeHarness) Credential(path string) (string, error) {
	_, data, err := readConfig(path)
	if err != nil || get(data, []string{"env", claudeMarker}).Data != "1" {
		return "", err
	}
	token, _ := get(data, claudeCredentialPath).Data.(string)
	return token, nil
}

func (claudeCodeHarness) Teardown(path string) ([]*Plan, error) {
	p, err := prepareTeardown(path, claudeTeardownPaths)
	if err != nil {
		return nil, err
	}
	if stop, ok := claudeStopHooksSetting(p.config, nil); ok && p.Managed {
		if err := put(p.config, stop.Path, stop.Installed); err != nil {
			return nil, err
		}
		p.Keys = append(p.Keys, pathKey(stop.Path))
		if err := p.encode(); err != nil {
			return nil, err
		}
	}
	return []*Plan{p}, nil
}

func (claudeCodeHarness) Inspect(d Detection) (Status, error) {
	r, data, err := inspectConfig(d)
	if err != nil {
		return r, err
	}
	r.managed(data, claudeTeardownPaths(data))
	if r.State == StateNotConfigured {
		return r, nil
	}
	if claudeHasServedModelHook(data) {
		r.Managed = append(r.Managed, pathKey(claudeStopHooksPath))
	}
	r.BackgroundRoute, _ = get(data, []string{"env", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}).Data.(string)
	r.addRoutes(claudeRoutes(data))
	return r, nil
}
