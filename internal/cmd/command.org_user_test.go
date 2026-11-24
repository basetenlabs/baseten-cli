package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/basetenlabs/baseten-cli/cmd"
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

func Test_Org_User_Describe_ByEmail(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/users", 200, map[string]any{
		"items":      []any{userFixture("u9", "grace@acme.io", "Grace Hopper")},
		"pagination": map[string]any{"has_more": false},
	})

	h.Require.NoError(h.Execute("org", "user", "describe", "--user-email", "grace@acme.io"))
	h.Require.Contains(h.Stdout.String(), "grace@acme.io")
	call := m.FindCall("GET", "/v1/users")
	h.Require.NotNil(call)
	h.Require.Equal("grace@acme.io", call.Query().Get("email"))
}

func Test_Org_User_Describe_ByEmail_NotFound(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/users", 200,
		map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": false}})

	err := h.Execute("org", "user", "describe", "--user-email", "ghost@acme.io")
	h.Require.ErrorContains(err, `no user with email "ghost@acme.io"`)
}

// Deliberately unrealistic: the API is not expected to return two users for one
// email, but if it does the CLI must not pick one.
func Test_Org_User_Describe_ByEmail_SharedEmailNeedsID(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/users", 200, map[string]any{
		"items": []any{
			userFixture("u1", "ada@acme.io", "Ada"), userFixture("u2", "ada@acme.io", "Ada L."),
		},
		"pagination": map[string]any{"has_more": false},
	})

	err := h.Execute("org", "user", "describe", "--user-email", "ada@acme.io")
	h.Require.ErrorContains(err, `more than one user has email "ada@acme.io"; pass --user-id instead`)
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
}

func Test_Org_User_Describe_IDAndEmail_Rejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("org", "user", "describe", "--user-id", "u1", "--user-email", "ada@acme.io")
	h.Require.Error(err)
}

func Test_Org_User_Describe_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/users/me", 200,
		userFixture("u1", "ada@acme.io", "Ada Lovelace"))

	h.Require.NoError(h.Execute("org", "user", "describe", "--user-id", "me", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"user_id": "u1"`)
}

func Test_Org_User_Access_PicksUser(t *testing.T) {
	access := map[string]any{
		"subject_type": "USER",
		"subject_id":   "u1",
		"subject_name": "Ada",
		"assignments":  []any{},
		"permissions":  nil,
	}
	for _, tc := range []struct {
		name string
		args []string
		path string
	}{
		{"yourself", []string{"--user-id", "me"}, "/v1/users/me/access"},
		{"user by ID", []string{"--user-id", "u2"}, "/v1/users/u2/access"},
		{"user by email", []string{"--user-email", "ada@example.com"}, "/v1/users/u1/access"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			orgAccessRoutes(h)
			m := h.MockManagementAPI()
			m.SetRoute("GET", tc.path, 200, access)

			h.Require.NoError(h.Execute(append([]string{"org", "user", "access"}, tc.args...)...))
			h.Require.NotNil(m.FindCall("GET", tc.path))
			h.Require.Contains(h.Stderr.String(), "Effective permissions aren't available yet.")
		})
	}
}

func Test_Org_User_Access_ListsWhereEachPermissionIsHeld(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/users/me/access", 200, map[string]any{
		"subject_type": "SERVICE_ACCOUNT",
		"subject_id":   "sa1",
		"subject_name": "ci-deployer",
		"assignments":  []any{orgAccessAssignmentFixture()},
		"permissions": []any{map[string]any{
			"name":         "model.deployment.write",
			"display_name": "Push deployments",
			"category":     "model.deployment",
			"grants": []any{map[string]any{
				"team_id":   "t123456",
				"team_name": "Engineering",
				"resource_set": []any{
					map[string]any{"type": "MODEL", "identifier": "m1"},
					map[string]any{"type": "ENVIRONMENT", "identifier": "staging"},
				},
			}},
		}},
	})

	h.Require.NoError(h.Execute("org", "user", "access", "--user-id", "me"))
	h.Require.Contains(h.Stdout.String(), "service account ci-deployer (sa1)")
	h.Require.Contains(h.Stdout.String(), "model:m1,environment:staging")
	h.Require.NotContains(h.Stderr.String(), "aren't available")
}
