package cmd_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	publiccmd "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

type gridRouteCase struct {
	Name     string
	Flags    []string
	Create   map[string]any
	Target   map[string]any
	Response map[string]any
}

func gridRouteCases() []gridRouteCase {
	targets := []struct {
		name   string
		flags  []string
		target map[string]any
	}{
		{
			name:   "model-api",
			flags:  []string{"--target-type", "baseten-model-api", "--target-model", "test-model-api"},
			target: map[string]any{"type": "BASETEN_MODEL_API", "model": "test-model-api"},
		},
		{
			name:   "connection",
			flags:  []string{"--target-type", "connection", "--target-model", "test-model", "--target-connection", "c123456"},
			target: map[string]any{"type": "CONNECTION", "model": "test-model", "connection_id": "c123456"},
		},
	}
	cases := make([]gridRouteCase, 0, len(targets))
	for _, tc := range targets {
		cases = append(cases, gridRouteCase{
			Name:     tc.name,
			Flags:    tc.flags,
			Create:   map[string]any{"team_id": "t123456", "display_name": "Assistant", "target": tc.target},
			Target:   tc.target,
			Response: gridRouteFixture(tc.target),
		})
	}
	return cases
}

func gridRouteFixture(target map[string]any) map[string]any {
	return map[string]any{
		"id":           "r123456",
		"name":         "acme/assistant",
		"team_id":      "t123456",
		"team_name":    "Engineering",
		"display_name": "Assistant",
		"description":  "",
		"target":       target,
		"invoke_url":   "https://coding.baseten.co",
		"created_at":   "2026-09-17T10:00:00Z",
		"metadata":     nil,
	}
}

func gridRoutePage(items []any, cursor any) map[string]any {
	return map[string]any{"items": items, "pagination": map[string]any{"has_more": cursor != nil, "cursor": cursor}}
}

func gridRouteTeams(h *CommandHarness) {
	h.MockManagementAPI().SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []any{teamFixture("t123456", "Engineering", true)}})
}

func Test_Grid_Route_Create_ContractTargets(t *testing.T) {
	for _, tc := range gridRouteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			h := NewCommandHarness(t)
			gridRouteTeams(h)
			m := h.MockManagementAPI()
			m.SetRouteFunc("POST", "/v1/grid/routes", func(w http.ResponseWriter, r *http.Request) {
				h.Require.Equal("Bearer test-key", r.Header.Get("Authorization"))
				h.Require.Equal("application/json", r.Header.Get("Content-Type"))
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tc.Response)
			})
			args := append([]string{"grid", "route", "create", "--team", "Engineering", "--display-name", "Assistant", "--output", "json"}, tc.Flags...)
			h.Require.NoError(h.Execute(args...))
			h.Require.Equal(tc.Create, m.FindCall("POST", "/v1/grid/routes").BodyJSON(t))
			var got managementapi.GridRoute
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
			h.Require.Equal("r123456", got.Id)
			h.Require.Equal("https://coding.baseten.co", got.InvokeUrl)
		})
	}
}

func Test_Grid_Route_Update_RejectsTargets(t *testing.T) {
	for _, tc := range gridRouteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			args := append([]string{"grid", "route", "update", "--id", "r123456", "--display-name", "New label"}, tc.Flags...)
			h.Require.Error(h.Execute(args...))
			h.Require.Equal(2, h.ExitCode, "non-classifier targets cannot be updated")
			h.Require.Empty(m.Calls())
		})
	}
}

