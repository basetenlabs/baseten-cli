package cmd_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func gridConnectionFixture(provider string) map[string]any {
	return map[string]any{"id": "c123456", "team_id": "t123456", "team_name": "Engineering", "config": map[string]any{"provider": strings.ToUpper(provider), "secret_id": "s123456", "secret_name": "provider-key"}, "created_at": "2026-10-19T10:00:00Z", "updated_at": "2026-10-19T10:00:00Z"}
}

func Test_Grid_Connection_Create_Update_Contracts(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai", "xai"} {
		for _, verb := range []string{"create", "update"} {
			for _, output := range []string{"json", "text"} {
				t.Run(provider+"/"+verb+"/"+output, func(t *testing.T) {
					h := NewCommandHarness(t)
					m := h.MockManagementAPI()
					method, path := "POST", "/v1/grid/connections"
					args := []string{"grid", "connection", verb, "--provider", provider, "--secret-name", "provider-key", "--output", output}
					if verb == "update" {
						method, path = "PATCH", path+"/c123456"
						args = append(args, "--id", "c123456")
					}
					m.SetRoute(method, path, 200, gridConnectionFixture(provider))
					h.Require.NoError(h.Execute(args...))
					h.Require.Len(m.Calls(), 1)
					h.Require.Equal(map[string]any{"config": map[string]any{"provider": strings.ToUpper(provider), "secret_name": "provider-key"}}, m.FindCall(method, path).BodyJSON(t))
					if output == "json" {
						var got any
						h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
						h.Require.Equal(gridConnectionFixture(provider), got)
					} else {
						h.Require.Empty(h.Stdout.String())
						if verb == "create" {
							h.Require.Contains(h.Stderr.String(), "Created connection c123456")
						} else {
							h.Require.Contains(h.Stderr.String(), "Updated connection c123456")
						}
					}
				})
			}
		}
	}
}

func Test_Grid_Connection_Create_ExplicitTeam(t *testing.T) {
	h := NewCommandHarness(t)
	gridRouteTeams(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/grid/connections", 200, gridConnectionFixture("anthropic"))
	h.Require.NoError(h.Execute("grid", "connection", "create", "--team", "Engineering", "--provider", "anthropic", "--secret-name", "provider-key"))
	h.Require.Equal("t123456", m.FindCall("POST", "/v1/grid/connections").BodyJSON(t)["team_id"])
}

func Test_Grid_Connection_List_Pagination(t *testing.T) {
	for _, team := range []string{"", "Engineering"} {
		t.Run(team, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			if team != "" {
				gridRouteTeams(h)
			}
			m.SetRouteFunc("GET", "/v1/grid/connections", func(w http.ResponseWriter, r *http.Request) {
				want := ""
				if team != "" {
					want = "t123456"
				}
				h.Require.Equal(want, r.URL.Query().Get("team_id"))
				var response any = gridRoutePage([]any{gridConnectionFixture("openai")}, "next")
				if r.URL.Query().Get("cursor") == "next" {
					connection := gridConnectionFixture("xai")
					connection["id"] = "c765432"
					if team == "" {
						connection["team_id"], connection["team_name"] = "t765432", "Research"
					}
					response = gridRoutePage([]any{connection}, nil)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			})
			args := []string{"grid", "connection", "list", "--output", "json"}
			if team != "" {
				args = append(args, "--team", team)
			}
			h.Require.NoError(h.Execute(args...))
			h.Require.Equal(2, countCalls(m, "GET", "/v1/grid/connections"))
			var got struct {
				Items []any `json:"items"`
			}
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
			h.Require.Len(got.Items, 2)
			if team == "" {
				h.Require.Contains(h.Stdout.String(), "Research")
				h.Require.Nil(m.FindCall("GET", "/v1/teams"))
			}
		})
	}
}

func Test_Grid_Connection_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/grid/connections", 200, gridRoutePage([]any{}, nil))
	h.Require.NoError(h.Execute("grid", "connection", "list"))
	h.Require.Contains(h.Stderr.String(), "No connections found")
	h.Require.NoError(h.Execute("grid", "connection", "list", "--output", "json"))
	h.Require.JSONEq(`{"items":[]}`, h.Stdout.String())
}

func Test_Grid_Connection_Delete_Confirmation(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	h.Require.ErrorContains(h.Execute("grid", "connection", "delete", "--id", "c123456"), "--yes")
	h.Require.Empty(m.Calls())
	m.SetRoute("DELETE", "/v1/grid/connections/c123456", 200, map[string]string{"id": "c123456"})
	h.Require.NoError(h.Execute("grid", "connection", "delete", "--id", "c123456", "--yes", "--output", "json"))
	h.Require.JSONEq(`{"id":"c123456"}`, h.Stdout.String())
	h.Require.Len(m.Calls(), 1)
}

