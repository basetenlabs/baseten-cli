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
			Name:        "fetch",
			Summary:     "Fetch an environment",
			Description: "Fetch a model environment by name.",
			Flags:       ModelEnvironmentFetchFlags{},
			Output: &CommandOutput[managementapi.Environment]{
				TextDescription: "Field-per-line summary: Name, Model, Current Deployment, Status, " +
					"Candidate Deployment (optional), Created.",
				Examples: []CommandExample{
					{
						Description: "Fetch the production environment of a model.",
						Command:     "baseten model environment fetch --model-id <model-id> --environment production",
					},
				},
				JQExample: CommandExample{
					Description: "Print the current deployment ID.",
					Command:     "baseten model environment fetch --model-id <model-id> --environment production --jq '.current_deployment.id'",
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