func Test_Grid_Route_Create_DefaultDisplayName(t *testing.T) {
	h := NewCommandHarness(t)
	gridRouteTeams(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/grid/routes", 200, gridRouteCases()[0].Response)
	h.Require.NoError(h.Execute("grid", "route", "create", "--team", "t123456", "--target-type", "baseten-model-api", "--target-model", "test-model-api"))
	body := m.FindCall("POST", "/v1/grid/routes").BodyJSON(t)
	h.Require.NotContains(body, "display_name")
	h.Require.Equal("t123456", body["team_id"])
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Created route acme/assistant (r123456)")
}

func Test_Grid_Route_Update_DisplayNameAndDescription(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("PATCH", "/v1/grid/routes/r123456", 200, gridRouteCases()[0].Response)
	h.Require.NoError(h.Execute("grid", "route", "update", "--id", "r123456", "--display-name", "New label", "--description", ""))
	h.Require.Equal(map[string]any{"display_name": "New label", "description": ""}, m.FindCall("PATCH", "/v1/grid/routes/r123456").BodyJSON(t))
}

func Test_Grid_Route_Addressed_ExactName(t *testing.T) {
	for _, verb := range []string{"describe", "update", "delete"} {
		t.Run(verb, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			fixture := gridRouteCases()[0].Response
			name := "acme/assistant+test:1"
			fixture["name"] = name
			m.SetRoute("GET", "/v1/grid/routes", 200, gridRoutePage([]any{fixture}, nil))
			method := map[string]string{"describe": "GET", "update": "PATCH", "delete": "DELETE"}[verb]
			m.SetRoute(method, "/v1/grid/routes/r123456", 200, fixture)
			args := []string{"grid", "route", verb, "--name", name}
			if verb == "update" {
				args = append(args, "--display-name", "Assistant")
			}
			if verb == "delete" {
				args = append(args, "--yes")
			}
			h.Require.NoError(h.Execute(args...))
			h.Require.Equal(name, m.FindCall("GET", "/v1/grid/routes").Query().Get("name"))
			h.Require.Len(m.Calls(), 2)
			h.Require.NotNil(m.FindCall(method, "/v1/grid/routes/r123456"))
		})
	}
}

