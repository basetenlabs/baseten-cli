package cmd_test

import (
	"testing"
)

func teamFixture(id, name string, isDefault bool) map[string]any {
	return map[string]any{
		"id":         id,
		"name":       name,
		"default":    isDefault,
		"created_at": "2026-01-02T03:04:05Z",
	}
}

func Test_Org_Team_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []any{}})

	h.Require.NoError(h.Execute("org", "team", "list"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No teams found.")
}

func Test_Org_Team_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/teams", 200, map[string]any{
		"teams": []any{teamFixture("t1", "Engineering", true)},
	})

	h.Require.NoError(h.Execute("org", "team", "list"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID")
	h.Require.Contains(out, "NAME")
	h.Require.Contains(out, "DEFAULT")
	h.Require.Contains(out, "CREATED")
	h.Require.Contains(out, "t1")
	h.Require.Contains(out, "Engineering")
	h.Require.Contains(out, "yes")
}

func Test_Org_Team_List_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/teams", 200, map[string]any{
		"teams": []any{teamFixture("t1", "Engineering", true)},
	})

	h.Require.NoError(h.Execute("org", "team", "list", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"id": "t1"`)
}

func Test_Org_Team_Describe_ByID(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	// --team-id is passed straight through; no team list lookup.
	m.SetRoute("GET", "/v1/teams/t1", 200, teamFixture("t1", "Engineering", true))

	h.Require.NoError(h.Execute("org", "team", "describe", "--team-id", "t1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID:")
	h.Require.Contains(out, "t1")
	h.Require.Contains(out, "Engineering")
	h.Require.Contains(out, "Default:")
}

func Test_Org_Team_Describe_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/teams", 200, map[string]any{
		"teams": []any{teamFixture("t1", "Engineering", true)},
	})

	h.Require.NoError(h.Execute("org", "team", "describe", "--team-name", "Engineering"))
	out := h.Stdout.String()
	h.Require.Contains(out, "t1")
	h.Require.Contains(out, "Engineering")
	call := m.FindCall("GET", "/v1/teams")
	h.Require.NotNil(call)
	h.Require.Equal("Engineering", call.Query().Get("name"))
}

func Test_Org_Team_Describe_ByName_NotFound(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []any{}})

	err := h.Execute("org", "team", "describe", "--team-name", "ghost")
	h.Require.ErrorContains(err, `no team named "ghost"`)
}

func Test_Org_Team_Describe_IDAndName_Rejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("org", "team", "describe", "--team-id", "t1", "--team-name", "Engineering")
	h.Require.Error(err)
}

func Test_Org_Team_Describe_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/teams/t1", 200, teamFixture("t1", "Engineering", true))

	h.Require.NoError(h.Execute("org", "team", "describe", "--team-id", "t1", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"id": "t1"`)
}
