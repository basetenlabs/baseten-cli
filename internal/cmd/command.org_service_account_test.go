package cmd_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func orgServiceAccountFixture(id, name, presetType string) map[string]any {
	return map[string]any{
		"id":                 id,
		"name":               name,
		"disabled":           false,
		"preset_permissions": map[string]any{"type": presetType},
		"created_at":         "2026-11-01T00:00:00Z",
		"updated_at":         "2026-11-01T00:00:00Z",
	}
}

func orgServiceAccountPage(items ...any) map[string]any {
	return map[string]any{"items": items, "pagination": map[string]any{"has_more": false}}
}

// orgSetFilteredListRoute serves items at path the way the server filters a list:
// name keeps the item with that name, ignoring case, and predefined_kind keeps
// identity providers of that kind.
func orgSetFilteredListRoute(m *MockManagementAPI, path string, items ...map[string]any) {
	m.SetRouteFunc("GET", path, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		matched := []any{}
		for _, item := range items {
			if name := query.Get("name"); name != "" && !strings.EqualFold(item["name"].(string), name) {
				continue
			}
			if kind := query.Get("predefined_kind"); kind != "" {
				if predefined, _ := item["predefined"].(map[string]any); predefined == nil || predefined["kind"] != kind {
					continue
				}
			}
			matched = append(matched, item)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": matched, "pagination": map[string]any{"has_more": false}})
	})
}

// orgServiceAccountRoutes serves one team account, ci-deployer, for name lookups.
func orgServiceAccountRoutes(h *CommandHarness) {
	account := orgServiceAccountFixture("sa1", "ci-deployer", "WORKSPACE_MANAGE_ALL")
	account["team_id"] = "t123456"
	m := h.MockManagementAPI()
	orgSetFilteredListRoute(m, "/v1/service_accounts", account)
	m.SetRoute("GET", "/v1/service_accounts/sa1", 200, account)
	m.SetRoute("PATCH", "/v1/service_accounts/sa1", 200, account)
}

