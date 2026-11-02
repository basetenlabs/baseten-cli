package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func gridMachineUserFixture() map[string]any {
	return map[string]any{"id": "u123456", "name": "review-bot", "team_id": "t123456", "team_name": "Engineering", "created_by": "u000001", "created_at": "2026-11-02T10:00:00Z"}
}

func Test_Grid_MachineUser_Create_IssuesFirstKey(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantBody map[string]any
	}{
		{"default key name", nil, map[string]any{"name": "review-bot", "team_id": "t123456", "api_key": map[string]any{}}},
		{"explicit key name", []string{"--api-key-name", "ci"}, map[string]any{"name": "review-bot", "team_id": "t123456", "api_key": map[string]any{"name": "ci"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			gridRouteTeams(h)
			m := h.MockManagementAPI()
			created := gridMachineUserFixture()
			created["api_key"] = "bsnt_mu.secret"
			m.SetRoute("POST", "/v1/grid/machine_users", 200, created)
			args := append([]string{"grid", "machine-user", "create", "--team", "Engineering", "--name", "review-bot"}, tc.args...)
			h.Require.NoError(h.Execute(args...))
			h.Require.Equal(tc.wantBody, m.FindCall("POST", "/v1/grid/machine_users").BodyJSON(t))
			h.Require.Equal("bsnt_mu.secret\n", h.Stdout.String())
			h.Require.Contains(h.Stderr.String(), "Created machine user review-bot (u123456) in team Engineering")
			h.Require.Contains(h.Stderr.String(), "Save this key now.")
		})
	}
}

func Test_Grid_MachineUser_Create_NoAPIKey(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/grid/machine_users", 200, gridMachineUserFixture())
	h.Require.NoError(h.Execute("grid", "machine-user", "create", "--name", "review-bot", "--no-api-key"))
	h.Require.Equal(map[string]any{"name": "review-bot"}, m.FindCall("POST", "/v1/grid/machine_users").BodyJSON(t))
	h.Require.Empty(h.Stdout.String())
	h.Require.NotContains(h.Stderr.String(), "Save this key")
}

func Test_Grid_MachineUser_Create_APIKeyNameConflictsWithNoAPIKey(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	err := h.Execute("grid", "machine-user", "create", "--name", "review-bot", "--no-api-key", "--api-key-name", "ci")
	h.Require.ErrorContains(err, "--api-key-name cannot be combined with --no-api-key")
	h.Require.Empty(m.Calls())
}

func Test_Grid_MachineUser_List_Pagination(t *testing.T) {
	for _, team := range []string{"", "Engineering"} {
		t.Run(team, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			if team != "" {
				gridRouteTeams(h)
			}
			m.SetRouteFunc("GET", "/v1/grid/machine_users", func(w http.ResponseWriter, r *http.Request) {
				want := ""
				if team != "" {
					want = "t123456"
				}
				h.Require.Equal(want, r.URL.Query().Get("team_id"))
				var response any = gridRoutePage([]any{gridMachineUserFixture()}, "next")
				if r.URL.Query().Get("cursor") == "next" {
					second := gridMachineUserFixture()
					second["id"], second["name"] = "u765432", "night-shift"
					response = gridRoutePage([]any{second}, nil)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			})
			args := []string{"grid", "machine-user", "list"}
			if team != "" {
				args = append(args, "--team", team)
			}
			h.Require.NoError(h.Execute(args...))
			h.Require.Equal(2, countCalls(m, "GET", "/v1/grid/machine_users"))
			out := h.Stdout.String()
			h.Require.Contains(out, "u123456")
			h.Require.Contains(out, "night-shift")
			h.Require.Contains(out, "2026-11-02T10:00:00Z")
		})
	}
}

func Test_Grid_MachineUser_List_EmptyJSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/grid/machine_users", 200, gridRoutePage([]any{}, nil))
	h.Require.NoError(h.Execute("grid", "machine-user", "list", "--output", "json"))
	h.Require.JSONEq(`{"items": []}`, h.Stdout.String())
}

func Test_Grid_MachineUser_Create_MissingKeyFailsLoudly(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("POST", "/v1/grid/machine_users", 200, gridMachineUserFixture())
	err := h.Execute("grid", "machine-user", "create", "--name", "review-bot")
	h.Require.ErrorContains(err, "the response has no API key")
	h.Require.Empty(h.Stdout.String())
}

