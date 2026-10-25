package cmd

import (
	"fmt"
	"strconv"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("model environment traffic-rule list", commandModelEnvironmentTrafficRuleList)
	Register("model environment traffic-rule create", commandModelEnvironmentTrafficRuleCreate)
	Register("model environment traffic-rule update", commandModelEnvironmentTrafficRuleUpdate)
	Register("model environment traffic-rule delete", commandModelEnvironmentTrafficRuleDelete)
}

func commandModelEnvironmentTrafficRuleList(
	ctx *CommandContext, flags *cmd.ModelEnvironmentTrafficRuleListFlags,
) error {
	env, err := getModelEnvironment(ctx, flags.ModelEnvironmentFlags)
	if err != nil {
		return err
	}
	rules := trafficRules(env)

	if ctx.JSON {
		ctx.OutputJSON(managementapi.EnvironmentTrafficRouting{Rules: normalizeJSONSlice(rules)})
		return nil
	}
	if len(rules) == 0 {
		ctx.LogLine("No traffic rules found.")
		return nil
	}
	rows := make([][]string, 0, len(rules))
	for _, rule := range rules {
		rows = append(rows, []string{
			rule.Id, enumFromAPIValue(rule.Type), trafficRuleTargetText(rule.Targets),
			fmt.Sprintf("%d%%", rule.PercentTraffic), enumFromAPIValue(string(rule.SamplingMode)),
			strconv.FormatBool(rule.PropagateClientCancellation), rule.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "TYPE", "TARGET", "PERCENT", "SAMPLING MODE", "PROPAGATE CANCELLATION", "EXPIRES"},
		Rows:    rows,
	})
	return nil
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
	existing, err := trafficRuleFind(ctx, flags.ModelEnvironmentFlags, flags.RuleID)
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

// trafficRules returns an environment's active rules. The API can omit traffic
// routing entirely, which reads as no rules.
func trafficRules(env *managementapi.Environment) []managementapi.TrafficRoutingRule {
	if env.TrafficRouting == nil {
		return nil
	}
	return env.TrafficRouting.Rules
}

// trafficRuleFind reads the environment and returns its active rule with the
// given id.
func trafficRuleFind(
	ctx *CommandContext, flags cmd.ModelEnvironmentFlags, ruleID string,
) (managementapi.TrafficRoutingRule, error) {
	env, err := getModelEnvironment(ctx, flags)
	if err != nil {
		return managementapi.TrafficRoutingRule{}, err
	}
	for _, rule := range trafficRules(env) {
		if rule.Id == ruleID {
			return rule, nil
		}
	}
	return managementapi.TrafficRoutingRule{}, cmd.NewErrNotFound(fmt.Errorf(
		"environment %s has no active traffic rule %s; run 'baseten model environment traffic-rule list' to see its rules",
		flags.Environment, ruleID))
}

// trafficRuleMerge builds the replacement for an existing rule, keeping every
// field the caller did not pass.
func trafficRuleMerge(
	existing managementapi.TrafficRoutingRule, flags *cmd.ModelEnvironmentTrafficRuleUpdateFlags,
) (managementapi.TrafficRoutingRuleUpsert, error) {
	// A rule's target is fixed, so the replacement has to resend it. A rule
	// whose target was deleted has none left to send.
	if len(existing.Targets) != 1 {
		return managementapi.TrafficRoutingRuleUpsert{}, cmd.NewErrValidation(fmt.Errorf(
			"traffic rule %s's target was deleted; delete the rule and create a new one", existing.Id))
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
