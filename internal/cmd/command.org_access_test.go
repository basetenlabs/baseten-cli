package cmd_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func orgAccessRoleFixture(id, name string) map[string]any {
	return map[string]any{
		"id":          id,
		"name":        name,
		"description": "",
		"built_in":    false,
		"kind":        "team",
		"team_id":     nil,
		"permissions": []any{"model.deployment.write"},
		"includes":    []any{},
	}
}

func orgAccessAssignmentFixture() map[string]any {
	return map[string]any{
		"id":      "as1",
		"subject": map[string]any{"type": "user", "id": "sa1"},
		"role_id": "role_1",
		"scope": map[string]any{
			"team_id":               "t123456",
			"resource_selection":    map[string]any{"mode": "all", "resources": []any{}},
			"environment_selection": []any{},
		},
		"created_by": "u0",
		"created_at": "2026-11-01T00:00:00Z",
	}
}

// orgAccessRoutes serves the Engineering team, the ci-deployer service account,
// and roles over two pages, so name lookups have to follow the cursor.
func orgAccessRoutes(h *CommandHarness) {
	gridRouteTeams(h)
	orgServiceAccountRoutes(h)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/access/roles", func(w http.ResponseWriter, r *http.Request) {
		page := map[string]any{
			"items":      []any{orgAccessRoleFixture("baseten:reader", "reader")},
			"pagination": map[string]any{"has_more": true, "cursor": "next"},
		}
		if r.URL.Query().Get("cursor") == "next" {
			page = map[string]any{
				"items":      []any{orgAccessRoleFixture("role_1", "deployer")},
				"pagination": map[string]any{"has_more": false},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	})
	m.SetRoute("GET", "/v1/users", 200, map[string]any{
		"items": []any{map[string]any{"user_id": "u1", "email": "ada@example.com"}},
	})
}

func Test_Org_Access_Role_Create_SendsTeamRole(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/roles", 200, orgAccessRoleFixture("role_1", "deployer"))

	h.Require.NoError(h.Execute("org", "access", "role", "create", "--name", "deployer", "--kind", "team",
		"--team", "Engineering", "--permission", "model.deployment.write", "--permission", "model.read"))
	h.Require.Equal(map[string]any{
		"name":        "deployer",
		"kind":        "team",
		"team_id":     "t123456",
		"description": "",
		"permissions": []any{"model.deployment.write", "model.read"},
		"includes":    []any{},
	}, m.FindCall("POST", "/v1/access/roles").BodyJSON(t))
	h.Require.Equal("role_1\n", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Created role deployer")
}

func Test_Org_Access_Role_List_FollowsPages(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)

	h.Require.NoError(h.Execute("org", "access", "role", "list", "--jq", ".items[].name"))
	h.Require.Equal("\"reader\"\n\"deployer\"\n", h.Stdout.String())
}

func Test_Org_Access_Role_Update_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("PATCH", "/v1/access/roles/role_1", 200, orgAccessRoleFixture("role_1", "deployer"))

	h.Require.NoError(h.Execute("org", "access", "role", "update", "--name", "deployer",
		"--description", "", "--permission", "model.deployment.delete"))
	h.Require.Equal(map[string]any{
		"description": "",
		"permissions": []any{"model.deployment.delete"},
	}, m.FindCall("PATCH", "/v1/access/roles/role_1").BodyJSON(t))
}

func Test_Org_Access_Role_Update_RequiresAChange(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})
	h.Require.ErrorContains(h.Execute("org", "access", "role", "update", "--id", "role_1"), "at least one")
}

func Test_Org_Access_Role_Delete_UnknownName(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)

	h.Require.ErrorContains(h.Execute("org", "access", "role", "delete", "--name", "missing"), `no role named "missing"`)
	h.Require.Nil(h.MockManagementAPI().FindCall("DELETE", "/v1/access/roles/missing"))
}

func Test_Org_Access_Assignment_Create_ServiceAccountOnModels(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/assignments", 200, orgAccessAssignmentFixture())

	h.Require.NoError(h.Execute("org", "access", "assignment", "create", "--role-name", "deployer",
		"--service-account-name", "ci-deployer", "--team", "Engineering",
		"--model-id", "m1", "--model-name", "llama"))
	h.Require.Equal(map[string]any{
		"subject": map[string]any{"type": "user", "id": "sa1"},
		"role_id": "role_1",
		"scope": map[string]any{
			"team_id": "t123456",
			"resource_selection": map[string]any{
				"mode": "selected",
				"resources": []any{
					map[string]any{"type": "model", "id": "m1"},
					map[string]any{"type": "model", "name": "llama"},
				},
			},
			"environment_selection": []any{},
		},
	}, m.FindCall("POST", "/v1/access/assignments").BodyJSON(t))
	h.Require.Equal("as1\n", h.Stdout.String())
}

