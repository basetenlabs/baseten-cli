package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// commandModelEnvironmentAutoscalingSchedule groups the
// `baseten model environment autoscaling-schedule` subcommands. Schedules are an
// id-keyed collection, so unlike the other environment settings they need their
// own verbs rather than a single update command.
var commandModelEnvironmentAutoscalingSchedule = Command{
	Name:    "autoscaling-schedule",
	Summary: "Manage an environment's autoscaling schedules",
	Children: []Command{
		{
			Name:    "create",
			Summary: "Create an autoscaling schedule",
			Description: "Add one autoscaling schedule to an environment.\n\n" +
				"--cadence selects the schedule's shape. 'daily' and 'hourly' are recurring and " +
				"need --weekdays and a minute window; 'one-time' runs once and needs --start-at " +
				"and --end-at instead.\n\n" +
				"--min-replica and --max-replica are required. Any other autoscaling flag you " +
				"omit is inherited from the environment while the schedule is active.\n\n" +
				"--timezone is required for the first schedule on an environment, since the " +
				"timezone is shared by the whole collection and has no default. Afterwards it " +
				"is optional and must match the existing one; use 'update-settings' to change " +
				"it for every schedule at once.\n\n" +
				"Run 'baseten model environment describe' to see the current values.",
			Flags: ModelEnvironmentAutoscalingScheduleCreateFlags{},
			Output: &CommandOutput[managementapi.UpdateEnvironmentResponse]{
				TextDescription: "On success, prints \"Created autoscaling schedule <name>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Scale up on weekday mornings.",
						Command:     "baseten model environment autoscaling-schedule create --model-id <model-id> --environment production --name weekday-peak --cadence daily --weekdays monday,tuesday,wednesday,thursday,friday --start-hour 8 --start-minute 0 --end-hour 18 --end-minute 0 --timezone America/New_York --min-replica 4 --max-replica 20",
					},
					{
						Description: "Scale up for a one-off launch window.",
						Command:     "baseten model environment autoscaling-schedule create --model-id <model-id> --environment production --name launch --cadence one-time --start-at 2026-09-01T08:00 --end-at 2026-09-01T20:00 --timezone America/New_York --min-replica 10 --max-replica 50",
					},
				},
				JQExample: CommandExample{
					Description: "Print the ids of every schedule after the create.",
					Command:     "baseten model environment autoscaling-schedule create --model-id <model-id> --environment production --name weekday-peak --cadence daily --weekdays monday --start-minute 0 --end-minute 0 --timezone UTC --min-replica 1 --max-replica 2 --jq '.environment.autoscaling_schedules.schedules[].id'",
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete an autoscaling schedule",
			Description: "Remove one autoscaling schedule from an environment, identified by " +
				"id or by name.\n\n" +
				"Run 'baseten model environment autoscaling-schedule list' to see both.",
			Flags: ModelEnvironmentAutoscalingScheduleDeleteFlags{},
			Output: &CommandOutput[managementapi.UpdateEnvironmentResponse]{
				TextDescription: "On success, prints \"Deleted autoscaling schedule <id>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Delete a schedule by id.",
						Command:     "baseten model environment autoscaling-schedule delete --model-id <model-id> --environment production --schedule-id <schedule-id>",
					},
					{
						Description: "Delete a schedule by name.",
						Command:     "baseten model environment autoscaling-schedule delete --model-id <model-id> --environment production --schedule-name weekday-peak",
					},
				},
				JQExample: CommandExample{
					Description: "Print the ids that remain.",
					Command:     "baseten model environment autoscaling-schedule delete --model-id <model-id> --environment production --schedule-id <schedule-id> --jq '.environment.autoscaling_schedules.schedules[].id'",
				},
			},
		},
		{
			Name:    "list",
			Summary: "List an environment's autoscaling schedules",
			Description: "List every autoscaling schedule on an environment with its id, which " +
				"the update and delete commands take.",
			Flags: ModelEnvironmentAutoscalingScheduleListFlags{},
			Output: &CommandOutput[managementapi.EnvironmentAutoscalingSchedules]{
				TextDescription: "Table with columns: ID, NAME, CADENCE, ENABLED, WINDOW, REPLICAS. " +
					"When no schedules exist, prints \"No autoscaling schedules found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List an environment's schedules.",
						Command:     "baseten model environment autoscaling-schedule list --model-id <model-id> --environment production",
					},
				},
				JQExample: CommandExample{
					Description: "Print each schedule's id and name.",
					Command:     "baseten model environment autoscaling-schedule list --model-id <model-id> --environment production --jq '.schedules[] | \"\\(.id) \\(.name)\"'",
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update an autoscaling schedule",
			Description: "Change one autoscaling schedule, identified by id or by name.\n\n" +
				"Only the flags you pass are changed; the rest of the schedule is preserved. " +
				"Because the API replaces a schedule wholesale, the command reads the " +
				"environment first and merges your changes into the existing schedule.\n\n" +
				"Pass --cadence to change a schedule's shape. The new shape's timing flags " +
				"are then required, since a recurring window and a one-time interval have " +
				"nothing in common to carry over; the name, enabled state, and autoscaling " +
				"overrides carry over either way.\n\n" +
				"--timezone is not accepted here, because it belongs to the whole collection; " +
				"use 'update-settings'.",
			Flags: ModelEnvironmentAutoscalingScheduleUpdateFlags{},
			Output: &CommandOutput[managementapi.UpdateEnvironmentResponse]{
				TextDescription: "On success, prints \"Updated autoscaling schedule <id>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Raise a schedule's replica floor.",
						Command:     "baseten model environment autoscaling-schedule update --model-id <model-id> --environment production --schedule-id <schedule-id> --min-replica 6",
					},
					{
						Description: "Turn a schedule off without deleting it, naming it instead of using its id.",
						Command:     "baseten model environment autoscaling-schedule update --model-id <model-id> --environment production --schedule-name weekday-peak --enabled false",
					},
					{
						Description: "Convert a recurring schedule into a one-time one.",
						Command:     "baseten model environment autoscaling-schedule update --model-id <model-id> --environment production --schedule-name weekday-peak --cadence one-time --start-at 2026-09-01T08:00 --end-at 2026-09-01T20:00",
					},
				},
				JQExample: CommandExample{
					Description: "Print the resulting schedule names.",
					Command:     "baseten model environment autoscaling-schedule update --model-id <model-id> --environment production --schedule-id <schedule-id> --min-replica 6 --jq '.environment.autoscaling_schedules.schedules[].name'",
				},
			},
		},
		{
			Name:    "update-settings",
			Summary: "Update collection-wide autoscaling schedule settings",
			Description: "Change the settings shared by all of an environment's autoscaling " +
				"schedules. Today that is only the timezone.\n\n" +
				"Setting the timezone rewrites every existing schedule, which is why it lives " +
				"here rather than on 'update'. The environment must already have at least one " +
				"schedule, since the timezone is stored per schedule.\n\n" +
				"Run 'baseten model environment autoscaling-schedule list' to see the schedules " +
				"this affects.",
			Flags: ModelEnvironmentAutoscalingScheduleUpdateSettingsFlags{},
			Output: &CommandOutput[managementapi.UpdateEnvironmentResponse]{
				TextDescription: "On success, prints \"Updated autoscaling schedule settings\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Move every schedule to a new timezone.",
						Command:     "baseten model environment autoscaling-schedule update-settings --model-id <model-id> --environment production --timezone Europe/Berlin",
					},
				},
				JQExample: CommandExample{
					Description: "Print the resulting timezone.",
					Command:     "baseten model environment autoscaling-schedule update-settings --model-id <model-id> --environment production --timezone Europe/Berlin --jq '.environment.autoscaling_schedules.timezone'",
				},
			},
		},
	},
}

