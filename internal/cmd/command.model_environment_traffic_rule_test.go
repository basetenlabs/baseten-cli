package cmd_test

import (
	"testing"
)

func trafficRuleFixture(id string, target map[string]any) map[string]any {
	target["model_id"] = "m-1"
	return map[string]any{
		"id":                            id,
		"type":                          "SHADOW",
		"targets":                       []any{target},
		"percent_traffic":               25,
		"expires_at":                    "2026-10-08T18:00:00Z",
		"sampling_mode":                 "USER_SESSION",
		"propagate_client_cancellation": true,
	}
}

// envWithTrafficRulesFixture returns an environment carrying active traffic
// rules, which is how describe, update's read-modify-write and create's
// response read them.
func envWithTrafficRulesFixture(rules ...map[string]any) map[string]any {
	env := envFixture("production", "d-1", "ACTIVE")
	items := make([]any, 0, len(rules))
	for _, rule := range rules {
		items = append(items, rule)
	}
	env["traffic_routing"] = map[string]any{"rules": items}
	return env
}

func patchResponseFixture(env map[string]any) map[string]any {
	return map[string]any{"environment": env, "status": "ACCEPTED", "message": "ok"}
}

func Test_Model_Environment_TrafficRule_Create_TargetEnvironment(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		patchResponseFixture(envWithTrafficRulesFixture(
			trafficRuleFixture("r-1", map[string]any{"environment": "staging"}))))

	h.Require.NoError(h.Execute("model", "environment", "traffic-rule", "create",
		"--model-id", "m-1", "--environment", "production",
		"--type", "shadow", "--target-environment", "staging", "--percent-traffic", "25",
		"--expires-at", "2026-10-08T18:00:00Z", "--sampling-mode", "user-session",
		"--propagate-client-cancellation", "true", "--yes"))
	h.Require.Contains(h.Stderr.String(), "Created traffic rule r-1")

	body := h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	// Only the traffic routing sub-object is sent, so no other setting is touched.
	h.Require.Len(body, 1)
	settings := body["traffic_routing_settings"].(map[string]any)
	h.Require.NotContains(settings, "delete_rules")
	rule := settings["rules"].([]any)[0].(map[string]any)
	h.Require.NotContains(rule, "id")
	h.Require.Equal("SHADOW", rule["type"])
	h.Require.Equal([]any{map[string]any{"environment": "staging"}}, rule["targets"])
	h.Require.Equal(float64(25), rule["percent_traffic"])
	h.Require.Equal("2026-10-08T18:00:00Z", rule["expires_at"])
	h.Require.Equal("USER_SESSION", rule["sampling_mode"])
	h.Require.Equal(true, rule["propagate_client_cancellation"])
}

func Test_Model_Environment_TrafficRule_Create_TargetDeployment(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		patchResponseFixture(envWithTrafficRulesFixture(
			trafficRuleFixture("r-1", map[string]any{"deployment_id": "d-2"}))))

	h.Require.NoError(h.Execute("model", "environment", "traffic-rule", "create",
		"--model-id", "m-1", "--environment", "production",
		"--type", "shadow", "--target-deployment-id", "d-2", "--percent-traffic", "10",
		"--expires-at", "2026-10-08T18:00:00Z", "--sampling-mode", "random",
		"--propagate-client-cancellation", "false", "--yes"))
	h.Require.Contains(h.Stderr.String(), "Created traffic rule r-1")

	body := h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	rule := body["traffic_routing_settings"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	h.Require.Equal([]any{map[string]any{"deployment_id": "d-2"}}, rule["targets"])
	h.Require.Equal("RANDOM", rule["sampling_mode"])
	h.Require.Equal(false, rule["propagate_client_cancellation"])
}

func Test_Model_Environment_TrafficRule_Create_RejectsTwoTargets(t *testing.T) {
	h := NewCommandHarness(t)

	err := h.Execute("model", "environment", "traffic-rule", "create",
		"--model-id", "m-1", "--environment", "production",
		"--type", "shadow", "--target-environment", "staging", "--target-deployment-id", "d-2",
		"--percent-traffic", "25", "--expires-at", "2026-10-08T18:00:00Z",
		"--sampling-mode", "random", "--propagate-client-cancellation", "false", "--yes")
	h.Require.ErrorContains(err, "target-deployment-id")
	h.Require.Nil(h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production"))
}

func Test_Model_Environment_TrafficRule_Create_NoTTY_RequiresYes(t *testing.T) {
	h := NewCommandHarness(t)

	err := h.Execute("model", "environment", "traffic-rule", "create",
		"--model-id", "m-1", "--environment", "production",
		"--type", "shadow", "--target-environment", "staging", "--percent-traffic", "25",
		"--expires-at", "2026-10-08T18:00:00Z", "--sampling-mode", "random",
		"--propagate-client-cancellation", "false")
	h.Require.ErrorContains(err, "stdin is not a terminal")
	h.Require.Nil(h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production"))
}

// The API replaces a rule wholesale, so update must resend the fields the
// caller did not pass, including the target, which can't change.
func Test_Model_Environment_TrafficRule_Update_MergesIntoExistingRule(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithTrafficRulesFixture(trafficRuleFixture("r-1", map[string]any{"environment": "staging"})))
	m.SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		patchResponseFixture(envFixture("production", "d-1", "ACTIVE")))

	h.Require.NoError(h.Execute("model", "environment", "traffic-rule", "update",
		"--model-id", "m-1", "--environment", "production", "--rule-id", "r-1",
		"--percent-traffic", "50"))
	h.Require.Contains(h.Stderr.String(), "Updated traffic rule r-1")

	body := m.FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	rule := body["traffic_routing_settings"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	h.Require.Equal("r-1", rule["id"])
	h.Require.Equal("SHADOW", rule["type"])
	// The response's model_id is dropped: the request's target has no such field.
	h.Require.Equal([]any{map[string]any{"environment": "staging"}}, rule["targets"])
	h.Require.Equal(float64(50), rule["percent_traffic"])
	h.Require.Equal("2026-10-08T18:00:00Z", rule["expires_at"])
	h.Require.Equal("USER_SESSION", rule["sampling_mode"])
	h.Require.Equal(true, rule["propagate_client_cancellation"])
}

func Test_Model_Environment_TrafficRule_Update_AllFields(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithTrafficRulesFixture(trafficRuleFixture("r-1", map[string]any{"deployment_id": "d-2"})))
	m.SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		patchResponseFixture(envFixture("production", "d-1", "ACTIVE")))

	h.Require.NoError(h.Execute("model", "environment", "traffic-rule", "update",
		"--model-id", "m-1", "--environment", "production", "--rule-id", "r-1",
		"--expires-at", "2026-10-10T12:00:00Z", "--sampling-mode", "random",
		"--propagate-client-cancellation", "false"))

	body := m.FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	rule := body["traffic_routing_settings"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	h.Require.Equal([]any{map[string]any{"deployment_id": "d-2"}}, rule["targets"])
	h.Require.Equal(float64(25), rule["percent_traffic"])
	h.Require.Equal("2026-10-10T12:00:00Z", rule["expires_at"])
	h.Require.Equal("RANDOM", rule["sampling_mode"])
	h.Require.Equal(false, rule["propagate_client_cancellation"])
}

