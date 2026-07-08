package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

var commandModel = Command{
	Name:    "model",
	Summary: "Manage Baseten models",
	Description: "Create, list, and push Baseten models.\n\n" +
		"Authentication is via 'baseten auth login' or the BASETEN_API_KEY environment variable.",
	Children: []Command{
		{
			Name:      "push",
			ArgsUsage: "[--dir DIR]",
			Summary:   "Push a model directory as a new model or deployment",
			Description: "Build a model archive, upload it to Baseten, and create either a new " +
				"model or a new deployment of an existing model.\n\n" +
				"The current directory is used by default; pass --dir to push a model " +
				"directory at another path.\n\n" +
				"The model is identified by the `model_name` field in config.yaml. " +
				"Use --override-name to override that for this push only.",
			Flags: ModelPushFlags{},
			Output: &CommandOutput[ModelPushResult]{
				TextDescription: "Narrative summary on stdout: success banner, deployment status, " +
					"log/predict URLs and example next-step commands. Under --output json the " +
					"narrative is redirected to stderr so stdout stays a clean JSON document.",
				JSONDescription: "Under --dry-run no upload or deployment happens; the push is " +
					"validated, upload credentials are requested, and stdout is the empty JSON " +
					"object `{}`. Otherwise stdout is the full model+deployment result.",
				Examples: []CommandExample{
					{
						Description: "Push the current directory as a new deployment.",
						Command:     "baseten model push",
					},
					{
						Description: "Push and stream build/runtime logs until the deployment is active.",
						Command:     "baseten model push --tail --wait",
					},
				},
				JQExample: CommandExample{
					Description: "Print the new deployment's predict URL.",
					Command:     "baseten model push --jq '.predict_url'",
				},
			},
		},
		{
			Name:      "watch",
			ArgsUsage: "[--dir DIR]",
			Summary:   "Watch a model directory and live-patch its development deployment",
			Description: "Watch a model directory and patch the model's development deployment " +
				"in place on every change, skipping a full rebuild.\n\n" +
				"The current directory is used by default; pass --dir to watch a model " +
				"directory at another path. The model is identified by the `model_name` " +
				"field in that directory's config.yaml, like 'baseten model push'.\n\n" +
				"The model must already have a development deployment; if it does not, " +
				"run 'baseten model push --develop' (or 'baseten model push --watch') first.\n\n" +
				"Runs until interrupted. Some changes cannot be expressed as a patch " +
				"(removing config.yaml, or any change under the data directory); the " +
				"watcher reports these and you must re-push.",
			Flags: ModelWatchFlags{},
			Output: &CommandOutput[JSONUndefined]{
				JSONOutputUnimportant: true,
				TextDescription: "Streams patch and sync status to stderr as changes are applied. " +
					"Runs until interrupted and produces no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Watch the current directory against its model's development deployment.",
						Command:     "baseten model watch",
					},
					{
						Description: "Watch another directory and hot-reload on model-code changes.",
						Command:     "baseten model watch --dir ./my-model --hot-reload",
					},
				},
			},
		},
		{
			Name:        "list",
			Summary:     "List models",
			Description: "List Baseten models.",
			Flags:       ModelListFlags{},
			Output: &CommandOutput[managementapi.Models]{
				TextDescription: "Table with columns: ID, NAME, TEAM, DEPLOYMENTS, CREATED. " +
					"When no models exist, prints \"No models found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List all models accessible to the caller.",
						Command:     "baseten model list",
					},
					{
						Description: "List only models in a specific team.",
						Command:     "baseten model list --team my-team",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the model IDs.",
					Command:     "baseten model list --jq '.models[].id'",
				},
			},
		},
		{
			Name:        "describe",
			Summary:     "Describe a model",
			Description: "Describe a Baseten model.",
			Flags:       ModelDescribeFlags{},
			Output: &CommandOutput[managementapi.Model]{
				TextDescription: "Field-per-line summary: ID, Name, Team, Deployments, " +
					"Instance, Production, Development, Created. Optional fields are omitted " +
					"when unset.",
				Examples: []CommandExample{
					{
						Description: "Describe a model by ID.",
						Command:     "baseten model describe --model-id <model-id>",
					},
					{
						Description: "Describe a model by name.",
						Command:     "baseten model describe --model-name <name>",
					},
				},
				JQExample: CommandExample{
					Description: "Print the production deployment ID.",
					Command:     "baseten model describe --model-id <model-id> --jq '.production_deployment_id'",
				},
			},
		},
		{
			Name:    "predict",
			Summary: "Run a prediction against a model",
			Description: "POST a JSON request to a model and write the response to stdout.\n\n" +
				"Targets the production environment by default. Use --environment, " +
				"--deployment-id, or --regional to target something else.\n\n" +
				"Streaming responses (Transfer-Encoding: chunked) are passed through " +
				"as they arrive. For machine-readable streaming JSON from OpenAI-compatible " +
				"models, use --output jsonl.",
			Flags: ModelPredictFlags{},
			Output: &CommandOutput[JSONUndefined]{
				TextDescription: "The model's response body, passed through verbatim. May be JSON, " +
					"plain text, or binary, and may stream when the model uses chunked " +
					"transfer encoding or SSE.",
				JSONDescription: "Under --output json, binary frames are base64-encoded under a " +
					"'body' key. Under --output jsonl, each SSE or binary chunk is emitted as " +
					"its own record, one per line.",
				Examples: []CommandExample{
					{
						Description: "Send an inline JSON body.",
						Command:     `baseten model predict --model-id <model-id> --data '{"prompt":"hello"}'`,
					},
					{
						Description: "Send a request body from a file.",
						Command:     "baseten model predict --model-id <model-id> --file request.json",
					},
				},
				JQExample: CommandExample{
					Description: "Extract a field when the model returns JSON.",
					Command:     `baseten model predict --model-id <model-id> --data '{"x":1}' --jq '.result'`,
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a model",
			Description: "Delete a Baseten model and all of its deployments.\n\n" +
				"Prompts for the model name to confirm the deletion. Pass --yes to " +
				"skip the prompt. When stdin is not a terminal, --yes is required.",
			Flags: ModelDeleteFlags{},
			Output: &CommandOutput[managementapi.ModelTombstone]{
				TextDescription: "On success, prints \"Deleted model <name> (<id>)\" to stderr; no " +
					"stdout output.",
				Examples: []CommandExample{
					{
						Description: "Delete by ID without confirmation.",
						Command:     "baseten model delete --model-id <model-id> --yes",
					},
					{
						Description: "Delete by name with interactive confirmation.",
						Command:     "baseten model delete --model-name <name>",
					},
				},
				JQExample: CommandExample{
					Description: "Print the deleted model's ID.",
					Command:     "baseten model delete --model-id <model-id> --yes --jq '.id'",
				},
			},
		},
		commandModelDeployment,
		commandModelEnvironment,
	},
}

