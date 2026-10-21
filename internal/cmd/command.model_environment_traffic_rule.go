package cmd

import (
	"fmt"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("model environment traffic-rule create", commandModelEnvironmentTrafficRuleCreate)
	Register("model environment traffic-rule update", commandModelEnvironmentTrafficRuleUpdate)
	Register("model environment traffic-rule delete", commandModelEnvironmentTrafficRuleDelete)
}

func commandModelEnvironmentTrafficRuleCreate(
	ctx *CommandContext, flags *cmd.ModelEnvironmentTrafficRuleCreateFlags,
) error {
	target := managementapi.TrafficRoutingTargetUpsert{}
	if flags.TargetEnvironment != "" {
		target.Environment = &flags.TargetEnvironment
	} else {
		target.DeploymentId = &flags.TargetDeploymentID
	}
	rule := managementapi.TrafficRoutingRuleUpsert{
		Type:                        enumToAPIValue(flags.Type),
		Targets:                     []managementapi.TrafficRoutingTargetUpsert{target},
		PercentTraffic:              flags.PercentTraffic,
		ExpiresAt:                   flags.ExpiresAt,
		SamplingMode:                managementapi.TrafficRoutingRuleShadowSamplingMode(enumToAPIValue(flags.SamplingMode)),
		PropagateClientCancellation: *flags.PropagateClientCancellation.Pointer(),
	}

	if !flags.Yes {
		prompt := fmt.Sprintf("Send a %s copy of %d%% of environment %s's traffic to %s until %s?",
			flags.Type, flags.PercentTraffic, flags.Environment, trafficRuleTargetUpsertText(target),
			flags.ExpiresAt.UTC().Format(time.RFC3339))
		if err := ctx.ConfirmYesNo(prompt); err != nil {
			return err
		}
	}

	resp, err := patchModelEnvironment(ctx, flags.ModelEnvironmentFlags,
		managementapi.UpdateEnvironmentRequest{
			TrafficRoutingSettings: &managementapi.UpdateTrafficRoutingSettings{
				Rules: &[]managementapi.TrafficRoutingRuleUpsert{rule},
			},
		})
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	// The create request carries no id, so the new rule is found by its target
	// in the environment the response returns.
	for _, created := range trafficRules(&resp.Environment) {
		if len(created.Targets) == 1 && trafficRuleTargetMatches(created.Targets[0], target) {
			ctx.Logf("Created traffic rule %s\n", created.Id)
			return nil
		}
	}
	ctx.Logf("Created traffic rule\n")
	return nil
}

func commandModelEnvironmentTrafficRuleUpdate(
	ctx *CommandContext, flags *cmd.ModelEnvironmentTrafficRuleUpdateFlags,
) error {
	if !flags.PercentTraffic.IsSet() && flags.ExpiresAt.IsZero() && !flags.SamplingMode.IsSet() &&
		!flags.PropagateClientCancellation.IsSet() {
		return cmd.NewErrUsagef("pass at least one of --percent-traffic, --expires-at, --sampling-mode " +
			"and --propagate-client-cancellation")
	}

	// The API replaces a rule wholesale, so the existing one has to be read and
	// merged with the caller's changes. This is racy against a concurrent edit;
	// losing the race means the other edit is overwritten.
	env, err := getModelEnvironment(ctx, flags.ModelEnvironmentFlags)
	if err != nil {
		return err
	}
	existing, err := trafficRuleFind(env, flags.Environment, flags.RuleID)
	if err != nil {
		return err
	}
	rule, err := trafficRuleMerge(existing, flags)
	if err != nil {
		return err
	}

	resp, err := patchModelEnvironment(ctx, flags.ModelEnvironmentFlags,
		managementapi.UpdateEnvironmentRequest{
			TrafficRoutingSettings: &managementapi.UpdateTrafficRoutingSettings{
				Rules: &[]managementapi.TrafficRoutingRuleUpsert{rule},
			},
		})
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Updated traffic rule %s\n", flags.RuleID)
	return nil
}

func commandModelEnvironmentTrafficRuleDelete(
	ctx *CommandContext, flags *cmd.ModelEnvironmentTrafficRuleDeleteFlags,
) error {
	if !flags.Yes {
		prompt := fmt.Sprintf("Delete traffic rule %s from environment %s?", flags.RuleID, flags.Environment)
		if err := ctx.ConfirmYesNo(prompt); err != nil {
			return err
		}
	}

	resp, err := patchModelEnvironment(ctx, flags.ModelEnvironmentFlags,
		managementapi.UpdateEnvironmentRequest{
			TrafficRoutingSettings: &managementapi.UpdateTrafficRoutingSettings{
				DeleteRules: &[]string{flags.RuleID},
			},
		})
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Deleted traffic rule %s\n", flags.RuleID)
	return nil
}

