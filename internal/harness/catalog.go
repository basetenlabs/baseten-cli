// Package harness translates accessible Routes into local harness settings.
// Its catalog is an internal boundary, not a proposed Routes REST schema.
package harness

import (
	"errors"
	"fmt"
	"strings"
)

type Route struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

func ValidateCatalog(routes []Route) ([]Route, error) {
	if len(routes) == 0 {
		return nil, errors.New("no accessible routes in catalog")
	}
	seen := map[string]bool{}
	for _, r := range routes {
		if r.Name == "" || strings.TrimSpace(r.Name) != r.Name || strings.ContainsAny(r.Name, "\r\n\t []") || seen[r.Name] {
			return nil, errors.New("catalog has an invalid or duplicate route name")
		}
		// Claude's built-in keywords are interpreted before sending a model ID.
		switch r.Name {
		case "default", "inherit", "opus", "sonnet", "haiku", "fable", "opusplan", "best":
			return nil, fmt.Errorf("route %q conflicts with a Claude model keyword", r.Name)
		}
		if strings.TrimSpace(r.DisplayName) == "" {
			return nil, fmt.Errorf("route %q has no display name", r.Name)
		}
		seen[r.Name] = true
	}
	return routes, nil
}

// TODO: Make the Claude Haiku and OpenCode small-task default server-driven.
const defaultSmallTaskModel = "deepseek-ai/DeepSeek-V4.1-Flash"

type Selection struct {
	Primary    string
	Background string
	Subagent   string
	Fallback   string
}

func (s Selection) Resolve(routes []Route) (Selection, error) {
	if s.Primary == "" {
		return s, errors.New("choose an initial route with --model")
	}
	if s.Background == "" {
		s.Background = s.Primary
	}
	if s.Subagent == "" {
		s.Subagent = s.Primary
	}
	if s.Fallback == "" {
		s.Fallback = s.Primary
	}
	for _, name := range []string{s.Primary, s.Background, s.Subagent, s.Fallback} {
		found := false
		for _, r := range routes {
			if r.Name == name {
				found = true
				break
			}
		}
		if !found {
			return s, fmt.Errorf("selected route %q is absent from the accessible catalog", name)
		}
	}
	return s, nil
}

// SmallTaskModel returns the setup default used by the harness's lightweight tasks.
func SmallTaskModel(name string, selection Selection) string {
	if name == "claude-code" {
		return defaultSmallTaskModel
	}
	if name == "opencode" {
		if selection.Background != "" {
			return selection.Background
		}
		return defaultSmallTaskModel
	}
	return ""
}
