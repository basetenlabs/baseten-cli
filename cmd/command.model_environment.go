package cmd

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
		},
		{
			Name:    "deactivate",
			Summary: "Deactivate the environment's active deployment",
			Description: "Deactivate the deployment associated with an environment.\n\n" +
				"Prompts for yes/no confirmation. Pass --yes to skip the prompt. When " +
				"stdin is not a terminal, --yes is required.",
			Flags: ModelEnvironmentDeactivateFlags{},
		},
		{
			Name:        "fetch",
			Summary:     "Fetch an environment",
			Description: "Fetch a model environment by name.",
			Flags:       ModelEnvironmentFetchFlags{},
		},
		{
			Name:        "list",
			Summary:     "List environments for a model",
			Description: "List all environments of a model.",
			Flags:       ModelEnvironmentListFlags{},
		},
	},
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

// ModelEnvironmentFetchFlags configures `baseten model environment fetch`.
type ModelEnvironmentFetchFlags struct {
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
