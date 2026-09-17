package cmd_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	publiccmd "github.com/basetenlabs/baseten-cli/cmd"
)

type routeCase struct {
	Name     string
	Flags    []string
	Create   map[string]any
	Update   map[string]any
	Response map[string]any
}

func routeCases() []routeCase {
	targets := []struct {
		name   string
		flags  []string
		target map[string]any
	}{
		{
			name:   "model-api",
			flags:  []string{"--target-model-api", "moonshotai/glm-5.3"},
			target: map[string]any{"type": "BASETEN_MODEL_API", "model_api": "moonshotai/glm-5.3"},
		},
		{
			name:   "anthropic",
			flags:  []string{"--target-provider", "anthropic", "--target-provider-model", "test-model", "--target-provider-secret", "provider-key"},
			target: map[string]any{"type": "ANTHROPIC", "model": "test-model", "secret_name": "provider-key"},
		},
		{
			name:   "openai",
			flags:  []string{"--target-provider", "openai", "--target-provider-model", "test-model", "--target-provider-secret", "provider-key"},
			target: map[string]any{"type": "OPENAI", "model": "test-model", "secret_name": "provider-key"},
		},
		{
			name:   "xai",
			flags:  []string{"--target-provider", "xai", "--target-provider-model", "test-model", "--target-provider-secret", "provider-key"},
			target: map[string]any{"type": "XAI", "model": "test-model", "secret_name": "provider-key"},
		},
		{
			name:   "vertex",
			flags:  []string{"--target-provider", "vertex", "--target-provider-model", "test-model", "--target-provider-secret", "provider-key", "--target-provider-vertex-project", "example-project", "--target-provider-vertex-location", "global"},
			target: map[string]any{"type": "VERTEX", "model": "test-model", "secret_name": "provider-key", "vertex_config": map[string]any{"project_id": "example-project", "location": "global"}},
		},
		{
			name:   "openai-compatible",
			flags:  []string{"--target-provider", "openai-compatible", "--target-provider-model", "test-model", "--target-provider-secret", "provider-key", "--target-provider-base-url", "https://api.example.com/v1"},
			target: map[string]any{"type": "OPENAI_COMPATIBLE", "model": "test-model", "secret_name": "provider-key", "base_url": "https://api.example.com/v1"},
		},
	}
	cases := make([]routeCase, 0, len(targets))
	for _, tc := range targets {
		cases = append(cases, routeCase{
			Name:     tc.name,
			Flags:    tc.flags,
			Create:   map[string]any{"name": "acme/assistant", "team_id": "t123456", "display_name": "Assistant", "target": tc.target},
			Update:   map[string]any{"target": tc.target},
			Response: routeFixture(tc.target),
		})
	}
	return cases
}

func routeFixture(target map[string]any) map[string]any {
	return map[string]any{
		"id":           "r123456",
		"name":         "acme/assistant",
		"team_id":      "t123456",
		"display_name": "Assistant",
		"description":  "",
		"target":       target,
		"invoke_url":   "https://coding.baseten.co",
		"created_at":   "2026-09-17T10:00:00Z",
	}
}

func routePage(items []any, cursor any) map[string]any {
	return map[string]any{"items": items, "pagination": map[string]any{"has_more": cursor != nil, "cursor": cursor}}
}

func routeTeams(h *CommandHarness) {
	h.MockManagementAPI().SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []any{teamFixture("t123456", "Engineering", true)}})
}

