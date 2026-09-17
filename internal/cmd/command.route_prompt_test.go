package cmd_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

type routePromptStep struct {
	kind, title, answer string
	err                 error
}

type scriptedRoutePrompter struct {
	h             *CommandHarness
	steps         []routePromptStep
	options       [][]cmd.RoutePromptOption
	initialInputs []string
}

func routePrompts(h *CommandHarness, steps ...routePromptStep) *scriptedRoutePrompter {
	p := &scriptedRoutePrompter{h: h, steps: steps}
	h.Context = cmd.WithRoutePrompter(h.Context, p)
	h.T.Cleanup(func() { h.Require.Empty(p.steps, "unconsumed prompts") })
	return p
}

func (p *scriptedRoutePrompter) next(kind, title string) routePromptStep {
	p.h.T.Helper()
	p.h.Require.NotEmpty(p.steps, "unexpected %s prompt: %s", kind, title)
	step := p.steps[0]
	p.steps = p.steps[1:]
	p.h.Require.Equal(step.kind, kind)
	p.h.Require.Contains(title, step.title)
	return step
}

func (p *scriptedRoutePrompter) Input(title string, value *string) error {
	p.initialInputs = append(p.initialInputs, *value)
	step := p.next("input", title)
	if step.err == nil {
		*value = step.answer
	}
	return step.err
}

func (p *scriptedRoutePrompter) Password(title string, value *string) error {
	step := p.next("password", title)
	if step.err == nil {
		*value = step.answer
	}
	return step.err
}

func (p *scriptedRoutePrompter) OptionalInput(title string, value *string, maxLength int) error {
	step := p.next("optional", title)
	if step.err == nil {
		*value = step.answer
	}
	return step.err
}

func (p *scriptedRoutePrompter) Select(title string, options []cmd.RoutePromptOption, value *string) error {
	step := p.next("select", title)
	p.options = append(p.options, options)
	if step.err != nil {
		return step.err
	}
	for _, option := range options {
		if option.Value == step.answer {
			*value = option.Value
			return nil
		}
	}
	return fmt.Errorf("scripted choice %q not offered", step.answer)
}

func (p *scriptedRoutePrompter) Confirm(title string) error { return p.next("confirm", title).err }

func Test_Route_Interactive_Create_AllTargets(t *testing.T) {
	for _, tc := range routeCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			// Duplicate names prove the dropdown passes the selected stable ID.
			m.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []any{teamFixture("t-other", "Engineering", false), teamFixture("t123456", "Engineering", true)}})
			m.SetRoute("POST", "/v1/routes", 200, tc.Response)
			target := tc.Create["target"].(map[string]any)
			steps := []routePromptStep{{kind: "select", title: "owning team", answer: "t123456"}, {kind: "input", title: "Route slug", answer: "acme/assistant"}, {kind: "optional", title: "Display name"}, {kind: "optional", title: "Description"}}
			if tc.Name == "model-api" {
				steps = append(steps, routePromptStep{kind: "select", title: "Target type", answer: "model-api"}, routePromptStep{kind: "input", title: "Model API", answer: target["model_api"].(string)})
			} else {
				steps = append(steps, routePromptStep{kind: "select", title: "Target type", answer: "provider"}, routePromptStep{kind: "select", title: "External provider", answer: tc.Name}, routePromptStep{kind: "input", title: "Provider model", answer: "test-model"})
				if tc.Name == "vertex" {
					steps = append(steps, routePromptStep{kind: "input", title: "project", answer: "example-project"}, routePromptStep{kind: "input", title: "location", answer: "global"})
				}
				if tc.Name == "openai-compatible" {
					steps = append(steps, routePromptStep{kind: "input", title: "HTTPS", answer: "https://api.example.com/v1"})
				}
				steps = append(steps, routePromptStep{kind: "input", title: "secret name", answer: "provider-key"})
			}
			p := routePrompts(h, steps...)
			h.Require.NoError(h.Execute("route", "create", "--output", "json"))
			expected := tc.Create
			delete(expected, "display_name")
			h.Require.Equal(expected, m.FindCall("POST", "/v1/routes").BodyJSON(t))
			h.Require.Equal([]cmd.RoutePromptOption{{Label: "Engineering (t-other)", Value: "t-other"}, {Label: "Engineering (t123456)", Value: "t123456"}}, p.options[0])
			var out map[string]any
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &out))
			h.Require.Equal("r123456", out["id"])
		})
	}
}

