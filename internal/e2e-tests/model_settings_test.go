//go:build e2e

package e2etests

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Values written by the settings phases. The deployment and environment phases
// write different values for the same fields so a later write cannot be
// mistaken for an earlier one, and every value is far enough from the API
// defaults that an unapplied write shows up as a mismatch rather than a pass.
// Replica counts are never touched: raising them would actually scale the
// deployment.
const (
	deploymentAutoscalingWindow  = 630
	deploymentScaleDownDelay     = 930
	deploymentConcurrencyTarget  = 2
	environmentAutoscalingWindow = 660
	environmentScaleDownDelay    = 960
	environmentConcurrencyTarget = 3

	// The API floor is 60 seconds.
	environmentRampUpDurationSeconds = 90
	environmentCleanupStrategy       = "SCALE_TO_ZERO"
)

// deploymentAutoscalingResponseJSON is everything the deployment autoscaling
// endpoint returns: it does not echo the deployment back. UNCHANGED means the
// values already matched and nothing was written, and QUEUED means the write
// landed but has not reached the running deployment yet, so the status is worth
// logging when an assertion below fails.
type deploymentAutoscalingResponseJSON struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// environmentUpdateResponseJSON is the environment PATCH response. Only the
// environment is read: the status and message beside it are deprecated, kept
// from when this endpoint updated autoscaling alone.
type environmentUpdateResponseJSON struct {
	Environment environmentSettingsJSON `json:"environment"`
}

// autoscalingSettingsJSON is the subset of the autoscaling settings carried by
// both the deployment and the environment describe payloads.
type autoscalingSettingsJSON struct {
	MinReplica        int  `json:"min_replica"`
	MaxReplica        int  `json:"max_replica"`
	AutoscalingWindow *int `json:"autoscaling_window"`
	ScaleDownDelay    *int `json:"scale_down_delay"`
	ConcurrencyTarget int  `json:"concurrency_target"`
}

type deploymentSettingsJSON struct {
	Name string `json:"name"`
	// Status is asserted nowhere, but a transient status is why an update can be
	// queued rather than applied, so it is worth logging.
	Status              string                   `json:"status"`
	AutoscalingSettings *autoscalingSettingsJSON `json:"autoscaling_settings"`
}

type environmentSettingsJSON struct {
	Name                string                  `json:"name"`
	AutoscalingSettings autoscalingSettingsJSON `json:"autoscaling_settings"`
	PromotionSettings   struct {
		PromotionCleanupStrategy *string `json:"promotion_cleanup_strategy,omitempty"`
		RampUpDurationSeconds    *int    `json:"ramp_up_duration_seconds,omitempty"`
		RollingDeploy            *bool   `json:"rolling_deploy,omitempty"`
	} `json:"promotion_settings"`
	AutoscalingSchedules *schedulesJSON `json:"autoscaling_schedules,omitempty"`
}

// schedulesJSON is the environment's schedule collection, which is the shape
// `autoscaling-schedule list` prints on its own.
type schedulesJSON struct {
	Timezone  *string        `json:"timezone,omitempty"`
	Schedules []scheduleJSON `json:"schedules"`
}

// scheduleJSON reads either shape of schedule: the recurring fields and the
// one-time fields are both optional here, so a reshape can be asserted by
// which set is populated.
type scheduleJSON struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Cadence             string `json:"cadence"`
	Enabled             bool   `json:"enabled"`
	AutoscalingSettings struct {
		MinReplica        int  `json:"min_replica"`
		MaxReplica        int  `json:"max_replica"`
		ConcurrencyTarget *int `json:"concurrency_target"`
	} `json:"autoscaling_settings"`

	Weekdays    []string `json:"weekdays,omitempty"`
	StartHour   *int     `json:"start_hour,omitempty"`
	StartMinute *int     `json:"start_minute,omitempty"`
	EndHour     *int     `json:"end_hour,omitempty"`
	EndMinute   *int     `json:"end_minute,omitempty"`

	StartAt *time.Time `json:"start_at,omitempty"`
	EndAt   *time.Time `json:"end_at,omitempty"`
}

// describeDeployment reads the initial deployment's current settings.
func (l *lifecycle) describeDeployment(t *testing.T) deploymentSettingsJSON {
	t.Helper()
	out := mustCLI(t, "model", "deployment", "describe",
		"--model-id", l.modelID, "--deployment-id", l.initialDeploymentID, "--output", "json")
	var deployment deploymentSettingsJSON
	require.NoError(t, json.Unmarshal([]byte(out), &deployment))
	require.NotNil(t, deployment.AutoscalingSettings, "deployment has no autoscaling settings")
	return deployment
}

