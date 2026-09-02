package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("model environment autoscaling-schedule list",
		commandModelEnvironmentAutoscalingScheduleList)
	Register("model environment autoscaling-schedule create",
		commandModelEnvironmentAutoscalingScheduleCreate)
	Register("model environment autoscaling-schedule update",
		commandModelEnvironmentAutoscalingScheduleUpdate)
	Register("model environment autoscaling-schedule delete",
		commandModelEnvironmentAutoscalingScheduleDelete)
	Register("model environment autoscaling-schedule update-settings",
		commandModelEnvironmentAutoscalingScheduleUpdateSettings)
}

// autoscalingScheduleOneTimeCadence is the discriminator value that selects the
// one-time schedule shape; the API's other cadences are all recurring.
const autoscalingScheduleOneTimeCadence = "ONE_TIME"

func commandModelEnvironmentAutoscalingScheduleList(
	ctx *CommandContext, flags *cmd.ModelEnvironmentAutoscalingScheduleListFlags,
) error {
	schedules, err := autoscalingScheduleGetAll(ctx, flags.ModelEnvironmentFlags)
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(schedules)
		return nil
	}
	if len(schedules.Schedules) == 0 {
		ctx.LogLine("No autoscaling schedules found.")
		return nil
	}
	rows := make([][]string, 0, len(schedules.Schedules))
	for _, item := range schedules.Schedules {
		row, err := autoscalingScheduleTableRow(item)
		if err != nil {
			// Skipped rather than fatal: the API can add a cadence an older CLI
			// does not know, and the schedules it can read are still worth
			// showing.
			ctx.Logf("Skipped an autoscaling schedule this CLI cannot read: %v\n", err)
			continue
		}
		rows = append(rows, row)
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "CADENCE", "ENABLED", "WINDOW", "REPLICAS"},
		Rows:    rows,
	})
	return nil
}

func commandModelEnvironmentAutoscalingScheduleCreate(
	ctx *CommandContext, flags *cmd.ModelEnvironmentAutoscalingScheduleCreateFlags,
) error {
	cadence := enumToAPIValue(flags.Cadence)
	if err := autoscalingScheduleValidateTiming(cadence, flags.AutoscalingScheduleTimingFlags); err != nil {
		return err
	}
	// The API requires a complete override block, and these two have no
	// environment-level fallback.
	if !flags.MinReplica.IsSet() || !flags.MaxReplica.IsSet() {
		return cmd.NewErrUsagef("--min-replica and --max-replica are required")
	}

	overrides := autoscalingScheduleNewOverrides(flags.AutoscalingSettingsFlags,
		*flags.MinReplica.Pointer(), *flags.MaxReplica.Pointer())
	enabled := true
	if value := flags.Enabled.Pointer(); value != nil {
		enabled = *value
	}

	var item managementapi.UpdateAutoscalingScheduleSettings_Schedules_Item
	if cadence == autoscalingScheduleOneTimeCadence {
		err := item.FromOneTimeAutoscalingScheduleUpsert(managementapi.OneTimeAutoscalingScheduleUpsert{
			AutoscalingSettings: overrides,
			Cadence:             cadence,
			Enabled:             enabled,
			Name:                flags.Name,
			StartAt:             flags.StartAt,
			EndAt:               flags.EndAt,
		})
		if err != nil {
			return err
		}
	} else {
		err := item.FromAutoscalingScheduleUpsert(managementapi.AutoscalingScheduleUpsert{
			AutoscalingSettings: overrides,
			Cadence:             managementapi.AutoscalingScheduleUpsertCadence(cadence),
			Enabled:             enabled,
			Name:                flags.Name,
			Weekdays:            autoscalingScheduleWeekdays(flags.Weekdays),
			StartHour:           flags.StartHour.Pointer(),
			StartMinute:         autoscalingScheduleIntOrZero(flags.StartMinute),
			EndHour:             flags.EndHour.Pointer(),
			EndMinute:           autoscalingScheduleIntOrZero(flags.EndMinute),
		})
		if err != nil {
			return err
		}
	}

	settings := managementapi.UpdateAutoscalingScheduleSettings{
		Schedules: &[]managementapi.UpdateAutoscalingScheduleSettings_Schedules_Item{item},
	}
	if flags.Timezone != "" {
		settings.Timezone = managementapi.NewOptional(&flags.Timezone)
	}
	resp, err := patchModelEnvironment(ctx, flags.ModelEnvironmentFlags,
		managementapi.UpdateEnvironmentRequest{AutoscalingScheduleSettings: &settings})
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Created autoscaling schedule %s\n", flags.Name)
	return nil
}