func Test_Org_Access_Assignment_Create_OrganizationRoleForDirectoryGroup(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/assignments", 200, orgAccessAssignmentFixture())

	h.Require.NoError(h.Execute("org", "access", "assignment", "create", "--role-id", "role_9",
		"--directory-group-id", "dg1"))
	h.Require.Equal(map[string]any{
		"subject": map[string]any{"type": "directory_group", "id": "dg1"},
		"role_id": "role_9",
		"scope": map[string]any{
			"resource_selection":    map[string]any{"mode": "all", "resources": []any{}},
			"environment_selection": []any{},
		},
	}, m.FindCall("POST", "/v1/access/assignments").BodyJSON(t))
	h.Require.Nil(m.FindCall("GET", "/v1/access/roles"))
}

func Test_Org_Access_Assignment_Create_ModelsNeedTeam(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})
	h.Require.ErrorContains(h.Execute("org", "access", "assignment", "create", "--role-id", "role_1",
		"--user-id", "u1", "--model-id", "m1"), "need --team")
}

func Test_Org_Access_Assignment_List_Filters(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/access/assignments", 200, map[string]any{
		"items":      []any{orgAccessAssignmentFixture()},
		"pagination": map[string]any{"has_more": false},
	})

	h.Require.NoError(h.Execute("org", "access", "assignment", "list", "--team", "Engineering",
		"--role-name", "deployer", "--user-email", "ada@example.com"))
	query, err := url.ParseQuery(m.FindCall("GET", "/v1/access/assignments").RawQuery)
	h.Require.NoError(err)
	h.Require.Equal(url.Values{
		"team_id":      {"t123456"},
		"role_id":      {"role_1"},
		"subject_type": {"user"},
		"subject_id":   {"u1"},
	}, query)
	h.Require.Contains(h.Stdout.String(), "as1")
}

func Test_Org_Access_Assignment_List_OneHolder(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})
	h.Require.ErrorContains(h.Execute("org", "access", "assignment", "list", "--user-id", "u1",
		"--directory-group-id", "dg1"), "at most one")
}

func Test_Org_Access_Describe_PicksEndpoint(t *testing.T) {
	access := map[string]any{
		"subject":     map[string]any{"type": "user", "id": "u1"},
		"assignments": []any{},
		"effective":   nil,
	}
	for _, tc := range []struct {
		name string
		args []string
		path string
	}{
		{"yourself", nil, "/v1/access/me"},
		{"user by email", []string{"--user-email", "ada@example.com"}, "/v1/access/users/u1"},
		{"service account by name", []string{"--service-account-name", "ci-deployer"}, "/v1/access/service_accounts/sa1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			orgAccessRoutes(h)
			m := h.MockManagementAPI()
			m.SetRoute("GET", tc.path, 200, access)

			h.Require.NoError(h.Execute(append([]string{"org", "access", "describe"}, tc.args...)...))
			h.Require.NotNil(m.FindCall("GET", tc.path))
			h.Require.Contains(h.Stderr.String(), "Effective permissions aren't available yet.")
		})
	}
}

func Test_Org_Access_Check_OnTeam(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	team := map[string]any{"type": "team", "id": "t123456"}
	m.SetRoute("POST", "/v1/access/check", 200, map[string]any{
		"results": []any{map[string]any{
			"check": map[string]any{"permission": "model.deployment.write", "resource": team},
			"allow": true,
		}},
	})

	h.Require.NoError(h.Execute("org", "access", "check", "--permission", "model.deployment.write",
		"--team", "Engineering"))
	h.Require.Equal(map[string]any{
		"checks": []any{map[string]any{"permission": "model.deployment.write", "resource": team}},
	}, m.FindCall("POST", "/v1/access/check").BodyJSON(t))
	h.Require.Contains(h.Stdout.String(), "yes")
}

func Test_Org_Access_Settings_Update(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("PATCH", "/v1/access/settings", 200, map[string]any{"allow_team_admins_to_manage_roles": false})

	h.Require.NoError(h.Execute("org", "access", "settings", "update", "--allow-team-admins-to-manage-roles", "false"))
	h.Require.Equal(map[string]any{"allow_team_admins_to_manage_roles": false},
		m.FindCall("PATCH", "/v1/access/settings").BodyJSON(t))
	h.Require.Contains(h.Stderr.String(), "Updated role settings")
}

func Test_Org_Access_RoleInUse_ReportsConflict(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("DELETE", "/v1/access/roles/role_1", 409, map[string]any{
		"code": "CONFLICT", "message": "The role is assigned.", "details": map[string]any{"reason": "role_in_use"},
	})

	h.Require.ErrorContains(h.Execute("org", "access", "role", "delete", "--id", "role_1"), "role_in_use")
}
