package cmd

import (
	"fmt"

	"github.com/basetenlabs/baseten-cli/cmd"
)

func init() {
	Register("model deployment replica terminate", commandModelDeploymentReplicaTerminate)
}

func commandModelDeploymentReplicaTerminate(ctx *CommandContext, flags *cmd.ModelDeploymentReplicaTerminateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}

	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Terminate replica %s of deployment %s?", flags.ReplicaID, flags.DeploymentID)); err != nil {
			return err
		}
	}

	resp, err := cl.API().DeleteModelsDeploymentsReplicas(ctx, ref.ID, flags.DeploymentID, flags.ReplicaID)
	if err != nil {
		return fmt.Errorf("terminate replica %s: %w", flags.ReplicaID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Terminated replica %s of deployment %s\n", flags.ReplicaID, flags.DeploymentID)
	return nil
}
