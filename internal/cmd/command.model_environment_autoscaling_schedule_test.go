package cmd_test

import (
	"testing"
)

func scheduleOverridesFixture(minReplica, maxReplica int) map[string]any {
	return map[string]any{
		"min_replica":                   minReplica,
		"max_replica":                   maxReplica,
		"autoscaling_window":            nil,
		"scale_down_delay":              nil,
		"concurrency_target":            nil,
		"target_utilization_percentage": nil,
		"target_in_flight_tokens":       nil,
		"max_scale_down_rate":           nil,
	}
}

func recurringScheduleFixture(id, name string) map[string]any {
	return map[string]any{
		"id":                   id,
		"name":                 name,
		"cadence":              "DAILY",
		"enabled":              true,
		"weekdays":             []any{"MONDAY", "TUESDAY"},
		"start_hour":           8,
		"start_minute":         0,
		"end_hour":             18,
		"end_minute":           30,
		"autoscaling_settings": scheduleOverridesFixture(2, 10),
	}
}

func oneTimeScheduleFixture(id, name string) map[string]any {
	return map[string]any{
		"id":                   id,
		"name":                 name,
		"cadence":              "ONE_TIME",
		"enabled":              false,
		"start_at":             "2026-09-01T08:00:00Z",
		"end_at":               "2026-09-01T20:00:00Z",
		"autoscaling_settings": scheduleOverridesFixture(10, 50),
	}
}

// envWithSchedulesFixture returns an environment carrying a schedule
// collection, which is how both list and the read-modify-write update read it.
func envWithSchedulesFixture(schedules ...map[string]any) map[string]any {
	env := envFixture("production", "d-1", "ACTIVE")
	items := make([]any, 0, len(schedules))
	for _, schedule := range schedules {
		items = append(items, schedule)
	}
	env["autoscaling_schedules"] = map[string]any{
		"timezone":      "America/New_York",
		"applied_state": nil,
		"schedules":     items,
	}
	return env
}

func Test_Model_Environment_AutoscalingSchedule_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envFixture("production", "d-1", "ACTIVE"))

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "list",
		"--model-id", "m-1", "--environment", "production"))
	h.Require.Contains(h.Stderr.String(), "No autoscaling schedules found.")
}

func Test_Model_Environment_AutoscalingSchedule_List_BothCadences(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithSchedulesFixture(
			recurringScheduleFixture("sch-1", "peak"),
			oneTimeScheduleFixture("sch-2", "launch")))

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "list",
		"--model-id", "m-1", "--environment", "production"))
	out := h.Stdout.String()
	h.Require.Contains(out, "sch-1")
	// Cadence and weekdays render in the spelling --cadence and --weekdays take.
	h.Require.Contains(out, "monday,tuesday 08:00-18:30")
	h.Require.Contains(out, "2-10")
	h.Require.Contains(out, "sch-2")
	h.Require.Contains(out, "one-time")
	h.Require.Contains(out, "2026-09-01T08:00:00Z")
}

func Test_Model_Environment_AutoscalingSchedule_Create_Recurring(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED", "message": "ok",
		})

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "create",
		"--model-id", "m-1", "--environment", "production",
		"--name", "peak", "--cadence", "daily", "--weekdays", "monday,tuesday",
		"--start-hour", "8", "--start-minute", "0", "--end-hour", "18", "--end-minute", "30",
		"--timezone", "America/New_York", "--min-replica", "2", "--max-replica", "10"))
	h.Require.Contains(h.Stderr.String(), "Created autoscaling schedule peak")

	body := h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	settings := body["autoscaling_schedule_settings"].(map[string]any)
	h.Require.Equal("America/New_York", settings["timezone"])
	schedule := settings["schedules"].([]any)[0].(map[string]any)
	h.Require.Equal("DAILY", schedule["cadence"])
	h.Require.Equal(true, schedule["enabled"])
	h.Require.Equal([]any{"MONDAY", "TUESDAY"}, schedule["weekdays"])

	// The override block is complete: unpassed fields are sent as null, which
	// the API reads as "inherit from the environment".
	overrides := schedule["autoscaling_settings"].(map[string]any)
	h.Require.Equal(float64(2), overrides["min_replica"])
	h.Require.Contains(overrides, "concurrency_target")
	h.Require.Nil(overrides["concurrency_target"])
}

func Test_Model_Environment_AutoscalingSchedule_Create_OneTime(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED", "message": "ok",
		})

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "create",
		"--model-id", "m-1", "--environment", "production",
		"--name", "launch", "--cadence", "one-time",
		"--start-at", "2026-09-01T08:00:00Z", "--end-at", "2026-09-01T20:00:00Z",
		"--timezone", "UTC", "--min-replica", "10", "--max-replica", "50"))

	body := h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	schedule := body["autoscaling_schedule_settings"].(map[string]any)["schedules"].([]any)[0].(map[string]any)
	h.Require.Equal("ONE_TIME", schedule["cadence"])
	h.Require.Contains(schedule, "start_at")
	h.Require.NotContains(schedule, "weekdays")
}