func Test_Route_Interactive_Create_OnlyMissingInputs(t *testing.T) {
	h := NewCommandHarness(t)
	routeTeams(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/routes", 200, routeCases(t)[2].Response)
	routePrompts(h, routePromptStep{kind: "optional", title: "Display name"}, routePromptStep{kind: "optional", title: "Description"}, routePromptStep{kind: "input", title: "secret name", answer: "provider-key"})
	h.Require.NoError(h.Execute("route", "create", "--team", "Engineering", "--name", "acme/assistant", "--target-provider", "openai", "--target-provider-model", "test-model"))
	h.Require.Equal("OPENAI", m.FindCall("POST", "/v1/routes").BodyJSON(t)["target"].(map[string]any)["type"])
}

func Test_Route_Interactive_FullySpecifiedSkipsPrompts(t *testing.T) {
	for _, args := range [][]string{
		{"create", "--team", "Engineering", "--name", "acme/assistant", "--target-model-api", "model", "--display-name", "Assistant", "--description", ""},
		{"describe", "--id", "r123456"},
		{"update", "--id", "r123456", "--display-name", "Label"},
		{"update", "--id", "r123456", "--target-model-api", "model"},
		{"delete", "--id", "r123456", "--yes"},
		{"list"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := NewCommandHarness(t)
			routeTeams(h)
			m := h.MockManagementAPI()
			m.SetHandlerFallback(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" && r.URL.Path == "/v1/routes" {
					_ = json.NewEncoder(w).Encode(routePage([]any{}, nil))
				} else {
					_ = json.NewEncoder(w).Encode(routeCases(t)[0].Response)
				}
			})
			routePrompts(h)
			h.Require.NoError(h.Execute(append([]string{"route"}, args...)...))
		})
	}
}

func Test_Route_Interactive_Describe_PagesDropdown(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	fixture := routeCases(t)[0].Response
	m.SetRouteFunc("GET", "/v1/routes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(routePage([]any{map[string]any{"id": "other", "name": "acme/other", "team_id": "team2"}}, "next"))
		} else {
			_ = json.NewEncoder(w).Encode(routePage([]any{fixture}, nil))
		}
	})
	m.SetRoute("GET", "/v1/routes/r123456", 200, fixture)
	p := routePrompts(h, routePromptStep{kind: "select", title: "Choose a Route", answer: "r123456"})
	h.Require.NoError(h.Execute("route", "describe"))
	h.Require.Len(p.options[0], 2)
	h.Require.Contains(p.options[0][1].Label, "acme/assistant")
	h.Require.Len(m.Calls(), 4)
}

func Test_Route_Interactive_Update_Choices(t *testing.T) {
	for _, change := range []string{"label", "target", "both"} {
		t.Run(change, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			fixture := routeCases(t)[0].Response
			m.SetRoute("GET", "/v1/routes", 200, routePage([]any{fixture}, nil))
			m.SetRoute("PATCH", "/v1/routes/r123456", 200, fixture)
			steps := []routePromptStep{{kind: "select", title: "Choose a Route", answer: "r123456"}, {kind: "select", title: "update", answer: change}}
			expected := map[string]any{}
			if change != "target" {
				steps = append(steps, routePromptStep{kind: "input", title: "display name", answer: "New label"})
				expected["display_name"] = "New label"
			}
			if change != "label" {
				steps = append(steps, routePromptStep{kind: "select", title: "Target type", answer: "model-api"}, routePromptStep{kind: "input", title: "Model API", answer: "replacement"})
				expected["target"] = map[string]any{"type": "BASETEN_MODEL_API", "model_api": "replacement"}
			}
			routePrompts(h, steps...)
			h.Require.NoError(h.Execute("route", "update"))
			h.Require.Equal(expected, m.FindCall("PATCH", "/v1/routes/r123456").BodyJSON(t))
			h.Require.Nil(m.FindCall("GET", "/v1/routes/r123456"), "do not fetch or merge old target")
		})
	}
}