// getModelEnvironment reads one environment of a model.
func getModelEnvironment(
	ctx *CommandContext, flags cmd.ModelEnvironmentFlags,
) (*managementapi.Environment, error) {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return nil, err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return nil, err
	}
	env, err := cl.API().GetModelsEnvironmentsEnvName(ctx, ref.ID, flags.Environment)
	if err != nil {
		return nil, fmt.Errorf("describe environment %s: %w", flags.Environment, err)
	}
	return env, nil
}

// trafficRules returns an environment's active rules. The API can omit traffic
// routing entirely, which reads as no rules.
func trafficRules(env *managementapi.Environment) []managementapi.TrafficRoutingRule {
	if env.TrafficRouting == nil {
		return nil
	}
	return env.TrafficRouting.Rules
}

func trafficRuleFind(
	env *managementapi.Environment, environment, ruleID string,
) (managementapi.TrafficRoutingRule, error) {
	for _, rule := range trafficRules(env) {
		if rule.Id == ruleID {
			return rule, nil
		}
	}
	return managementapi.TrafficRoutingRule{}, cmd.NewErrUsagef(
		"environment %s has no active traffic rule %s; run 'baseten model environment describe' to see its rules",
		environment, ruleID)
}

// trafficRuleMerge builds the replacement for an existing rule, keeping every
// field the caller did not pass.
func trafficRuleMerge(
	existing managementapi.TrafficRoutingRule, flags *cmd.ModelEnvironmentTrafficRuleUpdateFlags,
) (managementapi.TrafficRoutingRuleUpsert, error) {
	// A rule's target is fixed, so the replacement has to resend it. A rule
	// whose target was deleted has none left to send.
	if len(existing.Targets) != 1 {
		return managementapi.TrafficRoutingRuleUpsert{}, cmd.NewErrUsagef(
			"traffic rule %s's target was deleted; delete the rule and create a new one", existing.Id)
	}
	target := existing.Targets[0]
	rule := managementapi.TrafficRoutingRuleUpsert{
		Id:   &existing.Id,
		Type: existing.Type,
		Targets: []managementapi.TrafficRoutingTargetUpsert{{
			DeploymentId: target.DeploymentId,
			Environment:  target.Environment,
		}},
		PercentTraffic:              existing.PercentTraffic,
		ExpiresAt:                   existing.ExpiresAt,
		SamplingMode:                existing.SamplingMode,
		PropagateClientCancellation: existing.PropagateClientCancellation,
	}
	if value := flags.PercentTraffic.Pointer(); value != nil {
		rule.PercentTraffic = *value
	}
	if !flags.ExpiresAt.IsZero() {
		rule.ExpiresAt = flags.ExpiresAt
	}
	if value := flags.SamplingMode.Pointer(); value != nil {
		rule.SamplingMode = managementapi.TrafficRoutingRuleShadowSamplingMode(enumToAPIValue(*value))
	}
	if value := flags.PropagateClientCancellation.Pointer(); value != nil {
		rule.PropagateClientCancellation = *value
	}
	return rule, nil
}

func trafficRuleTargetMatches(
	target managementapi.TrafficRoutingTarget, upsert managementapi.TrafficRoutingTargetUpsert,
) bool {
	if upsert.Environment != nil {
		return target.Environment != nil && *target.Environment == *upsert.Environment
	}
	return target.DeploymentId != nil && *target.DeploymentId == *upsert.DeploymentId
}

func trafficRuleTargetUpsertText(target managementapi.TrafficRoutingTargetUpsert) string {
	if target.Environment != nil {
		return "environment " + *target.Environment
	}
	return "deployment " + *target.DeploymentId
}

// trafficRuleTargetText describes where a rule sends traffic, for describe.
func trafficRuleTargetText(targets []managementapi.TrafficRoutingTarget) string {
	if len(targets) == 0 {
		return "deleted target"
	}
	target := targets[0]
	if target.Environment != nil {
		return "environment " + *target.Environment
	}
	if target.DeploymentId != nil {
		return "deployment " + *target.DeploymentId
	}
	return "-"
}