func Test_Grid_Route_Addressed_InvalidSelectors(t *testing.T) {
	for _, verb := range []string{"describe", "update", "delete"} {
		for _, selector := range [][]string{nil, {"--id", ""}, {"--name", ""}, {"--id", "r1", "--name", "acme/a"}} {
			t.Run(verb+strings.Join(selector, " "), func(t *testing.T) {
				h := NewCommandHarness(t)
				m := h.MockManagementAPI()
				args := append([]string{"grid", "route", verb}, selector...)
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

func Test_Grid_Route_Addressed_FailedLookupNeverMutates(t *testing.T) {
	fixture := gridRouteCases()[0].Response
	for _, tc := range []struct {
		name string
		page any
		code int
	}{
		{"missing", gridRoutePage([]any{}, nil), 4},
		{"ambiguous", gridRoutePage([]any{fixture, fixture}, nil), 1},
		{"more", gridRoutePage([]any{fixture}, "next"), 1},
		{"wrong-name", gridRoutePage([]any{map[string]any{"id": "r1", "name": "other/name"}}, nil), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/grid/routes", 200, tc.page)
			h.Require.Error(h.Execute("grid", "route", "delete", "--name", "acme/assistant", "--yes", "--output", "json"))
			h.Require.Equal(tc.code, h.ExitCode)
			h.Require.Len(m.Calls(), 1)
		})
	}
}

func Test_Grid_Route_Delete_ConfirmationAndTombstone(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	h.Require.ErrorContains(h.Execute("grid", "route", "delete", "--id", "r123456"), "--yes")
	h.Require.Equal(2, h.ExitCode)
	h.Require.Empty(m.Calls())
	tombstone := map[string]any{"id": "r123456", "name": "acme/assistant"}
	m.SetRoute("DELETE", "/v1/grid/routes/r123456", 200, tombstone)
	h.Require.NoError(h.Execute("grid", "route", "delete", "--id", "r123456", "--yes", "--output", "json"))
	h.Require.JSONEq(`{"id":"r123456","name":"acme/assistant"}`, h.Stdout.String())
	h.Require.NoError(h.Execute("grid", "route", "delete", "--id", "r123456", "--yes"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Deleted route acme/assistant (r123456)")
}

func Test_Grid_Route_List_PaginationAndFilters(t *testing.T) {
	h := NewCommandHarness(t)
	gridRouteTeams(h)
	m := h.MockManagementAPI()
	fixture := gridRouteCases()[0].Response
	m.SetRouteFunc("GET", "/v1/grid/routes", func(w http.ResponseWriter, r *http.Request) {
		h.Require.Equal("t123456", r.URL.Query().Get("team_id"))
		h.Require.NotContains(r.URL.Query(), "name")
		h.Require.NotContains(r.URL.Query(), "limit")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			_ = json.NewEncoder(w).Encode(gridRoutePage([]any{fixture}, "next+/="))
		case "next+/=":
			_ = json.NewEncoder(w).Encode(gridRoutePage([]any{fixture}, nil))
		default:
			t.Error("unexpected cursor")
			w.WriteHeader(400)
		}
	})
	h.Require.NoError(h.Execute("grid", "route", "list", "--team", "Engineering", "--output", "json"))
	var got publiccmd.GridRouteList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Len(got.Items, 2)
	h.Require.NotContains(h.Stdout.String(), "pagination")
}

func Test_Grid_Route_List_WithoutFilters(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/grid/routes", 200, gridRoutePage([]any{}, nil))
	h.Require.NoError(h.Execute("grid", "route", "list", "--output", "json"))
	q := m.FindCall("GET", "/v1/grid/routes").Query()
	h.Require.NotContains(q, "team_id")
	h.Require.NotContains(q, "name")
	h.Require.NotContains(q, "limit")
	h.Require.JSONEq(`{"items":[]}`, h.Stdout.String())
}

func Test_Grid_Route_List_TextAndJQ(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/grid/routes", 200, gridRoutePage([]any{gridRouteCases()[0].Response, gridRouteCases()[1].Response}, nil))
	h.Require.NoError(h.Execute("grid", "route", "list"))
	h.Require.Regexp(`^ID\s+DISPLAY NAME\s+NAME\s+CREATED\s*\n`, h.Stdout.String(), "display name comes before name")
	for _, text := range []string{"r123456", "Assistant", "acme/assistant", "2026-09-17T10:00:00Z"} {
		h.Require.Contains(h.Stdout.String(), text)
	}
	for _, text := range []string{"TEAM", "Engineering", "TARGET", "test-model-api", "c123456"} {
		h.Require.NotContains(h.Stdout.String(), text, "the team is the listing's scope, and targets are for describe")
	}
	h.Require.Empty(h.Stderr.String())
	h.Require.NoError(h.Execute("grid", "route", "list", "--jq", ".items[0].name"))
	h.Require.Equal("\"acme/assistant\"\n", h.Stdout.String())
	m.SetRoute("GET", "/v1/grid/routes", 200, gridRoutePage([]any{}, nil))
	h.Require.NoError(h.Execute("grid", "route", "list"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No routes found.")
}

func Test_Grid_Route_Usage_InvalidTargets(t *testing.T) {
	provider := []string{"--target-type", "connection", "--target-model", "test-model", "--target-connection", "c123456"}
	cases := [][]string{
		{}, {"--target-type", "baseten-model-api", "--target-model", ""}, {"--target-type", ""},
		{"--target-type", "baseten-model-api", "--target-model", "model", "--target-type", "connection"},
		{"--target-type", "baseten-model-api", "--target-model", "model", "--target-connection", "c123456"},
		{"--target-model", "model"}, {"--target-type", "connection"},
		{"--target-type", "connection", "--target-model", "model"},
		{"--target-type", "connection", "--target-connection", "c123456"},
		{"--target-type", "invalid", "--target-model", "model", "--target-connection", "c123456"},
		// Targets and flags the routes API no longer accepts.
		{"--target-type", "openai", "--target-model", "model", "--target-secret", "key"},
		{"--target-type", "anthropic", "--target-model", "model", "--target-secret", "key"},
		{"--target-type", "xai", "--target-model", "model", "--target-secret", "key"},
		{"--target-type", "openai-compatible", "--target-model", "model", "--target-secret", "key"},
		{"--target-type", "vertex", "--target-model", "model", "--target-secret", "key"},
		append(append([]string{}, provider...), "--target-secret", "key"),
		append(append([]string{}, provider...), "--target-base-url", "https://api.example.com"),
		append(append([]string{}, provider...), "--target-vertex-project", "project"),
		append(append([]string{}, provider...), "--target-vertex-location", "global"),
		append(append([]string{}, provider...), "--name", "acme/assistant"),
	}
	for i, flags := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			h.Require.Error(h.Execute(append([]string{"grid", "route", "create", "--team", "Engineering"}, flags...)...))
			h.Require.Equal(2, h.ExitCode)
			h.Require.Empty(m.Calls())
		})
	}
}

func Test_Grid_Route_Usage_InvalidValues(t *testing.T) {
	for _, args := range [][]string{
		{"list", "--limit", "1"}, {"list", "--all"},
		{"list", "--name", ""}, {"list", "--cursor", ""},
		{"update", "--id", "r1", "--team", "other"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			h.Require.Error(h.Execute(append([]string{"grid", "route"}, args...)...))
			h.Require.Equal(2, h.ExitCode)
			h.Require.Empty(m.Calls())
		})
	}
}

func Test_Grid_Route_Describe_AllTargetFields(t *testing.T) {
	for _, tc := range gridRouteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/grid/routes/r123456", 200, tc.Response)
			h.Require.NoError(h.Execute("grid", "route", "describe", "--id", "r123456"))
			for _, field := range []string{"ID:", "Name:", "Display Name:", "Team ID:", "Target Type:", "Target Model:", "Invoke URL:", "Created:"} {
				h.Require.Contains(h.Stdout.String(), field)
			}
			h.Require.Contains(h.Stdout.String(), "Target Type:      "+tc.Flags[1])
			h.Require.Contains(h.Stdout.String(), "Target Model:     "+tc.Flags[3])
			h.Require.NotContains(h.Stdout.String(), "Provider:")
			h.Require.NotContains(h.Stdout.String(), "Secret")
			if tc.Name == "connection" {
				h.Require.Contains(h.Stdout.String(), "Target Connection: c123456")
			} else {
				h.Require.NotContains(h.Stdout.String(), "Connection")
			}
		})
	}
}

