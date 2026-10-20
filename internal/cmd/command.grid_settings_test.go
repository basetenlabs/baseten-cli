package cmd_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func gridSettingsFixture() map[string]any {
	return map[string]any{
		"team_id":   "t123456",
		"team_name": "Engineering",
		"harness_defaults": map[string]any{
			"claude_code": map[string]any{"primary": nil, "background": nil},
			"opencode":    map[string]any{"primary": nil, "background": nil},
			"codex":       map[string]any{"primary": nil}, "pi": map[string]any{"primary": nil},
		},
		"spend_limit": map[string]any{"per_member_monthly_limit_usd": "100", "team_wide_monthly_limit_usd": nil},
	}
}

func gridUserSettingsFixture() map[string]any {
	return map[string]any{
		"team_id": "t123456", "team_name": "Engineering", "user_id": "u123456", "email": "member@example.com",
		"month_spend_usd": "12.5", "is_blocked": false, "blocked_by": nil,
		"spend_limit": map[string]any{
			"user_monthly_limit_usd": nil, "month_override_usd": nil,
			"team_default":                map[string]any{"team_id": "t123456", "team_name": "Engineering", "per_member_monthly_limit_usd": "100"},
			"effective":                   map[string]any{"monthly_limit_usd": "100", "source": "team"},
			"team_wide_monthly_limit_usd": nil,
		},
	}
}

func Test_Grid_TeamSettings_UserSettings_ReadContracts(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		path string
		body any
		text string
	}{
		{"team", []string{"team-settings", "describe"}, "/v1/grid/team_settings", gridSettingsFixture(), "Per-member monthly limit"},
		{"user", []string{"user-settings", "describe"}, "/v1/grid/team_settings/users/me", gridUserSettingsFixture(), "member@example.com"},
		{"users", []string{"user-settings", "list"}, "/v1/grid/team_settings/users", gridRoutePage([]any{gridUserSettingsFixture()}, nil), "member@example.com"},
	} {
		for _, team := range []string{"", "t123456", "Engineering"} {
			for _, output := range []string{"text", "json", "jsonl"} {
				t.Run(tc.name+"/"+team+"/"+output, func(t *testing.T) {
					h := NewCommandHarness(t)
					m := h.MockManagementAPI()
					if team != "" {
						gridRouteTeams(h)
					}
					m.SetRouteFunc("GET", tc.path, func(w http.ResponseWriter, r *http.Request) {
						h.Require.Equal("Bearer test-key", r.Header.Get("Authorization"))
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(tc.body)
					})
					args := append([]string{"grid"}, tc.args...)
					if team != "" {
						args = append(args, "--team", team)
					}
					h.Require.NoError(h.Execute(append(args, "--output", output)...))
					query := m.FindCall("GET", tc.path).Query()
					if team == "" {
						h.Require.NotContains(query, "team_id")
						h.Require.Len(m.Calls(), 1)
					} else {
						h.Require.Equal("t123456", query.Get("team_id"))
					}
					if output == "text" {
						h.Require.Contains(h.Stdout.String(), tc.text)
						h.Require.Contains(h.Stdout.String(), "Engineering")
					} else {
						var got any
						h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
						want := tc.body
						if tc.name == "users" {
							want = map[string]any{"items": []any{gridUserSettingsFixture()}}
						}
						h.Require.Equal(want, got)
					}
				})
			}
		}
	}
}

func Test_Grid_TeamSettings_Update_PatchSemantics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		body  map[string]any
	}{
		{"zero", []string{"--per-member-monthly-limit-usd", "0"}, map[string]any{"spend_limit": map[string]any{"per_member_monthly_limit_usd": "0"}}},
		{"exact", []string{"--team-wide-monthly-limit-usd", "123456789.12"}, map[string]any{"spend_limit": map[string]any{"team_wide_monthly_limit_usd": "123456789.12"}}},
		{"clear-limits", []string{"--per-member-monthly-limit-usd", "null", "--team-wide-monthly-limit-usd", "null"}, map[string]any{"spend_limit": map[string]any{"per_member_monthly_limit_usd": nil, "team_wide_monthly_limit_usd": nil}}},
		{"clear-spend-limit", []string{"--clear-spend-limit"}, map[string]any{"spend_limit": nil}},
		{"claude", []string{"--harness", "claude-code", "--primary-route-id", "r123456", "--background-route-id", "null"}, map[string]any{"harness_defaults": map[string]any{"claude_code": map[string]any{"primary": "r123456", "background": nil}}}},
		{"opencode", []string{"--harness", "opencode", "--background-route-id", "r123456"}, map[string]any{"harness_defaults": map[string]any{"opencode": map[string]any{"background": "r123456"}}}},
		{"codex", []string{"--harness", "codex", "--primary-route-id", "null"}, map[string]any{"harness_defaults": map[string]any{"codex": map[string]any{"primary": nil}}}},
		{"pi", []string{"--harness", "pi", "--primary-route-id", "r123456"}, map[string]any{"harness_defaults": map[string]any{"pi": map[string]any{"primary": "r123456"}}}},
		{"clear-all", []string{"--clear-harness-defaults"}, map[string]any{"harness_defaults": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("PATCH", "/v1/grid/team_settings", 200, gridSettingsFixture())
			h.Require.NoError(h.Execute(append([]string{"grid", "team-settings", "update", "--output", "json"}, tc.flags...)...))
			h.Require.Equal(tc.body, m.FindCall("PATCH", "/v1/grid/team_settings").BodyJSON(t))
			h.Require.Len(m.Calls(), 1)
			h.Require.Contains(h.Stdout.String(), "harness_defaults")
		})
	}
}

