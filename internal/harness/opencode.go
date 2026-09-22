package harness

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

type openCodeHarness struct{ baseHarness }

func (h openCodeHarness) Prepare(path string, routes []Route, s Selection, endpoint, token string) ([]*Plan, error) {
	explicitSubagent := s.Subagent != ""
	defaultOpenCodeBackground := s.Background == ""

	if len(routes) == 0 {
		return nil, errors.New("no accessible routes")
	}
	if s.Fallback != "" {
		return nil, errors.New("--fallback-route is supported only for Claude Code")
	}
	var err error
	s, err = s.resolve(routes)
	if err != nil {
		return nil, err
	}

	if filepath.Ext(path) != ".json" && filepath.Ext(path) != ".jsonc" {
		return nil, errors.New("OpenCode --config must name a .json or .jsonc file")
	}
	models := map[string]any{}
	if defaultOpenCodeBackground {
		s.Background = defaultSmallTaskModel
		models[defaultSmallTaskModel] = map[string]any{"name": "DeepSeek V4.1 Flash"}
	}
	for _, r := range routes {
		models[r.Name] = map[string]any{"name": r.DisplayName}
	}
	values := []setting{
		desired([]string{"model"}, providerID+"/"+s.Primary),
		desired([]string{"small_model"}, providerID+"/"+s.Background),
		desired([]string{"provider", providerID}, map[string]any{
			"npm":  "@ai-sdk/openai-compatible",
			"name": "Baseten",
			"options": map[string]any{
				"baseURL": strings.TrimRight(endpoint, "/") + "/v1",
				"apiKey":  token,
			},
			"models": models,
		}),
	}
	if explicitSubagent {
		for _, role := range []string{"general", "explore"} {
			values = append(values, desired([]string{"agent", role, "model"}, providerID+"/"+s.Subagent))
		}
	}

	p, err := prepareSettings(path, routes, func(data map[string]any) ([]setting, error) {
		if _, ok := data["providers"]; ok {
			return nil, errors.New("OpenCode V2 providers require a separately verified adapter")
		}
		username := ""
		if runtime.GOOS == "darwin" {
			current, err := user.Current()
			if err != nil {
				return nil, err
			}
			username = current.Username
		}
		for _, policy := range openCodePolicyPaths(runtime.GOOS, username) {
			if _, err := os.Stat(policy); err == nil {
				return nil, fmt.Errorf("managed policy detected at %s", policy)
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		return values, nil
	})
	if err != nil {
		return nil, err
	}
	p.credentialPath = []string{"provider", providerID, "options", "apiKey"}
	return []*Plan{p}, nil
}

func (h openCodeHarness) SmallTaskModel(s Selection) string {
	if s.Background != "" {
		return s.Background
	}
	return defaultSmallTaskModel
}

func openCodePolicyPaths(platform, username string) []string {
	dir := "/etc/opencode"
	var paths []string
	if platform == "darwin" {
		dir = "/Library/Application Support/opencode"
		paths = append(paths,
			filepath.Join("/Library/Managed Preferences", username, "ai.opencode.managed.plist"),
			"/Library/Managed Preferences/ai.opencode.managed.plist",
		)
	} else if platform == "windows" {
		root := os.Getenv("ProgramData")
		if root == "" {
			root = `C:\ProgramData`
		}
		dir = filepath.Join(root, "opencode")
	}
	return append(paths, filepath.Join(dir, "opencode.json"), filepath.Join(dir, "opencode.jsonc"))
}

func (h openCodeHarness) Inspect(d Detection) (Status, error) {
	r, data, j, err := inspectConfig(d)
	if err != nil {
		return r, err
	}
	r.DefaultRoute = strings.TrimPrefix(r.DefaultRoute, providerID+"/")
	small, _ := data["small_model"].(string)
	r.SmallTaskModel = strings.TrimPrefix(small, providerID+"/")
	labels := map[string]string{}
	if models, ok := get(data, []string{"provider", providerID, "models"}).Data.(map[string]any); ok {
		for name, model := range models {
			if row, ok := model.(map[string]any); ok {
				labels[name], _ = row["name"].(string)
			}
		}
	}
	r.addRoutes(j, labels)
	return r, nil
}
