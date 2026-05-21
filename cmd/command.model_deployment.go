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
	},
}

// ModelDeploymentIDFlags identifies a deployment of a model. Embedded by
// commands that act on a specific deployment.
type ModelDeploymentIDFlags struct {
	ModelRefFlags
	DeploymentID string `flag:"deployment-id" desc:"ID of the deployment." required:"true"`
}

// ModelDeploymentLogsFlags configures `baseten model deployment logs`.
type ModelDeploymentLogsFlags struct {
	CommandFlags
	ModelDeploymentIDFlags

	Tail bool `flag:"tail" desc:"Stream new logs as they arrive until the deployment leaves a runnable state or you interrupt with Ctrl-C. Cannot be combined with --start, --end, or --since. For machine-readable streaming, prefer --output jsonl over --output json."`

	Start time.Time     `flag:"start" desc:"Start of the log time range. Accepts ISO 8601 (e.g. '2026-05-14', '2026-05-14T12:00:00', '2026-05-14T12:00:00Z'). Values without a timezone designator are interpreted in the local timezone. If omitted but --end is given, defaults to 7 days before --end. Window must be at most 7 days."`
	End   time.Time     `flag:"end" desc:"End of the log time range. Accepts ISO 8601; values without a timezone designator are interpreted in the local timezone. If omitted but --start is given, defaults to now. Window must be at most 7 days."`
	Since time.Duration `flag:"since" desc:"Shortcut for fetching logs from a relative time ago until now. Accepts a Go duration (e.g. '30m', '1h30m') or '<N>d' (e.g. '3d'). Maximum '7d'. Mutually exclusive with --start and --end."`
}