func Test_Grid_Route_API_ErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		status, exit int
		code         string
	}{
		{400, 5, "VALIDATION_ERROR"}, {401, 3, "PERMISSION_DENIED"}, {403, 3, "PERMISSION_DENIED"}, {404, 4, "NOT_FOUND"}, {409, 5, "CONFLICT"}, {429, 5, "RATE_LIMITED"}, {500, 6, "INTERNAL_ERROR"}, {503, 6, "UPSTREAM_UNAVAILABLE"},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("PATCH", "/v1/grid/routes/r1", tc.status, map[string]any{"code": tc.code, "message": "fixture failure", "details": map[string]any{"field": "target"}})
			h.Require.Error(h.Execute("grid", "route", "update", "--id", "r1", "--display-name", "New", "--output", "json"))
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

func Test_Grid_Route_API_MalformedResponse(t *testing.T) {
	for _, tc := range []struct{ contentType, body string }{
		{"text/html", "<html>proxy</html>"}, {"application/json", "{bad"},
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRouteFunc("GET", "/v1/grid/routes/r1", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.body))
			})
			h.Require.Error(h.Execute("grid", "route", "describe", "--id", "r1", "--output", "json"))
			h.Require.NotContains(h.Stdout.String(), `"invoke_url"`)
		})
	}
}

func Test_Grid_Route_API_ProfileAndOutputModes(t *testing.T) {
	h := newAuthHarness(t)
	m := h.MockManagementAPI()
	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile("route-profile", m.URL, "profile-fixture-key", true, nil))
	m.SetRouteFunc("GET", "/v1/grid/routes/r123456", func(w http.ResponseWriter, r *http.Request) {
		h.Require.Equal("Bearer profile-fixture-key", r.Header.Get("Authorization"))
		h.Require.Regexp(`^baseten-cli/\S+ \(Go/\S+; [^)]+\)$`, r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gridRouteCases()[0].Response)
	})
	h.Require.NoError(h.Execute("grid", "route", "describe", "--id", "r123456", "--profile", "route-profile", "--output", "jsonl"))
	h.Require.Equal(1, strings.Count(h.Stdout.String(), "\n"))
	h.Require.NoError(h.Execute("grid", "route", "describe", "--id", "r123456", "--profile", "route-profile", "--output", "none"))
	h.Require.Empty(h.Stdout.String())
}