// describeEnvironment reads the production environment's current settings.
func (l *lifecycle) describeEnvironment(t *testing.T) environmentSettingsJSON {
	t.Helper()
	out := mustCLI(t, "model", "environment", "describe",
		"--model-id", l.modelID, "--environment", "production", "--output", "json")
	var environment environmentSettingsJSON
	require.NoError(t, json.Unmarshal([]byte(out), &environment))
	return environment
}

// listSchedules reads the production environment's schedule collection through
// the list command, which prints the collection rather than the environment.
func (l *lifecycle) listSchedules(t *testing.T) schedulesJSON {
	t.Helper()
	out := mustCLI(t, "model", "environment", "autoscaling-schedule", "list",
		"--model-id", l.modelID, "--environment", "production", "--output", "json")
	var schedules schedulesJSON
	require.NoError(t, json.Unmarshal([]byte(out), &schedules))
	return schedules
}

// findSchedule returns the schedule with the given name, failing when absent.
func findSchedule(t *testing.T, schedules schedulesJSON, name string) scheduleJSON {
	t.Helper()
	for _, schedule := range schedules.Schedules {
		if schedule.Name == name {
			return schedule
		}
	}
	t.Fatalf("no autoscaling schedule named %q in %d schedules", name, len(schedules.Schedules))
	return scheduleJSON{}
}

// DeploymentSettings renames the deployment and updates its autoscaling
// settings. A deployment serving an environment is updated through that
// environment server-side, so each write is also asserted on the environment.
func (l *lifecycle) DeploymentSettings(t *testing.T) {
	t.Run("Rename", func(t *testing.T) {
		renamed := fmt.Sprintf("cli-e2e-renamed-%s", randomSuffix(t))
		mustCLI(t, "model", "deployment", "rename",
			"--model-id", l.modelID, "--deployment-id", l.initialDeploymentID, "--new-name", renamed)
		require.Equal(t, renamed, l.describeDeployment(t).Name)

		// Renamed back so the later name-based lookups still resolve.
		mustCLI(t, "model", "deployment", "rename",
			"--model-id", l.modelID, "--deployment-id", l.initialDeploymentID,
			"--new-name", l.deploymentName)
		require.Equal(t, l.deploymentName, l.describeDeployment(t).Name)
	})

	t.Run("UpdateAutoscaling", func(t *testing.T) {
		before := l.describeDeployment(t)
		out := mustCLI(t, "model", "deployment", "update-autoscaling",
			"--model-id", l.modelID, "--deployment-id", l.initialDeploymentID,
			"--autoscaling-window", fmt.Sprint(deploymentAutoscalingWindow),
			"--scale-down-delay", fmt.Sprint(deploymentScaleDownDelay),
			"--concurrency-target", fmt.Sprint(deploymentConcurrencyTarget),
			"--output", "json")
		var resp deploymentAutoscalingResponseJSON
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		t.Logf("update-autoscaling returned %s (%s); deployment status was %s",
			resp.Status, resp.Message, before.Status)

		// The omitted replica counts are left alone.
		deployment := l.describeDeployment(t)
		require.Equal(t, before.AutoscalingSettings.MinReplica, deployment.AutoscalingSettings.MinReplica)
		require.Equal(t, before.AutoscalingSettings.MaxReplica, deployment.AutoscalingSettings.MaxReplica)

		// The changed values are asserted on the environment, not on the
		// deployment. A deployment serving a stable environment is written
		// through that environment, and only the environment baseline is written
		// synchronously: an ACCEPTED response means the deployment's own copy is
		// queued, so reading it back here races the queue.
		environment := l.describeEnvironment(t)
		require.Equal(t, deploymentAutoscalingWindow, *environment.AutoscalingSettings.AutoscalingWindow)
		require.Equal(t, deploymentScaleDownDelay, *environment.AutoscalingSettings.ScaleDownDelay)
		require.Equal(t, deploymentConcurrencyTarget, environment.AutoscalingSettings.ConcurrencyTarget)
	})
}