func commandModelEnvironmentAutoscalingScheduleUpdate(
	ctx *CommandContext, flags *cmd.ModelEnvironmentAutoscalingScheduleUpdateFlags,
) error {
	// The API replaces a schedule wholesale, so the existing one has to be read
	// and merged with the caller's changes. This is racy against a concurrent
	// edit; losing the race means the other edit is overwritten.
	schedules, err := autoscalingScheduleGetAll(ctx, flags.ModelEnvironmentFlags)
	if err != nil {
		return err
	}
	existing, err := autoscalingScheduleFind(schedules, flags.ModelEnvironmentAutoscalingScheduleRefFlags)
	if err != nil {
		return err
	}
	item, err := autoscalingScheduleMerge(existing, flags)
	if err != nil {
		return err
	}

	resp, err := patchModelEnvironment(ctx, flags.ModelEnvironmentFlags,
		managementapi.UpdateEnvironmentRequest{
			AutoscalingScheduleSettings: &managementapi.UpdateAutoscalingScheduleSettings{
				Schedules: &[]managementapi.UpdateAutoscalingScheduleSettings_Schedules_Item{item},
			},
		})
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Updated autoscaling schedule %s\n", flags.ScheduleID)
	return nil
}

func commandModelEnvironmentAutoscalingScheduleDelete(
	ctx *CommandContext, flags *cmd.ModelEnvironmentAutoscalingScheduleDeleteFlags,
) error {
	// The API deletes by id, so a name costs one read to resolve. An id needs
	// no read at all.
	scheduleID := flags.ScheduleID
	if scheduleID == "" {
		schedules, err := autoscalingScheduleGetAll(ctx, flags.ModelEnvironmentFlags)
		if err != nil {
			return err
		}
		existing, err := autoscalingScheduleFind(schedules, flags.ModelEnvironmentAutoscalingScheduleRefFlags)
		if err != nil {
			return err
		}
		if scheduleID, _, _, _, _, err = autoscalingScheduleFields(existing); err != nil {
			return err
		}
	}

	resp, err := patchModelEnvironment(ctx, flags.ModelEnvironmentFlags,
		managementapi.UpdateEnvironmentRequest{
			AutoscalingScheduleSettings: &managementapi.UpdateAutoscalingScheduleSettings{
				DeleteSchedules: &[]string{scheduleID},
			},
		})
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Deleted autoscaling schedule %s\n", scheduleID)
	return nil
}

func commandModelEnvironmentAutoscalingScheduleUpdateSettings(
	ctx *CommandContext, flags *cmd.ModelEnvironmentAutoscalingScheduleUpdateSettingsFlags,
) error {
	resp, err := patchModelEnvironment(ctx, flags.ModelEnvironmentFlags,
		managementapi.UpdateEnvironmentRequest{
			AutoscalingScheduleSettings: &managementapi.UpdateAutoscalingScheduleSettings{
				Timezone: managementapi.NewOptional(&flags.Timezone),
			},
		})
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Updated autoscaling schedule settings\n")
	return nil
}

// autoscalingScheduleGetAll reads an environment's schedule collection. The
// collection is absent rather than empty when the environment has never had a
// schedule, which callers treat the same way.
func autoscalingScheduleGetAll(
	ctx *CommandContext, flags cmd.ModelEnvironmentFlags,
) (*managementapi.EnvironmentAutoscalingSchedules, error) {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return nil, err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return nil, err
	}
	env, err := cl.API().GetModelsEnvironmentsEnvName(ctx, ref.ID, flags.Environment)
	if err != nil {
		return nil, fmt.Errorf("describe environment %s: %w", flags.Environment, err)
	}
	if env.AutoscalingSchedules == nil {
		return &managementapi.EnvironmentAutoscalingSchedules{}, nil
	}
	return env.AutoscalingSchedules, nil
}

// autoscalingScheduleValidateTiming rejects timing flags that belong to the
// other cadence. The command picks which request shape to build, so a flag from
// the wrong shape would otherwise be dropped without a word.
func autoscalingScheduleValidateTiming(
	cadence string, flags cmd.AutoscalingScheduleTimingFlags,
) error {
	if cadence == autoscalingScheduleOneTimeCadence {
		if err := autoscalingScheduleRejectRecurringFlags(flags); err != nil {
			return err
		}
		if flags.StartAt.IsZero() || flags.EndAt.IsZero() {
			return cmd.NewErrUsagef("--start-at and --end-at are required with --cadence one-time")
		}
		return nil
	}
	if err := autoscalingScheduleRejectOneTimeFlags(flags); err != nil {
		return err
	}
	if len(flags.Weekdays) == 0 {
		return cmd.NewErrUsagef("--weekdays is required with --cadence %s", strings.ToLower(cadence))
	}
	return nil
}

