package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

// commandModelEnvironment groups the `baseten model environment` subcommands.
var commandModelEnvironment = Command{
	Name:    "environment",
	Summary: "Manage environments of a model",
	Children: []Command{
		{
			Name:        "activate",
			Summary:     "Activate the environment's active deployment",
			Description: "Activate the deployment associated with an environment.",
			Flags:       ModelEnvironmentActivateFlags{},
			Output: &CommandOutput[managementapi.ActivateResponse]{
				TextDescription: "On success, prints \"Activated environment <name>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Activate the deployment associated with an environment.",
						Command:     "baseten model environment activate --model-id <model-id> --environment production",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the success flag.",
					Command:     "baseten model environment activate --model-id <model-id> --environment production --jq '.success'",
				},
			},
		},
		{
			Name:    "deactivate",
			Summary: "Deactivate the environment's active deployment",
			Description: "Deactivate the deployment associated with an environment.\n\n" +
				"Prompts for yes/no confirmation. Pass --yes to skip the prompt. When " +
				"stdin is not a terminal, --yes is required.",
			Flags: ModelEnvironmentDeactivateFlags{},
			Output: &CommandOutput[managementapi.DeactivateResponse]{
				TextDescription: "On success, prints \"Deactivated environment <name>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Deactivate an environment without the confirmation prompt.",
						Command:     "baseten model environment deactivate --model-id <model-id> --environment production --yes",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the success flag.",
					Command:     "baseten model environment deactivate --model-id <model-id> --environment production --yes --jq '.success'",
				},
			},
		},
		{
			Name:        "describe",
			Summary:     "Describe an environment",
			Description: "Describe a model environment by name.",
			Flags:       ModelEnvironmentDescribeFlags{},
			Output: &CommandOutput[managementapi.Environment]{
				TextDescription: "Field-per-line summary: Name, Model, Current Deployment, Status, " +
					"Candidate Deployment (optional), Invoke URL, Logs URL, Created, " +
					"Backpressure, indented Autoscaling and Promotion blocks covering every " +
					"setting the update commands can change, and one line per autoscaling " +
					"schedule. Settings that are unset or inherited show as '-'.",
				Examples: []CommandExample{
					{
						Description: "Describe the production environment of a model.",
						Command:     "baseten model environment describe --model-id <model-id> --environment production",
					},
				},
				JQExample: CommandExample{
					Description: "Print the current deployment ID.",
					Command:     "baseten model environment describe --model-id <model-id> --environment production --jq '.current_deployment.id'",
				},
			},
		},
		{
			Name:        "list",
			Summary:     "List environments for a model",
			Description: "List all environments of a model.",
			Flags:       ModelEnvironmentListFlags{},
			Output: &CommandOutput[managementapi.Environments]{
				TextDescription: "Table with columns: NAME, CURRENT DEPLOYMENT, STATUS. " +
					"When no environments exist, prints \"No environments found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List all environments of a model.",
						Command:     "baseten model environment list --model-id <model-id>",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the environment names.",
					Command:     "baseten model environment list --model-id <model-id> --jq '.environments[].name'",
				},
			},
		},
		{
			Name:    "logs",
			Summary: "Stream or tail logs for an environment",
			Description: "Fetch logs for a model environment, spanning every deployment that " +
				"was active on the environment across the time range.\n\n" +
				"By default returns up to --limit lines from the last 30 minutes, newest first. " +
				"Use --start/--end or --since to scope the window (max 7 days). " +
				"Use --tail to stream live logs until the environment's current " +
				"deployment leaves a runnable state or you interrupt with Ctrl-C.\n\n" +
				"For machine-readable streaming, prefer --output jsonl over --output json.",
			Flags: ModelEnvironmentLogsFlags{},
			Output: &CommandOutput[managementapi.Log]{
				JSONArrayStreamed: true,
				TextDescription:   "One line per log record: \"[YYYY-MM-DD HH:MM:SS]: (replica) message\".",
				Examples: []CommandExample{
					{
						Description: "Print logs for the production environment over the last hour.",
						Command:     "baseten model environment logs --model-id <model-id> --environment production --since 1h",
					},
					{
						Description: "Tail live logs until the environment's current deployment leaves a runnable state.",
						Command:     "baseten model environment logs --model-id <model-id> --environment production --tail",
					},
				},
				JQExample: CommandExample{
					Description: "Stream just the log messages as a JSONL stream.",
					Command:     "baseten model environment logs --model-id <model-id> --environment production --output jsonl --jq '.message'",
				},
			},
		},
		{
			Name:    "metrics",
			Summary: "Fetch metrics for an environment",
			Description: "Fetch metrics aggregated across every deployment that was active on the " +
				"environment over the time range.\n\n" +
				"--mode selects what you get back: a current snapshot, a windowed " +
				"summary, or a series; see its flag help for details. Scope the window " +
				"with --start/--end or --since (max 7 days), which only apply to " +
				"summary and series. In series mode the window is split at each promotion " +
				"so every point reflects the deployment(s) serving the environment at that time.",
			Flags: ModelEnvironmentMetricsFlags{},
			Output: &CommandOutput[managementapi.GetModelMetricsResponse]{
				TextDescription: "For current/summary, a table with columns METRIC, one column per " +
					"label dimension (e.g. QUANTILE, STAT), and VALUE; summary COUNTER values show " +
					"\"total (rate/s)\". For series, a sparkline per metric label set with its " +
					"min-max range and end value, or a per-step table under --no-chart.",
				JSONDescription: "The metrics response: metric_descriptors, index-mapped metric_values, " +
					"the resolved mode, and the returned window.",
				Examples: []CommandExample{
					{
						Description: "Show a current snapshot of the default metrics for the production environment.",
						Command:     "baseten model environment metrics --model-id <model-id> --environment production",
					},
					{
						Description: "Summarize request volume and latency over the last hour.",
						Command:     "baseten model environment metrics --model-id <model-id> --environment production --mode summary --since 1h --metric baseten_inference_requests_total --metric baseten_end_to_end_response_time_seconds",
					},
					{
						Description: "Plot a series over the last 6 hours.",
						Command:     "baseten model environment metrics --model-id <model-id> --environment production --mode series --since 6h",
					},
				},
				JQExample: CommandExample{
					Description: "Print the metric names returned.",
					Command:     "baseten model environment metrics --model-id <model-id> --environment production --jq '.metric_descriptors[].name'",
				},
			},
		},
		{
			Name:    "update-autoscaling",
			Summary: "Update autoscaling settings for an environment",
			Description: "Update an environment's autoscaling settings, which apply to whichever " +
				"deployment currently serves it.\n\n" +
				"Only the flags you pass are changed; every other setting is left alone.\n\n" +
				"Run 'baseten model environment describe' to see the current values.",
			Flags: ModelEnvironmentUpdateAutoscalingFlags{},
			Output: &CommandOutput[managementapi.UpdateEnvironmentResponse]{
				TextDescription: "On success, prints \"Updated autoscaling settings for environment <name>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Raise the production environment's replica bounds.",
						Command:     "baseten model environment update-autoscaling --model-id <model-id> --environment production --min-replica 2 --max-replica 10",
					},
				},
				JQExample: CommandExample{
					Description: "Print the environment's resulting minimum replica count.",
					Command:     "baseten model environment update-autoscaling --model-id <model-id> --environment production --min-replica 2 --jq '.environment.autoscaling_settings.min_replica'",
				},
			},
		},
		{
			Name:    "update-promotion",
			Summary: "Update promotion settings for an environment",
			Description: "Update how deployments are promoted into an environment: whether to " +
				"redeploy, whether to roll out gradually and with what rolling deploy " +
				"config, what to do with the outgoing deployment, and whether to ramp up " +
				"traffic.\n\n" +
				"Only the flags you pass are changed; every other setting is left alone.\n\n" +
				"Run 'baseten model environment describe' to see the current values.",
			Flags: ModelEnvironmentUpdatePromotionFlags{},
			Output: &CommandOutput[managementapi.UpdateEnvironmentResponse]{
				TextDescription: "On success, prints \"Updated promotion settings for environment <name>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Turn on rolling deploys and scale the outgoing deployment to zero.",
						Command:     "baseten model environment update-promotion --model-id <model-id> --environment production --rolling-deploy true --promotion-cleanup-strategy scale-to-zero",
					},
					{
						Description: "Slow the rollout by lowering the surge percentage.",
						Command:     "baseten model environment update-promotion --model-id <model-id> --environment production --max-surge-percent 10",
					},
				},
				JQExample: CommandExample{
					Description: "Print the resulting cleanup strategy.",
					Command:     "baseten model environment update-promotion --model-id <model-id> --environment production --rolling-deploy true --jq '.environment.promotion_settings.promotion_cleanup_strategy'",
				},
			},
		},
		{
			Name:    "update-request-backpressure",
			Summary: "Update request backpressure settings for an environment",
			Description: "Set the policy applied when the environment's request queue is full. " +
				"Pass --policy null to clear an existing policy and fall back to the default.\n\n" +
				"Run 'baseten model environment describe' to see the current values.",
			Flags: ModelEnvironmentUpdateRequestBackpressureFlags{},
			Output: &CommandOutput[managementapi.UpdateEnvironmentResponse]{
				TextDescription: "The resulting policy, or \"none\" when no policy is set.",
				Examples: []CommandExample{
					{
						Description: "Reject requests once the queue is full.",
						Command:     "baseten model environment update-request-backpressure --model-id <model-id> --environment production --policy reject-on-full",
					},
					{
						Description: "Clear the policy.",
						Command:     "baseten model environment update-request-backpressure --model-id <model-id> --environment production --policy null",
					},
				},
				JQExample: CommandExample{
					Description: "Print the resulting policy.",
					Command:     "baseten model environment update-request-backpressure --model-id <model-id> --environment production --policy reject-on-full --jq '.environment.request_backpressure_settings.policy'",
				},
			},
		},
		commandModelEnvironmentAutoscalingSchedule,
	},
}

