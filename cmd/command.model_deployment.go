package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// commandModelDeployment groups the `baseten model deployment` subcommands.
var commandModelDeployment = Command{
	Name:    "deployment",
	Summary: "Manage deployments of a model",
	Children: []Command{
		{
			Name:        "activate",
			Summary:     "Activate a deployment",
			Description: "Activate a model deployment.",
			Flags:       ModelDeploymentActivateFlags{},
			Output: &CommandOutput[managementapi.ActivateResponse]{
				TextDescription: "On success, prints \"Activated deployment <id>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Activate a deployment.",
						Command:     "baseten model deployment activate --model-id <model-id> --deployment-id <deployment-id>",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the success flag.",
					Command:     "baseten model deployment activate --model-id <model-id> --deployment-id <deployment-id> --jq '.success'",
				},
			},
		},
		{
			Name:    "config",
			Summary: "Fetch a deployment's config",
			Description: "Fetch the config of a deployed model.\n\n" +
				"By default prints the original config.yaml. Use --output json to emit " +
				"the full response {config, raw_config} as JSON.",
			Flags: ModelDeploymentConfigFlags{},
			Output: &CommandOutput[managementapi.DeploymentConfigResponse]{
				TextDescription: "The original config.yaml text (preserving comments and ordering) " +
					"when available, otherwise the parsed config marshaled as YAML.",
				JSONDescription: "The full {config, raw_config} envelope. raw_config is the " +
					"original config.yaml text; config is the parsed shape.",
				Examples: []CommandExample{
					{
						Description: "Print the deployment's config.yaml.",
						Command:     "baseten model deployment config --model-id <model-id> --deployment-id <deployment-id>",
					},
				},
				JQExample: CommandExample{
					Description: "Extract the parsed model_name field.",
					Command:     "baseten model deployment config --model-id <model-id> --deployment-id <deployment-id> --jq '.config.model_name'",
				},
			},
		},
		{
			Name:    "deactivate",
			Summary: "Deactivate a deployment",
			Description: "Deactivate a model deployment.\n\n" +
				"Prompts for yes/no confirmation. Pass --yes to skip the prompt. When " +
				"stdin is not a terminal, --yes is required.",
			Flags: ModelDeploymentDeactivateFlags{},
			Output: &CommandOutput[managementapi.DeactivateResponse]{
				TextDescription: "On success, prints \"Deactivated deployment <id>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Deactivate a deployment without the confirmation prompt.",
						Command:     "baseten model deployment deactivate --model-id <model-id> --deployment-id <deployment-id> --yes",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the success flag.",
					Command:     "baseten model deployment deactivate --model-id <model-id> --deployment-id <deployment-id> --yes --jq '.success'",
				},
			},
		},
		{
			Name:    "download",
			Summary: "Download the Truss source for a deployment",
			Description: "Download the Truss source for a model deployment as an uncompressed tar.\n\n" +
				"Exactly one of --out-file or --out-dir is required. --out-file writes the " +
				"raw tar bytes; --out-dir extracts the tar into the directory. Use " +
				"--overwrite to replace an existing file or write into a non-empty directory.",
			Flags: ModelDeploymentDownloadFlags{},
			Output: &CommandOutput[ModelDeploymentDownloadResult]{
				TextDescription: "Writes the Truss to disk; prints progress and the final destination " +
					"path to stderr; no stdout output.",
				JSONDescription: "On success, stdout is a JSON object with either out_file or out_dir " +
					"set to the path written.",
				Examples: []CommandExample{
					{
						Description: "Save the Truss as a tar file.",
						Command:     "baseten model deployment download --model-id <model-id> --deployment-id <deployment-id> --out-file truss.tar",
					},
					{
						Description: "Extract the Truss into a directory.",
						Command:     "baseten model deployment download --model-id <model-id> --deployment-id <deployment-id> --out-dir ./truss",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the destination path.",
					Command:     "baseten model deployment download --model-id <model-id> --deployment-id <deployment-id> --out-file truss.tar --jq '.out_file'",
				},
			},
		},
		{
			Name:    "promote",
			Summary: "Promote a deployment to an environment",
			Description: "Promote a model deployment to an environment.\n\n" +
				"Defaults to the production environment. Cleanup of the previous deployment " +
				"is controlled by the target environment's promotion cleanup strategy.\n\n" +
				"Prompts for yes/no confirmation. Pass --yes to skip the prompt. When " +
				"stdin is not a terminal, --yes is required.",
			Flags: ModelDeploymentPromoteFlags{},
			Output: &CommandOutput[managementapi.Deployment]{
				TextDescription: "On success, prints \"Promoted deployment <id> to environment <env>\" " +
					"to stderr; no stdout output.",
				JSONDescription: "Under --output json, the promoted deployment object.",
				Examples: []CommandExample{
					{
						Description: "Promote a deployment to production without the confirmation prompt.",
						Command:     "baseten model deployment promote --model-id <model-id> --deployment-id <deployment-id> --yes",
					},
					{
						Description: "Promote to a non-production environment using the deployment's own instance type.",
						Command:     "baseten model deployment promote --model-id <model-id> --deployment-id <deployment-id> --environment staging --override-env-instance-type --yes",
					},
				},
				JQExample: CommandExample{
					Description: "Print the promoted deployment's status.",
					Command:     "baseten model deployment promote --model-id <model-id> --deployment-id <deployment-id> --yes --jq '.status'",
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a single deployment",
			Description: "Delete a single model deployment.\n\n" +
				"Deployments associated with an environment (e.g. production, development) " +
				"and the only deployment of a model cannot be deleted server-side.\n\n" +
				"Prompts for yes/no confirmation. Pass --yes to skip the prompt. When " +
				"stdin is not a terminal, --yes is required.",
			Flags: ModelDeploymentDeleteFlags{},
			Output: &CommandOutput[managementapi.DeploymentTombstone]{
				TextDescription: "On success, prints \"Deleted deployment <id>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Delete a deployment without the confirmation prompt.",
						Command:     "baseten model deployment delete --model-id <model-id> --deployment-id <deployment-id> --yes",
					},
				},
				JQExample: CommandExample{
					Description: "Print the deleted deployment's ID.",
					Command:     "baseten model deployment delete --model-id <model-id> --deployment-id <deployment-id> --yes --jq '.id'",
				},
			},
		},
		{
			Name:        "describe",
			Summary:     "Describe a deployment",
			Description: "Describe a model deployment by ID.",
			Flags:       ModelDeploymentDescribeFlags{},
			Output: &CommandOutput[managementapi.Deployment]{
				TextDescription: "Field-per-line summary: ID, Name, Model, Environment (optional), " +
					"Status, Instance (optional), Replicas, Created.",
				Examples: []CommandExample{
					{
						Description: "Describe a deployment by ID.",
						Command:     "baseten model deployment describe --model-id <model-id> --deployment-id <deployment-id>",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the deployment status.",
					Command:     "baseten model deployment describe --model-id <model-id> --deployment-id <deployment-id> --jq '.status'",
				},
			},
		},
		{
			Name:        "list",
			Summary:     "List deployments for a model",
			Description: "List all deployments of a model.",
			Flags:       ModelDeploymentListFlags{},
			Output: &CommandOutput[managementapi.Deployments]{
				TextDescription: "Table with columns: ID, NAME, ENVIRONMENT, STATUS, INSTANCE, " +
					"REPLICAS, CREATED. When no deployments exist, prints \"No deployments found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List all deployments of a model.",
						Command:     "baseten model deployment list --model-id <model-id>",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the deployment IDs.",
					Command:     "baseten model deployment list --model-id <model-id> --jq '.deployments[].id'",
				},
			},
		},
		{
			Name:    "logs",
			Summary: "Stream or tail logs for a deployment",
			Description: "Fetch logs for a model deployment.\n\n" +
				"By default returns logs from the server's default recent window. " +
				"Use --start/--end or --since to scope the window (max 7 days). " +
				"Use --tail to stream live logs until the deployment leaves a " +
				"runnable state or you interrupt with Ctrl-C.\n\n" +
				"For machine-readable streaming, prefer --output jsonl over --output json.",
			Flags: ModelDeploymentLogsFlags{},
			Output: &CommandOutput[managementapi.Log]{
				JSONArrayStreamed: true,
				TextDescription:   "One line per log record: \"[YYYY-MM-DD HH:MM:SS]: (replica) message\".",
				Examples: []CommandExample{
					{
						Description: "Print logs for a deployment over the last hour.",
						Command:     "baseten model deployment logs --model-id <model-id> --deployment-id <deployment-id> --since 1h",
					},
					{
						Description: "Tail live logs until the deployment leaves a runnable state.",
						Command:     "baseten model deployment logs --model-id <model-id> --deployment-id <deployment-id> --tail",
					},
				},
				JQExample: CommandExample{
					Description: "Stream just the log messages as a JSONL stream.",
					Command:     "baseten model deployment logs --model-id <model-id> --deployment-id <deployment-id> --output jsonl --jq '.message'",
				},
			},
		},
		{
			Name:    "metrics",
			Summary: "Fetch metrics for a deployment",
			Description: "Fetch metrics for a model deployment.\n\n" +
				"--mode selects what you get back: a current snapshot, a windowed " +
				"summary, or a series; see its flag help for details. Scope the window " +
				"with --start/--end or --since (max 7 days), which only apply to " +
				"summary and series.",
			Flags: ModelDeploymentMetricsFlags{},
			Output: &CommandOutput[managementapi.GetDeploymentMetricsResponse]{
				TextDescription: "For current/summary, a table with columns METRIC, one column per " +
					"label dimension (e.g. QUANTILE, STAT), and VALUE; summary COUNTER values show " +
					"\"total (rate/s)\". For series, a sparkline per metric label set with its " +
					"min-max range and end value, or a per-step table under --no-chart.",
				JSONDescription: "The metrics response: metric_descriptors, index-mapped metric_values, " +
					"the resolved mode, and the returned window.",
				Examples: []CommandExample{
					{
						Description: "Show a current snapshot of the default metrics.",
						Command:     "baseten model deployment metrics --model-name <model-name> --deployment-id <deployment-id>",
					},
					{
						Description: "Summarize request volume and latency over the last hour.",
						Command:     "baseten model deployment metrics --model-id <model-id> --deployment-id <deployment-id> --mode summary --since 1h --metric baseten_inference_requests_total --metric baseten_end_to_end_response_time_seconds",
					},
					{
						Description: "Plot a series over the last 6 hours.",
						Command:     "baseten model deployment metrics --model-id <model-id> --deployment-id <deployment-id> --mode series --since 6h",
					},
				},
				JQExample: CommandExample{
					Description: "Print the metric names returned.",
					Command:     "baseten model deployment metrics --model-id <model-id> --deployment-id <deployment-id> --jq '.metric_descriptors[].name'",
				},
			},
		},
		commandModelDeploymentReplica,
	},
}