func autoscalingScheduleRejectRecurringFlags(flags cmd.AutoscalingScheduleTimingFlags) error {
	for _, flag := range []struct {
		name string
		set  bool
	}{
		{"weekdays", len(flags.Weekdays) > 0},
		{"start-hour", flags.StartHour.IsSet()},
		{"start-minute", flags.StartMinute.IsSet()},
		{"end-hour", flags.EndHour.IsSet()},
		{"end-minute", flags.EndMinute.IsSet()},
	} {
		if flag.set {
			return cmd.NewErrUsagef("--%s is not valid on a one-time schedule", flag.name)
		}
	}
	return nil
}

func autoscalingScheduleRejectOneTimeFlags(flags cmd.AutoscalingScheduleTimingFlags) error {
	if !flags.StartAt.IsZero() {
		return cmd.NewErrUsagef("--start-at is only valid on a one-time schedule")
	}
	if !flags.EndAt.IsZero() {
		return cmd.NewErrUsagef("--end-at is only valid on a one-time schedule")
	}
	return nil
}

// autoscalingScheduleNewOverrides builds the complete override block a schedule
// upsert requires. Every field is sent, and a field the caller left out is sent
// as null, which the API reads as "inherit from the environment".
func autoscalingScheduleNewOverrides(
	flags cmd.AutoscalingSettingsFlags, minReplica, maxReplica int,
) managementapi.AutoscalingScheduleSettingsRequest {
	return managementapi.AutoscalingScheduleSettingsRequest{
		MinReplica:                  minReplica,
		MaxReplica:                  maxReplica,
		AutoscalingWindow:           flags.AutoscalingWindow.Pointer(),
		ScaleDownDelay:              flags.ScaleDownDelay.Pointer(),
		ConcurrencyTarget:           flags.ConcurrencyTarget.Pointer(),
		TargetUtilizationPercentage: flags.TargetUtilizationPercentage.Pointer(),
		TargetInFlightTokens:        flags.TargetInFlightTokens.Pointer(),
		MaxScaleDownRate:            flags.MaxScaleDownRate.Pointer(),
	}
}

func autoscalingScheduleWeekdays(values []string) []managementapi.AutoscalingScheduleWeekday {
	weekdays := make([]managementapi.AutoscalingScheduleWeekday, 0, len(values))
	for _, value := range values {
		weekdays = append(weekdays, managementapi.AutoscalingScheduleWeekday(enumToAPIValue(value)))
	}
	return weekdays
}

// autoscalingScheduleIntOrZero flattens a flag onto a required API field, where
// the API's own default is zero.
func autoscalingScheduleIntOrZero(flag cmd.OptionalFlag[int]) int {
	if value := flag.Pointer(); value != nil {
		return *value
	}
	return 0
}

// autoscalingScheduleFind locates a schedule by id or by name. Names are unique
// per environment, enforced server-side, so a name identifies exactly one.
// A schedule this CLI cannot read is skipped rather than failing the lookup,
// since it is not the one being asked for unless its id or name matches, which
// cannot be known without reading it.
func autoscalingScheduleFind(
	schedules *managementapi.EnvironmentAutoscalingSchedules, ref cmd.ModelEnvironmentAutoscalingScheduleRefFlags,
) (any, error) {
	for _, item := range schedules.Schedules {
		value, err := item.ValueByDiscriminator()
		if err != nil {
			continue
		}
		id, name, _, _, _, err := autoscalingScheduleFields(value)
		if err != nil {
			continue
		}
		if ref.ScheduleID != "" && id == ref.ScheduleID {
			return value, nil
		}
		if ref.ScheduleName != "" && name == ref.ScheduleName {
			return value, nil
		}
	}
	if ref.ScheduleID != "" {
		return nil, fmt.Errorf("no autoscaling schedule %s on this environment", ref.ScheduleID)
	}
	return nil, fmt.Errorf("no autoscaling schedule named %q on this environment", ref.ScheduleName)
}

