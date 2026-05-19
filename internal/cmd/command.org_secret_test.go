package cmd_test

import (
	"net/http"
	"strings"
	"testing"
)

func Test_Org_Secret_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/secrets", 200, map[string]any{"secrets": []any{}})

	h.Require.NoError(h.Execute("org", "secret", "list"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No secrets found.")
}

func Test_Org_Secret_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/secrets", 200, map[string]any{
		"secrets": []any{
			map[string]any{"name": "alpha", "team_name": "default", "created_at": "2026-01-02T03:04:05Z"},
			map[string]any{"name": "beta", "team_name": "ml", "created_at": "2026-02-03T04:05:06Z"},
		},
	})

	h.Require.NoError(h.Execute("org", "secret", "list"))
	out := h.Stdout.String()
	h.Require.Contains(out, "NAME")
	h.Require.Contains(out, "TEAM")
	h.Require.Contains(out, "CREATED")
	h.Require.Contains(out, "alpha")
	h.Require.Contains(out, "beta")
	h.Require.Contains(out, "2026-01-02T03:04:05Z")
}

func Test_Org_Secret_List_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/secrets", 200, map[string]any{
		"secrets": []any{
			map[string]any{"name": "alpha", "team_name": "default", "created_at": "2026-01-02T03:04:05Z"},
		},
	})

	h.Require.NoError(h.Execute("org", "secret", "list", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"name": "alpha"`)
}

func Test_Org_Secret_Set_ValueFlag(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/secrets", 200, map[string]any{
		"name": "my-secret", "team_name": "default", "created_at": "2026-01-01T00:00:00Z",
	})

	h.Require.NoError(h.Execute("org", "secret", "set", "--name", "my-secret", "--value", "hunter2"))
	call := m.FindCall("POST", "/v1/secrets")
	h.Require.NotNil(call)
	h.Require.Contains(call.Body, `"name":"my-secret"`)
	h.Require.Contains(call.Body, `"value":"hunter2"`)
	h.Require.Contains(h.Stderr.String(), "Set secret my-secret")
}

func Test_Org_Secret_Set_ValueFlagEmpty(t *testing.T) {
	// Explicit --value="" must be honored (use Cobra's Changed).
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/secrets", 200, map[string]any{
		"name": "empty", "team_name": "default", "created_at": "2026-01-01T00:00:00Z",
	})

	h.Require.NoError(h.Execute("org", "secret", "set", "--name", "empty", "--value", ""))
	call := m.FindCall("POST", "/v1/secrets")
	h.Require.NotNil(call)
	h.Require.Contains(call.Body, `"value":""`)
}

func Test_Org_Secret_Set_Stdin(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/secrets", 200, map[string]any{
		"name": "from-stdin", "team_name": "default", "created_at": "2026-01-01T00:00:00Z",
	})
	// Trailing CRLF should be stripped.
	h.Stdin.WriteString("piped-value\r\n")

	h.Require.NoError(h.Execute("org", "secret", "set", "--name", "from-stdin"))
	call := m.FindCall("POST", "/v1/secrets")
	h.Require.NotNil(call)
	h.Require.Contains(call.Body, `"value":"piped-value"`)
}

func Test_Org_Secret_Set_StdinEmpty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit when stdin is empty")
	})
	// Stdin is empty by default.
	err := h.Execute("org", "secret", "set", "--name", "x")
	h.Require.Error(err)
	h.Require.True(strings.Contains(err.Error(), "no secret value supplied"))
}

func Test_Org_Secret_Delete(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/secrets/my-secret", 200, map[string]any{
		"name": "my-secret", "team_name": "default", "created_at": "2026-01-01T00:00:00Z",
	})

	h.Require.NoError(h.Execute("org", "secret", "delete", "--name", "my-secret"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/secrets/my-secret"))
	h.Require.Contains(h.Stderr.String(), "Deleted secret my-secret")
}

func Test_Org_Secret_Team_RoutesToTeamScopedEndpoint(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	// ResolveTeam lists teams to translate a --team name into an ID.
	m.SetRoute("GET", "/v1/teams", 200, map[string]any{
		"teams": []any{
			map[string]any{"id": "team-1", "name": "ml"},
		},
	})
	m.SetRoute("GET", "/v1/teams/team-1/secrets", 200, map[string]any{"secrets": []any{}})

	h.Require.NoError(h.Execute("org", "secret", "list", "--team", "ml"))
	h.Require.NotNil(m.FindCall("GET", "/v1/teams/team-1/secrets"))
}