// AutoscalingScheduleTimingFlags describes when a schedule is active. Which
// flags apply depends on the schedule's cadence: recurring schedules use
// weekdays and a clock window, one-time schedules use an absolute interval.
type AutoscalingScheduleTimingFlags struct {
	Enabled OptionalFlag[bool] `flag:"enabled" desc:"Whether the schedule is enabled. Defaults to true on create."`

	Weekdays    []string          `flag:"weekdays" desc:"Weekdays the schedule runs, comma-separated. Recurring schedules only." enum:"monday,tuesday,wednesday,thursday,friday,saturday,sunday"`
	StartHour   OptionalFlag[int] `flag:"start-hour" desc:"Hour the window opens, in the collection timezone. Omit for an hourly schedule that runs every hour. Recurring schedules only."`
	StartMinute OptionalFlag[int] `flag:"start-minute" desc:"Minute the window opens. Recurring schedules only."`
	EndHour     OptionalFlag[int] `flag:"end-hour" desc:"Hour the window closes, in the collection timezone. Omit for an hourly schedule that runs every hour. Recurring schedules only."`
	EndMinute   OptionalFlag[int] `flag:"end-minute" desc:"Minute the window closes. Recurring schedules only."`

	StartAt time.Time `flag:"start-at" desc:"Inclusive start of the window, ISO 8601 (for example 2026-09-01T08:00). Values without a timezone designator are read in the local timezone. Must be in the future. One-time schedules only."`
	EndAt   time.Time `flag:"end-at" desc:"Exclusive end of the window, ISO 8601. One-time schedules only."`
}

