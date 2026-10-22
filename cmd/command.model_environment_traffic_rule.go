package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

const trafficRulePrereleaseNotice = "PRE-RELEASE: traffic-rule commands are not GA yet. " +
	"Their arguments, flags, and output may change.\n\n"

// commandModelEnvironmentTrafficRule groups the
// `baseten model environment traffic-rule` subcommands.
var commandModelEnvironmentTrafficRule = Command{
	Name:        "traffic-rule",
	Summary:     "Manage an environment's traffic routing rules (PRE-RELEASE)",
	Description: trafficRulePrereleaseNotice + "Manage an environment's traffic routing rules.",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List an environment's traffic routing rules (PRE-RELEASE)",
			Description: trafficRulePrereleaseNotice +
				"List every active traffic routing rule on an environment with its id, which " +
				"the update and delete commands take.",
			Flags: ModelEnvironmentTrafficRuleListFlags{},
			Output: &CommandOutput[managementapi.EnvironmentTrafficRouting]{
				TextDescription: "Table with columns: ID, TYPE, TARGET, PERCENT, SAMPLING MODE, " +
					"PROPAGATE CANCELLATION, EXPIRES. " +
					"When no rules exist, prints \"No traffic rules found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List an environment's rules.",
						Command:     "baseten model environment traffic-rule list --model-id <model-id> --environment production",
					},
				},
				JQExample: CommandExample{
					Description: "Print each rule's id and percentage.",
					CommandLines: []string{
						"baseten model environment traffic-rule list --model-id <model-id> --environment production",
						"--jq '.rules[] | \"\\(.id) \\(.percent_traffic)\"'",
					},
				},
			},
		},
		{
			Name:    "create",
			Summary: "Create a traffic routing rule (PRE-RELEASE)",
			Description: trafficRulePrereleaseNotice +
				"Add a traffic routing rule to an environment. A shadow rule sends a copy " +
				"of a percentage of the environment's traffic to a target, and the target's " +
				"responses are discarded.\n\n" +
				"Set the target with exactly one of --target-environment and " +
				"--target-deployment-id. The target can't be changed later: delete the rule and " +
				"create a new one instead.\n\n" +
				"Run 'baseten model environment traffic-rule list' to see the environment's active rules.",
			Flags: ModelEnvironmentTrafficRuleCreateFlags{},
			Output: &CommandOutput[managementapi.UpdateEnvironmentResponse]{
				TextDescription: "On success, prints \"Created traffic rule\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Shadow 25% of production's traffic to staging.",
						CommandLines: []string{
							"baseten model environment traffic-rule create --model-id <model-id> --environment production",
							"--type shadow --target-environment staging --percent-traffic 25",
							"--expires-at 2026-10-08T18:00 --sampling-mode user-session",
							"--propagate-client-cancellation true",
						},
					},
					{
						Description: "Shadow 10% of production's traffic to a specific deployment.",
						CommandLines: []string{
							"baseten model environment traffic-rule create --model-id <model-id> --environment production",
							"--type shadow --target-deployment-id <deployment-id> --percent-traffic 10",
							"--expires-at 2026-10-08T18:00 --sampling-mode random",
							"--propagate-client-cancellation false",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print the ids of the environment's active rules after the create.",
					CommandLines: []string{
						"baseten model environment traffic-rule create --model-id <model-id> --environment production",
						"--type shadow --target-environment staging --percent-traffic 25",
						"--expires-at 2026-10-08T18:00 --sampling-mode random",
						"--propagate-client-cancellation false",
						"--jq '.environment.traffic_routing.rules[].id'",
					},
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a traffic routing rule (PRE-RELEASE)",
			Description: trafficRulePrereleaseNotice +
				"Change one traffic routing rule, identified by id.\n\n" +
				"Only the flags you pass are changed; the rest of the rule is preserved. " +
				"Because the API replaces a rule wholesale, the command reads the environment " +
				"first and merges your changes into the existing rule.\n\n" +
				"The target can't be changed: delete the rule and create a new one instead.",
			Flags: ModelEnvironmentTrafficRuleUpdateFlags{},
			Output: &CommandOutput[managementapi.UpdateEnvironmentResponse]{
				TextDescription: "On success, prints \"Updated traffic rule <id>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Raise the percentage of traffic a rule shadows.",
						CommandLines: []string{
							"baseten model environment traffic-rule update --model-id <model-id> --environment production",
							"--rule-id <rule-id> --percent-traffic 50",
						},
					},
					{
						Description: "Extend a rule's expiry.",
						CommandLines: []string{
							"baseten model environment traffic-rule update --model-id <model-id> --environment production",
							"--rule-id <rule-id> --expires-at 2026-10-10T18:00",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print each active rule's percentage after the update.",
					CommandLines: []string{
						"baseten model environment traffic-rule update --model-id <model-id> --environment production",
						"--rule-id <rule-id> --percent-traffic 50",
						"--jq '.environment.traffic_routing.rules[].percent_traffic'",
					},
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a traffic routing rule (PRE-RELEASE)",
			Description: trafficRulePrereleaseNotice +
				"Remove one traffic routing rule from an environment, identified by id.",
			Flags: ModelEnvironmentTrafficRuleDeleteFlags{},
			Output: &CommandOutput[managementapi.UpdateEnvironmentResponse]{
				TextDescription: "On success, prints \"Deleted traffic rule <id>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Delete a rule.",
						CommandLines: []string{
							"baseten model environment traffic-rule delete --model-id <model-id> --environment production",
							"--rule-id <rule-id>",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print the ids of the rules that remain.",
					CommandLines: []string{
						"baseten model environment traffic-rule delete --model-id <model-id> --environment production",
						"--rule-id <rule-id> --yes --jq '.environment.traffic_routing.rules[].id'",
					},
				},
			},
		},
	},
}

// ModelEnvironmentTrafficRuleListFlags configures
// `baseten model environment traffic-rule list`.
type ModelEnvironmentTrafficRuleListFlags struct {
	CommandFlags
	ModelEnvironmentFlags
}

// ModelEnvironmentTrafficRuleCreateFlags configures
// `baseten model environment traffic-rule create`.
type ModelEnvironmentTrafficRuleCreateFlags struct {
	CommandFlags
	ModelEnvironmentFlags

	Type                        string             `flag:"type" desc:"Rule type. A shadow rule sends a copy of the environment's traffic to its target." enum:"shadow" required:"true"`
	TargetEnvironment           string             `flag:"target-environment" desc:"Name of the target environment. Traffic goes to whichever deployment the environment is serving." oneof:"target"`
	TargetDeploymentID          string             `flag:"target-deployment-id" desc:"ID of the target deployment." oneof:"target"`
	PercentTraffic              int                `flag:"percent-traffic" desc:"Percentage of the environment's traffic to shadow." required:"true"`
	ExpiresAt                   time.Time          `flag:"expires-at" desc:"When the rule stops, ISO 8601 (for example 2026-10-08T18:00), at most 7 days out. Values without a timezone designator are read in the local timezone." required:"true"`
	SamplingMode                string             `flag:"sampling-mode" desc:"How requests are picked for shadowing. random picks each request independently. user-session keeps a whole session together." enum:"random,user-session" required:"true"`
	PropagateClientCancellation OptionalFlag[bool] `flag:"propagate-client-cancellation" desc:"Whether a shadow request is cancelled when the original client disconnects." required:"true"`
}

// ModelEnvironmentTrafficRuleUpdateFlags configures
// `baseten model environment traffic-rule update`.
type ModelEnvironmentTrafficRuleUpdateFlags struct {
	CommandFlags
	ModelEnvironmentFlags

	RuleID                      string               `flag:"rule-id" desc:"ID of the rule. Run 'baseten model environment traffic-rule list' to see the environment's rules." required:"true"`
	PercentTraffic              OptionalFlag[int]    `flag:"percent-traffic" desc:"New percentage of the environment's traffic to shadow."`
	ExpiresAt                   time.Time            `flag:"expires-at" desc:"New time the rule stops, ISO 8601 (for example 2026-10-08T18:00), at most 7 days out. Values without a timezone designator are read in the local timezone."`
	SamplingMode                OptionalFlag[string] `flag:"sampling-mode" desc:"New way requests are picked for shadowing." enum:"random,user-session"`
	PropagateClientCancellation OptionalFlag[bool]   `flag:"propagate-client-cancellation" desc:"Whether a shadow request is cancelled when the original client disconnects."`
}

// ModelEnvironmentTrafficRuleDeleteFlags configures
// `baseten model environment traffic-rule delete`.
type ModelEnvironmentTrafficRuleDeleteFlags struct {
	CommandFlags
	ModelEnvironmentFlags

	RuleID string `flag:"rule-id" desc:"ID of the rule. Run 'baseten model environment traffic-rule list' to see the environment's rules." required:"true"`

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}