func Test_Route_Interactive_CancellationNeverMutates(t *testing.T) {
	cancelled := errors.New("cancelled")
	for _, tc := range []struct {
		args  []string
		steps []routePromptStep
	}{
		{[]string{"create"}, []routePromptStep{{kind: "select", title: "owning team", err: cancelled}}},
		{[]string{"create", "--team", "Engineering"}, []routePromptStep{{kind: "input", title: "Route slug", err: cancelled}}},
		{[]string{"update", "--id", "r123456"}, []routePromptStep{{kind: "select", title: "update", answer: "target"}, {kind: "select", title: "Target type", err: cancelled}}},
		{[]string{"delete"}, []routePromptStep{{kind: "select", title: "Choose a Route", err: cancelled}}},
		{[]string{"delete"}, []routePromptStep{{kind: "select", title: "Choose a Route", answer: "r123456"}, {kind: "confirm", title: "r123456", err: cancelled}}},
	} {
		t.Run(fmt.Sprint(tc.args, tc.steps), func(t *testing.T) {
			h := NewCommandHarness(t)
			routeTeams(h)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/routes", 200, routePage([]any{routeCases(t)[0].Response}, nil))
			routePrompts(h, tc.steps...)
			h.Require.ErrorContains(h.Execute(append([]string{"route"}, tc.args...)...), "cancelled")
			for _, call := range m.Calls() {
				h.Require.Equal("GET", call.Method)
			}
		})
	}
}

func Test_Route_Interactive_Delete_Confirmed(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/routes", 200, routePage([]any{routeCases(t)[0].Response}, nil))
	m.SetRoute("DELETE", "/v1/routes/r123456", 200, map[string]any{"id": "r123456", "name": "acme/assistant"})
	routePrompts(h, routePromptStep{kind: "select", title: "Choose a Route", answer: "r123456"}, routePromptStep{kind: "confirm", title: "r123456"})
	h.Require.NoError(h.Execute("route", "delete"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/routes/r123456"))
}

func Test_Route_Interactive_EmptyChoices(t *testing.T) {
	for _, verb := range []string{"create", "describe"} {
		t.Run(verb, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []any{}})
			m.SetRoute("GET", "/v1/routes", 200, routePage([]any{}, nil))
			routePrompts(h)
			h.Require.Error(h.Execute("route", verb))
			h.Require.Equal(4, h.ExitCode)
		})
	}
}

func Test_Route_Interactive_InvalidFlagsNeverPrompt(t *testing.T) {
	for _, args := range [][]string{
		{"describe", "--id", "r1", "--name", "acme/a"},
		{"describe", "--id", ""},
		{"create", "--name", ""},
		{"create", "--target-model-api", "m", "--target-provider-secret", "key"},
		{"update", "--target-provider", "openai", "--target-provider-base-url", "https://api.example.com"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			routePrompts(h)
			h.Require.Error(h.Execute(append([]string{"route"}, args...)...))
			h.Require.Equal(2, h.ExitCode)
			h.Require.Empty(m.Calls())
		})
	}
}

func Test_Route_Help_Prerelease(t *testing.T) {
	for _, verb := range []string{"", "create", "list", "describe", "update", "delete"} {
		h := NewCommandHarness(t)
		args := []string{"route"}
		if verb != "" {
			args = append(args, verb)
		}
		args = append(args, "--help")
		h.Require.NoError(h.Execute(args...))
		h.Require.Contains(h.Stdout.String(), "Pre-release")
	}
}

func Test_Route_Interactive_Create_OptionalMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, label, description string
		cancel                   bool
	}{
		{name: "filled", label: "Review helper", description: "For code reviews"},
		{name: "skipped"},
		{name: "long-label", label: strings.Repeat("a", 256)},
		{name: "long-description", description: strings.Repeat("a", 1001)},
		{name: "cancel", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			routeTeams(h)
			m := h.MockManagementAPI()
			m.SetRoute("POST", "/v1/routes", 200, routeCases(t)[0].Response)
			steps := []routePromptStep{{kind: "optional", title: "Display name", answer: tc.label}, {kind: "optional", title: "Description", answer: tc.description}}
			if tc.cancel {
				steps[1].err = errors.New("cancelled")
			}
			routePrompts(h, steps...)
			err := h.Execute("route", "create", "--team", "Engineering", "--name", "acme/assistant", "--target-model-api", "model")
			if tc.cancel || len(tc.label) > 255 || len(tc.description) > 1000 {
				h.Require.Error(err)
				h.Require.Nil(m.FindCall("POST", "/v1/routes"))
				return
			}
			h.Require.NoError(err)
			body := m.FindCall("POST", "/v1/routes").BodyJSON(t)
			if tc.label == "" {
				h.Require.NotContains(body, "display_name")
			} else {
				h.Require.Equal(tc.label, body["display_name"])
			}
			if tc.description == "" {
				h.Require.NotContains(body, "description")
			} else {
				h.Require.Equal(tc.description, body["description"])
			}
		})
	}
}

