package cmd

import (
	"fmt"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("model environment list", commandModelEnvironmentList)
	Register("model environment describe", commandModelEnvironmentDescribe)
	Register("model environment activate", commandModelEnvironmentActivate)
	Register("model environment deactivate", commandModelEnvironmentDeactivate)
	Register("model environment update-autoscaling", commandModelEnvironmentUpdateAutoscaling)
	Register("model environment update-promotion", commandModelEnvironmentUpdatePromotion)
	Register("model environment update-request-backpressure", commandModelEnvironmentUpdateRequestBackpressure)
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
		// The current deployment is null until something is promoted to the
		// environment.
		depID, depStatus := "-", "-"
		if e.CurrentDeployment != nil {
			depID, depStatus = e.CurrentDeployment.Id, string(e.CurrentDeployment.Status)
		}
		rows = append(rows, []string{e.Name, depID, depStatus})
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
	remote, err := ctx.authInfo.Remote()
	if err != nil {
		return err
	}
	ctx.Outputf("Name:                %s\n", env.Name)
	ctx.Outputf("Model:               %s\n", env.ModelId)
	// The current deployment is null until something is promoted to the
	// environment.
	if env.CurrentDeployment != nil {
		ctx.Outputf("Current Deployment:  %s\n", env.CurrentDeployment.Id)
		ctx.Outputf("Status:              %s\n", env.CurrentDeployment.Status)
	}
	if env.CandidateDeployment != nil {
		ctx.Outputf("Candidate Deployment: %s\n", env.CandidateDeployment.Id)
	}
	ctx.Outputf("Invoke URL:          %s\n", hyperlink(ctx.Stdout, remote.EnvironmentPredictURL(env.ModelId, env.Name)))
	if env.CurrentDeployment != nil {
		ctx.Outputf("Logs URL:            %s\n", hyperlink(ctx.Stdout, remote.LogsURL(env.ModelId, env.CurrentDeployment.Id)))
	}
	ctx.Outputf("Created:             %s\n", env.CreatedAt.UTC().Format(time.RFC3339))
	ctx.Outputf("Backpressure:        %s\n", backpressurePolicyText(env.RequestBackpressureSettings.Policy))
	outputAutoscalingSettings(ctx, env.AutoscalingSettings)
	outputPromotionSettings(ctx, env.PromotionSettings)
	if env.AutoscalingSchedules != nil {
		outputAutoscalingSchedules(ctx, env.AutoscalingSchedules)
	}
	return nil
}

// outputAutoscalingSchedules prints an environment's schedules one per line.
// The layout is deliberately not the list command's table: describe is already
// a field-per-line summary, so a nested table would read as a different
// document. The list command remains the way to get ids for update and delete.
//
// A schedule this CLI cannot read is noted on stderr and skipped rather than
// failing the command. The API can add a cadence an older CLI does not know,
// and that should not cost the user the rest of the description.
func outputAutoscalingSchedules(
	ctx *CommandContext, schedules *managementapi.EnvironmentAutoscalingSchedules,
) {
	if len(schedules.Schedules) == 0 {
		return
	}
	timezone := "-"
	if schedules.Timezone != nil {
		timezone = *schedules.Timezone
	}
	ctx.Outputf("Autoscaling Schedules (%s):\n", timezone)
	for _, item := range schedules.Schedules {
		value, err := item.ValueByDiscriminator()
		if err == nil {
			var id, name, cadence string
			var enabled bool
			var settings managementapi.AutoscalingScheduleSettings
			if id, name, cadence, enabled, settings, err = autoscalingScheduleFields(value); err == nil {
				state := "enabled"
				if !enabled {
					state = "disabled"
				}
				ctx.Outputf("  %s (%s): %s %s, replicas %d-%d, %s\n",
					name, id, cadence, autoscalingScheduleWindowText(value),
					settings.MinReplica, settings.MaxReplica, state)
				continue
			}
		}
		ctx.Logf("Skipped an autoscaling schedule this CLI cannot read: %v\n", err)
	}
}

