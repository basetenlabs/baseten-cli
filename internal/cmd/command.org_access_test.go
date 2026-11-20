package cmd_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
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

func orgAccessAssignmentFixture() map[string]any {
	return map[string]any{
		"id":           "as1",
		"granted_via":  "ASSIGNMENT",
		"role_id":      "role_1",
		"role_name":    "deployer",
		"subject_type": "SERVICE_ACCOUNT",
		"subject_id":   "sa1",
		"subject_name": "ci-deployer",
		"team_id":      "t123456",
		"team_name":    "Engineering",
		"resource_sets": []any{
			[]any{map[string]any{"type": "ENVIRONMENT", "identifier": "production"}},
			[]any{
				map[string]any{"type": "ENVIRONMENT", "identifier": "staging"},
				map[string]any{"type": "MODEL", "identifier": "m1"},
			},
		},
		"created_by": "u0",
		"created_at": "2026-11-01T00:00:00Z",
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

func Test_Org_Access_Role_Create_SendsTeamRole(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/roles", 200, orgAccessRoleFixture("role_1", "deployer"))

	h.Require.NoError(h.Execute("org", "access", "role", "create", "--name", "deployer", "--kind", "team",
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

func Test_Org_Access_Role_List_FollowsPages(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)

	h.Require.NoError(h.Execute("org", "access", "role", "list", "--jq", ".items[].name"))
	h.Require.Equal("\"reader\"\n\"deployer\"\n", h.Stdout.String())
}

func Test_Org_Access_Role_Update_AddsAndRemoves(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("PATCH", "/v1/access/roles/role_1", 200, orgAccessRoleFixture("role_1", "deployer"))

	h.Require.NoError(h.Execute("org", "access", "role", "update", "--name", "deployer",
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

func Test_Org_Access_Assignment_Create_ServiceAccountWithResourceSets(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/assignments", 200, orgAccessAssignmentFixture())

	h.Require.NoError(h.Execute("org", "access", "assignment", "create", "--role-name", "deployer",
		"--service-account-name", "ci-deployer", "--team", "Engineering",
		"--resource-set", "model:m1,environment:staging", "--resource-set", "ENVIRONMENT:production"))
	h.Require.Equal(map[string]any{
		"role_id":      "role_1",
		"subject_type": "SERVICE_ACCOUNT",
		"subject_id":   "sa1",
		"team_id":      "t123456",
		"resource_sets": []any{
			[]any{
				map[string]any{"type": "MODEL", "identifier": "m1"},
				map[string]any{"type": "ENVIRONMENT", "identifier": "staging"},
			},
			[]any{map[string]any{"type": "ENVIRONMENT", "identifier": "production"}},
		},
	}, m.FindCall("POST", "/v1/access/assignments").BodyJSON(t))
	h.Require.Equal("as1\n", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Assigned role deployer")
}

func Test_Org_Access_Assignment_Create_OrganizationRoleForDirectoryGroup(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/assignments", 200, orgAccessAssignmentFixture())

	h.Require.NoError(h.Execute("org", "access", "assignment", "create", "--role-id", "role_9",
		"--directory-group-id", "dg1"))
	h.Require.Equal(map[string]any{
		"role_id":      "role_9",
		"subject_type": "DIRECTORY_GROUP",
		"subject_id":   "dg1",
	}, m.FindCall("POST", "/v1/access/assignments").BodyJSON(t))
	h.Require.Nil(m.FindCall("GET", "/v1/access/roles"))
}

func Test_Org_Access_Assignment_Create_RefusesMalformedResourceSet(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})
	h.Require.ErrorContains(h.Execute("org", "access", "assignment", "create", "--role-id", "role_1",
		"--user-id", "u1", "--resource-set", "model"), "isn't TYPE:ID")
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
		"subject_type": {"USER"},
		"subject_id":   {"u1"},
	}, query)
	h.Require.Contains(h.Stdout.String(), "service account ci-deployer")
	h.Require.Contains(h.Stdout.String(), "environment:production; environment:staging,model:m1")
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
		{"yourself", nil, "/v1/users/me/access"},
		{"user by email", []string{"--user-email", "ada@example.com"}, "/v1/users/u1/access"},
		{"service account by name", []string{"--service-account-name", "ci-deployer"}, "/v1/service_accounts/sa1/access"},
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

func Test_Org_Access_Describe_ListsWhereEachPermissionIsHeld(t *testing.T) {
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

	h.Require.NoError(h.Execute("org", "access", "describe"))
	h.Require.Contains(h.Stdout.String(), "service account ci-deployer (sa1)")
	h.Require.Contains(h.Stdout.String(), "model:m1,environment:staging")
	h.Require.NotContains(h.Stderr.String(), "aren't available")
}

func Test_Org_Access_Check_InTeamWhereAResourceSetSelects(t *testing.T) {
	h := NewCommandHarness(t)
	orgAccessRoutes(h)
	m := h.MockManagementAPI()
	resourceSet := []any{
		map[string]any{"type": "MODEL", "identifier": "m1"},
		map[string]any{"type": "ENVIRONMENT", "identifier": "staging"},
	}
	m.SetRoute("POST", "/v1/access/check", 200, map[string]any{
		"results": []any{map[string]any{
			"permission":   "model.deployment.write",
			"team_id":      "t123456",
			"resource_set": resourceSet,
			"allowed":      true,
		}},
	})

	h.Require.NoError(h.Execute("org", "access", "check", "--permission", "model.deployment.write",
		"--team", "Engineering", "--resource-set", "model:m1,environment:staging"))
	h.Require.Equal(map[string]any{
		"checks": []any{map[string]any{
			"permission":   "model.deployment.write",
			"team_id":      "t123456",
			"resource_set": resourceSet,
		}},
	}, m.FindCall("POST", "/v1/access/check").BodyJSON(t))
	h.Require.Contains(h.Stdout.String(), "yes")
}

func Test_Org_Access_Check_ForTheOrganization(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/check", 200, map[string]any{"results": []any{}})

	h.Require.NoError(h.Execute("org", "access", "check", "--permission", "access.role.write"))
	h.Require.Equal(map[string]any{
		"checks": []any{map[string]any{"permission": "access.role.write"}},
	}, m.FindCall("POST", "/v1/access/check").BodyJSON(t))
}

func Test_Org_Access_Settings_Update(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("PATCH", "/v1/organizations/me", 200, map[string]any{
		"org_id":                            "o1",
		"allow_personal_api_keys":           true,
		"allow_team_admins_to_manage_roles": false,
	})

	h.Require.NoError(h.Execute("org", "access", "settings", "update", "--allow-team-admins-to-manage-roles", "false"))
	h.Require.Equal(map[string]any{"allow_team_admins_to_manage_roles": false},
		m.FindCall("PATCH", "/v1/organizations/me").BodyJSON(t))
	h.Require.Contains(h.Stderr.String(), "Updated role settings")
}

func Test_Org_Access_RoleInUse_ReportsConflict(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("DELETE", "/v1/access/roles/role_1", 409, map[string]any{
		"code": "CONFLICT", "message": "The role is assigned.", "details": map[string]any{"reason": "ROLE_IN_USE"},
	})

	h.Require.ErrorContains(h.Execute("org", "access", "role", "delete", "--id", "role_1"), "ROLE_IN_USE")
}