func Test_Route_Interactive_PrefixPrefill(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		response          any
		selected, initial string
	}{
		{"single", 200, map[string]any{"data": map[string]any{"route_slug_prefixes": []string{"acme"}}}, "", "acme/"},
		{"multiple", 200, map[string]any{"data": map[string]any{"route_slug_prefixes": []string{"acme", "labs"}}}, "labs", "labs/"},
		{"empty", 200, map[string]any{"data": map[string]any{"route_slug_prefixes": []string{}}}, "", ""},
		{"forbidden", 403, map[string]any{}, "", ""},
		{"graphql error", 200, map[string]any{"errors": []any{map[string]any{"message": "denied"}}, "data": map[string]any{"route_slug_prefixes": []string{"ignored"}}}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			routeTeams(h)
			m := h.MockManagementAPI()
			t.Setenv("BASETEN_REMOTE_URL", m.URL)
			m.SetRouteFunc("POST", "/graphql/", func(w http.ResponseWriter, r *http.Request) {
				h.Require.Equal("Bearer test-key", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.response)
			})
			m.SetRoute("POST", "/v1/routes", 200, routeCases(t)[0].Response)
			steps := []routePromptStep{}
			if tc.selected != "" {
				steps = append(steps, routePromptStep{kind: "select", title: "organization prefix", answer: tc.selected})
			}
			steps = append(steps, routePromptStep{kind: "input", title: "Route slug", answer: "acme/assistant"})
			p := routePrompts(h, steps...)
			h.Require.NoError(h.Execute("route", "create", "--team", "Engineering", "--target-model-api", "model", "--display-name", "Assistant", "--description", ""))
			h.Require.Equal([]string{tc.initial}, p.initialInputs)
			call := m.FindCall("POST", "/graphql/")
			h.Require.NotNil(call)
			h.Require.Equal("t123456", call.BodyJSON(t)["variables"].(map[string]any)["teamId"])
			h.Require.Equal("acme/assistant", m.FindCall("POST", "/v1/routes").BodyJSON(t)["name"])
		})
	}
}

func Test_Route_Interactive_ModelAPIPicker(t *testing.T) {
	for _, mode := range []string{"select", "manual", "cancel", "empty", "failed"} {
		t.Run(mode, func(t *testing.T) {
			h := NewCommandHarness(t)
			routeTeams(h)
			m := h.MockManagementAPI()
			pages := 0
			m.SetRouteFunc("GET", "/v1/model_apis", func(w http.ResponseWriter, r *http.Request) {
				pages++
				h.Require.Equal("false", r.URL.Query().Get("added_only"))
				w.Header().Set("Content-Type", "application/json")
				if mode == "failed" {
					w.WriteHeader(403)
					_, _ = w.Write([]byte(`{"error":"denied"}`))
					return
				}
				if mode == "empty" {
					_ = json.NewEncoder(w).Encode(routePage([]any{}, nil))
					return
				}
				if pages == 1 {
					_ = json.NewEncoder(w).Encode(routePage([]any{modelAPIFixture("first/model", "First", "test")}, "next"))
				} else {
					h.Require.Equal("next", r.URL.Query().Get("cursor"))
					_ = json.NewEncoder(w).Encode(routePage([]any{modelAPIFixture("second/model", "Second", "test")}, nil))
				}
			})
			m.SetRoute("POST", "/v1/routes", 200, routeCases(t)[0].Response)
			steps := []routePromptStep{{kind: "select", title: "Target type", answer: "model-api"}}
			switch mode {
			case "select":
				steps = append(steps, routePromptStep{kind: "select", title: "Choose a Model API", answer: "second/model"})
			case "manual":
				steps = append(steps, routePromptStep{kind: "select", title: "Choose a Model API", answer: "__manual__"}, routePromptStep{kind: "input", title: "Model API", answer: "second/model"})
			case "cancel":
				steps = append(steps, routePromptStep{kind: "select", title: "Choose a Model API", err: errors.New("cancelled")})
			default:
				steps = append(steps, routePromptStep{kind: "input", title: "Model API", answer: "second/model"})
			}
			p := routePrompts(h, steps...)
			err := h.Execute("route", "create", "--team", "Engineering", "--name", "acme/assistant", "--display-name", "Assistant", "--description", "")
			if mode == "cancel" {
				h.Require.ErrorContains(err, "cancelled")
				h.Require.Nil(m.FindCall("POST", "/v1/routes"))
				return
			}
			h.Require.NoError(err)
			h.Require.Equal("second/model", m.FindCall("POST", "/v1/routes").BodyJSON(t)["target"].(map[string]any)["model_api"])
			if mode == "select" || mode == "manual" {
				h.Require.Equal(2, pages)
				h.Require.Equal([]cmd.RoutePromptOption{{Label: "first/model", Value: "first/model"}, {Label: "second/model", Value: "second/model"}, {Label: "Enter a Model API name manually", Value: "__manual__"}}, p.options[1])
			}
		})
	}
}