// outputPromotionSettings prints the promotion block for environment describe,
// flattening the rolling deploy config the way update-promotion's flags do.
func outputPromotionSettings(ctx *CommandContext, settings managementapi.PromotionSettings) {
	ctx.Outputf("Promotion:\n")
	ctx.Outputf("  Redeploy On Promotion:   %s\n", describeSettingText(settings.RedeployOnPromotion, "%t"))
	ctx.Outputf("  Rolling Deploy:          %s\n", describeSettingText(settings.RollingDeploy, "%t"))
	ctx.Outputf("  Cleanup Strategy:        %s\n", describeEnumText(settings.PromotionCleanupStrategy))
	ctx.Outputf("  Ramp Up While Promoting: %s\n", describeSettingText(settings.RampUpWhilePromoting, "%t"))
	ctx.Outputf("  Ramp Up Duration:        %s\n", describeSettingText(settings.RampUpDurationSeconds, "%ds"))
	if config := settings.RollingDeployConfig; config != nil {
		ctx.Outputf("  Rolling Deploy Strategy: %s\n", describeEnumText(config.RollingDeployStrategy))
		ctx.Outputf("  Max Surge:               %s\n", describeSettingText(config.MaxSurgePercent, "%d%%"))
		ctx.Outputf("  Max Unavailable:         %s\n", describeSettingText(config.MaxUnavailablePercent, "%d%%"))
		ctx.Outputf("  Stabilization Time:      %s\n", describeSettingText(config.StabilizationTimeSeconds, "%ds"))
		ctx.Outputf("  Replica Overhead:        %s\n", describeSettingText(config.ReplicaOverheadPercent, "%d%%"))
	}
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

func commandModelEnvironmentUpdateAutoscaling(
	ctx *CommandContext, flags *cmd.ModelEnvironmentUpdateAutoscalingFlags,
) error {
	settings := autoscalingSettingsFromFlags(flags.AutoscalingSettingsFlags)
	resp, err := patchModelEnvironment(ctx, flags.ModelEnvironmentFlags,
		managementapi.UpdateEnvironmentRequest{AutoscalingSettings: &settings})
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Updated autoscaling settings for environment %s\n", flags.Environment)
	return nil
}

func commandModelEnvironmentUpdatePromotion(
	ctx *CommandContext, flags *cmd.ModelEnvironmentUpdatePromotionFlags,
) error {
	settings := promotionSettingsFromFlags(flags)
	resp, err := patchModelEnvironment(ctx, flags.ModelEnvironmentFlags,
		managementapi.UpdateEnvironmentRequest{PromotionSettings: &settings})
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Updated promotion settings for environment %s\n", flags.Environment)
	return nil
}

func commandModelEnvironmentUpdateRequestBackpressure(
	ctx *CommandContext, flags *cmd.ModelEnvironmentUpdateRequestBackpressureFlags,
) error {
	settings := requestBackpressureFromFlags(flags.RequestBackpressureFlags)
	resp, err := patchModelEnvironment(ctx, flags.ModelEnvironmentFlags,
		managementapi.UpdateEnvironmentRequest{RequestBackpressureSettings: &settings})
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	policy := resp.Environment.RequestBackpressureSettings
	ctx.Logf("Request backpressure policy: %s\n", backpressurePolicyText(policy.Policy))
	return nil
}

// patchModelEnvironment resolves the model and sends one environment PATCH.
// Every environment settings command targets the same endpoint and differs only
// in which sub-object of the body it fills in.
func patchModelEnvironment(
	ctx *CommandContext, flags cmd.ModelEnvironmentFlags, body managementapi.UpdateEnvironmentRequest,
) (*managementapi.UpdateEnvironmentResponse, error) {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return nil, err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return nil, err
	}
	resp, err := cl.API().PatchModelsEnvironments(ctx, ref.ID, flags.Environment, body)
	if err != nil {
		return nil, fmt.Errorf("update environment %s: %w", flags.Environment, err)
	}
	return resp, nil
}

// promotionSettingsFromFlags maps the promotion flags onto the request body,
// flattening the rolling deploy config back into its nested shape. The nested
// object is only sent when one of its own flags was passed, so touching a
// top-level promotion setting does not rewrite the rolling deploy config.
func promotionSettingsFromFlags(
	flags *cmd.ModelEnvironmentUpdatePromotionFlags,
) managementapi.UpdatePromotionSettings {
	settings := managementapi.UpdatePromotionSettings{
		RedeployOnPromotion:   flags.RedeployOnPromotion.Pointer(),
		RollingDeploy:         flags.RollingDeploy.Pointer(),
		RampUpWhilePromoting:  flags.RampUpWhilePromoting.Pointer(),
		RampUpDurationSeconds: flags.RampUpDurationSeconds.Pointer(),
	}
	if strategy := flags.PromotionCleanupStrategy.Pointer(); strategy != nil {
		cleanup := managementapi.PromotionCleanupStrategy(enumToAPIValue(*strategy))
		settings.PromotionCleanupStrategy = &cleanup
	}

	config := managementapi.UpdateRollingDeployConfig{
		MaxSurgePercent:          flags.MaxSurgePercent.Pointer(),
		MaxUnavailablePercent:    flags.MaxUnavailablePercent.Pointer(),
		StabilizationTimeSeconds: flags.StabilizationTimeSeconds.Pointer(),
		ReplicaOverheadPercent:   flags.ReplicaOverheadPercent.Pointer(),
	}
	if strategy := flags.RollingDeployStrategy.Pointer(); strategy != nil {
		rolling := managementapi.RollingDeployStrategy(enumToAPIValue(*strategy))
		config.RollingDeployStrategy = &rolling
	}
	if config != (managementapi.UpdateRollingDeployConfig{}) {
		settings.RollingDeployConfig = &config
	}
	return settings
}