// ModelPushResult is the JSON output of `baseten model push` on a successful
// non-dry-run push.
type ModelPushResult struct {
	Model      managementapi.Model      `json:"model"`
	Deployment managementapi.Deployment `json:"deployment"`
	PredictURL string                   `json:"predict_url"`
	LogsURL    string                   `json:"logs_url"`
}

// ModelRefFlags identifies a model by ID or by name (with optional --team for
// disambiguation across teams in the same org). Embedded by commands that act
// on a specific model.
type ModelRefFlags struct {
	ModelID   string `flag:"model-id" desc:"ID of the model." oneof:"model-ref"`
	ModelName string `flag:"model-name" desc:"Name of the model. Use --team to disambiguate when the same name exists in multiple teams." oneof:"model-ref"`
	Team      string `flag:"team" desc:"Team name or ID. Only valid with --model-name."`
}

// LogFlags is the shared log-query flag set for `baseten model deployment logs`
// and `baseten model environment logs`. Both commands accept the same window,
// filter, and tail flags; only the log source differs.
type LogFlags struct {
	Tail bool `flag:"tail" desc:"Stream new logs as they arrive until the deployment leaves a runnable state or you interrupt with Ctrl-C. Cannot be combined with the time-range or filter flags. For machine-readable streaming, prefer --output jsonl over --output json."`

	Start time.Time     `flag:"start" desc:"Start of the log time range. Accepts ISO 8601 (e.g. '2026-05-14', '2026-05-14T12:00:00', '2026-05-14T12:00:00Z'). Values without a timezone designator are interpreted in the local timezone. Default is 30 minutes before the end. Window must be at most 7 days."`
	End   time.Time     `flag:"end" desc:"End of the log time range. Accepts ISO 8601; values without a timezone designator are interpreted in the local timezone. Default is now. Window must be at most 7 days."`
	Since time.Duration `flag:"since" desc:"Shortcut for fetching logs from a relative time ago until now. Accepts a Go duration (e.g. '30m', '1h30m') or '<N>d' (e.g. '3d'). Maximum '7d'. Mutually exclusive with --start and --end."`

	Limit int `flag:"limit" desc:"Maximum number of log lines to return, paging backward from the end of the window. Use 0 for no limit (every log line in the window). Not applicable with --tail." default:"5000"`

	// PageSize is the per-request fetch size while paging. Hidden; exists so
	// tests can force multiple pages without generating a full page of logs.
	PageSize int `flag:"page-size" hidden:"true" desc:"Log lines fetched per backend request while paging." default:"1000"`

	MinLevel      string   `flag:"min-level" desc:"Only return logs at or above this severity level." enum:"debug,info,warning,error"`
	Includes      []string `flag:"includes" desc:"Case-sensitive substring that must appear in the log message. May be repeated; all must match."`
	Excludes      []string `flag:"excludes" desc:"Case-sensitive substring; lines containing it are dropped. May be repeated."`
	SearchPattern string   `flag:"search-pattern" desc:"RE2 regular expression matched against the log message. Prefer --includes and --excludes for plain substring matches."`
	Replica       string   `flag:"replica" desc:"Only return logs emitted by this replica (5-char short ID)."`
	RequestID     string   `flag:"request-id" desc:"Only return logs tagged with this inference request ID."`
}