func Test_Grid_Route_List_LaterPageErrorHasNoPartialSuccess(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/grid/routes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(gridRoutePage([]any{gridRouteCases()[0].Response}, "next"))
		} else {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "PERMISSION_DENIED", "message": "fixture permission changed"})
		}
	})
	h.Require.Error(h.Execute("grid", "route", "list", "--output", "json"))
	h.Require.Equal(3, h.ExitCode)
	h.Require.Len(m.Calls(), 2)
	var envelope publiccmd.JSONErrorEnvelope
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &envelope))
	h.Require.Equal(403, envelope.Error.APIStatusCode)
	h.Require.NotContains(h.Stdout.String(), `"items"`)
}

func Test_Grid_Route_Metadata_Description(t *testing.T) {
	for _, description := range []string{"", "route used for reviews"} {
		h := NewCommandHarness(t)
		m := h.MockManagementAPI()
		response := gridRouteCases()[0].Response
		response["description"] = description
		m.SetRoute("PATCH", "/v1/grid/routes/r123456", 200, response)
		h.Require.NoError(h.Execute("grid", "route", "update", "--id", "r123456", "--description", description, "--output", "json"))
		h.Require.Equal(map[string]any{"description": description}, m.FindCall("PATCH", "/v1/grid/routes/r123456").BodyJSON(t))
		var got managementapi.GridRoute
		h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
		h.Require.Equal(description, got.Description)
	}
}

func Test_Grid_Route_Describe_TeamName(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/grid/routes/r123456", 200, gridRouteCases()[0].Response)
	h.Require.NoError(h.Execute("grid", "route", "describe", "--id", "r123456"))
	h.Require.Contains(h.Stdout.String(), "Team ID:          t123456")
	h.Require.Contains(h.Stdout.String(), "Team Name:        Engineering")
	h.Require.Contains(h.Stdout.String(), "Target Type:      baseten-model-api")
	h.Require.Len(m.Calls(), 1, "team name comes directly from the route response")
}

func Test_Grid_Route_Describe_StructuredOutputSkipsTeamLookup(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/grid/routes/r123456", 200, gridRouteCases()[0].Response)
	h.Require.NoError(h.Execute("grid", "route", "describe", "--id", "r123456", "--output", "json"))
	h.Require.Len(m.Calls(), 1)
	h.Require.Contains(h.Stdout.String(), `"team_id": "t123456"`)
}

func Test_Grid_Route_RequiresFlagsWithoutReadingStdin(t *testing.T) {
	for _, args := range [][]string{
		{"create"},
		{"create", "--team", "Engineering"},
		{"describe"},
		{"update"},
		{"update", "--id", "r123456"},
		{"delete", "--id", "r123456"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			h.Stdin.WriteString("acme/assistant\nyes\n")
			h.Require.Error(h.Execute(append([]string{"grid", "route"}, args...)...))
			h.Require.Equal(2, h.ExitCode)
			h.Require.Equal("acme/assistant\nyes\n", h.Stdin.String())
			h.Require.Empty(m.Calls())
		})
	}
}

func Test_Grid_Route_TargetNamesAreLiteral(t *testing.T) {
	for _, args := range [][]string{
		{"--target-type", "baseten-model-api", "--target-model", "__manual__"},
		{"--target-type", "connection", "--target-model", "test-model", "--target-connection", "__create_connection__"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := NewCommandHarness(t)
			gridRouteTeams(h)
			m := h.MockManagementAPI()
			m.SetRoute("POST", "/v1/grid/routes", 200, gridRouteCases()[0].Response)
			h.Require.NoError(h.Execute(append([]string{"grid", "route", "create", "--team", "t123456", "--output", "json"}, args...)...))
			body := m.FindCall("POST", "/v1/grid/routes").BodyJSON(t)
			target := body["target"].(map[string]any)
			if args[1] == "baseten-model-api" {
				h.Require.Equal("__manual__", target["model"])
			} else {
				h.Require.Equal("__create_connection__", target["connection_id"])
			}
			h.Require.Len(m.Calls(), 2)
			h.Require.NotNil(m.FindCall("GET", "/v1/teams"))
		})
	}
}