func Test_Route_Create_ContractTargets(t *testing.T) {
	for _, tc := range routeCases() {
		t.Run(tc.Name, func(t *testing.T) {
			h := NewCommandHarness(t)
			routeTeams(h)
			m := h.MockManagementAPI()
			m.SetRouteFunc("POST", "/v1/routes", func(w http.ResponseWriter, r *http.Request) {
				h.Require.Equal("Bearer test-key", r.Header.Get("Authorization"))
				h.Require.Equal("application/json", r.Header.Get("Content-Type"))
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tc.Response)
			})
			args := append([]string{"route", "create", "--name", "acme/assistant", "--team", "Engineering", "--display-name", "Assistant", "--output", "json"}, tc.Flags...)
			h.Require.NoError(h.Execute(args...))
			h.Require.Equal(tc.Create, m.FindCall("POST", "/v1/routes").BodyJSON(t))
			var got publiccmd.Route
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
			h.Require.Equal("r123456", got.ID)
			h.Require.Equal("https://coding.baseten.co", got.InvokeURL)
			if tc.Name == "openai-compatible" {
				h.Require.Equal("https://api.example.com/v1", *got.Target.BaseURL)
			}
			if tc.Name == "vertex" {
				h.Require.Equal("example-project", got.Target.VertexConfig.ProjectID)
			}
		})
	}
}

func Test_Route_Update_ContractTargets(t *testing.T) {
	for _, tc := range routeCases() {
		t.Run(tc.Name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("PATCH", "/v1/routes/r123456", 200, tc.Response)
			args := append([]string{"route", "update", "--id", "r123456", "--output", "json"}, tc.Flags...)
			h.Require.NoError(h.Execute(args...))
			h.Require.Equal(tc.Update, m.FindCall("PATCH", "/v1/routes/r123456").BodyJSON(t))
			h.Require.Len(m.Calls(), 1, "target replacement must not fetch or merge the old target")
		})
	}
}

func Test_Route_Create_DefaultDisplayName(t *testing.T) {
	h := NewCommandHarness(t)
	routeTeams(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/routes", 200, routeCases()[0].Response)
	h.Require.NoError(h.Execute("route", "create", "--name", "acme/assistant", "--team", "t123456", "--target-model-api", "moonshotai/glm-5.3"))
	body := m.FindCall("POST", "/v1/routes").BodyJSON(t)
	h.Require.NotContains(body, "display_name")
	h.Require.Equal("t123456", body["team_id"])
	h.Require.Contains(h.Stdout.String(), "Model API:")
}

func Test_Route_Update_LabelAndCombined(t *testing.T) {
	for _, combined := range []bool{false, true} {
		t.Run(fmt.Sprint(combined), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("PATCH", "/v1/routes/r123456", 200, routeCases()[0].Response)
			args := []string{"route", "update", "--id", "r123456", "--display-name", "New label"}
			expected := map[string]any{"display_name": "New label"}
			if combined {
				args = append(args, "--target-model-api", "new-model")
				expected["target"] = map[string]any{"type": "BASETEN_MODEL_API", "model_api": "new-model"}
			}
			h.Require.NoError(h.Execute(args...))
			h.Require.Equal(expected, m.FindCall("PATCH", "/v1/routes/r123456").BodyJSON(t))
		})
	}
}

func Test_Route_Addressed_ExactName(t *testing.T) {
	for _, verb := range []string{"describe", "update", "delete"} {
		t.Run(verb, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			fixture := routeCases()[0].Response
			name := "acme/assistant+test:1"
			fixture["name"] = name
			m.SetRoute("GET", "/v1/routes", 200, routePage([]any{fixture}, nil))
			method := map[string]string{"describe": "GET", "update": "PATCH", "delete": "DELETE"}[verb]
			m.SetRoute(method, "/v1/routes/r123456", 200, fixture)
			args := []string{"route", verb, "--name", name}
			if verb == "update" {
				args = append(args, "--display-name", "Assistant")
			}
			if verb == "delete" {
				args = append(args, "--yes")
			}
			h.Require.NoError(h.Execute(args...))
			h.Require.Equal(name, m.FindCall("GET", "/v1/routes").Query().Get("name"))
			if verb == "describe" {
				h.Require.Len(m.Calls(), 3)
			} else {
				h.Require.Len(m.Calls(), 2)
			}
			h.Require.NotNil(m.FindCall(method, "/v1/routes/r123456"))
		})
	}
}

