package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func serviceAccountFixture(id, name, presetType string) map[string]any {
	return map[string]any{
		"id":                 id,
		"name":               name,
		"disabled":           false,
		"preset_permissions": map[string]any{"type": presetType},
		"created_at":         "2026-11-01T00:00:00Z",
		"updated_at":         "2026-11-01T00:00:00Z",
	}
}

func serviceAccountPage(items ...any) map[string]any {
	return map[string]any{"items": items, "pagination": map[string]any{"has_more": false}}
}

// serviceAccountRoutes serves one team account, ci-deployer, for name lookups.
func serviceAccountRoutes(h *CommandHarness) {
	account := serviceAccountFixture("sa1", "ci-deployer", "WORKSPACE_MANAGE_ALL")
	account["team_id"] = "t123456"
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/service_accounts", 200, serviceAccountPage(account))
	m.SetRoute("GET", "/v1/service_accounts/sa1", 200, account)
	m.SetRoute("PATCH", "/v1/service_accounts/sa1", 200, account)
}

func Test_Org_ServiceAccount_Create_TeamAccount(t *testing.T) {
	h := NewCommandHarness(t)
	gridRouteTeams(h)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/service_accounts", 200, serviceAccountFixture("sa1", "ci-deployer", "WORKSPACE_MANAGE_ALL"))

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
	m.SetRoute("POST", "/v1/service_accounts", 200, serviceAccountFixture("sa1", "invoker", "WORKSPACE_INVOKE"))

	h.Require.NoError(h.Execute("org", "service-account", "create", "--name", "invoker", "--type", "workspace-invoke",
		"--model-id", "m1", "--model-id", "m2", "--jq", ".id"))
	h.Require.Equal(map[string]any{
		"name":               "invoker",
		"preset_permissions": map[string]any{"type": "WORKSPACE_INVOKE", "model_ids": []any{"m1", "m2"}},
	}, m.FindCall("POST", "/v1/service_accounts").BodyJSON(t))
	h.Require.Equal("\"sa1\"\n", h.Stdout.String())
}

func Test_Org_ServiceAccount_Create_RejectsFlagOutsideType(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})

	err := h.Execute("org", "service-account", "create", "--name", "x", "--type", "workspace-manage-api-keys", "--model-id", "m1")
	h.Require.ErrorContains(err, "--model-id can't be used with --type workspace-manage-api-keys")
}

func Test_Org_ServiceAccount_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	disabled := serviceAccountFixture("sa2", "old", "WORKSPACE_INVOKE_MODEL_APIS")
	disabled["disabled"] = true
	disabled["last_used_at"] = "2026-11-02T00:00:00Z"
	m := h.MockManagementAPI()
	// Two pages, to check the list follows the cursor.
	first := serviceAccountPage(serviceAccountFixture("sa1", "ci-deployer", "WORKSPACE_MANAGE_ALL"))
	first["items"].([]any)[0].(map[string]any)["team_id"] = "t123456"
	first["pagination"] = map[string]any{"has_more": true, "cursor": "next"}
	m.SetRouteFunc("GET", "/v1/service_accounts", func(w http.ResponseWriter, r *http.Request) {
		page := first
		if r.URL.Query().Get("cursor") == "next" {
			page = serviceAccountPage(disabled)
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
	m.SetRoute("GET", "/v1/service_accounts", 200, serviceAccountPage())

	h.Require.NoError(h.Execute("org", "service-account", "list", "--team", "Engineering", "--disabled", "true"))
	h.Require.Contains(h.Stderr.String(), "No service accounts found.")
	q := m.FindCall("GET", "/v1/service_accounts").Query()
	h.Require.Equal("t123456", q.Get("team_id"))
	h.Require.Equal("true", q.Get("disabled"))
}

func Test_Org_ServiceAccount_List_EmptyJSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/service_accounts", 200, serviceAccountPage())

	h.Require.NoError(h.Execute("org", "service-account", "list", "--output", "json"))
	h.Require.JSONEq(`{"items":[]}`, h.Stdout.String())
}

func Test_Org_ServiceAccount_Describe_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	serviceAccountRoutes(h)

	h.Require.NoError(h.Execute("org", "service-account", "describe", "--name", "ci-deployer"))
	out := h.Stdout.String()
	h.Require.Contains(out, "ID:               sa1")
	h.Require.Contains(out, "Type:             workspace-manage-all")
	h.Require.Contains(out, "Environments:     all")
	h.Require.NotContains(out, "Models:")
	h.Require.NotContains(out, "Model APIs:")
}

func Test_Org_ServiceAccount_Describe_ByName_NoMatch(t *testing.T) {
	h := NewCommandHarness(t)
	serviceAccountRoutes(h)

	err := h.Execute("org", "service-account", "describe", "--name", "missing")
	h.Require.ErrorContains(err, `no service account named "missing"`)
}

func Test_Org_ServiceAccount_Describe_ByID_SkipsLookup(t *testing.T) {
	h := NewCommandHarness(t)
	serviceAccountRoutes(h)

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
	serviceAccountRoutes(h)

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

func Test_Org_ServiceAccount_DisableEnable(t *testing.T) {
	for _, tc := range []struct {
		command  string
		disabled bool
		message  string
	}{
		{"disable", true, "Disabled service account ci-deployer"},
		{"enable", false, "Enabled service account ci-deployer"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			h := NewCommandHarness(t)
			serviceAccountRoutes(h)

			h.Require.NoError(h.Execute("org", "service-account", tc.command, "--name", "ci-deployer"))
			h.Require.Equal(map[string]any{"disabled": tc.disabled},
				h.MockManagementAPI().FindCall("PATCH", "/v1/service_accounts/sa1").BodyJSON(t))
			h.Require.Contains(h.Stderr.String(), tc.message)
		})
	}
}

func Test_Org_ServiceAccount_Delete_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	serviceAccountRoutes(h)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/service_accounts/sa1", 200, map[string]any{"id": "sa1", "deleted": true})

	h.Require.NoError(h.Execute("org", "service-account", "delete", "--name", "ci-deployer"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/service_accounts/sa1"))
	h.Require.Contains(h.Stderr.String(), "Deleted service account sa1")
	h.Require.Equal("", h.Stdout.String())
}

func Test_Org_ServiceAccount_Delete_ByName_MultipleMatches(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/service_accounts", 200, serviceAccountPage(
		serviceAccountFixture("sa1", "dup", "WORKSPACE_INVOKE"),
		serviceAccountFixture("sa2", "dup", "WORKSPACE_INVOKE"),
	))

	err := h.Execute("org", "service-account", "delete", "--name", "dup")
	h.Require.ErrorContains(err, `multiple service accounts named "dup"; pass the ID instead`)
}