// Cadence picks which request shape is built, so a flag belonging to the other
// shape would be dropped silently rather than rejected by the server.
func Test_Model_Environment_AutoscalingSchedule_Create_RejectsCadenceMismatch(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "environment", "autoscaling-schedule", "create",
		"--model-id", "m-1", "--environment", "production",
		"--name", "launch", "--cadence", "one-time", "--weekdays", "monday",
		"--start-at", "2026-09-01T08:00:00Z", "--end-at", "2026-09-01T20:00:00Z",
		"--min-replica", "1", "--max-replica", "2")
	h.Require.ErrorContains(err, "--weekdays is not valid on a one-time schedule")
}

func Test_Model_Environment_AutoscalingSchedule_Create_RequiresReplicaBounds(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "environment", "autoscaling-schedule", "create",
		"--model-id", "m-1", "--environment", "production",
		"--name", "peak", "--cadence", "daily", "--weekdays", "monday")
	h.Require.ErrorContains(err, "--min-replica and --max-replica are required")
}

// Update is a read-modify-write because the API replaces a schedule wholesale,
// so everything the caller did not name has to survive the round trip.
func Test_Model_Environment_AutoscalingSchedule_Update_PreservesUntouchedFields(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithSchedulesFixture(recurringScheduleFixture("sch-1", "peak")))
	m.SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED", "message": "ok",
		})

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "update",
		"--model-id", "m-1", "--environment", "production",
		"--schedule-id", "sch-1", "--min-replica", "6"))

	body := m.FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	schedule := body["autoscaling_schedule_settings"].(map[string]any)["schedules"].([]any)[0].(map[string]any)
	h.Require.Equal("sch-1", schedule["id"])
	h.Require.Equal("peak", schedule["name"])
	h.Require.Equal([]any{"MONDAY", "TUESDAY"}, schedule["weekdays"])
	h.Require.Equal(float64(8), schedule["start_hour"])
	overrides := schedule["autoscaling_settings"].(map[string]any)
	h.Require.Equal(float64(6), overrides["min_replica"])
	h.Require.Equal(float64(10), overrides["max_replica"])
}

func Test_Model_Environment_AutoscalingSchedule_Update_Disable(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithSchedulesFixture(recurringScheduleFixture("sch-1", "peak")))
	m.SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED", "message": "ok",
		})

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "update",
		"--model-id", "m-1", "--environment", "production",
		"--schedule-id", "sch-1", "--enabled", "false"))

	body := m.FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	schedule := body["autoscaling_schedule_settings"].(map[string]any)["schedules"].([]any)[0].(map[string]any)
	h.Require.Equal(false, schedule["enabled"])
}

// Names are unique per environment (enforced server-side), so --schedule-name
// identifies exactly one schedule and saves looking an id up first.
func Test_Model_Environment_AutoscalingSchedule_Update_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithSchedulesFixture(recurringScheduleFixture("sch-1", "peak")))
	m.SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED", "message": "ok",
		})

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "update",
		"--model-id", "m-1", "--environment", "production",
		"--schedule-name", "peak", "--min-replica", "6"))

	body := m.FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	schedule := body["autoscaling_schedule_settings"].(map[string]any)["schedules"].([]any)[0].(map[string]any)
	h.Require.Equal("sch-1", schedule["id"])
}

func Test_Model_Environment_AutoscalingSchedule_Update_RequiresOneRef(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "environment", "autoscaling-schedule", "update",
		"--model-id", "m-1", "--environment", "production", "--min-replica", "6")
	h.Require.Error(err)
}

// Cadence is changeable: the API rebuilds the row from the upsert alone, and
// each shape clears the other's storage columns. Timing cannot carry over, but
// name, enabled, and the overrides must.
func Test_Model_Environment_AutoscalingSchedule_Update_RecurringToOneTime(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithSchedulesFixture(recurringScheduleFixture("sch-1", "peak")))
	m.SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED", "message": "ok",
		})

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "update",
		"--model-id", "m-1", "--environment", "production", "--schedule-id", "sch-1",
		"--cadence", "one-time",
		"--start-at", "2026-09-01T08:00:00Z", "--end-at", "2026-09-01T20:00:00Z"))

	body := m.FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	schedule := body["autoscaling_schedule_settings"].(map[string]any)["schedules"].([]any)[0].(map[string]any)
	h.Require.Equal("ONE_TIME", schedule["cadence"])
	h.Require.Equal("sch-1", schedule["id"])
	h.Require.Equal("peak", schedule["name"])
	h.Require.NotContains(schedule, "weekdays")
	// Overrides are shared by both shapes, so they survive the reshape.
	h.Require.Equal(float64(2), schedule["autoscaling_settings"].(map[string]any)["min_replica"])
}

