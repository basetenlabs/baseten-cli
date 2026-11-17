package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

var commandModelEnvironmentReplica = Command{
	Name:    "replica",
	Summary: "Manage replicas of an environment",
	Children: []Command{
		modelReplicaListCommand("environment"),
		modelReplicaDescribeCommand("environment"),
		{
			Name: "terminate", Summary: "Terminate a specific replica",
			Description: "Terminate a replica in the environment's current deployment. " +
				"Prompts for confirmation; use --yes to skip the prompt.",
			Flags: ModelEnvironmentReplicaTerminateFlags{},
			Output: &CommandOutput[managementapi.TerminateReplicaResponse]{
				TextDescription: "Reports the result on stderr.",
				Examples: []CommandExample{{Description: "Terminate a replica.", CommandLines: []string{
					"baseten model environment replica terminate --model-id <model-id>",
					"--env-name production --replica-id <replica-id> --yes",
				}}},
				JQExample: CommandExample{Description: "Print the success flag.", CommandLines: []string{
					"baseten model environment replica terminate --model-id <model-id>",
					"--env-name production --replica-id <replica-id> --yes --jq '.success'",
				}},
			},
		},
	},
}

func modelReplicaListCommand(scope string) Command {
	flags := any(ModelDeploymentReplicaListFlags{})
	ref := "--deployment-id <deployment-id>"
	if scope == "environment" {
		flags = ModelEnvironmentReplicaListFlags{}
		ref = "--env-name production"
	}
	prefix := "baseten model " + scope + " replica list --model-id <model-id>"
	return Command{
		Name: "list", Summary: "List replica lifecycle status",
		Description: "List replicas, including retained terminated replicas. Fetches all pages. " +
			"Repeat --status to filter by lifecycle status.",
		Flags: flags,
		Output: &CommandOutput[ModelReplicaListResult]{
			TextDescription:  "Table of replica lifecycle fields.",
			JSONDescription:  "An object containing all replicas in items. JSONL emits one replica per line.",
			JSONAlternatives: []CommandOutputAlternative{JSONAlternativeFor[managementapi.DeploymentReplica]("output format is jsonl")},
			Examples:         []CommandExample{{Description: "List replicas.", CommandLines: []string{prefix, ref}}},
			JQExample:        CommandExample{Description: "Print replica IDs.", CommandLines: []string{prefix, ref + " --jq '.items[].id'"}},
		},
	}
}

func modelReplicaDescribeCommand(scope string) Command {
	flags := any(ModelDeploymentReplicaDescribeFlags{})
	ref := "--deployment-id <deployment-id>"
	if scope == "environment" {
		flags = ModelEnvironmentReplicaDescribeFlags{}
		ref = "--env-name production"
	}
	prefix := "baseten model " + scope + " replica describe --model-id <model-id>"
	return Command{
		Name: "describe", Summary: "Describe a replica", Flags: flags,
		Output: &CommandOutput[managementapi.DeploymentReplica]{
			TextDescription: "Replica lifecycle fields.",
			Examples:        []CommandExample{{Description: "Describe a replica.", CommandLines: []string{prefix, ref + " --replica-id <replica-id>"}}},
			JQExample:       CommandExample{Description: "Print the status.", CommandLines: []string{prefix, ref + " --replica-id <replica-id>", "--jq '.status'"}},
		},
	}
}

type ModelReplicaListResult struct {
	Items []managementapi.DeploymentReplica `json:"items"`
}

type ModelReplicaStatusFlags struct {
	Status []string `flag:"status" desc:"Include this status. Repeat to select multiple statuses." enum:"starting,active,unhealthy,terminating,terminated,unknown"`
}

type ModelDeploymentReplicaListFlags struct {
	CommandFlags
	ModelDeploymentIDFlags
	ModelReplicaStatusFlags
}

type ModelDeploymentReplicaDescribeFlags struct {
	CommandFlags
	ModelDeploymentReplicaIDFlags
}

type ModelEnvironmentReplicaFlags struct {
	ModelRefFlags
	EnvName string `flag:"env-name" desc:"Name of the environment." required:"true"`
}

type ModelEnvironmentReplicaListFlags struct {
	CommandFlags
	ModelEnvironmentReplicaFlags
	ModelReplicaStatusFlags
}

type ModelEnvironmentReplicaDescribeFlags struct {
	CommandFlags
	ModelEnvironmentReplicaFlags
	ReplicaID string `flag:"replica-id" desc:"ID of the replica." required:"true"`
}

type ModelEnvironmentReplicaTerminateFlags struct {
	CommandFlags
	ModelEnvironmentReplicaFlags
	ReplicaID string `flag:"replica-id" desc:"ID of the replica." required:"true"`
	Yes       bool   `flag:"yes" desc:"Skip confirmation. Required when stdin is not a terminal."`
}
