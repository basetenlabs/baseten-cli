// Package harness translates accessible Routes into local harness settings.
// Its catalog is an internal boundary, not a proposed Routes REST schema.
package harness

import (
	"errors"
	"fmt"
)

type Route struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// TODO: Make the Claude Haiku and OpenCode small-task default server-driven.
const defaultSmallTaskModel = "deepseek-ai/DeepSeek-V4.1-Flash"

type Selection struct {
	Primary    string
	Background string
	Subagent   string
	Fallback   string
}

func (s Selection) resolve(routes []Route) (Selection, error) {
	if len(routes) == 0 {
		return s, errors.New("no accessible routes")
	}
	if s.Primary == "" {
		s.Primary = routes[0].Name
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
