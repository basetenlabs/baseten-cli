package cmd

import (
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("model environment logs", commandModelEnvironmentLogs)
}

func commandModelEnvironmentLogs(ctx *CommandContext, flags *cmd.ModelEnvironmentLogsFlags) error {
	api, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, api.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}

	fetchLogs := func(q logQuery) (*managementapi.GetLogsResponse, error) {
		return api.API().GetModelsEnvironmentsLogs(ctx, ref.ID, flags.Environment,
			managementapi.GetV1ModelsModelIdEnvironmentsEnvNameLogsParams{
				StartEpochMillis: q.StartEpochMillis,
				EndEpochMillis:   q.EndEpochMillis,
				MinLevel:         q.MinLevel,
				Includes:         q.Includes,
				Excludes:         q.Excludes,
				SearchPattern:    q.SearchPattern,
				Replica:          q.Replica,
				RequestId:        q.RequestId,
			})
	}
	// The tail stop condition tracks the environment's current deployment, so
	// each status poll resolves the environment and reports its current
	// deployment's status.
	fetchStatus := func() (*managementapi.Deployment, error) {
		env, err := api.API().GetModelsEnvironmentsEnvName(ctx, ref.ID, flags.Environment)
		if err != nil {
			return nil, err
		}
		return &env.CurrentDeployment, nil
	}
	return runLogsCommand(ctx, &flags.LogFlags, fetchLogs, fetchStatus)
}