func Test_Model_Environment_TrafficRule_Update_RequiresAChange(t *testing.T) {
	h := NewCommandHarness(t)

	err := h.Execute("model", "environment", "traffic-rule", "update",
		"--model-id", "m-1", "--environment", "production", "--rule-id", "r-1")
	h.Require.ErrorContains(err, "pass at least one of")
	h.Require.Nil(h.MockManagementAPI().FindCall("GET", "/v1/models/m-1/environments/production"))
}

func Test_Model_Environment_TrafficRule_Update_UnknownRule(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithTrafficRulesFixture(trafficRuleFixture("r-1", map[string]any{"environment": "staging"})))

	err := h.Execute("model", "environment", "traffic-rule", "update",
		"--model-id", "m-1", "--environment", "production", "--rule-id", "r-missing",
		"--percent-traffic", "50")
	h.Require.ErrorContains(err, "environment production has no active traffic rule r-missing")
	h.Require.Nil(m.FindCall("PATCH", "/v1/models/m-1/environments/production"))
}

func Test_Model_Environment_TrafficRule_Update_DeletedTarget(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	rule := trafficRuleFixture("r-1", map[string]any{"environment": "staging"})
	rule["targets"] = []any{}
	m.SetRoute("GET", "/v1/models/m-1/environments/production", 200, envWithTrafficRulesFixture(rule))

	err := h.Execute("model", "environment", "traffic-rule", "update",
		"--model-id", "m-1", "--environment", "production", "--rule-id", "r-1",
		"--percent-traffic", "50")
	h.Require.ErrorContains(err, "traffic rule r-1's target was deleted; delete the rule and create a new one")
	h.Require.Nil(m.FindCall("PATCH", "/v1/models/m-1/environments/production"))
}

func Test_Model_Environment_TrafficRule_Delete(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		patchResponseFixture(envFixture("production", "d-1", "ACTIVE")))

	h.Require.NoError(h.Execute("model", "environment", "traffic-rule", "delete",
		"--model-id", "m-1", "--environment", "production", "--rule-id", "r-1", "--yes"))
	h.Require.Contains(h.Stderr.String(), "Deleted traffic rule r-1")

	body := m.FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	h.Require.Equal(map[string]any{"delete_rules": []any{"r-1"}}, body["traffic_routing_settings"])
}

func Test_Model_Environment_TrafficRule_Delete_NoTTY_RequiresYes(t *testing.T) {
	h := NewCommandHarness(t)

	err := h.Execute("model", "environment", "traffic-rule", "delete",
		"--model-id", "m-1", "--environment", "production", "--rule-id", "r-1")
	h.Require.ErrorContains(err, "stdin is not a terminal")
	h.Require.Nil(h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production"))
}

func Test_Model_Environment_Describe_ShowsTrafficRules(t *testing.T) {
	h := NewCommandHarness(t)
	deleted := trafficRuleFixture("r-2", map[string]any{})
	deleted["targets"] = []any{}
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithTrafficRulesFixture(
			trafficRuleFixture("r-1", map[string]any{"environment": "staging"}), deleted))

	h.Require.NoError(h.Execute("model", "environment", "describe",
		"--model-id", "m-1", "--environment", "production"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Traffic Rules:")
	// Enums render in the spelling the traffic-rule flags take.
	h.Require.Contains(out, "r-1: shadow 25% to environment staging, user-session, "+
		"propagate client cancellation true, expires 2026-10-08T18:00:00Z")
	h.Require.Contains(out, "r-2: shadow 25% to deleted target")
}

func Test_Model_Environment_Describe_NoTrafficRouting(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envFixture("production", "d-1", "ACTIVE"))

	h.Require.NoError(h.Execute("model", "environment", "describe",
		"--model-id", "m-1", "--environment", "production"))
	h.Require.NotContains(h.Stdout.String(), "Traffic Rules:")
}