// ModelDeploymentIDFlags identifies a deployment of a model. Embedded by
// commands that act on a specific deployment.
type ModelDeploymentIDFlags struct {
	ModelRefFlags
	DeploymentID string `flag:"deployment-id" desc:"ID of the deployment." required:"true"`
}

// ModelDeploymentListFlags configures `baseten model deployment list`.
type ModelDeploymentListFlags struct {
	CommandFlags
	ModelRefFlags
}

// ModelDeploymentDescribeFlags configures `baseten model deployment describe`.
type ModelDeploymentDescribeFlags struct {
	CommandFlags
	ModelDeploymentIDFlags
}

// ModelDeploymentConfigFlags configures `baseten model deployment config`.
type ModelDeploymentConfigFlags struct {
	CommandFlags
	ModelDeploymentIDFlags
}

// ModelDeploymentActivateFlags configures `baseten model deployment activate`.
type ModelDeploymentActivateFlags struct {
	CommandFlags
	ModelDeploymentIDFlags
}

// ModelDeploymentDeactivateFlags configures `baseten model deployment deactivate`.
type ModelDeploymentDeactivateFlags struct {
	CommandFlags
	ModelDeploymentIDFlags

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// ModelDeploymentDeleteFlags configures `baseten model deployment delete`.
type ModelDeploymentDeleteFlags struct {
	CommandFlags
	ModelDeploymentIDFlags

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// ModelDeploymentDownloadFlags configures `baseten model deployment download`.
type ModelDeploymentDownloadFlags struct {
	CommandFlags
	ModelDeploymentIDFlags

	OutFile   string `flag:"out-file" desc:"Save the Truss as an uncompressed tar file at this path." oneof:"download-out"`
	OutDir    string `flag:"out-dir" desc:"Extract the Truss tar into this directory." oneof:"download-out"`
	Overwrite bool   `flag:"overwrite" desc:"Allow overwriting an existing file or non-empty directory."`
}

// ModelDeploymentDownloadResult is the JSON output of `baseten model deployment
// download`. Exactly one of OutFile or OutDir is set, matching whichever flag
// the caller passed.
type ModelDeploymentDownloadResult struct {
	OutFile string `json:"out_file,omitempty"`
	OutDir  string `json:"out_dir,omitempty"`
}

// ModelDeploymentPromoteFlags configures `baseten model deployment promote`.
type ModelDeploymentPromoteFlags struct {
	CommandFlags
	ModelDeploymentIDFlags

	Environment             string `flag:"environment" desc:"Target environment name. Defaults to production." default:"production"`
	OverrideEnvInstanceType bool   `flag:"override-env-instance-type" desc:"Use this deployment's instance type instead of preserving the target environment's."`

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// ModelDeploymentLogsFlags configures `baseten model deployment logs`.
type ModelDeploymentLogsFlags struct {
	CommandFlags
	ModelDeploymentIDFlags

	Tail bool `flag:"tail" desc:"Stream new logs as they arrive until the deployment leaves a runnable state or you interrupt with Ctrl-C. Cannot be combined with the time-range or filter flags. For machine-readable streaming, prefer --output jsonl over --output json."`

	Start time.Time     `flag:"start" desc:"Start of the log time range. Accepts ISO 8601 (e.g. '2026-05-14', '2026-05-14T12:00:00', '2026-05-14T12:00:00Z'). Values without a timezone designator are interpreted in the local timezone. If omitted, the server defaults the start to 30 minutes before the end. Window must be at most 7 days."`
	End   time.Time     `flag:"end" desc:"End of the log time range. Accepts ISO 8601; values without a timezone designator are interpreted in the local timezone. If omitted, the server defaults the end to now. Window must be at most 7 days."`
	Since time.Duration `flag:"since" desc:"Shortcut for fetching logs from a relative time ago until now. Accepts a Go duration (e.g. '30m', '1h30m') or '<N>d' (e.g. '3d'). Maximum '7d'. Mutually exclusive with --start and --end."`

	MinLevel      string   `flag:"min-level" desc:"Only return logs at or above this severity level." enum:"debug,info,warning,error"`
	Includes      []string `flag:"includes" desc:"Case-sensitive substring that must appear in the log message. May be repeated; all must match."`
	Excludes      []string `flag:"excludes" desc:"Case-sensitive substring; lines containing it are dropped. May be repeated."`
	SearchPattern string   `flag:"search-pattern" desc:"RE2 regular expression matched against the log message. Prefer --includes and --excludes for plain substring matches."`
	Replica       string   `flag:"replica" desc:"Only return logs emitted by this replica (5-char short ID)."`
	RequestID     string   `flag:"request-id" desc:"Only return logs tagged with this inference request ID."`
}

// ModelDeploymentMetricsFlags configures `baseten model deployment metrics`.
type ModelDeploymentMetricsFlags struct {
	CommandFlags
	ModelDeploymentIDFlags

	Mode string `flag:"mode" desc:"Aggregation mode. 'current' returns an instantaneous snapshot at now; 'summary' aggregates the whole window into one value per metric; 'series' returns evenly-spaced points across the window. --start/--end/--since are only meaningful for summary and series." enum:"current,summary,series" default:"current"`

	Start time.Time     `flag:"start" desc:"Start of the metrics time range. Accepts ISO 8601 (e.g. '2026-05-14', '2026-05-14T12:00:00', '2026-05-14T12:00:00Z'). Values without a timezone designator are interpreted in the local timezone. If omitted, the server defaults the start to one hour before the end. Window must be at most 7 days."`
	End   time.Time     `flag:"end" desc:"End of the metrics time range. Accepts ISO 8601; values without a timezone designator are interpreted in the local timezone. If omitted, the server defaults the end to now. Window must be at most 7 days."`
	Since time.Duration `flag:"since" desc:"Shortcut for a window from a relative time ago until now. Accepts a Go duration (e.g. '30m', '1h30m') or '<N>d' (e.g. '3d'). Maximum '7d'. Mutually exclusive with --start and --end."`

	Metric []string `flag:"metric" desc:"Name of a metric to return; see https://docs.baseten.co/observability/export-metrics/supported-metrics for the available names. May be repeated. When omitted, a default set is returned."`

	NoChart bool `flag:"no-chart" desc:"For --mode series, emit a per-step table instead of sparklines."`
}