func Test_Org_ServiceAccount_Create_TeamAccount(t *testing.T) {
	h := NewCommandHarness(t)
	gridRouteTeams(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/service_accounts", 200, orgServiceAccountFixture("sa1", "ci-deployer", "WORKSPACE_MANAGE_ALL"))

	h.Require.NoError(h.Execute("org", "service-account", "create", "--name", "ci-deployer", "--team", "Engineering",
		"--type", "workspace-manage-all", "--environment", "production", "--description", "CI"))
	h.Require.Equal(map[string]any{
		"name":               "ci-deployer",
		"team_id":            "t123456",
		"description":        "CI",
		"preset_permissions": map[string]any{"type": "WORKSPACE_MANAGE_ALL", "environment_names": []any{"production"}},
	}, m.FindCall("POST", "/v1/service_accounts").BodyJSON(t))
	h.Require.Equal("sa1\n", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Created service account ci-deployer")
}

func Test_Org_ServiceAccount_Create_OrgAccountWithModels(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/service_accounts", 200, orgServiceAccountFixture("sa1", "invoker", "WORKSPACE_INVOKE"))

	h.Require.NoError(h.Execute("org", "service-account", "create", "--name", "invoker", "--type", "workspace-invoke",
		"--model-id", "m1", "--model-id", "m2", "--jq", ".id"))
	h.Require.Equal(map[string]any{
		"name":               "invoker",
		"preset_permissions": map[string]any{"type": "WORKSPACE_INVOKE", "model_ids": []any{"m1", "m2"}},
	}, m.FindCall("POST", "/v1/service_accounts").BodyJSON(t))
	h.Require.Equal("\"sa1\"\n", h.Stdout.String())
}

func Test_Org_ServiceAccount_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	disabled := orgServiceAccountFixture("sa2", "old", "WORKSPACE_INVOKE_MODEL_APIS")
	disabled["disabled"] = true
	disabled["last_used_at"] = "2026-11-02T00:00:00Z"
	m := h.MockManagementAPI()
	// Two pages, to check the list follows the cursor.
	first := orgServiceAccountPage(orgServiceAccountFixture("sa1", "ci-deployer", "WORKSPACE_MANAGE_ALL"))
	first["items"].([]any)[0].(map[string]any)["team_id"] = "t123456"
	first["pagination"] = map[string]any{"has_more": true, "cursor": "next"}
	m.SetRouteFunc("GET", "/v1/service_accounts", func(w http.ResponseWriter, r *http.Request) {
		page := first
		if r.URL.Query().Get("cursor") == "next" {
			page = orgServiceAccountPage(disabled)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	})

	h.Require.NoError(h.Execute("org", "service-account", "list"))
	out := h.Stdout.String()
	for _, want := range []string{"ID", "NAME", "TYPE", "TEAM", "DISABLED", "LAST USED", "ci-deployer", "workspace-manage-all", "t123456",
		"old", "workspace-invoke-model-apis", "true", "2026-11-02T00:00:00Z"} {
		h.Require.Contains(out, want)
	}
}

func Test_Org_ServiceAccount_List_Filters(t *testing.T) {
	h := NewCommandHarness(t)
	gridRouteTeams(h)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/service_accounts", 200, orgServiceAccountPage())

	h.Require.NoError(h.Execute("org", "service-account", "list", "--team", "Engineering", "--disabled", "true"))
	h.Require.Contains(h.Stderr.String(), "No service accounts found.")
	q := m.FindCall("GET", "/v1/service_accounts").Query()
	h.Require.Equal("t123456", q.Get("team_id"))
	h.Require.Equal("true", q.Get("disabled"))
}

func Test_Org_ServiceAccount_List_EmptyJSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/service_accounts", 200, orgServiceAccountPage())

	h.Require.NoError(h.Execute("org", "service-account", "list", "--output", "json"))
	h.Require.JSONEq(`{"items":[]}`, h.Stdout.String())
}

func Test_Org_ServiceAccount_Describe_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	orgServiceAccountRoutes(h)

	h.Require.NoError(h.Execute("org", "service-account", "describe", "--name", "ci-deployer"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID:               sa1")
	h.Require.Contains(out, "Type:             workspace-manage-all")
	h.Require.NotContains(out, "Environments:")
	h.Require.NotContains(out, "Models:")
	h.Require.NotContains(out, "Model APIs:")
}

func Test_Org_ServiceAccount_Describe_ByName_NoMatch(t *testing.T) {
	h := NewCommandHarness(t)
	orgServiceAccountRoutes(h)

	err := h.Execute("org", "service-account", "describe", "--name", "missing")
	h.Require.ErrorContains(err, `no service account named "missing"`)
}

func Test_Org_ServiceAccount_Describe_ByID_SkipsLookup(t *testing.T) {
	h := NewCommandHarness(t)
	orgServiceAccountRoutes(h)

	h.Require.NoError(h.Execute("org", "service-account", "describe", "--id", "sa1", "--jq", ".preset_permissions.type"))
	h.Require.Equal("\"WORKSPACE_MANAGE_ALL\"\n", h.Stdout.String())
	h.Require.Nil(h.MockManagementAPI().FindCall("GET", "/v1/service_accounts"))
}

func Test_Org_ServiceAccount_Describe_RequiresRef(t *testing.T) {
	h := NewCommandHarness(t)
	h.Require.Error(h.Execute("org", "service-account", "describe"))
}

func Test_Org_ServiceAccount_Update_ReplacesAccess(t *testing.T) {
	h := NewCommandHarness(t)
	orgServiceAccountRoutes(h)

	h.Require.NoError(h.Execute("org", "service-account", "update", "--name", "ci-deployer", "--new-name", "ci",
		"--description", "", "--type", "workspace-invoke", "--environment", "production"))
	h.Require.Equal(map[string]any{
		"name":               "ci",
		"description":        "",
		"preset_permissions": map[string]any{"type": "WORKSPACE_INVOKE", "environment_names": []any{"production"}},
	}, h.MockManagementAPI().FindCall("PATCH", "/v1/service_accounts/sa1").BodyJSON(t))
	h.Require.Contains(h.Stderr.String(), "Updated service account ci-deployer")
}

func Test_Org_ServiceAccount_Update_PermissionFlagRequiresType(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("org", "service-account", "update", "--id", "sa1", "--environment", "production")
	h.Require.ErrorContains(err, "pass --type too")
}

func Test_Org_ServiceAccount_Update_NothingToUpdate(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("org", "service-account", "update", "--id", "sa1")
	h.Require.ErrorContains(err, "nothing to update")
}

func Test_Org_ServiceAccount_Update_Disable(t *testing.T) {
	h := NewCommandHarness(t)
	orgServiceAccountRoutes(h)

	h.Require.NoError(h.Execute("org", "service-account", "update", "--name", "ci-deployer", "--disabled", "true"))
	h.Require.Equal(map[string]any{"disabled": true},
		h.MockManagementAPI().FindCall("PATCH", "/v1/service_accounts/sa1").BodyJSON(t))
	h.Require.Contains(h.Stderr.String(), "Updated service account ci-deployer")
}

func Test_Org_ServiceAccount_Delete_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	orgServiceAccountRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/service_accounts/sa1", 200, map[string]any{"id": "sa1", "deleted": true})

	h.Require.NoError(h.Execute("org", "service-account", "delete", "--name", "ci-deployer"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/service_accounts/sa1"))
	h.Require.Contains(h.Stderr.String(), "Deleted service account sa1")
	h.Require.Equal("", h.Stdout.String())
}

func Test_Org_ServiceAccount_Access_PicksServiceAccount(t *testing.T) {
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
		{"service account by name", []string{"--name", "ci-deployer"}, "/v1/service_accounts/sa1/access"},
		{"service account by ID", []string{"--id", "sa2"}, "/v1/service_accounts/sa2/access"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			orgRoleRoutes(h)
			m := h.MockManagementAPI()
			m.SetRoute("GET", tc.path, 200, access)

			h.Require.NoError(h.Execute(append([]string{"org", "service-account", "access"}, tc.args...)...))
			h.Require.NotNil(m.FindCall("GET", tc.path))
			h.Require.Contains(h.Stderr.String(), "Effective permissions aren't available yet.")
		})
	}
}
