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

func Test_Model_Environment_Describe(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envFixture("production", "d-1", "ACTIVE"))

	h.Require.NoError(h.Execute("model", "environment", "describe",
		"--model-id", "m-1", "--environment", "production"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Name:")
	h.Require.Contains(out, "production")
	h.Require.Contains(out, "d-1")
	h.Require.Contains(out, "ACTIVE")
}

// Describe is the only way to read these settings back, since the update
// commands deliberately have no per-setting describe of their own.
func Test_Model_Environment_Describe_ShowsSettings(t *testing.T) {
	h := NewCommandHarness(t)
	env := envWithSchedulesFixture(recurringScheduleFixture("sch-1", "peak"))
	env["autoscaling_settings"] = map[string]any{
		"min_replica": 1, "max_replica": 5, "concurrency_target": 2,
		"autoscaling_window": 600, "scale_down_delay": nil,
		"target_utilization_percentage": 70,
	}
	env["promotion_settings"] = map[string]any{
		"rolling_deploy": true, "promotion_cleanup_strategy": "SCALE_TO_ZERO",
		"rolling_deploy_config": map[string]any{"max_surge_percent": 10},
	}
	env["request_backpressure_settings"] = map[string]any{"policy": "REJECT_ON_FULL"}
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments/production", 200, env)

	h.Require.NoError(h.Execute("model", "environment", "describe",
		"--model-id", "m-1", "--environment", "production"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Backpressure:        reject-on-full")
	h.Require.Contains(out, "Autoscaling:")
	h.Require.Contains(out, "Autoscaling Window:      600s")
	h.Require.Contains(out, "Target Utilization:      70%")
	// An unset field reads as a dash, not a misleading zero.
	h.Require.Contains(out, "Scale Down Delay:        -")
	h.Require.Contains(out, "Promotion:")
	// Enums the flags accept are shown in the flags' spelling, so a value read
	// here can be passed straight back to update-promotion.
	h.Require.Contains(out, "Cleanup Strategy:        scale-to-zero")
	h.Require.Contains(out, "Max Surge:               10%")
	h.Require.Contains(out, "Autoscaling Schedules (America/New_York):")
	h.Require.Contains(out, "peak (sch-1): daily monday,tuesday 08:00-18:30, replicas 2-10, enabled")
}

func Test_Model_Environment_Describe_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envFixture("production", "d-1", "ACTIVE"))

	h.Require.NoError(h.Execute("model", "environment", "describe",
		"--model-id", "m-1", "--environment", "production", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"name": "production"`)
}

func Test_Model_Environment_Describe_MissingEnvironment(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "environment", "describe", "--model-id", "m-1")
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

// Each environment settings command targets the shared environment PATCH, so
// each must send only its own sub-object; a stray sibling key would reset
// settings the caller never named.
func Test_Model_Environment_UpdateAutoscaling(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED",
			"message":     "Update accepted",
		})

	h.Require.NoError(h.Execute("model", "environment", "update-autoscaling",
		"--model-id", "m-1", "--environment", "production", "--min-replica", "2"))
	h.Require.Contains(h.Stderr.String(), "Updated autoscaling settings for environment production")

	body := h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	h.Require.Len(body, 1)
	settings, ok := body["autoscaling_settings"].(map[string]any)
	h.Require.True(ok)
	h.Require.Len(settings, 1)
	h.Require.Equal(float64(2), settings["min_replica"])
}

// The rolling deploy config is flattened into top-level flags, so it has to be
// nested back on the way out.
func Test_Model_Environment_UpdatePromotion_NestsRollingDeployConfig(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED",
			"message":     "Update accepted",
		})

	h.Require.NoError(h.Execute("model", "environment", "update-promotion",
		"--model-id", "m-1", "--environment", "production",
		"--rolling-deploy", "true", "--max-surge-percent", "10",
		"--promotion-cleanup-strategy", "scale-to-zero"))

	body := h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	settings, ok := body["promotion_settings"].(map[string]any)
	h.Require.True(ok)
	h.Require.Equal(true, settings["rolling_deploy"])
	h.Require.Equal("SCALE_TO_ZERO", settings["promotion_cleanup_strategy"])
	config, ok := settings["rolling_deploy_config"].(map[string]any)
	h.Require.True(ok)
	h.Require.Len(config, 1)
	h.Require.Equal(float64(10), config["max_surge_percent"])
}

// Touching only a top-level promotion setting must not send an empty rolling
// deploy config alongside it.
func Test_Model_Environment_UpdatePromotion_OmitsUntouchedRollingDeployConfig(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED",
			"message":     "Update accepted",
		})

	h.Require.NoError(h.Execute("model", "environment", "update-promotion",
		"--model-id", "m-1", "--environment", "production", "--redeploy-on-promotion", "true"))

	body := h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	settings := body["promotion_settings"].(map[string]any)
	h.Require.Len(settings, 1)
	h.Require.Equal(true, settings["redeploy_on_promotion"])
}

func Test_Model_Environment_UpdateRequestBackpressure_NullClearsPolicy(t *testing.T) {
	h := NewCommandHarness(t)
	env := envFixture("production", "d-1", "ACTIVE")
	env["request_backpressure_settings"] = map[string]any{"policy": nil}
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{"environment": env, "status": "ACCEPTED", "message": "Update accepted"})

	h.Require.NoError(h.Execute("model", "environment", "update-request-backpressure",
		"--model-id", "m-1", "--environment", "production", "--policy", "null"))
	h.Require.Contains(h.Stderr.String(), "Request backpressure policy: none")

	body := h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	settings, ok := body["request_backpressure_settings"].(map[string]any)
	h.Require.True(ok)
	h.Require.Nil(settings["policy"])
	h.Require.Contains(settings, "policy")
}