func Test_Route_Addressed_InvalidSelectors(t *testing.T) {
	for _, verb := range []string{"describe", "update", "delete"} {
		for _, selector := range [][]string{nil, {"--id", ""}, {"--name", ""}, {"--id", "r1", "--name", "acme/a"}} {
			t.Run(verb+strings.Join(selector, " "), func(t *testing.T) {
				h := NewCommandHarness(t)
				m := h.MockManagementAPI()
				args := append([]string{"route", verb}, selector...)
				if verb == "update" {
					args = append(args, "--display-name", "Label")
				}
				if verb == "delete" {
					args = append(args, "--yes")
				}
				h.Require.Error(h.Execute(args...))
				h.Require.Equal(2, h.ExitCode)
				h.Require.Empty(m.Calls())
			})
		}
	}
}

func Test_Route_Addressed_FailedLookupNeverMutates(t *testing.T) {
	fixture := routeCases()[0].Response
	for _, tc := range []struct {
		name string
		page any
		code int
	}{
		{"missing", routePage([]any{}, nil), 4},
		{"ambiguous", routePage([]any{fixture, fixture}, nil), 1},
		{"more", routePage([]any{fixture}, "next"), 1},
		{"wrong-name", routePage([]any{map[string]any{"id": "r1", "name": "other/name"}}, nil), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/routes", 200, tc.page)
			h.Require.Error(h.Execute("route", "delete", "--name", "acme/assistant", "--yes", "--output", "json"))
			h.Require.Equal(tc.code, h.ExitCode)
			h.Require.Len(m.Calls(), 1)
		})
	}
}

func Test_Route_Delete_ConfirmationAndTombstone(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	h.Require.ErrorContains(h.Execute("route", "delete", "--id", "r123456"), "--yes")
	h.Require.Equal(2, h.ExitCode)
	h.Require.Empty(m.Calls())
	tombstone := map[string]any{"id": "r123456", "name": "acme/assistant"}
	m.SetRoute("DELETE", "/v1/routes/r123456", 200, tombstone)
	h.Require.NoError(h.Execute("route", "delete", "--id", "r123456", "--yes", "--output", "json"))
	h.Require.JSONEq(`{"id":"r123456","name":"acme/assistant"}`, h.Stdout.String())
	h.Require.NoError(h.Execute("route", "delete", "--id", "r123456", "--yes"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Deleted Route acme/assistant (r123456)")
}

func Test_Route_List_PaginationAndFilters(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprint(all), func(t *testing.T) {
			h := NewCommandHarness(t)
			routeTeams(h)
			m := h.MockManagementAPI()
			fixture := routeCases()[0].Response
			m.SetRouteFunc("GET", "/v1/routes", func(w http.ResponseWriter, r *http.Request) {
				h.Require.Equal("t123456", r.URL.Query().Get("team_id"))
				h.Require.Equal("acme/assistant", r.URL.Query().Get("name"))
				h.Require.Equal("1", r.URL.Query().Get("limit"))
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Query().Get("cursor") {
				case "start+/=":
					_ = json.NewEncoder(w).Encode(routePage([]any{fixture}, "next+/="))
				case "next+/=":
					_ = json.NewEncoder(w).Encode(routePage([]any{fixture}, nil))
				default:
					t.Error("unexpected cursor")
					w.WriteHeader(400)
				}
			})
			args := []string{"route", "list", "--team", "Engineering", "--name", "acme/assistant", "--limit", "1", "--cursor", "start+/=", "--output", "json"}
			if all {
				args = append(args, "--all")
			}
			h.Require.NoError(h.Execute(args...))
			var got publiccmd.RouteList
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
			if all {
				h.Require.Len(got.Items, 2)
				h.Require.False(got.Pagination.HasMore)
				h.Require.Nil(got.Pagination.Cursor)
			} else {
				h.Require.Len(got.Items, 1)
				h.Require.True(got.Pagination.HasMore)
				h.Require.Equal("next+/=", *got.Pagination.Cursor)
			}
		})
	}
}