func Test_Grid_Connection_InvalidFlags(t *testing.T) {
	for _, args := range [][]string{
		{"create"}, {"create", "--provider", "vertex", "--secret-name", "provider-key"},
		{"create", "--provider", "openai", "--secret-name", ""},
		{"update", "--id", "", "--provider", "openai", "--secret-name", "provider-key"},
		{"delete", "--id", "", "--yes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			h.Require.Error(h.Execute(append([]string{"grid", "connection"}, args...)...))
			h.Require.Equal(2, h.ExitCode)
			h.Require.Empty(m.Calls())
		})
	}
}

func Test_Grid_Connection_Update_ServerRejectsProviderChange(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("PATCH", "/v1/grid/connections/c123456", 400, map[string]string{"error": "Connection provider is immutable"})
	h.Require.Error(h.Execute("grid", "connection", "update", "--id", "c123456", "--provider", "xai", "--secret-name", "provider-key", "--output", "json"))
	h.Require.Equal(400, decodeJSONErrorEnvelope(h).APIStatusCode)
	h.Require.Len(m.Calls(), 1)
}

func Test_Grid_Connection_List_UnknownProvider(t *testing.T) {
	for _, output := range []string{"text", "json", "jsonl"} {
		t.Run(output, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			providers := []string{"anthropic", "openai", "xai"}
			items := make([]any, 0, len(providers)+1)
			for _, provider := range providers {
				connection := gridConnectionFixture(provider)
				connection["id"] = "c-" + provider
				items = append(items, connection)
			}
			// Simulate a provider and configuration shape added after this SDK.
			unknown := gridConnectionFixture("future_provider")
			unknown["id"] = "c-future"
			unknown["config"] = map[string]any{
				"provider":   "FUTURE_PROVIDER",
				"credential": map[string]any{"name": "future-key"},
			}
			items = append(items, unknown)
			m.SetRoute("GET", "/v1/grid/connections", 200, gridRoutePage(items, nil))
			h.Require.NoError(h.Execute("grid", "connection", "list", "--output", output))
			h.Require.Len(m.Calls(), 1)
			if output == "text" {
				lines := strings.Split(strings.TrimSpace(h.Stdout.String()), "\n")
				h.Require.Len(lines, len(items)+1)
				for i, provider := range providers {
					h.Require.Equal([]string{"c-" + provider, "Engineering", provider, "provider-key"}, strings.Fields(lines[i+1]))
				}
				h.Require.Equal([]string{"c-future", "Engineering", "<unknown>", "<unknown>"}, strings.Fields(lines[len(items)]))
			} else {
				var got any
				h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
				h.Require.Equal(map[string]any{"items": items}, got)
			}
		})
	}
}

func Test_Grid_Connection_List_MalformedConfig(t *testing.T) {
	type configCase struct {
		name   string
		config any
	}
	cases := []configCase{
		{"missing-provider", map[string]any{}},
		{"empty-provider", map[string]any{"provider": ""}},
		{"invalid-provider", map[string]any{"provider": 42}},
		{"null-config", nil},
		{"invalid-config", "not an object"},
	}
	for _, provider := range []string{"ANTHROPIC", "OPENAI", "XAI"} {
		cases = append(cases, configCase{provider, map[string]any{"provider": provider, "secret_id": 42, "secret_name": "provider-key"}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			connection := gridConnectionFixture("anthropic")
			connection["id"], connection["config"] = "c-bad", tc.config
			m.SetRoute("GET", "/v1/grid/connections", 200, gridRoutePage([]any{gridConnectionFixture("openai"), connection}, nil))
			h.Require.ErrorContains(h.Execute("grid", "connection", "list"), "reading connection c-bad config")
			h.Require.Empty(h.Stdout.String())
		})
	}
}

func Test_Grid_Connection_List_MalformedResponse(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/grid/connections", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":`))
	})
	h.Require.ErrorContains(h.Execute("grid", "connection", "list"), "unexpected end of JSON input")
	h.Require.Empty(h.Stdout.String())
}

func Test_Grid_Connection_List_TextAndJSON(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	connection := gridConnectionFixture("openai")
	m.SetRoute("GET", "/v1/grid/connections", 200, gridRoutePage([]any{connection}, nil))

	h.Require.NoError(h.Execute("grid", "connection", "list"))
	h.Require.Regexp(`^ID\s+TEAM\s+PROVIDER\s+SECRET NAME\s*\n`, h.Stdout.String())
	h.Require.NotContains(h.Stdout.String(), "s123456")
	h.Require.Contains(h.Stdout.String(), "provider-key")

	h.Require.NoError(h.Execute("grid", "connection", "list", "--output", "json"))
	var got any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal(map[string]any{"items": []any{connection}}, got)
}
