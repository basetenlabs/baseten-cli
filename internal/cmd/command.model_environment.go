package cmd

import (
	"fmt"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
)

func init() {
	Register("model environment list", commandModelEnvironmentList)
	Register("model environment describe", commandModelEnvironmentDescribe)
	Register("model environment activate", commandModelEnvironmentActivate)
	Register("model environment deactivate", commandModelEnvironmentDeactivate)
}

func commandModelEnvironmentList(ctx *CommandContext, flags *cmd.ModelEnvironmentListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	resp, err := cl.API().GetModelsEnvironments(ctx, ref.ID)
	if err != nil {
		return fmt.Errorf("list environments for model %s: %w", ref.ID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	if len(resp.Environments) == 0 {
		ctx.LogLine("No environments found.")
		return nil
	}
	rows := make([][]string, 0, len(resp.Environments))
	for _, e := range resp.Environments {
		rows = append(rows, []string{
			e.Name,
			e.CurrentDeployment.Id,
			string(e.CurrentDeployment.Status),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"NAME", "CURRENT DEPLOYMENT", "STATUS"},
		Rows:    rows,
	})
	return nil
}

func commandModelEnvironmentDescribe(ctx *CommandContext, flags *cmd.ModelEnvironmentDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	env, err := cl.API().GetModelsEnvironmentsEnvName(ctx, ref.ID, flags.Environment)
	if err != nil {
		return fmt.Errorf("describe environment %s: %w", flags.Environment, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(env)
		return nil
	}
	ctx.Outputf("Name:                %s\n", env.Name)
	ctx.Outputf("Model:               %s\n", env.ModelId)
	ctx.Outputf("Current Deployment:  %s\n", env.CurrentDeployment.Id)
	ctx.Outputf("Status:              %s\n", env.CurrentDeployment.Status)
	if env.CandidateDeployment != nil {
		ctx.Outputf("Candidate Deployment: %s\n", env.CandidateDeployment.Id)
	}
	ctx.Outputf("Created:             %s\n", env.CreatedAt.UTC().Format(time.RFC3339))
	return nil
}

func commandModelEnvironmentActivate(ctx *CommandContext, flags *cmd.ModelEnvironmentActivateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	resp, err := cl.API().PostModelsEnvironmentsActivate(ctx, ref.ID, flags.Environment)
	if err != nil {
		return fmt.Errorf("activate environment %s: %w", flags.Environment, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Activated environment %s\n", flags.Environment)
	return nil
}

func commandModelEnvironmentDeactivate(ctx *CommandContext, flags *cmd.ModelEnvironmentDeactivateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}

	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Deactivate environment %s?", flags.Environment)); err != nil {
			return err
		}
	}

	resp, err := cl.API().PostModelsEnvironmentsDeactivate(ctx, ref.ID, flags.Environment)
	if err != nil {
		return fmt.Errorf("deactivate environment %s: %w", flags.Environment, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Deactivated environment %s\n", flags.Environment)
	return nil
}
