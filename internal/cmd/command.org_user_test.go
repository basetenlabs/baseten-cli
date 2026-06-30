package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func userFixture(id, email, name string) map[string]any {
	return map[string]any{
		"user_id":        id,
		"email":          email,
		"name":           name,
		"workspace_name": "Acme",
	}
}

func Test_Org_User_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/users", 200,
		map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": false}})

	h.Require.NoError(h.Execute("org", "user", "list"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No users found.")
}

func Test_Org_User_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/users", 200, map[string]any{
		"items":      []any{userFixture("u1", "ada@acme.io", "Ada Lovelace")},
		"pagination": map[string]any{"has_more": false},
	})

	h.Require.NoError(h.Execute("org", "user", "list"))
	out := h.Stdout.String()
	h.Require.Contains(out, "USER ID")
	h.Require.Contains(out, "EMAIL")
	h.Require.Contains(out, "NAME")
	h.Require.Contains(out, "u1")
	h.Require.Contains(out, "ada@acme.io")
	h.Require.Contains(out, "Ada Lovelace")
	// Workspace is constant across rows, so it is not a column.
	h.Require.NotContains(out, "WORKSPACE")
}

func Test_Org_User_List_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/users", 200, map[string]any{
		"items":      []any{userFixture("u1", "ada@acme.io", "Ada Lovelace")},
		"pagination": map[string]any{"has_more": false},
	})

	h.Require.NoError(h.Execute("org", "user", "list", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"user_id": "u1"`)
}

func Test_Org_User_List_Paginates(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":      []any{userFixture("u1", "a@acme.io", "A")},
				"pagination": map[string]any{"has_more": true, "cursor": "page2"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":      []any{userFixture("u2", "b@acme.io", "B")},
			"pagination": map[string]any{"has_more": false},
		})
	})

	h.Require.NoError(h.Execute("org", "user", "list"))
	out := h.Stdout.String()
	h.Require.Contains(out, "u1")
	h.Require.Contains(out, "u2")
}

func Test_Org_User_Describe_Me(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/users/me", 200,
		userFixture("u1", "ada@acme.io", "Ada Lovelace"))

	h.Require.NoError(h.Execute("org", "user", "describe", "--user-id", "me"))
	out := h.Stdout.String()
	h.Require.Contains(out, "User ID:")
	h.Require.Contains(out, "u1")
	h.Require.Contains(out, "ada@acme.io")
	h.Require.Contains(out, "Acme")
}

func Test_Org_User_Describe_ByID(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/users/u9", 200,
		userFixture("u9", "grace@acme.io", "Grace Hopper"))

	h.Require.NoError(h.Execute("org", "user", "describe", "--user-id", "u9"))
	h.Require.Contains(h.Stdout.String(), "grace@acme.io")
}

func Test_Org_User_Describe_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/users/me", 200,
		userFixture("u1", "ada@acme.io", "Ada Lovelace"))

	h.Require.NoError(h.Execute("org", "user", "describe", "--user-id", "me", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"user_id": "u1"`)
}