func Test_Route_List_CursorWithoutFilters(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/routes", 200, routePage([]any{}, nil))
	h.Require.NoError(h.Execute("route", "list", "--cursor", "opaque", "--output", "json"))
	q := m.FindCall("GET", "/v1/routes").Query()
	h.Require.NotContains(q, "team_id")
	h.Require.NotContains(q, "name")
	h.Require.Equal("100", q.Get("limit"))
	h.Require.JSONEq(`{"items":[],"pagination":{"has_more":false,"cursor":null}}`, h.Stdout.String())
}

func Test_Route_List_TextAndJQ(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/routes", 200, routePage([]any{routeCases()[0].Response, routeCases()[1].Response}, "next"))
	h.Require.NoError(h.Execute("route", "list"))
	for _, text := range []string{"ID", "NAME", "TEAM ID", "DISPLAY NAME", "moonshotai/glm-5.3", "ANTHROPIC:test-model"} {
		h.Require.Contains(h.Stdout.String(), text)
	}
	h.Require.Contains(h.Stderr.String(), `--cursor "next"`)
	h.Require.NoError(h.Execute("route", "list", "--jq", ".items[0].name"))
	h.Require.Equal("\"acme/assistant\"\n", h.Stdout.String())
	m.SetRoute("GET", "/v1/routes", 200, routePage([]any{}, nil))
	h.Require.NoError(h.Execute("route", "list"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No Routes found.")
}

func Test_Route_List_InvalidPagination(t *testing.T) {
	for _, page := range []any{
		routePage([]any{}, "repeat"),
		map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": true, "cursor": nil}},
	} {
		h := NewCommandHarness(t)
		m := h.MockManagementAPI()
		m.SetRoute("GET", "/v1/routes", 200, page)
		h.Require.ErrorContains(h.Execute("route", "list", "--all", "--output", "json"), "cursor")
		h.Require.LessOrEqual(len(m.Calls()), 2)
		h.Require.NotContains(h.Stdout.String(), `"items"`, "no partial success output")
	}
}

func Test_Route_Usage_InvalidTargets(t *testing.T) {
	provider := []string{"--target-provider", "openai", "--target-provider-model", "test-model", "--target-provider-secret", "key"}
	cases := [][]string{
		{}, {"--target-model-api", ""}, {"--target-provider", ""},
		{"--target-model-api", "model", "--target-provider", "openai"},
		{"--target-model-api", "model", "--target-provider-secret", "key"},
		{"--target-provider-model", "model"}, {"--target-provider", "openai"},
		{"--target-provider", "openai", "--target-provider-model", "model"},
		{"--target-provider", "openai", "--target-provider-secret", "key"},
		{"--target-provider", "invalid", "--target-provider-model", "model", "--target-provider-secret", "key"},
		{"--target-provider", "openai-compatible", "--target-provider-model", "model", "--target-provider-secret", "key"},
		{"--target-provider", "vertex", "--target-provider-model", "model", "--target-provider-secret", "key"},
		append(append([]string{}, provider...), "--target-provider-base-url", "https://api.example.com"),
		append(append([]string{}, provider...), "--target-provider-vertex-project", "project"),
		append(append([]string{}, provider...), "--target-provider-vertex-location", "global"),
	}
	for _, verb := range []string{"create", "update"} {
		for i, flags := range cases {
			t.Run(fmt.Sprintf("%s/%d", verb, i), func(t *testing.T) {
				h := NewCommandHarness(t)
				m := h.MockManagementAPI()
				args := []string{"route", verb}
				if verb == "create" {
					args = append(args, "--name", "acme/assistant", "--team", "Engineering")
				} else {
					args = append(args, "--id", "r123456")
				}
				args = append(args, flags...)
				h.Require.Error(h.Execute(args...))
				h.Require.Equal(2, h.ExitCode)
				h.Require.Empty(m.Calls())
			})
		}
	}
}

func Test_Route_Usage_InvalidValues(t *testing.T) {
	for _, args := range [][]string{
		{"list", "--limit", "0"}, {"list", "--limit", "1001"}, {"list", "--limit", "-1"},
		{"list", "--name", ""}, {"list", "--team", ""}, {"list", "--cursor", ""},
		{"update", "--id", "r1", "--display-name", ""},
		{"update", "--id", "r1", "--display-name", strings.Repeat("a", 256)},
		{"update", "--id", "r1", "--team", "other"},
		{"create", "--name", "", "--team", "Engineering", "--target-model-api", "model"},
		{"create", "--name", "acme/a", "--team", "", "--target-model-api", "model"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			h.Require.Error(h.Execute(append([]string{"route"}, args...)...))
			h.Require.Equal(2, h.ExitCode)
			h.Require.Empty(m.Calls())
		})
	}
}

func Test_Route_Usage_InvalidBaseURL(t *testing.T) {
	for _, baseURL := range []string{"http://api.example.com", "https://", "https://user:pass@api.example.com", "https://api.example.com:443", "https://api.example.com:bad"} {
		t.Run(baseURL, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			h.Require.Error(h.Execute("route", "update", "--id", "r1", "--target-provider", "openai-compatible", "--target-provider-model", "model", "--target-provider-secret", "key", "--target-provider-base-url", baseURL))
			h.Require.Equal(2, h.ExitCode)
			h.Require.Empty(m.Calls())
		})
	}
}