// ModelEnvironmentUpdateAutoscalingFlags configures
// `baseten model environment update-autoscaling`.
type ModelEnvironmentUpdateAutoscalingFlags struct {
	CommandFlags
	ModelEnvironmentFlags
	AutoscalingSettingsFlags
}

// ModelEnvironmentUpdateRequestBackpressureFlags configures
// `baseten model environment update-request-backpressure`.
type ModelEnvironmentUpdateRequestBackpressureFlags struct {
	CommandFlags
	ModelEnvironmentFlags
	RequestBackpressureFlags
}

// ModelEnvironmentUpdatePromotionFlags configures
// `baseten model environment update-promotion`. The rolling deploy config is
// flattened into these flags; its nesting in the API exists only to group the
// fields.
type ModelEnvironmentUpdatePromotionFlags struct {
	CommandFlags
	ModelEnvironmentFlags

	RedeployOnPromotion      OptionalFlag[bool]   `flag:"redeploy-on-promotion" desc:"Whether to deploy on all promotions. Enabling this allows model code to safely handle environment-specific logic: when a deployment is promoted, a new deployment is created with a copy of the image."`
	RollingDeploy            OptionalFlag[bool]   `flag:"rolling-deploy" desc:"Whether the environment should rely on rolling deploy orchestration."`
	PromotionCleanupStrategy OptionalFlag[string] `flag:"promotion-cleanup-strategy" desc:"The cleanup strategy to use after a promotion completes." enum:"keep,scale-to-zero,deactivate"`
	RampUpWhilePromoting     OptionalFlag[bool]   `flag:"ramp-up-while-promoting" desc:"Whether to ramp up traffic while promoting."`
	RampUpDurationSeconds    OptionalFlag[int]    `flag:"ramp-up-duration-seconds" desc:"Duration of the ramp up, in seconds."`

	RollingDeployStrategy    OptionalFlag[string] `flag:"rolling-deploy-strategy" desc:"The rolling deploy strategy to use for promotions." enum:"replica"`
	MaxSurgePercent          OptionalFlag[int]    `flag:"max-surge-percent" desc:"The maximum surge percentage for rolling deploys."`
	MaxUnavailablePercent    OptionalFlag[int]    `flag:"max-unavailable-percent" desc:"The maximum unavailable percentage for rolling deploys."`
	StabilizationTimeSeconds OptionalFlag[int]    `flag:"stabilization-time-seconds" desc:"The stabilization time for rolling deploys, in seconds."`
	ReplicaOverheadPercent   OptionalFlag[int]    `flag:"replica-overhead-percent" desc:"The replica overhead percentage for rolling deploys."`
}

