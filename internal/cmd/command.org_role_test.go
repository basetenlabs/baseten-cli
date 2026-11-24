package cmd_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/basetenlabs/baseten-cli/cmd"
)

func orgAccessRoleFixture(id, name string) map[string]any {
	return map[string]any{
		"id":               id,
		"name":             name,
		"description":      "",
		"built_in":         false,
		"kind":             "TEAM",
		"team_id":          nil,
		"team_name":        nil,
		"permissions":      []any{"model.deployment.write"},
		"include_role_ids": []any{},
	}
}

// orgAccessRoutes serves the Engineering team, the ci-deployer service account,
// a user, and roles over two pages, so name lookups have to follow the cursor.
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

func Test_Org_Role_Create_SendsTeamRole(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/roles", 200, orgAccessRoleFixture("role_1", "deployer"))

	h.Require.NoError(h.Execute("org", "role", "create", "--name", "deployer", "--kind", "team",
		"--team", "Engineering", "--permission", "model.deployment.write", "--permission", "model.read",
		"--include", "baseten:reader"))
	h.Require.Equal(map[string]any{
		"name":             "deployer",
		"kind":             "TEAM",
		"team_id":          "t123456",
		"description":      "",
		"permissions":      []any{"model.deployment.write", "model.read"},
		"include_role_ids": []any{"baseten:reader"},
	}, m.FindCall("POST", "/v1/access/roles").BodyJSON(t))
	h.Require.Equal("role_1\n", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Created role deployer")
}

func Test_Org_Role_List_FollowsPages(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)

	h.Require.NoError(h.Execute("org", "role", "list", "--jq", ".items[].name"))
	h.Require.Equal("\"reader\"\n\"deployer\"\n", h.Stdout.String())
}

func Test_Org_Role_List_CountsPermissions(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)

	h.Require.NoError(h.Execute("org", "role", "list"))
	h.Require.Regexp(`deployer\s+team\s+-\s+false\s+1\s`, h.Stdout.String())
	h.Require.NotContains(h.Stdout.String(), "model.deployment.write")
}

func Test_Org_Role_Describe_ListsPermissions(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	role := orgAccessRoleFixture("role_1", "deployer")
	role["permissions"] = []any{"model.deployment.write", "model.read"}
	h.MockManagementAPI().SetRoute("GET", "/v1/access/roles/role_1", 200, role)

	h.Require.NoError(h.Execute("org", "role", "describe", "--name", "deployer"))
	h.Require.Contains(h.Stdout.String(), "Permissions: model.deployment.write, model.read")
}

func Test_Org_Role_Update_AddsAndRemoves(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("PATCH", "/v1/access/roles/role_1", 200, orgAccessRoleFixture("role_1", "deployer"))

	h.Require.NoError(h.Execute("org", "role", "update", "--name", "deployer",
		"--description", "", "--add-permission", "model.deployment.delete",
		"--remove-permission", "model.deployment.write", "--remove-include", "baseten:reader"))
	h.Require.Equal(map[string]any{
		"description":                "",
		"permissions":                []any{"model.deployment.delete"},
		"permissions_to_delete":      []any{"model.deployment.write"},
		"include_role_ids_to_delete": []any{"baseten:reader"},
	}, m.FindCall("PATCH", "/v1/access/roles/role_1").BodyJSON(t))
	query, err := url.ParseQuery(m.FindCall("GET", "/v1/access/roles").RawQuery)
	h.Require.NoError(err)
	h.Require.Equal("deployer", query.Get("name"))
}

func Test_Org_Role_Update_RequiresAChange(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})
	h.Require.ErrorContains(h.Execute("org", "role", "update", "--id", "role_1"), "at least one")
}

func Test_Org_Role_Delete_UnknownName(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)

	h.Require.ErrorContains(h.Execute("org", "role", "delete", "--name", "missing"), `no role named "missing"`)
	h.Require.Nil(h.MockManagementAPI().FindCall("DELETE", "/v1/access/roles/missing"))
}

// The API lists built-in roles with the organization's custom roles, and a
// built-in added later can share a custom role's name.
func Test_Org_Role_Describe_NameSharedByTwoRolesNeedsID(t *testing.T) {
	builtIn := orgAccessRoleFixture("baseten:deployer", "Deployer")
	builtIn["built_in"] = true
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/access/roles", 200, map[string]any{
		"items":      []any{builtIn, orgAccessRoleFixture("role_7", "Deployer")},
		"pagination": map[string]any{"has_more": false},
	})

	h.Require.ErrorContains(h.Execute("org", "role", "describe", "--name", "Deployer"),
		`more than one role is named "Deployer" (baseten:deployer, role_7); pass its ID instead`)
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Nil(m.FindCall("GET", "/v1/access/roles/role_7"))
}

func Test_Org_Role_Delete_InUseReportsConflict(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("DELETE", "/v1/access/roles/role_1", 409, map[string]any{
		"code": "CONFLICT", "message": "The role is assigned.", "details": map[string]any{"reason": "ROLE_IN_USE"},
	})

	h.Require.ErrorContains(h.Execute("org", "role", "delete", "--id", "role_1"), "ROLE_IN_USE")
}