func Test_Model_Environment_AutoscalingSchedule_Update_OneTimeToRecurring(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithSchedulesFixture(oneTimeScheduleFixture("sch-2", "launch")))
	m.SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED", "message": "ok",
		})

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "update",
		"--model-id", "m-1", "--environment", "production", "--schedule-id", "sch-2",
		"--cadence", "daily", "--weekdays", "monday", "--start-minute", "0", "--end-minute", "30"))

	body := m.FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	schedule := body["autoscaling_schedule_settings"].(map[string]any)["schedules"].([]any)[0].(map[string]any)
	h.Require.Equal("DAILY", schedule["cadence"])
	h.Require.Equal([]any{"MONDAY"}, schedule["weekdays"])
	h.Require.NotContains(schedule, "start_at")
	h.Require.Equal(float64(10), schedule["autoscaling_settings"].(map[string]any)["min_replica"])
}

// There is no recurring window to inherit from a one-time schedule, so the new
// shape's timing has to be supplied rather than silently defaulted.
func Test_Model_Environment_AutoscalingSchedule_Update_ReshapeRequiresTiming(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithSchedulesFixture(oneTimeScheduleFixture("sch-2", "launch")))

	err := h.Execute("model", "environment", "autoscaling-schedule", "update",
		"--model-id", "m-1", "--environment", "production", "--schedule-id", "sch-2",
		"--cadence", "daily")
	h.Require.ErrorContains(err, "--weekdays is required when changing a schedule to --cadence daily")
}

func Test_Model_Environment_AutoscalingSchedule_Update_UnknownID(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithSchedulesFixture(recurringScheduleFixture("sch-1", "peak")))

	err := h.Execute("model", "environment", "autoscaling-schedule", "update",
		"--model-id", "m-1", "--environment", "production",
		"--schedule-id", "sch-missing", "--min-replica", "6")
	h.Require.ErrorContains(err, "no autoscaling schedule sch-missing")
	h.Require.Nil(m.FindCall("PATCH", "/v1/models/m-1/environments/production"))
}

func Test_Model_Environment_AutoscalingSchedule_Delete_ByName(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/models/m-1/environments/production", 200,
		envWithSchedulesFixture(recurringScheduleFixture("sch-1", "peak")))
	m.SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED", "message": "ok",
		})

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "delete",
		"--model-id", "m-1", "--environment", "production", "--schedule-name", "peak"))
	h.Require.Contains(h.Stderr.String(), "Deleted autoscaling schedule sch-1")

	body := m.FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	settings := body["autoscaling_schedule_settings"].(map[string]any)
	h.Require.Equal([]any{"sch-1"}, settings["delete_schedules"])
}

// Deleting by id needs no lookup, which is the reason to keep taking one.
func Test_Model_Environment_AutoscalingSchedule_Delete_ByIDSkipsRead(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED", "message": "ok",
		})

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "delete",
		"--model-id", "m-1", "--environment", "production", "--schedule-id", "sch-1"))
	h.Require.Nil(m.FindCall("GET", "/v1/models/m-1/environments/production"))
}

func Test_Model_Environment_AutoscalingSchedule_Delete(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED", "message": "ok",
		})

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "delete",
		"--model-id", "m-1", "--environment", "production", "--schedule-id", "sch-1"))
	h.Require.Contains(h.Stderr.String(), "Deleted autoscaling schedule sch-1")

	body := h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	settings := body["autoscaling_schedule_settings"].(map[string]any)
	h.Require.Equal([]any{"sch-1"}, settings["delete_schedules"])
}

func Test_Model_Environment_AutoscalingSchedule_UpdateSettings(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("PATCH", "/v1/models/m-1/environments/production", 200,
		map[string]any{
			"environment": envFixture("production", "d-1", "ACTIVE"),
			"status":      "ACCEPTED", "message": "ok",
		})

	h.Require.NoError(h.Execute("model", "environment", "autoscaling-schedule", "update-settings",
		"--model-id", "m-1", "--environment", "production", "--timezone", "Europe/Berlin"))

	body := h.MockManagementAPI().FindCall("PATCH", "/v1/models/m-1/environments/production").BodyJSON(h.T)
	settings := body["autoscaling_schedule_settings"].(map[string]any)
	h.Require.Equal("Europe/Berlin", settings["timezone"])
	h.Require.NotContains(settings, "schedules")
}