func Test_Grid_MachineUser_Describe_Text(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/grid/machine_users/u123456", 200, gridMachineUserFixture())
	h.Require.NoError(h.Execute("grid", "machine-user", "describe", "--id", "u123456"))
	h.Require.Contains(h.Stdout.String(), "Team:       Engineering (t123456)")
	h.Require.Contains(h.Stdout.String(), "Created by: u000001")
}

func Test_Grid_MachineUser_Update_SendsName(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	renamed := gridMachineUserFixture()
	renamed["name"] = "night-shift"
	m.SetRoute("PATCH", "/v1/grid/machine_users/u123456", 200, renamed)
	h.Require.NoError(h.Execute("grid", "machine-user", "update", "--id", "u123456", "--name", "night-shift", "--output", "json"))
	h.Require.Equal(map[string]any{"name": "night-shift"}, m.FindCall("PATCH", "/v1/grid/machine_users/u123456").BodyJSON(t))
	var got map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal(renamed, got)
}

func Test_Grid_MachineUser_Delete_WithYes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/grid/machine_users/u123456", 200, map[string]any{"id": "u123456"})
	h.Require.NoError(h.Execute("grid", "machine-user", "delete", "--id", "u123456", "--yes"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/grid/machine_users/u123456"))
	h.Require.Contains(h.Stderr.String(), "Deleted machine user u123456")
}

func Test_Grid_MachineUser_Delete_NonInteractiveRequiresYes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	h.Require.Error(h.Execute("grid", "machine-user", "delete", "--id", "u123456"))
	h.Require.Empty(m.Calls())
}

func gridMachineUserKeys(h *CommandHarness) *MockManagementAPI {
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/grid/machine_users/u123456/api_keys", 200, map[string]any{"keys": []any{
		map[string]any{"name": "ci", "prefix": "bsnt_ci", "type": "GRID", "created_at": "2026-11-02T10:00:00Z", "last_used_at": "2026-11-02T11:00:00Z"},
		map[string]any{"name": "laptop", "prefix": "bsnt_lap", "type": "GRID", "created_at": "2026-11-02T10:00:00Z"},
	}})
	return m
}

func Test_Grid_MachineUser_APIKey_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	gridMachineUserKeys(h)
	h.Require.NoError(h.Execute("grid", "machine-user", "api-key", "list", "--machine-user-id", "u123456"))
	out := h.Stdout.String()
	h.Require.Contains(out, "LAST USED")
	h.Require.Contains(out, "bsnt_ci****")
	h.Require.Contains(out, "2026-11-02T11:00:00Z")
}

func Test_Grid_MachineUser_APIKey_Create_PrintsKeyOnce(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/grid/machine_users/u123456/api_keys", 200, map[string]any{"api_key": "bsnt_new.secret"})
	h.Require.NoError(h.Execute("grid", "machine-user", "api-key", "create", "--machine-user-id", "u123456", "--name", "nightly"))
	h.Require.Equal(map[string]any{"name": "nightly"}, m.FindCall("POST", "/v1/grid/machine_users/u123456/api_keys").BodyJSON(t))
	h.Require.Equal("bsnt_new.secret\n", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Save this key now.")
}

func Test_Grid_MachineUser_APIKey_Revoke_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	m := gridMachineUserKeys(h)
	m.SetRoute("DELETE", "/v1/grid/machine_users/u123456/api_keys/bsnt_ci", 200, map[string]any{"prefix": "bsnt_ci"})
	h.Require.NoError(h.Execute("grid", "machine-user", "api-key", "revoke", "--machine-user-id", "u123456", "--name", "ci"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/grid/machine_users/u123456/api_keys/bsnt_ci"))
	h.Require.Contains(h.Stderr.String(), "Revoked API key bsnt_ci")
}

func Test_Grid_MachineUser_APIKey_Revoke_ByPrefixSkipsList(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/grid/machine_users/u123456/api_keys/bsnt_ci", 200, map[string]any{"prefix": "bsnt_ci"})
	h.Require.NoError(h.Execute("grid", "machine-user", "api-key", "revoke", "--machine-user-id", "u123456", "--prefix", "bsnt_ci"))
	h.Require.Len(m.Calls(), 1)
}
