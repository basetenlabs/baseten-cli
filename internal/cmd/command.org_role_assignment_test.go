package cmd_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/basetenlabs/baseten-cli/cmd"
)

func orgRoleAssignmentFixture() map[string]any {
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
			map[string]any{"all_of": []any{map[string]any{"type": "ENVIRONMENT", "identifier": "production"}}},
			map[string]any{"all_of": []any{
				map[string]any{"type": "ENVIRONMENT", "identifier": "staging"},
				map[string]any{"type": "MODEL", "identifier": "m1"},
			}},
		},
		"created_by": "u0",
		"created_at": "2026-11-01T00:00:00Z",
	}
}

func Test_Org_RoleAssignment_Create_ServiceAccountWithResourceSets(t *testing.T) {
	h := NewCommandHarness(t)
	orgRoleRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/assignments", 200, orgRoleAssignmentFixture())

	h.Require.NoError(h.Execute("org", "role-assignment", "create", "--role-name", "deployer",
		"--service-account-name", "ci-deployer", "--team", "Engineering",
		"--resource-set", "model:m1,environment:staging", "--resource-set", "ENVIRONMENT:production"))
	h.Require.Equal(map[string]any{
		"role_id":      "role_1",
		"subject_type": "SERVICE_ACCOUNT",
		"subject_id":   "sa1",
		"team_id":      "t123456",
		"resource_sets": []any{
			map[string]any{"all_of": []any{
				map[string]any{"type": "MODEL", "identifier": "m1"},
				map[string]any{"type": "ENVIRONMENT", "identifier": "staging"},
			}},
			map[string]any{"all_of": []any{map[string]any{"type": "ENVIRONMENT", "identifier": "production"}}},
		},
	}, m.FindCall("POST", "/v1/access/assignments").BodyJSON(t))
	h.Require.Equal("as1\n", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Assigned role deployer")
}

func Test_Org_RoleAssignment_Create_OrganizationRoleForDirectoryGroup(t *testing.T) {
	h := NewCommandHarness(t)
	orgRoleRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/assignments", 200, orgRoleAssignmentFixture())

	h.Require.NoError(h.Execute("org", "role-assignment", "create", "--role-id", "role_9",
		"--directory-group-id", "dg1"))
	h.Require.Equal(map[string]any{
		"role_id":      "role_9",
		"subject_type": "DIRECTORY_GROUP",
		"subject_id":   "dg1",
	}, m.FindCall("POST", "/v1/access/assignments").BodyJSON(t))
	h.Require.Nil(m.FindCall("GET", "/v1/access/roles"))
}

func Test_Org_RoleAssignment_Create_RefusesMalformedResourceSet(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})
	h.Require.ErrorContains(h.Execute("org", "role-assignment", "create", "--role-id", "role_1",
		"--user-id", "u1", "--resource-set", "model"), "isn't TYPE:ID")
}

func Test_Org_RoleAssignment_List_Filters(t *testing.T) {
	h := NewCommandHarness(t)
	orgRoleRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/access/assignments", 200, map[string]any{
		"items":      []any{orgRoleAssignmentFixture()},
		"pagination": map[string]any{"has_more": false},
	})

	h.Require.NoError(h.Execute("org", "role-assignment", "list", "--team", "Engineering",
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

func Test_Org_RoleAssignment_List_OneHolder(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})
	h.Require.ErrorContains(h.Execute("org", "role-assignment", "list", "--user-id", "u1",
		"--directory-group-id", "dg1"), "at most one")
}

func Test_Org_RoleAssignment_Create_LooksUpRoleNameInTheTeam(t *testing.T) {
	h := NewCommandHarness(t)
	orgRoleRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/access/assignments", 200, orgRoleAssignmentFixture())

	h.Require.NoError(h.Execute("org", "role-assignment", "create", "--role-name", "deployer",
		"--user-id", "u1", "--team", "Engineering"))
	query, err := url.ParseQuery(m.FindCall("GET", "/v1/access/roles").RawQuery)
	h.Require.NoError(err)
	h.Require.Equal("deployer", query.Get("name"))
	h.Require.Equal("t123456", query.Get("team_id"))
}

func Test_Org_RoleAssignment_Create_RequiresAHolder(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})
	h.Require.ErrorContains(h.Execute("org", "role-assignment", "create", "--role-id", "role_1",
		"--user-id", ""), "pass one of --user-id")
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
}

func Test_Org_RoleAssignment_Describe_ShowsHolderAndResourceSets(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/access/assignments/as1", 200, orgRoleAssignmentFixture())

	h.Require.NoError(h.Execute("org", "role-assignment", "describe", "--id", "as1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Role:          deployer (role_1)")
	h.Require.Contains(out, "Holder:        service account ci-deployer (sa1)")
	h.Require.Contains(out, "Team:          Engineering")
	h.Require.Contains(out, "Resource sets: environment:production; environment:staging,model:m1")
	h.Require.Contains(out, "Created:       2026-11-01T00:00:00Z")
}

func Test_Org_RoleAssignment_Delete_RemovesByID(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/access/assignments/as1", 200, map[string]any{"id": "as1"})

	h.Require.NoError(h.Execute("org", "role-assignment", "delete", "--id", "as1"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/access/assignments/as1"))
	h.Require.Contains(h.Stderr.String(), "Removed role assignment as1")
}