// autoscalingScheduleMerge applies the caller's changes to the schedule as it
// exists today and returns the complete replacement the API expects.
//
// A schedule may change cadence: the API rebuilds the row from the upsert alone
// and each shape clears the other's storage columns. What cannot carry over is
// the timing, since the two shapes describe it with different fields, so
// reshaping requires the new shape's timing flags the same way create does.
// Everything shared, meaning the name, enabled, and the autoscaling overrides,
// carries over either way.
func autoscalingScheduleMerge(
	existing any, flags *cmd.ModelEnvironmentAutoscalingScheduleUpdateFlags,
) (managementapi.UpdateAutoscalingScheduleSettings_Schedules_Item, error) {
	var item managementapi.UpdateAutoscalingScheduleSettings_Schedules_Item

	id, name, currentCadence, enabled, currentOverrides, err := autoscalingScheduleFields(existing)
	if err != nil {
		return item, err
	}
	cadence := currentCadence
	if value := flags.Cadence.Pointer(); value != nil {
		cadence = *value
	}
	reshaping := cadence != currentCadence

	if value := flags.Name.Pointer(); value != nil {
		name = *value
	}
	if value := flags.Enabled.Pointer(); value != nil {
		enabled = *value
	}
	overrides := autoscalingScheduleMergeOverrides(currentOverrides, flags.AutoscalingSettingsFlags)

	if enumToAPIValue(cadence) == autoscalingScheduleOneTimeCadence {
		if err := autoscalingScheduleRejectRecurringFlags(flags.AutoscalingScheduleTimingFlags); err != nil {
			return item, err
		}
		startAt, endAt := flags.StartAt, flags.EndAt
		if current, ok := existing.(managementapi.OneTimeAutoscalingSchedule); ok {
			if startAt.IsZero() {
				startAt = current.StartAt
			}
			if endAt.IsZero() {
				endAt = current.EndAt
			}
		}
		if startAt.IsZero() || endAt.IsZero() {
			return item, cmd.NewErrUsagef(
				"--start-at and --end-at are required when changing a schedule to --cadence one-time")
		}
		return item, item.FromOneTimeAutoscalingScheduleUpsert(
			managementapi.OneTimeAutoscalingScheduleUpsert{
				AutoscalingSettings: overrides,
				Cadence:             autoscalingScheduleOneTimeCadence,
				Enabled:             enabled,
				Id:                  &id,
				Name:                name,
				StartAt:             startAt,
				EndAt:               endAt,
			})
	}

	if err := autoscalingScheduleRejectOneTimeFlags(flags.AutoscalingScheduleTimingFlags); err != nil {
		return item, err
	}
	upsert := managementapi.AutoscalingScheduleUpsert{
		AutoscalingSettings: overrides,
		Cadence:             managementapi.AutoscalingScheduleUpsertCadence(enumToAPIValue(cadence)),
		Enabled:             enabled,
		Id:                  &id,
		Name:                name,
	}
	if current, ok := existing.(managementapi.AutoscalingSchedule); ok {
		upsert.Weekdays = current.Weekdays
		upsert.StartHour, upsert.StartMinute = current.StartHour, current.StartMinute
		upsert.EndHour, upsert.EndMinute = current.EndHour, current.EndMinute
	}
	if len(flags.Weekdays) > 0 {
		upsert.Weekdays = autoscalingScheduleWeekdays(flags.Weekdays)
	}
	if flags.StartHour.IsSet() {
		upsert.StartHour = flags.StartHour.Pointer()
	}
	if flags.StartMinute.IsSet() {
		upsert.StartMinute = *flags.StartMinute.Pointer()
	}
	if flags.EndHour.IsSet() {
		upsert.EndHour = flags.EndHour.Pointer()
	}
	if flags.EndMinute.IsSet() {
		upsert.EndMinute = *flags.EndMinute.Pointer()
	}
	if reshaping && len(upsert.Weekdays) == 0 {
		return item, cmd.NewErrUsagef(
			"--weekdays is required when changing a schedule to --cadence %s", cadence)
	}
	return item, item.FromAutoscalingScheduleUpsert(upsert)
}