// EnvironmentSettings updates the production environment's autoscaling and
// promotion settings. Rolling deploy is deliberately left alone: turning it on
// would change how the later Redeploy phase behaves, and an in-progress rolling
// promotion makes the backend reject further settings updates.
func (l *lifecycle) EnvironmentSettings(t *testing.T) {
	t.Run("UpdateAutoscaling", func(t *testing.T) {
		out := mustCLI(t, "model", "environment", "update-autoscaling",
			"--model-id", l.modelID, "--environment", "production",
			"--autoscaling-window", fmt.Sprint(environmentAutoscalingWindow),
			"--scale-down-delay", fmt.Sprint(environmentScaleDownDelay),
			"--concurrency-target", fmt.Sprint(environmentConcurrencyTarget),
			"--output", "json")
		var resp environmentUpdateResponseJSON
		require.NoError(t, json.Unmarshal([]byte(out), &resp))

		// The PATCH echoes the updated environment, which must agree with a
		// subsequent read.
		require.Equal(t, environmentAutoscalingWindow, *resp.Environment.AutoscalingSettings.AutoscalingWindow)

		environment := l.describeEnvironment(t)
		require.Equal(t, environmentAutoscalingWindow, *environment.AutoscalingSettings.AutoscalingWindow)
		require.Equal(t, environmentScaleDownDelay, *environment.AutoscalingSettings.ScaleDownDelay)
		require.Equal(t, environmentConcurrencyTarget, environment.AutoscalingSettings.ConcurrencyTarget)

		// The deployment's own copy is deliberately not read here, for the same
		// reason as the deployment phase: it is written through the queue.
	})

	t.Run("UpdatePromotion", func(t *testing.T) {
		before := l.describeEnvironment(t)
		mustCLI(t, "model", "environment", "update-promotion",
			"--model-id", l.modelID, "--environment", "production",
			"--ramp-up-duration-seconds", fmt.Sprint(environmentRampUpDurationSeconds),
			"--promotion-cleanup-strategy", "scale-to-zero")

		environment := l.describeEnvironment(t)
		require.Equal(t, environmentRampUpDurationSeconds, *environment.PromotionSettings.RampUpDurationSeconds)
		require.Equal(t, environmentCleanupStrategy, *environment.PromotionSettings.PromotionCleanupStrategy)

		// Rolling deploy was omitted, so it is unchanged.
		require.Equal(t, before.PromotionSettings.RollingDeploy, environment.PromotionSettings.RollingDeploy)
	})
}