func Test_Grid_UserSettings_Update_PatchSemantics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		limit map[string]any
	}{
		{"zero", []string{"--user-monthly-limit-usd", "0"}, map[string]any{"user_monthly_limit_usd": "0"}},
		{"override", []string{"--month-override-usd", "250.50"}, map[string]any{"month_override_usd": "250.50"}},
		{"clear", []string{"--user-monthly-limit-usd", "null", "--month-override-usd", "null"}, map[string]any{"user_monthly_limit_usd": nil, "month_override_usd": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("PATCH", "/v1/grid/team_settings/users/me", 200, gridUserSettingsFixture())
			h.Require.NoError(h.Execute(append([]string{"grid", "user-settings", "update", "--output", "json"}, tc.flags...)...))
			h.Require.Equal(map[string]any{"spend_limit": tc.limit}, m.FindCall("PATCH", "/v1/grid/team_settings/users/me").BodyJSON(t))
			h.Require.Len(m.Calls(), 1)
		})
	}
}

func Test_Grid_TeamSettings_UserSettings_Update_ExplicitTeamAndUser(t *testing.T) {
	for _, tc := range []struct {
		group, path string
		flags       []string
	}{
		{"team-settings", "/v1/grid/team_settings", []string{"--per-member-monthly-limit-usd", "100"}},
		{"user-settings", "/v1/grid/team_settings/users/u123456", []string{"--user-id", "u123456", "--month-override-usd", "100"}},
	} {
		t.Run(tc.group, func(t *testing.T) {
			h := NewCommandHarness(t)
			gridRouteTeams(h)
			m := h.MockManagementAPI()
			body := gridSettingsFixture()
			if tc.group == "user-settings" {
				body = gridUserSettingsFixture()
			}
			m.SetRoute("PATCH", tc.path, 200, body)
			h.Require.NoError(h.Execute(append([]string{"grid", tc.group, "update", "--team", "Engineering"}, tc.flags...)...))
			call := m.FindCall("PATCH", tc.path)
			h.Require.Empty(call.Query())
			h.Require.Equal("t123456", call.BodyJSON(t)["team_id"])
		})
	}
}

func Test_Grid_TeamSettings_UserSettings_RejectsEmptyOrConflictingUpdates(t *testing.T) {
	for _, args := range [][]string{
		{"team-settings", "update"}, {"team-settings", "update", "--team", "t123456"},
		{"team-settings", "update", "--harness", "codex"},
		{"team-settings", "update", "--primary-route-id", "r123456"},
		{"team-settings", "update", "--harness", "pi", "--background-route-id", "null"},
		{"team-settings", "update", "--clear-harness-defaults", "--harness", "codex"},
		{"team-settings", "update", "--clear-spend-limit", "--per-member-monthly-limit-usd", "100"},
		{"team-settings", "update", "--clear-spend-limit", "--team-wide-monthly-limit-usd", "100"},
		{"user-settings", "update"}, {"user-settings", "update", "--user-id", "", "--month-override-usd", "0"},
		{"user-settings", "describe", "--user-id", ""},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			h.Require.Error(h.Execute(append([]string{"grid"}, args...)...))
			h.Require.Equal(2, h.ExitCode)
			h.Require.Empty(m.Calls())
		})
	}
}

