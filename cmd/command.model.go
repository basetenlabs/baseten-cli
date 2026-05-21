package cmd

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
		},
		{
			Name:        "list",
			Summary:     "List models",
			Description: "List Baseten models.",
			Flags:       ModelListFlags{},
		},
		{
			Name:        "fetch",
			Summary:     "Fetch a model",
			Description: "Fetch a Baseten model.",
			Flags:       ModelFetchFlags{},
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
		},
		{
			Name:    "delete",
			Summary: "Delete a model",
			Description: "Delete a Baseten model and all of its deployments.\n\n" +
				"Prompts for the model name to confirm the deletion. Pass --yes to " +
				"skip the prompt. When stdin is not a terminal, --yes is required.",
			Flags: ModelDeleteFlags{},
		},
		commandModelDeployment,
	},
}

// ModelRefFlags identifies a model by ID or by name (with optional --team for
// disambiguation across teams in the same org). Embedded by commands that act
// on a specific model.
type ModelRefFlags struct {
	ModelID   string `flag:"model-id" desc:"ID of the model." oneof:"model-ref"`
	ModelName string `flag:"model-name" desc:"Name of the model. Use --team to disambiguate when the same name exists in multiple teams." oneof:"model-ref"`
	Team      string `flag:"team" desc:"Team name or ID. Only valid with --model-name."`
}

// ModelPushFlags configures `baseten model push`.
type ModelPushFlags struct {
	CommandFlags

	Dir string `flag:"dir" desc:"Model directory to push. Defaults to the current directory." default:"."`

	Team string `flag:"team" desc:"Team the model belongs to. Only valid for new models."`

	DryRun bool `flag:"dry-run" desc:"Validate the push and request upload credentials without uploading or creating anything."`

	Promote        bool   `flag:"promote" desc:"Promote the new deployment to the production environment."`
	Environment    string `flag:"environment" desc:"Stable environment to push to. Mutually exclusive with --promote."`
	DeploymentName string `flag:"deployment-name" desc:"Human-readable name for the new deployment."`

	NoBuildCache bool   `flag:"no-build-cache" desc:"Force a full rebuild without using cached layers."`
	Labels       string `flag:"labels" desc:"User-provided labels for the deployment as a JSON object, e.g. '{\"team\":\"ml\",\"priority\":1}'."`

	Tail bool `flag:"tail" desc:"Stream build and runtime logs to stderr after pushing. Logs are always text-formatted; use 'baseten model deployment logs --tail' for structured log streaming."`
	Wait bool `flag:"wait" desc:"Block until the deployment is active. Exits non-zero on a terminal-failure status."`

	Watch          bool `flag:"watch" desc:"Watch the model directory and push on change. (not yet implemented)"`
	WatchHotReload bool `flag:"watch-hot-reload" desc:"Hot-reload the running container on watched changes. (not yet implemented)"`
	WatchKeepalive bool `flag:"watch-keepalive" desc:"Keep the watcher alive after the deployment exits. (not yet implemented)"`

	DeployTimeout string `flag:"deploy-timeout" desc:"Deployment timeout as a Go duration (e.g. 30m, 1h); allowed range 10m to 24h."`

	OverrideName            string `flag:"override-name" desc:"Override the model_name from config.yaml for this push only. The on-disk config.yaml is not modified."`
	OverrideEnvInstanceType bool   `flag:"override-env-instance-type" desc:"Use this deployment's instance type instead of preserving the target environment's. Only meaningful when an environment is targeted."`

	DisableArchiveDownload bool `flag:"disable-archive-download" desc:"Disable archive download for the new model. Only valid for new models."`
}

// ModelListFlags configures `baseten model list`.
type ModelListFlags struct {
	CommandFlags

	Team string `flag:"team" desc:"Team name or ID to scope the listing to. Defaults to all teams the caller can see."`
}

// ModelFetchFlags configures `baseten model fetch`.
type ModelFetchFlags struct {
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

	Environment  string `flag:"environment" desc:"Environment to target (e.g. production, development). Defaults to production. Mutually exclusive with --deployment-id and --regional."`
	DeploymentID string `flag:"deployment-id" desc:"Specific deployment to target. Mutually exclusive with --environment and --regional."`
	Regional     string `flag:"regional" desc:"Regional environment name; routes via the regional hostname. Mutually exclusive with --environment and --deployment-id."`

	Data string `flag:"data" desc:"Inline JSON request body." oneof:"predict-input"`
	File string `flag:"file" desc:"Path to a JSON file containing the request body. Use '-' for stdin." oneof:"predict-input"`

	Websocket bool `flag:"websocket" desc:"Use the WebSocket predict endpoint. Sends the body as one frame, reads one frame back, then closes. Not for multi-message or back-and-forth sessions."`
}