// AutoscalingSchedule drives the schedule collection through its full lifecycle.
// Every schedule is created disabled: an enabled schedule whose window happens
// to be open when CI runs would scale the deployment, and an enabled schedule
// also makes the backend defer environment autoscaling edits to the reconciler
// rather than writing the deployment, which would invalidate the assertions in
// the phases above. That is also why this phase runs last.
func (l *lifecycle) AutoscalingSchedule(t *testing.T) {
	dailyName := fmt.Sprintf("cli-e2e-daily-%s", randomSuffix(t))
	hourlyName := fmt.Sprintf("cli-e2e-hourly-%s", randomSuffix(t))
	// Renamed by the Rename sub-test, and the target of everything after it.
	renamedName := dailyName + "-renamed"

	t.Run("Create", func(t *testing.T) {
		mustCLI(t, "model", "environment", "autoscaling-schedule", "create",
			"--model-id", l.modelID, "--environment", "production",
			"--name", dailyName, "--cadence", "daily",
			"--weekdays", "monday,wednesday",
			"--start-hour", "3", "--start-minute", "15",
			"--end-hour", "4", "--end-minute", "45",
			"--timezone", "America/New_York",
			"--min-replica", "1", "--max-replica", "1",
			"--enabled", "false")

		schedules := l.listSchedules(t)
		require.Equal(t, "America/New_York", *schedules.Timezone)
		schedule := findSchedule(t, schedules, dailyName)
		require.NotEmpty(t, schedule.ID)
		require.Equal(t, "DAILY", schedule.Cadence)
		require.False(t, schedule.Enabled)
		require.Equal(t, []string{"MONDAY", "WEDNESDAY"}, schedule.Weekdays)
		require.Equal(t, 3, *schedule.StartHour)
		require.Equal(t, 15, *schedule.StartMinute)
		require.Equal(t, 4, *schedule.EndHour)
		require.Equal(t, 45, *schedule.EndMinute)
		require.Equal(t, 1, schedule.AutoscalingSettings.MinReplica)
		require.Equal(t, 1, schedule.AutoscalingSettings.MaxReplica)

		// An override the create omitted is null, meaning inherit.
		require.Nil(t, schedule.AutoscalingSettings.ConcurrencyTarget)

		// The environment describe carries the same collection, which is what
		// makes the collection visible without the dedicated list command.
		environment := l.describeEnvironment(t)
		require.NotNil(t, environment.AutoscalingSchedules, "environment describe omitted the schedules")
		findSchedule(t, *environment.AutoscalingSchedules, dailyName)
	})

	t.Run("UpdateMerges", func(t *testing.T) {
		before := findSchedule(t, l.listSchedules(t), dailyName)
		mustCLI(t, "model", "environment", "autoscaling-schedule", "update",
			"--model-id", l.modelID, "--environment", "production",
			"--schedule-name", dailyName, "--concurrency-target", "5")

		after := findSchedule(t, l.listSchedules(t), dailyName)
		require.Equal(t, 5, *after.AutoscalingSettings.ConcurrencyTarget)

		// Everything the update did not mention survived the wholesale replace
		// the API performs, which is the point of the read-and-merge.
		require.Equal(t, before.ID, after.ID)
		require.Equal(t, before.Cadence, after.Cadence)
		require.Equal(t, before.Enabled, after.Enabled)
		require.Equal(t, before.Weekdays, after.Weekdays)
		require.Equal(t, before.StartHour, after.StartHour)
		require.Equal(t, before.StartMinute, after.StartMinute)
		require.Equal(t, before.EndHour, after.EndHour)
		require.Equal(t, before.EndMinute, after.EndMinute)
		require.Equal(t, before.AutoscalingSettings.MinReplica, after.AutoscalingSettings.MinReplica)
		require.Equal(t, before.AutoscalingSettings.MaxReplica, after.AutoscalingSettings.MaxReplica)
	})

	t.Run("Rename", func(t *testing.T) {
		before := findSchedule(t, l.listSchedules(t), dailyName)
		mustCLI(t, "model", "environment", "autoscaling-schedule", "update",
			"--model-id", l.modelID, "--environment", "production",
			"--schedule-name", dailyName, "--name", renamedName)

		after := findSchedule(t, l.listSchedules(t), renamedName)
		require.Equal(t, before.ID, after.ID, "rename should keep the schedule id")
	})

	t.Run("Reshape", func(t *testing.T) {
		before := findSchedule(t, l.listSchedules(t), renamedName)
		// Well past now, since the API rejects a one-time window in the past.
		startAt := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Minute)
		endAt := startAt.Add(2 * time.Hour)
		layout := "2006-01-02T15:04"

		mustCLI(t, "model", "environment", "autoscaling-schedule", "update",
			"--model-id", l.modelID, "--environment", "production",
			"--schedule-name", renamedName, "--cadence", "one-time",
			"--start-at", startAt.Format(layout), "--end-at", endAt.Format(layout))

		after := findSchedule(t, l.listSchedules(t), renamedName)
		require.Equal(t, "ONE_TIME", after.Cadence)
		require.Equal(t, before.ID, after.ID)
		require.False(t, after.Enabled, "reshape should carry the enabled state over")
		require.Equal(t, before.AutoscalingSettings.MinReplica, after.AutoscalingSettings.MinReplica)
		require.Equal(t, before.AutoscalingSettings.ConcurrencyTarget, after.AutoscalingSettings.ConcurrencyTarget)

		require.NotNil(t, after.StartAt, "one-time schedule has no start_at")
		require.NotNil(t, after.EndAt, "one-time schedule has no end_at")
		require.True(t, after.StartAt.Equal(startAt), "start_at %s != %s", after.StartAt, startAt)
		require.True(t, after.EndAt.Equal(endAt), "end_at %s != %s", after.EndAt, endAt)

		// The recurring fields the old shape used are gone.
		require.Empty(t, after.Weekdays)
	})

	t.Run("UpdateSettings", func(t *testing.T) {
		mustCLI(t, "model", "environment", "autoscaling-schedule", "update-settings",
			"--model-id", l.modelID, "--environment", "production", "--timezone", "UTC")

		require.Equal(t, "UTC", *l.listSchedules(t).Timezone)
	})

	t.Run("CreateSecond", func(t *testing.T) {
		// The timezone is omitted here: it belongs to the collection, which
		// already has one.
		mustCLI(t, "model", "environment", "autoscaling-schedule", "create",
			"--model-id", l.modelID, "--environment", "production",
			"--name", hourlyName, "--cadence", "hourly",
			"--weekdays", "saturday", "--start-minute", "0", "--end-minute", "30",
			"--min-replica", "1", "--max-replica", "1",
			"--enabled", "false")

		schedules := l.listSchedules(t)
		require.Len(t, schedules.Schedules, 2)
		require.Equal(t, "HOURLY", findSchedule(t, schedules, hourlyName).Cadence)
		// Creating a second schedule leaves the first one alone.
		findSchedule(t, schedules, renamedName)
	})

	t.Run("DeleteByID", func(t *testing.T) {
		hourly := findSchedule(t, l.listSchedules(t), hourlyName)
		mustCLI(t, "model", "environment", "autoscaling-schedule", "delete",
			"--model-id", l.modelID, "--environment", "production", "--schedule-id", hourly.ID)

		schedules := l.listSchedules(t)
		require.Len(t, schedules.Schedules, 1)
		findSchedule(t, schedules, renamedName)
	})

	t.Run("DeleteByName", func(t *testing.T) {
		mustCLI(t, "model", "environment", "autoscaling-schedule", "delete",
			"--model-id", l.modelID, "--environment", "production", "--schedule-name", renamedName)

		require.Empty(t, l.listSchedules(t).Schedules)
	})
}