func Test_Grid_TeamSettings_UserSettings_ServerErrorsPropagate(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		args         []string
	}{
		{"GET", "/v1/grid/team_settings", []string{"team-settings", "describe"}},
		{"GET", "/v1/grid/team_settings/users", []string{"user-settings", "list"}},
		{"GET", "/v1/grid/team_settings/users/me", []string{"user-settings", "describe"}},
		{"PATCH", "/v1/grid/team_settings", []string{"team-settings", "update", "--per-member-monthly-limit-usd", "-1"}},
		{"PATCH", "/v1/grid/team_settings/users/me", []string{"user-settings", "update", "--month-override-usd", "1.234"}},
	} {
		for _, status := range []int{400, 403, 404, 500} {
			t.Run(fmt.Sprintf("%s/%s/%d", tc.method, tc.path, status), func(t *testing.T) {
				h := NewCommandHarness(t)
				m := h.MockManagementAPI()
				m.SetRoute(tc.method, tc.path, status, map[string]string{"error": "server rejected request"})
				args := append([]string{"grid"}, tc.args...)
				args = append(args, "--output", "json")
				h.Require.Error(h.Execute(args...))
				h.Require.Equal(status, decodeJSONErrorEnvelope(h).APIStatusCode)
				h.Require.Len(m.Calls(), 1)
			})
		}
	}
}

func Test_Grid_UserSettings_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/grid/team_settings/users", 200, gridRoutePage([]any{}, nil))
	h.Require.NoError(h.Execute("grid", "user-settings", "list"))
	h.Require.Contains(h.Stderr.String(), "No user settings")
	h.Require.NoError(h.Execute("grid", "user-settings", "list", "--output", "json"))
	h.Require.JSONEq(`{"items":[]}`, h.Stdout.String())
}

func Test_Grid_UserSettings_List_MalformedResponse(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/grid/team_settings/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":`))
	})
	h.Require.ErrorContains(h.Execute("grid", "user-settings", "list"), "unexpected end of JSON input")
	h.Require.Len(m.Calls(), 1)
}

func Test_Grid_UserSettings_List_Pagination(t *testing.T) {
	for _, team := range []string{"", "t123456", "Engineering"} {
		for _, output := range []string{"text", "json", "jsonl"} {
			t.Run(team+"/"+output, func(t *testing.T) {
				h := NewCommandHarness(t)
				m := h.MockManagementAPI()
				gridRouteTeams(h)
				first, second := gridUserSettingsFixture(), gridUserSettingsFixture()
				if team == "" {
					// The same user can have a membership in more than one team.
					second["team_id"], second["team_name"] = "t765432", "Research"
					limit := second["spend_limit"].(map[string]any)
					limit["team_default"] = map[string]any{"team_id": "t765432", "team_name": "Research", "per_member_monthly_limit_usd": "100"}
				} else {
					second["user_id"], second["email"] = "u765432", "another@example.com"
				}
				m.SetRouteFunc("GET", "/v1/grid/team_settings/users", func(w http.ResponseWriter, r *http.Request) {
					want := ""
					if team != "" {
						want = "t123456"
					}
					h.Require.Equal(want, r.URL.Query().Get("team_id"))
					body := gridRoutePage([]any{first}, "next")
					if r.URL.Query().Get("cursor") == "next" {
						body = gridRoutePage([]any{second}, nil)
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(body)
				})
				args := []string{"grid", "user-settings", "list", "--output", output}
				if team != "" {
					args = append(args, "--team", team)
				}
				h.Require.NoError(h.Execute(args...))
				h.Require.Equal(2, countCalls(m, "GET", "/v1/grid/team_settings/users"))
				if team == "" {
					h.Require.Nil(m.FindCall("GET", "/v1/teams"))
				}
				if output == "text" {
					h.Require.Contains(h.Stdout.String(), "Engineering")
					h.Require.Contains(h.Stdout.String(), second["team_name"])
					h.Require.Contains(h.Stdout.String(), second["user_id"])
				} else {
					var got any
					h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
					h.Require.Equal(map[string]any{"items": []any{first, second}}, got)
				}
			})
		}
	}
}

func Test_Grid_UserSettings_List_PageError(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/grid/team_settings/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") != "" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "forbidden"})
			return
		}
		_ = json.NewEncoder(w).Encode(gridRoutePage([]any{gridUserSettingsFixture()}, "next"))
	})
	h.Require.Error(h.Execute("grid", "user-settings", "list", "--output", "json"))
	h.Require.Equal(403, decodeJSONErrorEnvelope(h).APIStatusCode)
	h.Require.NotContains(h.Stdout.String(), "member@example.com")
	h.Require.Equal(2, countCalls(m, "GET", "/v1/grid/team_settings/users"))
}
