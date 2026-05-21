package cmd

import "time"

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
		},
		{
			Name:    "config",
			Summary: "Fetch a deployment's config",
			Description: "Fetch the config of a deployed model.\n\n" +
				"By default prints the original config.yaml. Use --output json to emit " +
				"the full response {config, raw_config} as JSON.",
			Flags: ModelDeploymentConfigFlags{},
		},
		{
			Name:    "deactivate",
			Summary: "Deactivate a deployment",
			Description: "Deactivate a model deployment.\n\n" +
				"Prompts for yes/no confirmation. Pass --yes to skip the prompt. When " +
				"stdin is not a terminal, --yes is required.",
			Flags: ModelDeploymentDeactivateFlags{},
		},
		{
			Name:    "download",
			Summary: "Download the Truss source for a deployment",
			Description: "Download the Truss source for a model deployment as an uncompressed tar.\n\n" +
				"Exactly one of --out-file or --out-dir is required. --out-file writes the " +
				"raw tar bytes; --out-dir extracts the tar into the directory. Use " +
				"--overwrite to replace an existing file or write into a non-empty directory.",
			Flags: ModelDeploymentDownloadFlags{},
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
		},
		{
			Name:        "fetch",
			Summary:     "Fetch a deployment",
			Description: "Fetch a model deployment by ID.",
			Flags:       ModelDeploymentFetchFlags{},
		},
		{
			Name:        "list",
			Summary:     "List deployments for a model",
			Description: "List all deployments of a model.",
			Flags:       ModelDeploymentListFlags{},
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

// ModelDeploymentFetchFlags configures `baseten model deployment fetch`.
type ModelDeploymentFetchFlags struct {
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

	Tail bool `flag:"tail" desc:"Stream new logs as they arrive until the deployment leaves a runnable state or you interrupt with Ctrl-C. Cannot be combined with --start, --end, or --since. For machine-readable streaming, prefer --output jsonl over --output json."`

	Start time.Time     `flag:"start" desc:"Start of the log time range. Accepts ISO 8601 (e.g. '2026-05-14', '2026-05-14T12:00:00', '2026-05-14T12:00:00Z'). Values without a timezone designator are interpreted in the local timezone. If omitted but --end is given, defaults to 7 days before --end. Window must be at most 7 days."`
	End   time.Time     `flag:"end" desc:"End of the log time range. Accepts ISO 8601; values without a timezone designator are interpreted in the local timezone. If omitted but --start is given, defaults to now. Window must be at most 7 days."`
	Since time.Duration `flag:"since" desc:"Shortcut for fetching logs from a relative time ago until now. Accepts a Go duration (e.g. '30m', '1h30m') or '<N>d' (e.g. '3d'). Maximum '7d'. Mutually exclusive with --start and --end."`
}
