package cmd

// commandModelDeploymentReplica groups the `baseten model deployment replica`
// subcommands.
var commandModelDeploymentReplica = Command{
	Name:    "replica",
	Summary: "Manage replicas of a deployment",
	Children: []Command{
		{
			Name:    "terminate",
			Summary: "Terminate a specific replica",
			Description: "Terminate a single replica of a model deployment.\n\n" +
				"Prompts for yes/no confirmation. Pass --yes to skip the prompt. When " +
				"stdin is not a terminal, --yes is required.",
			Flags: ModelDeploymentReplicaTerminateFlags{},
		},
	},
}

// ModelDeploymentReplicaIDFlags identifies a replica of a deployment.
// Embedded by commands that act on a specific replica.
type ModelDeploymentReplicaIDFlags struct {
	ModelDeploymentIDFlags
	ReplicaID string `flag:"replica-id" desc:"ID of the replica." required:"true"`
}

// ModelDeploymentReplicaTerminateFlags configures `baseten model deployment
// replica terminate`.
type ModelDeploymentReplicaTerminateFlags struct {
	CommandFlags
	ModelDeploymentReplicaIDFlags

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}
