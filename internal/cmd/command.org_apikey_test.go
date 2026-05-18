package cmd_test

import (
	"net/http"
	"testing"
)

func Test_Org_APIKey_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": []any{}})

	h.Require.NoError(h.Execute("org", "api-key", "list"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No API keys found.")
}

func Test_Org_APIKey_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/api_keys", 200, map[string]any{
		"keys": []any{
			map[string]any{
				"name":      "ci-key",
				"prefix":    "bsnt_abc",
				"type":      "PERSONAL",
				"team_name": "default",
			},
			map[string]any{
				"prefix": "bsnt_xyz",
				"type":   "WORKSPACE_INVOKE",
			},
		},
	})

	h.Require.NoError(h.Execute("org", "api-key", "list"))
	out := h.Stdout.String()
	h.Require.Contains(out, "NAME")
	h.Require.Contains(out, "KEY")
	h.Require.Contains(out, "TYPE")
	h.Require.Contains(out, "TEAM")
	h.Require.Contains(out, "ci-key")
	h.Require.Contains(out, "bsnt_abc****")
	h.Require.Contains(out, "personal")
	h.Require.Contains(out, "workspace-invoke")
}

func Test_Org_APIKey_Create_Personal(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/api_keys", 200, map[string]any{"api_key": "bsnt_secret_value"})

	h.Require.NoError(h.Execute("org", "api-key", "create", "--type", "personal", "--name", "ci-key"))
	call := m.FindCall("POST", "/v1/api_keys")
	h.Require.NotNil(call)
	h.Require.Contains(call.Body, `"type":"PERSONAL"`)
	h.Require.Contains(call.Body, `"name":"ci-key"`)

	h.Require.Equal("bsnt_secret_value\n", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Save this key now")
}

func Test_Org_APIKey_Create_ModelIDRequiresScopedType(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})

	err := h.Execute("org", "api-key", "create", "--type", "personal", "--model-id", "m1")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "workspace-export-metrics or workspace-invoke")
}

func Test_Org_APIKey_Create_WorkspaceInvokeWithModelIDs(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/api_keys", 200, map[string]any{"api_key": "bsnt_val"})

	h.Require.NoError(h.Execute(
		"org", "api-key", "create",
		"--type", "workspace-invoke",
		"--model-id", "m1",
		"--model-id", "m2",
	))
	call := m.FindCall("POST", "/v1/api_keys")
	h.Require.NotNil(call)
	h.Require.Contains(call.Body, `"type":"WORKSPACE_INVOKE"`)
	h.Require.Contains(call.Body, `"model_ids":["m1","m2"]`)
}

func Test_Org_APIKey_Delete_ByPrefix(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/api_keys/bsnt_abc", 200, map[string]any{"prefix": "bsnt_abc"})

	h.Require.NoError(h.Execute("org", "api-key", "delete", "--prefix", "bsnt_abc"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/api_keys/bsnt_abc"))
	h.Require.Contains(h.Stderr.String(), "Deleted API key bsnt_abc")
}

func Test_Org_APIKey_Delete_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/api_keys", 200, map[string]any{
		"keys": []any{
			map[string]any{"name": "ci-key", "prefix": "bsnt_abc", "type": "PERSONAL"},
			map[string]any{"name": "other", "prefix": "bsnt_xyz", "type": "PERSONAL"},
		},
	})
	m.SetRoute("DELETE", "/v1/api_keys/bsnt_abc", 200, map[string]any{"prefix": "bsnt_abc"})

	h.Require.NoError(h.Execute("org", "api-key", "delete", "--name", "ci-key"))
	h.Require.NotNil(m.FindCall("GET", "/v1/api_keys"))
	h.Require.NotNil(m.FindCall("DELETE", "/v1/api_keys/bsnt_abc"))
}

func Test_Org_APIKey_Delete_ByName_NoMatch(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/api_keys", 200, map[string]any{
		"keys": []any{
			map[string]any{"name": "other", "prefix": "bsnt_xyz", "type": "PERSONAL"},
		},
	})

	err := h.Execute("org", "api-key", "delete", "--name", "missing")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), `no API key named "missing"`)
}

func Test_Org_APIKey_Delete_ByName_MultipleMatches(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/api_keys", 200, map[string]any{
		"keys": []any{
			map[string]any{"name": "dup", "prefix": "bsnt_a", "type": "PERSONAL"},
			map[string]any{"name": "dup", "prefix": "bsnt_b", "type": "PERSONAL"},
		},
	})

	err := h.Execute("org", "api-key", "delete", "--name", "dup")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), `multiple API keys named "dup"`)
}

func Test_Org_APIKey_Delete_RequiresIdentifier(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("org", "api-key", "delete")
	h.Require.Error(err)
}