// ModelPushFlags configures `baseten model push`.
type ModelPushFlags struct {
	CommandFlags

	Dir string `flag:"dir" desc:"Model directory to push. Defaults to the current directory." default:"."`

	Team string `flag:"team" desc:"Team the model belongs to. Only valid for new models."`

	DryRun bool `flag:"dry-run" desc:"Validate the push and request upload credentials without uploading or creating anything."`

	Environment    string `flag:"environment" desc:"Stable environment to push to."`
	DeploymentName string `flag:"deployment-name" desc:"Human-readable name for the new deployment."`

	NoBuildCache bool   `flag:"no-build-cache" desc:"Force a full rebuild without using cached layers."`
	Labels       string `flag:"labels" desc:"User-provided labels for the deployment as a JSON object, e.g. '{\"team\":\"ml\",\"priority\":1}'."`

	Tail bool `flag:"tail" desc:"Stream build and runtime logs to stderr after pushing. Logs are always text-formatted; use 'baseten model deployment logs --tail' for structured log streaming."`
	Wait bool `flag:"wait" desc:"Block until the deployment is active. Exits non-zero on a terminal-failure status."`

	Develop bool `flag:"develop" desc:"Push as a development deployment: the model's single mutable dev slot, created if absent and overwritten in place otherwise. Incompatible with --environment and --deployment-name."`

	Watch            bool `flag:"watch" desc:"After pushing, watch the model directory and live-patch the development deployment on change. Implies --develop."`
	WatchHotReload   bool `flag:"watch-hot-reload" desc:"With --watch, hot-reload the running container when every change is to model code; mixed changes fall back to a cold patch."`
	WatchNoKeepalive bool `flag:"watch-no-keepalive" desc:"With --watch, let the development deployment scale to zero while watching. By default it is kept warm by periodic pings."`

	DeployTimeout string `flag:"deploy-timeout" desc:"Deployment timeout as a Go duration (e.g. 30m, 1h); allowed range 10m to 24h."`

	OverrideName            string `flag:"override-name" desc:"Override the model_name from config.yaml for this push only. The on-disk config.yaml is not modified."`
	OverrideEnvInstanceType bool   `flag:"override-env-instance-type" desc:"Use this deployment's instance type instead of preserving the target environment's. Only meaningful when an environment is targeted."`

	DisableArchiveDownload bool `flag:"disable-archive-download" desc:"Disable archive download for the new model. Only valid for new models."`
}

// ModelWatchFlags configures `baseten model watch`.
type ModelWatchFlags struct {
	CommandFlags

	Dir string `flag:"dir" desc:"Model directory to watch. Defaults to the current directory." default:"."`

	Team string `flag:"team" desc:"Team the model belongs to. Use to disambiguate when the same model_name exists in multiple teams."`

	HotReload   bool `flag:"hot-reload" desc:"Hot-reload the running container when every change is to model code; mixed changes fall back to a cold patch."`
	NoKeepalive bool `flag:"no-keepalive" desc:"Let the development deployment scale to zero while watching. By default it is kept warm by periodic pings."`
}

// ModelListFlags configures `baseten model list`.
type ModelListFlags struct {
	CommandFlags

	Team string `flag:"team" desc:"Team name or ID to scope the listing to. Defaults to all teams the caller can see."`
}

// ModelDescribeFlags configures `baseten model describe`.
type ModelDescribeFlags struct {
	CommandFlags
	ModelRefFlags
}

// ModelDeleteFlags configures `baseten model delete`.
type ModelDeleteFlags struct {
	CommandFlags
	ModelRefFlags

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// ModelPredictFlags configures `baseten model predict`.
type ModelPredictFlags struct {
	CommandFlags
	ModelRefFlags

	Environment    string `flag:"environment" desc:"Environment to target (e.g. production, development). Defaults to production. Mutually exclusive with --deployment-id, --deployment-name, and --regional."`
	DeploymentID   string `flag:"deployment-id" desc:"Specific deployment to target. Mutually exclusive with --environment, --deployment-name, and --regional."`
	DeploymentName string `flag:"deployment-name" desc:"Name of the deployment to target. Mutually exclusive with --environment, --deployment-id, and --regional."`
	Regional       string `flag:"regional" desc:"Regional environment name; routes via the regional hostname. Mutually exclusive with --environment, --deployment-id, and --deployment-name."`

	Data string `flag:"data" desc:"Inline JSON request body." oneof:"predict-input"`
	File string `flag:"file" desc:"Path to a JSON file containing the request body. Use '-' for stdin." oneof:"predict-input"`

	Websocket bool `flag:"websocket" desc:"Use the WebSocket predict endpoint. Sends the body as one frame, reads one frame back, then closes. Not for multi-message or back-and-forth sessions."`
}