func Test_Route_Describe_AllProviderFields(t *testing.T) {
	for _, tc := range routeCases() {
		t.Run(tc.Name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/routes/r123456", 200, tc.Response)
			h.Require.NoError(h.Execute("route", "describe", "--id", "r123456"))
			for _, field := range []string{"ID:", "Name:", "Display Name:", "Team ID:", "Target Type:", "Invoke URL:", "Created:"} {
				h.Require.Contains(h.Stdout.String(), field)
			}
			if tc.Name == "vertex" {
				h.Require.Contains(h.Stdout.String(), "example-project")
				h.Require.Contains(h.Stdout.String(), "global")
			}
			if tc.Name == "openai-compatible" {
				h.Require.Contains(h.Stdout.String(), "api.example.com/v1")
			}
			if tc.Name != "model-api" {
				h.Require.Contains(h.Stdout.String(), "provider-key")
			}
		})
	}
}

func Test_Route_API_ErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		status, exit int
		code         string
	}{
		{400, 5, "VALIDATION_ERROR"}, {401, 3, "PERMISSION_DENIED"}, {403, 3, "PERMISSION_DENIED"}, {404, 4, "NOT_FOUND"}, {409, 5, "CONFLICT"}, {429, 5, "RATE_LIMITED"}, {500, 6, "INTERNAL_ERROR"}, {503, 6, "UPSTREAM_UNAVAILABLE"},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("PATCH", "/v1/routes/r1", tc.status, map[string]any{"code": tc.code, "message": "fixture failure", "details": map[string]any{"field": "target"}})
			h.Require.Error(h.Execute("route", "update", "--id", "r1", "--display-name", "New", "--output", "json"))
			h.Require.Equal(tc.exit, h.ExitCode)
			var envelope publiccmd.JSONErrorEnvelope
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &envelope))
			h.Require.Equal(tc.status, envelope.Error.APIStatusCode)
			h.Require.Equal(tc.code, envelope.Error.APIErrorCode)
			h.Require.Equal("target", envelope.Error.APIDetails["field"])
			h.Require.Len(m.Calls(), 1, "do not retry writes")
		})
	}
}

func Test_Route_API_MalformedResponse(t *testing.T) {
	for _, tc := range []struct{ contentType, body string }{
		{"text/html", "<html>proxy</html>"}, {"application/json", "{bad"},
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRouteFunc("GET", "/v1/routes/r1", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.body))
			})
			h.Require.Error(h.Execute("route", "describe", "--id", "r1", "--output", "json"))
			h.Require.NotContains(h.Stdout.String(), `"invoke_url"`)
		})
	}
}