// autoscalingScheduleMergeOverrides layers the caller's autoscaling flags over
// the schedule's current overrides. A field the caller did not pass keeps
// whatever the schedule already had, including null for "inherit".
func autoscalingScheduleMergeOverrides(
	current managementapi.AutoscalingScheduleSettings, flags cmd.AutoscalingSettingsFlags,
) managementapi.AutoscalingScheduleSettingsRequest {
	merged := managementapi.AutoscalingScheduleSettingsRequest{
		MinReplica:                  current.MinReplica,
		MaxReplica:                  current.MaxReplica,
		AutoscalingWindow:           current.AutoscalingWindow,
		ScaleDownDelay:              current.ScaleDownDelay,
		ConcurrencyTarget:           current.ConcurrencyTarget,
		TargetUtilizationPercentage: current.TargetUtilizationPercentage,
		TargetInFlightTokens:        current.TargetInFlightTokens,
		MaxScaleDownRate:            current.MaxScaleDownRate,
	}
	if flags.MinReplica.IsSet() {
		merged.MinReplica = *flags.MinReplica.Pointer()
	}
	if flags.MaxReplica.IsSet() {
		merged.MaxReplica = *flags.MaxReplica.Pointer()
	}
	if flags.AutoscalingWindow.IsSet() {
		merged.AutoscalingWindow = flags.AutoscalingWindow.Pointer()
	}
	if flags.ScaleDownDelay.IsSet() {
		merged.ScaleDownDelay = flags.ScaleDownDelay.Pointer()
	}
	if flags.ConcurrencyTarget.IsSet() {
		merged.ConcurrencyTarget = flags.ConcurrencyTarget.Pointer()
	}
	if flags.TargetUtilizationPercentage.IsSet() {
		merged.TargetUtilizationPercentage = flags.TargetUtilizationPercentage.Pointer()
	}
	if flags.TargetInFlightTokens.IsSet() {
		merged.TargetInFlightTokens = flags.TargetInFlightTokens.Pointer()
	}
	if flags.MaxScaleDownRate.IsSet() {
		merged.MaxScaleDownRate = flags.MaxScaleDownRate.Pointer()
	}
	return merged
}

// autoscalingScheduleTableRow renders one schedule as a row for the list
// command, which is where the ids needed by update and delete come from.
func autoscalingScheduleTableRow(
	item managementapi.EnvironmentAutoscalingSchedules_Schedules_Item,
) ([]string, error) {
	value, err := item.ValueByDiscriminator()
	if err != nil {
		return nil, err
	}
	id, name, cadence, enabled, settings, err := autoscalingScheduleFields(value)
	if err != nil {
		return nil, err
	}
	return []string{
		id, name, cadence, strconv.FormatBool(enabled),
		autoscalingScheduleWindowText(value),
		fmt.Sprintf("%d-%d", settings.MinReplica, settings.MaxReplica),
	}, nil
}

// autoscalingScheduleFields pulls the properties both schedule shapes have in
// common, so the renderers only special-case the window.
func autoscalingScheduleFields(value any) (
	id, name, cadence string,
	enabled bool,
	settings managementapi.AutoscalingScheduleSettings,
	err error,
) {
	// Cadence and weekdays are shown in the spelling --cadence and --weekdays
	// accept, so a schedule can be read out and recreated without translation.
	switch schedule := value.(type) {
	case managementapi.AutoscalingSchedule:
		return schedule.Id, schedule.Name, enumFromAPIValue(string(schedule.Cadence)),
			schedule.Enabled, schedule.AutoscalingSettings, nil
	case managementapi.OneTimeAutoscalingSchedule:
		return schedule.Id, schedule.Name, enumFromAPIValue(schedule.Cadence),
			schedule.Enabled, schedule.AutoscalingSettings, nil
	}
	return "", "", "", false, managementapi.AutoscalingScheduleSettings{},
		fmt.Errorf("unsupported autoscaling schedule shape %T", value)
}

// autoscalingScheduleWindowText renders when a schedule is active, collapsing
// the recurring and one-time shapes into one string. An hourly schedule with no
// hour bound repeats every hour, which shows as '*'.
func autoscalingScheduleWindowText(value any) string {
	clock := func(hour *int, minute int) string {
		if hour == nil {
			return fmt.Sprintf("*:%02d", minute)
		}
		return fmt.Sprintf("%02d:%02d", *hour, minute)
	}
	switch schedule := value.(type) {
	case managementapi.AutoscalingSchedule:
		weekdays := make([]string, 0, len(schedule.Weekdays))
		for _, day := range schedule.Weekdays {
			weekdays = append(weekdays, enumFromAPIValue(string(day)))
		}
		return fmt.Sprintf("%s %s-%s", strings.Join(weekdays, ","),
			clock(schedule.StartHour, schedule.StartMinute),
			clock(schedule.EndHour, schedule.EndMinute))
	case managementapi.OneTimeAutoscalingSchedule:
		return fmt.Sprintf("%s-%s",
			schedule.StartAt.Format(time.RFC3339), schedule.EndAt.Format(time.RFC3339))
	}
	return "-"
}