// ModelEnvironmentFlags identifies an environment of a model by name.
// Embedded by commands that act on a specific environment.
type ModelEnvironmentFlags struct {
	ModelRefFlags
	Environment string `flag:"environment" desc:"Name of the environment (e.g. production)." required:"true"`
}

// ModelEnvironmentListFlags configures `baseten model environment list`.
type ModelEnvironmentListFlags struct {
	CommandFlags
	ModelRefFlags
}

// ModelEnvironmentDescribeFlags configures `baseten model environment describe`.
type ModelEnvironmentDescribeFlags struct {
	CommandFlags
	ModelEnvironmentFlags
}

// ModelEnvironmentActivateFlags configures `baseten model environment activate`.
type ModelEnvironmentActivateFlags struct {
	CommandFlags
	ModelEnvironmentFlags
}

// ModelEnvironmentDeactivateFlags configures `baseten model environment deactivate`.
type ModelEnvironmentDeactivateFlags struct {
	CommandFlags
	ModelEnvironmentFlags

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// ModelEnvironmentLogsFlags configures `baseten model environment logs`.
type ModelEnvironmentLogsFlags struct {
	CommandFlags
	ModelEnvironmentFlags
	LogFlags
}

// ModelEnvironmentMetricsFlags configures `baseten model environment metrics`.
type ModelEnvironmentMetricsFlags struct {
	CommandFlags
	ModelEnvironmentFlags
	MetricsFlags
}
