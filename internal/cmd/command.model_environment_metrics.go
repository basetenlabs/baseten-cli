package cmd

import (
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("model environment metrics", commandModelEnvironmentMetrics)
}

func commandModelEnvironmentMetrics(ctx *CommandContext, flags *cmd.ModelEnvironmentMetricsFlags) error {
	api, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, api.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	fetch := func(q metricsQuery) (*managementapi.GetModelMetricsResponse, error) {
		return api.API().GetModelsEnvironmentsMetrics(ctx, ref.ID, flags.Environment,
			managementapi.GetV1ModelsModelIdEnvironmentsEnvNameMetricsParams{
				Mode:             &q.Mode,
				StartEpochMillis: q.StartEpochMillis,
				EndEpochMillis:   q.EndEpochMillis,
				Metrics:          q.Metrics,
			})
	}
	return runMetricsCommand(ctx, &flags.MetricsFlags, fetch)
}