// ModelEnvironmentAutoscalingScheduleListFlags configures
// `baseten model environment autoscaling-schedule list`.
type ModelEnvironmentAutoscalingScheduleListFlags struct {
	CommandFlags
	ModelEnvironmentFlags
}

// ModelEnvironmentAutoscalingScheduleCreateFlags configures
// `baseten model environment autoscaling-schedule create`.
type ModelEnvironmentAutoscalingScheduleCreateFlags struct {
	CommandFlags
	ModelEnvironmentFlags
	AutoscalingScheduleTimingFlags
	AutoscalingSettingsFlags

	Name     string `flag:"name" desc:"Name of the schedule, unique among the environment's schedules." required:"true"`
	Cadence  string `flag:"cadence" desc:"Shape of the schedule. Recurring schedules use --weekdays and a minute window; one-time schedules use --start-at and --end-at." enum:"daily,hourly,one-time" required:"true"`
	Timezone string `flag:"timezone" desc:"IANA timezone shared by every schedule on the environment, for example America/New_York. Required for the first schedule; afterwards it must match the existing one."`
}

// ModelEnvironmentAutoscalingScheduleRefFlags identifies one schedule on an
// environment, by id or by name. Schedule names are unique per environment, so
// either identifies exactly one. The name flag is --schedule-name rather than
// --name because update uses --name for the new name when renaming.
type ModelEnvironmentAutoscalingScheduleRefFlags struct {
	ScheduleID   string `flag:"schedule-id" desc:"Stable identifier of the schedule, from 'autoscaling-schedule list'." oneof:"schedule-ref"`
	ScheduleName string `flag:"schedule-name" desc:"Name of the schedule, unique among the environment's schedules." oneof:"schedule-ref"`
}

// ModelEnvironmentAutoscalingScheduleUpdateFlags configures
// `baseten model environment autoscaling-schedule update`.
type ModelEnvironmentAutoscalingScheduleUpdateFlags struct {
	CommandFlags
	ModelEnvironmentFlags
	ModelEnvironmentAutoscalingScheduleRefFlags
	AutoscalingScheduleTimingFlags
	AutoscalingSettingsFlags

	Name    OptionalFlag[string] `flag:"name" desc:"New name for the schedule, unique among the environment's schedules."`
	Cadence OptionalFlag[string] `flag:"cadence" desc:"New shape for the schedule. Changing it requires the new shape's timing flags, since there is nothing to carry over." enum:"daily,hourly,one-time"`
}

// ModelEnvironmentAutoscalingScheduleDeleteFlags configures
// `baseten model environment autoscaling-schedule delete`.
type ModelEnvironmentAutoscalingScheduleDeleteFlags struct {
	CommandFlags
	ModelEnvironmentFlags
	ModelEnvironmentAutoscalingScheduleRefFlags
}

// ModelEnvironmentAutoscalingScheduleUpdateSettingsFlags configures
// `baseten model environment autoscaling-schedule update-settings`.
type ModelEnvironmentAutoscalingScheduleUpdateSettingsFlags struct {
	CommandFlags
	ModelEnvironmentFlags

	Timezone string `flag:"timezone" desc:"IANA timezone to apply to every schedule on the environment, for example America/New_York." required:"true"`
}