func Test_Route_Interactive_SecretPicker(t *testing.T) {
	for _, mode := range []string{"existing", "create", "collision", "cancel", "failed", "update"} {
		t.Run(mode, func(t *testing.T) {
			h := NewCommandHarness(t)
			routeTeams(h)
			m := h.MockManagementAPI()
			teamID := "t123456"
			if mode == "update" {
				teamID = "other-team"
			}
			path := "/v1/teams/" + teamID + "/secrets"
			names := []any{map[string]any{"name": "existing-key", "team_name": "Engineering", "created_at": "2026-01-02T03:04:05Z"}}
			if mode == "collision" {
				names = append(names, map[string]any{"name": "anthropic-api-key"})
			}
			m.SetRoute("GET", path, 200, map[string]any{"secrets": names})
			status := 200
			if mode == "failed" {
				status = 403
			}
			m.SetRoute("POST", path, status, map[string]any{"name": "anthropic-api-key", "team_name": "Engineering", "created_at": "2026-01-02T03:04:05Z"})
			fixture := routeCases(t)[0].Response
			m.SetRoute("POST", "/v1/routes", 200, fixture)
			m.SetRoute("PATCH", "/v1/routes/r123456", 200, fixture)
			m.SetRoute("GET", "/v1/routes/r123456", 200, map[string]any{"id": "r123456", "team_id": teamID})
			chosen := "existing-key"
			if mode != "existing" && mode != "update" {
				chosen = "__create_secret__"
			}
			steps := []routePromptStep{{kind: "select", title: "credential secret", answer: chosen}}
			if chosen == "__create_secret__" {
				steps = append(steps, routePromptStep{kind: "input", title: "New secret name", answer: "anthropic-api-key"})
				if mode != "collision" {
					step := routePromptStep{kind: "password", title: "Secret value", answer: "test-secret-value"}
					if mode == "cancel" {
						step.err = errors.New("cancelled")
					}
					steps = append(steps, step)
				}
			}
			p := routePrompts(h, steps...)
			args := []string{"route", "create", "--team", "Engineering", "--name", "acme/assistant", "--display-name", "Assistant", "--description", ""}
			if mode == "update" {
				args = []string{"route", "update", "--id", "r123456"}
			}
			args = append(args, "--target-provider", "anthropic", "--target-provider-model", "claude")
			err := h.Execute(args...)
			h.Require.NotNil(m.FindCall("GET", path))
			h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "test-secret-value")
			if chosen == "__create_secret__" {
				h.Require.Equal([]string{"anthropic-api-key"}, p.initialInputs)
			}
			if mode == "collision" || mode == "cancel" || mode == "failed" {
				h.Require.Error(err)
				h.Require.Nil(m.FindCall("POST", "/v1/routes"))
				if mode != "failed" {
					h.Require.Nil(m.FindCall("POST", path))
				}
				return
			}
			h.Require.NoError(err)
			if mode == "create" {
				h.Require.Equal(map[string]any{"name": "anthropic-api-key", "value": "test-secret-value"}, m.FindCall("POST", path).BodyJSON(t))
				chosen = "anthropic-api-key"
			} else {
				h.Require.Nil(m.FindCall("POST", path))
			}
			call := m.FindCall("POST", "/v1/routes")
			if mode == "update" {
				call = m.FindCall("PATCH", "/v1/routes/r123456")
			}
			h.Require.Equal(chosen, call.BodyJSON(t)["target"].(map[string]any)["secret_name"])
		})
	}
}
