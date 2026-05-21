package cmd_test

import (
	"testing"
)

func envFixture(name, depID, status string) map[string]any {
	return map[string]any{
		"name":                 name,
		"model_id":             "m-1",
		"created_at":           "2026-01-02T03:04:05Z",
		"autoscaling_settings": map[string]any{},
		"instance_type":        map[string]any{},
		"promotion_settings":   map[string]any{},
		"current_deployment":   depFixture(depID, "first", name, status),
	}
}

func Test_Model_Environment_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments", 200,
		map[string]any{"environments": []any{}})

	h.Require.NoError(h.Execute("model", "environment", "list", "--model-id", "m-1"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No environments found.")
}

func Test_Model_Environment_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments", 200,
		map[string]any{"environments": []any{
			envFixture("production", "d-1", "ACTIVE"),
			envFixture("staging", "d-2", "INACTIVE"),
		}})

	h.Require.NoError(h.Execute("model", "environment", "list", "--model-id", "m-1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "NAME")
	h.Require.Contains(out, "CURRENT DEPLOYMENT")
	h.Require.Contains(out, "STATUS")
	h.Require.Contains(out, "production")
	h.Require.Contains(out, "d-1")
	h.Require.Contains(out, "ACTIVE")
	h.Require.Contains(out, "staging")
	h.Require.Contains(out, "d-2")
	h.Require.Contains(out, "INACTIVE")
}

func Test_Model_Environment_List_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments", 200,
		map[string]any{"environments": []any{envFixture("production", "d-1", "ACTIVE")}})

	h.Require.NoError(h.Execute("model", "environment", "list", "--model-id", "m-1", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"name": "production"`)
}

func Test_Model_Environment_Fetch(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envFixture("production", "d-1", "ACTIVE"))

	h.Require.NoError(h.Execute("model", "environment", "fetch",
		"--model-id", "m-1", "--environment", "production"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Name:")
	h.Require.Contains(out, "production")
	h.Require.Contains(out, "d-1")
	h.Require.Contains(out, "ACTIVE")
}

func Test_Model_Environment_Fetch_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envFixture("production", "d-1", "ACTIVE"))

	h.Require.NoError(h.Execute("model", "environment", "fetch",
		"--model-id", "m-1", "--environment", "production", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"name": "production"`)
}

func Test_Model_Environment_Fetch_MissingEnvironment(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "environment", "fetch", "--model-id", "m-1")
	h.Require.Error(err)
}

func Test_Model_Environment_Activate(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/models/m-1/environments/production/activate", 200,
		map[string]any{"deployment_id": "d-1"})

	h.Require.NoError(h.Execute("model", "environment", "activate",
		"--model-id", "m-1", "--environment", "production"))
	h.Require.NotNil(m.FindCall("POST", "/v1/models/m-1/environments/production/activate"))
	h.Require.Contains(h.Stderr.String(), "Activated environment production")
}

func Test_Model_Environment_Deactivate_Yes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/models/m-1/environments/production/deactivate", 200,
		map[string]any{"deployment_id": "d-1"})

	h.Require.NoError(h.Execute("model", "environment", "deactivate",
		"--model-id", "m-1", "--environment", "production", "--yes"))
	h.Require.NotNil(m.FindCall("POST", "/v1/models/m-1/environments/production/deactivate"))
	h.Require.Contains(h.Stderr.String(), "Deactivated environment production")
}

func Test_Model_Environment_Deactivate_NoTTY_RequiresYes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()

	err := h.Execute("model", "environment", "deactivate",
		"--model-id", "m-1", "--environment", "production")
	h.Require.ErrorContains(err, "stdin is not a terminal")
	h.Require.Nil(m.FindCall("POST", "/v1/models/m-1/environments/production/deactivate"))
}
