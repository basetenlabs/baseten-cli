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
	},
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

	NoCache bool   `flag:"no-cache" desc:"Force a full rebuild without using cached layers."`
	Labels  string `flag:"labels" desc:"User-provided labels for the deployment as a JSON object, e.g. '{\"team\":\"ml\",\"priority\":1}'."`

	Tail bool `flag:"tail" desc:"Stream build and runtime logs after pushing. (not yet implemented)"`
	Wait bool `flag:"wait" desc:"Block until the deployment is active. (not yet implemented)"`

	Watch          bool `flag:"watch" desc:"Watch the model directory and push on change. (not yet implemented)"`
	WatchHotReload bool `flag:"watch-hot-reload" desc:"Hot-reload the running container on watched changes. (not yet implemented)"`
	WatchKeepalive bool `flag:"watch-keepalive" desc:"Keep the watcher alive after the deployment exits. (not yet implemented)"`

	DeployTimeout string `flag:"deploy-timeout" desc:"Deployment timeout as a Go duration (e.g. 30m, 1h); allowed range 10m to 24h."`

	OverrideName                         string `flag:"override-name" desc:"Override the model_name from config.yaml for this push only. The on-disk config.yaml is not modified."`
	OverrideEnvInstanceType              bool   `flag:"override-env-instance-type" desc:"Use this deployment's instance type instead of preserving the target environment's. Only meaningful when an environment is targeted."`
	PreservePreviousProductionDeployment bool   `flag:"preserve-previous-production-deployment" desc:"Keep the previous production deployment running after promotion. Only meaningful when promoting to production."`

	DisableArchiveDownload bool `flag:"disable-archive-download" desc:"Disable archive download for the new model. Only valid for new models."`
}

// ModelListFlags configures `baseten model list`.
type ModelListFlags struct {
	CommandFlags
}

// ModelFetchFlags configures `baseten model fetch`.
type ModelFetchFlags struct {
	CommandFlags

	ModelID string `flag:"model-id" desc:"ID of the model to fetch." required:"true"`
	TeamID  string `flag:"team-id" desc:"Team the model belongs to."`
}