func Test_Grid_Route_MutationOutputConventions(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		for _, format := range []string{"text", "json", "jsonl", "none"} {
			t.Run(verb+"/"+format, func(t *testing.T) {
				h := NewCommandHarness(t)
				gridRouteTeams(h)
				m := h.MockManagementAPI()
				// A successful mutation must not be reported as failed just
				// because a newer API returns an unfamiliar target variant.
				fixture := gridRouteFixture(map[string]any{"type": "FUTURE_PROVIDER", "model": "future-model"})
				args := []string{"grid", "route", verb, "--output", format}
				message := "Updated route acme/assistant (r123456)"
				if verb == "create" {
					m.SetRoute("POST", "/v1/grid/routes", 200, fixture)
					args = append(args, "--team", "Engineering", "--target-type", "baseten-model-api", "--target-model", "model")
					message = "Created route acme/assistant (r123456)"
				} else {
					m.SetRoute("PATCH", "/v1/grid/routes/r123456", 200, fixture)
					args = append(args, "--id", "r123456", "--description", "new description")
				}
				h.Require.NoError(h.Execute(args...))
				switch format {
				case "text", "none":
					h.Require.Empty(h.Stdout.String())
					h.Require.Equal(message+"\n", h.Stderr.String())
				default:
					expected, err := json.Marshal(fixture)
					h.Require.NoError(err)
					h.Require.JSONEq(string(expected), h.Stdout.String())
					h.Require.Empty(h.Stderr.String())
					if format == "jsonl" {
						h.Require.Equal(1, strings.Count(h.Stdout.String(), "\n"))
					}
				}
			})
		}
	}
}

func Test_Grid_Route_List_UnknownTarget(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	unknown := gridRouteFixture(map[string]any{"type": "FUTURE_PROVIDER", "model": "future-model"})
	unknown["name"] = "acme/future"
	items := []any{unknown}
	for _, tc := range gridRouteCases() {
		items = append(items, tc.Response)
	}
	m.SetRoute("GET", "/v1/grid/routes", 200, gridRoutePage(items, nil))
	h.Require.NoError(h.Execute("grid", "route", "list"))
	h.Require.Empty(h.Stderr.String())
	h.Require.Contains(h.Stdout.String(), "acme/future", "a route with an unknown target is still listed")
	h.Require.NoError(h.Execute("grid", "route", "list", "--output", "json"))
	var result publiccmd.GridRouteList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Len(result.Items, len(items))
	h.Require.Contains(h.Stdout.String(), "FUTURE_PROVIDER")
	h.Require.Empty(h.Stderr.String())
}

func Test_Grid_Route_ClassifierTarget(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	target := map[string]any{
		"type":           "CLASSIFIER",
		"classifier":     map[string]any{"id": "c123456", "name": "intent-v2"},
		"default_route":  map[string]any{"id": "r1", "slug": "acme/fast"},
		"allowed_routes": []any{map[string]any{"id": "r1", "slug": "acme/fast"}, map[string]any{"id": "r2", "slug": "acme/smart"}},
	}
	classifier := gridRouteFixture(target)
	classifier["name"] = "acme/auto"
	m.SetRoute("GET", "/v1/grid/routes/r123456", 200, classifier)
	h.Require.NoError(h.Execute("grid", "route", "describe", "--id", "r123456"))
	for _, text := range []string{
		"Target Type:      classifier",
		"Classifier:       intent-v2",
		"Default Route:    acme/fast",
		"Allowed Routes:   acme/fast, acme/smart",
	} {
		h.Require.Contains(h.Stdout.String(), text)
	}
	h.Require.NotContains(h.Stdout.String(), "Target Model:")

	// A router created before classifiers could be selected has none, but
	// still has its routes.
	target["classifier"] = nil
	m.SetRoute("GET", "/v1/grid/routes/r123456", 200, gridRouteFixture(target))
	h.Stdout.Reset()
	h.Require.NoError(h.Execute("grid", "route", "describe", "--id", "r123456"))
	h.Require.NotContains(h.Stdout.String(), "Classifier:")
	h.Require.Contains(h.Stdout.String(), "Default Route:    acme/fast")
}