func Test_Route_API_ProfileAndOutputModes(t *testing.T) {
	h := newAuthHarness(t)
	m := h.MockManagementAPI()
	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile("route-profile", m.URL, "profile-fixture-key", true, nil))
	m.SetRouteFunc("GET", "/v1/routes/r123456", func(w http.ResponseWriter, r *http.Request) {
		h.Require.Equal("Bearer profile-fixture-key", r.Header.Get("Authorization"))
		h.Require.Regexp(`^baseten-cli/\S+ \(Go/\S+; [^)]+\)$`, r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(routeCases()[0].Response)
	})
	h.Require.NoError(h.Execute("route", "describe", "--id", "r123456", "--profile", "route-profile", "--output", "jsonl"))
	h.Require.Equal(1, strings.Count(h.Stdout.String(), "\n"))
	h.Require.NoError(h.Execute("route", "describe", "--id", "r123456", "--profile", "route-profile", "--output", "none"))
	h.Require.Empty(h.Stdout.String())
}

func Test_Route_List_LaterPageErrorHasNoPartialSuccess(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/routes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(routePage([]any{routeCases()[0].Response}, "next"))
		} else {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "PERMISSION_DENIED", "message": "fixture permission changed"})
		}
	})
	h.Require.Error(h.Execute("route", "list", "--all", "--output", "json"))
	h.Require.Equal(3, h.ExitCode)
	h.Require.Len(m.Calls(), 2)
	var envelope publiccmd.JSONErrorEnvelope
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &envelope))
	h.Require.Equal(403, envelope.Error.APIStatusCode)
	h.Require.NotContains(h.Stdout.String(), `"items"`)
}

func Test_Route_Metadata_Description(t *testing.T) {
	for _, description := range []string{"", "Route used for reviews"} {
		h := NewCommandHarness(t)
		m := h.MockManagementAPI()
		response := routeCases()[0].Response
		response["description"] = description
		m.SetRoute("PATCH", "/v1/routes/r123456", 200, response)
		h.Require.NoError(h.Execute("route", "update", "--id", "r123456", "--description", description, "--output", "json"))
		h.Require.Equal(map[string]any{"description": description}, m.FindCall("PATCH", "/v1/routes/r123456").BodyJSON(t))
		var got publiccmd.Route
		h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
		h.Require.Equal(description, got.Description)
	}
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	h.Require.Error(h.Execute("route", "update", "--id", "r1", "--description", strings.Repeat("a", 1001)))
	h.Require.Empty(m.Calls())
}

func Test_Route_Describe_TeamName(t *testing.T) {
	for _, status := range []int{200, 403, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/routes/r123456", 200, routeCases()[0].Response)
			if status == 200 {
				m.SetRoute("GET", "/v1/teams/t123456", 200, teamFixture("t123456", "Engineering", false))
			} else {
				m.SetRoute("GET", "/v1/teams/t123456", status, map[string]any{"code": "PERMISSION_DENIED", "message": "Team unavailable"})
			}
			h.Require.NoError(h.Execute("route", "describe", "--id", "r123456"))
			h.Require.Contains(h.Stdout.String(), "Team ID:          t123456")
			if status == 200 {
				h.Require.Contains(h.Stdout.String(), "Team Name:        Engineering")
			} else {
				h.Require.NotContains(h.Stdout.String(), "Team Name:")
				h.Require.Empty(h.Stderr.String())
			}
			h.Require.Contains(h.Stdout.String(), "Model API:")
		})
	}
}

func Test_Route_Describe_StructuredOutputSkipsTeamLookup(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/routes/r123456", 200, routeCases()[0].Response)
	h.Require.NoError(h.Execute("route", "describe", "--id", "r123456", "--output", "json"))
	h.Require.Len(m.Calls(), 1)
	h.Require.Contains(h.Stdout.String(), `"team_id": "t123456"`)
}
