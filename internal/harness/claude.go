package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const TestedClaudeVersion = "2.1.272"

var TestedVersions = map[string]string{"claude-code": TestedClaudeVersion, "codex": "0.134.0", "opencode": "1.18.31"}

const FixtureToken = "baseten-harness-local-fixture"

type Detection struct {
	Name      string `json:"harness"`
	Path      string `json:"config"`
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	Supported bool   `json:"supported"`
}

// Execer is satisfied by the CLI's shared subprocess executor.
type Execer interface {
	LookPath(string) (string, error)
	Exec(*exec.Cmd) error
}

func Detect(ctx context.Context, executor Execer, name, path string) (Detection, error) {
	d := Detection{Name: name, Path: path}
	binaryName := name
	switch name {
	case "claude-code":
		binaryName = "claude"
	case "codex", "opencode":
	default:
		return d, fmt.Errorf("unknown harness %s", name)
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return d, err
		}
		switch name {
		case "claude-code":
			dir := os.Getenv("CLAUDE_CONFIG_DIR")
			if dir == "" {
				dir = filepath.Join(home, ".claude")
			}
			d.Path = filepath.Join(dir, "settings.json")
		case "codex":
			dir := os.Getenv("CODEX_HOME")
			if dir == "" {
				dir = filepath.Join(home, ".codex")
			}
			d.Path = filepath.Join(dir, "config.toml")
		case "opencode":
			dir := os.Getenv("XDG_CONFIG_HOME")
			if dir == "" {
				dir = filepath.Join(home, ".config")
			}
			d.Path = filepath.Join(dir, "opencode", "opencode.json")
		}
	}
	var err error
	d.Path, err = filepath.Abs(d.Path)
	if err != nil {
		return d, err
	}
	binary, err := executor.LookPath(binaryName)
	if err != nil {
		return d, nil
	}
	d.Installed = true
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	command := exec.CommandContext(ctx, binary, "--version")
	command.Stdout = &out
	err = executor.Exec(command)
	if err != nil {
		return d, nil
	}
	v := strings.TrimSpace(out.String())
	if len(v) > 100 || strings.ContainsAny(v, "\r\n\x1b") {
		return d, nil
	}
	d.Version = strings.TrimPrefix(strings.TrimSuffix(v, " (Claude Code)"), "codex-cli ")
	d.Supported = d.Version == TestedVersions[name] && runtime.GOOS == "darwin"
	return d, nil
}
func FixtureEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Port() == "" {
		return errors.New("fixture endpoint must use loopback HTTP: http://127.0.0.1:<port> or http://[::1]:<port>")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return errors.New("fixture endpoint requires a valid port")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("fixture endpoint must use a literal loopback IP")
	}
	return nil
}

// ClaudeSettings consumes normalized capabilities. It never guesses a model's
// capabilities or identity from the backing provider or target model name.
func ClaudeSettings(routes []Route, selection Selection, endpoint, token string, replacePicker bool, current map[string]any, prior *Journal) ([]Setting, error) {
	if _, err := ValidateCatalog(routes); err != nil {
		return nil, err
	}
	for _, r := range routes {
		if !r.Messages {
			return nil, fmt.Errorf("Route %q lacks verified Messages support", r.Name)
		}
	}
	explicitSubagent := selection.Subagent != ""
	s, err := selection.Resolve(routes)
	if err != nil {
		return nil, err
	}
	if token == "" || strings.ContainsAny(token, "\r\n\t ") {
		return nil, errors.New("missing or invalid harness credential")
	}
	for _, key := range []string{"apiKeyHelper", "modelOverrides"} {
		if _, ok := current[key]; ok {
			return nil, fmt.Errorf("existing %s must be resolved before setup", key)
		}
	}
	blocked := []string{"ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"}
	for _, key := range blocked {
		if get(current, []string{"env", key}).Exists || os.Getenv(key) != "" {
			return nil, fmt.Errorf("%s overrides harness configuration; resolve it before setup", key)
		}
	}
	options := []any{}
	allowed := []any{}
	// Preserve the original user entries on reruns, rather than accumulating our
	// old catalog. The journal tracks ownership of the two complete arrays.
	for _, item := range []struct {
		path []string
		dst  *[]any
	}{{[]string{"modelPicker", "options"}, &options}, {[]string{"availableModels"}, &allowed}} {
		v := get(current, item.path)
		if prior != nil {
			for _, p := range prior.Settings {
				if pathKey(p.Path) == pathKey(item.path) {
					v = p.Before
				}
			}
		}
		if v.Exists {
			a, ok := v.Data.([]any)
			if !ok {
				return nil, fmt.Errorf("%s must be an array", pathKey(item.path))
			}
			*item.dst = append(*item.dst, a...)
		}
	}
	for _, r := range routes {
		found := false
		for _, o := range options {
			m, ok := o.(map[string]any)
			if !ok {
				return nil, errors.New("invalid existing picker entry")
			}
			if m["model"] == r.Name {
				found = true
			}
		}
		if !found {
			options = append(options, map[string]any{"model": r.Name, "label": r.DisplayName})
		}
		found = false
		for _, a := range allowed {
			if _, ok := a.(string); !ok {
				return nil, errors.New("availableModels must contain strings")
			}
			if a == r.Name {
				found = true
			}
		}
		if !found {
			allowed = append(allowed, r.Name)
		}
	}
	values := []Setting{
		desired([]string{"model"}, s.Primary),
		desired([]string{"fallbackModel"}, []string{s.Fallback}),
		desired([]string{"modelPicker", "options"}, options),
		desired([]string{"modelPicker", "replaceBuiltInOptions"}, replacePicker),
		desired([]string{"availableModels"}, allowed),
	}
	for _, kv := range [][2]string{{"ANTHROPIC_BASE_URL", endpoint}, {"ANTHROPIC_AUTH_TOKEN", token}, {"ANTHROPIC_DEFAULT_SONNET_MODEL", s.Primary}, {"ANTHROPIC_DEFAULT_OPUS_MODEL", s.Primary}, {"ANTHROPIC_DEFAULT_FABLE_MODEL", s.Primary}, {"ANTHROPIC_DEFAULT_HAIKU_MODEL", s.Background}, {"ANTHROPIC_SMALL_FAST_MODEL", s.Background}, {"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY", "0"}} {
		values = append(values, desired([]string{"env", kv[0]}, kv[1]))
	}
	if explicitSubagent {
		values = append(values, desired([]string{"env", "CLAUDE_CODE_SUBAGENT_MODEL"}, s.Subagent))
	}
	return values, nil
}

func CheckPolicy(path string) error {
	for _, p := range []string{filepath.Join(filepath.Dir(path), "managed-settings.json"), "/Library/Application Support/ClaudeCode/managed-settings.json", "/etc/claude-code/managed-settings.json"} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("managed policy detected at %s; ask your administrator to configure the harness", p)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