func Test_Grid_Route_Describe_UnknownTarget(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/grid/routes/r123456", 200, gridRouteFixture(map[string]any{"type": "FUTURE_PROVIDER"}))
	h.Require.NoError(h.Execute("grid", "route", "describe", "--id", "r123456"))
	for _, text := range []string{"r123456", "acme/assistant", "Engineering", "<unrecognized type>", "https://coding.baseten.co", "2026-09-17T10:00:00Z"} {
		h.Require.Contains(h.Stdout.String(), text)
	}
	h.Require.Empty(h.Stderr.String())
	h.Require.NoError(h.Execute("grid", "route", "describe", "--id", "r123456", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), "FUTURE_PROVIDER")
	h.Require.NoError(h.Execute("grid", "route", "describe", "--id", "r123456", "--output", "none"))
	h.Require.Empty(h.Stdout.String())
}

func Test_Grid_Route_Delete_ResolvesBeforeConfirmation(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/grid/routes", 200, gridRoutePage([]any{gridRouteCases()[0].Response}, nil))
	devNullStdin(t, h)
	h.Require.ErrorContains(h.Execute("grid", "route", "delete", "--name", "acme/assistant"), "stdin is not a terminal; pass --yes")
	h.Require.Equal(2, h.ExitCode)
	h.Require.Len(m.Calls(), 1)
	h.Require.NotNil(m.FindCall("GET", "/v1/grid/routes"))
	h.Require.Nil(m.FindCall("DELETE", "/v1/grid/routes/r123456"))
}

func Test_Grid_Route_Create_DefaultTeam(t *testing.T) {
	for _, tc := range gridRouteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("POST", "/v1/grid/routes", 200, tc.Response)
			args := append([]string{"grid", "route", "create", "--output", "json"}, tc.Flags...)
			h.Require.NoError(h.Execute(args...))
			body := m.FindCall("POST", "/v1/grid/routes").BodyJSON(t)
			h.Require.NotContains(body, "team_id", "the server selects the default team")
			h.Require.Equal(tc.Target, body["target"])
			h.Require.Len(m.Calls(), 1, "default team selection must not require a team-list request")
			h.Require.Contains(h.Stdout.String(), `"team_name": "Engineering"`)
		})
	}
}

func Test_Grid_Route_Classifier_CreateAndUpdate(t *testing.T) {
	target := map[string]any{"type": "CLASSIFIER", "classifier_id": "general-v1", "allowed_route_ids": []any{"r123456", "r234567"}, "default_route_id": "r123456"}
	response := gridRouteFixture(map[string]any{"type": "CLASSIFIER", "classifier": map[string]any{"id": "general-v1", "name": "General"}, "allowed_routes": []any{map[string]any{"id": "r123456", "slug": "acme/a"}, map[string]any{"id": "r234567", "slug": "acme/b"}}, "default_route": map[string]any{"id": "r123456", "slug": "acme/a"}})
	for _, verb := range []string{"create", "update"} {
		t.Run(verb, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			method, path := "POST", "/v1/grid/routes"
			args := []string{"grid", "route", verb, "--classifier-id", "general-v1", "--allowed-route-id", "r123456", "--allowed-route-id", "r234567", "--default-route-id", "r123456", "--output", "json"}
			if verb == "create" {
				args = append(args, "--target-type", "classifier")
			} else {
				method, path = "PATCH", path+"/r345678"
				args = append(args, "--id", "r345678")
			}
			m.SetRoute(method, path, 200, response)
			h.Require.NoError(h.Execute(args...))
			h.Require.Len(m.Calls(), 1)
			h.Require.Equal(map[string]any{"target": target}, m.FindCall(method, path).BodyJSON(t))
		})
	}
}

func Test_Grid_Route_Classifier_InvalidFlags(t *testing.T) {
	for _, args := range [][]string{
		{"create", "--target-type", "classifier"},
		{"create", "--target-type", "classifier", "--target-model", "model"},
		{"create", "--target-type", "connection", "--target-model", "model", "--target-connection", "c123456", "--classifier-id", "general-v1"},
		{"update", "--id", "r123456", "--classifier-id", "general-v1"},
		{"update", "--id", "r123456", "--display-name", "Name", "--classifier-id", ""},
		{"update", "--id", "r123456", "--classifier-id", "general-v1", "--allowed-route-id", "", "--default-route-id", "r234567"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			h.Require.Error(h.Execute(append([]string{"grid", "route"}, args...)...))
			h.Require.Equal(2, h.ExitCode)
			h.Require.Empty(m.Calls())
		})
	}
}
