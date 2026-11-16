package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// commandOrgTrustRule groups the `baseten org trust-rule` subcommands.
var commandOrgTrustRule = Command{
	Name:    "trust-rule",
	Summary: "Manage trust rules (PRE-RELEASE)",
	Description: "Manage trust rules, which let an identity provider's tokens act as a " +
		"service account when the token's audience matches and the rule's CEL condition holds.",
	Children: []Command{
		{
			Name:    "create",
			Summary: "Create a trust rule (PRE-RELEASE)",
			Description: "Create a trust rule. A workload passes the rule's ID when it exchanges " +
				"a token. The condition must pin a specific claims.sub in a top-level && clause; a " +
				"GitHub Actions condition must also pin claims.repository_owner_id.",
			Flags: OrgTrustRuleCreateFlags{},
			Output: &CommandOutput[managementapi.AccessTrustRule]{
				TextDescription: "Prints the new rule's ID on stdout, and \"Created trust rule " +
					"<name>\" on stderr.",
				Examples: []CommandExample{
					{
						Description: "Let pushes to main in one GitHub repository act as a service account.",
						CommandLines: []string{
							"baseten org trust-rule create --name models-main-deploy",
							"--identity-provider-name github-actions --service-account-name ci-deployer",
							"--condition \"claims.repository_owner_id == '12345' &&",
							"claims.sub == 'repo:acme/models:ref:refs/heads/main'\"",
							"--audience https://api.baseten.co",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print just the new rule's ID.",
					CommandLines: []string{
						"baseten org trust-rule create --name deploy --identity-provider-name gcp",
						"--service-account-name ci-deployer --condition \"claims.sub == '1234567890'\"",
						"--audience https://api.baseten.co --jq '.id'",
					},
				},
			},
		},
		{
			Name:        "list",
			Summary:     "List trust rules (PRE-RELEASE)",
			Description: "List the trust rules you can administer.",
			Flags:       OrgTrustRuleListFlags{},
			Output: &CommandOutput[OrgTrustRuleList]{
				TextDescription: "Table with columns: ID, NAME, IDENTITY PROVIDER, SERVICE ACCOUNT " +
					"(names), CREATED, LAST USED. When none exist, prints \"No trust rules found.\" " +
					"to stderr.",
				Examples: []CommandExample{
					{
						Description: "List all trust rules.",
						Command:     "baseten org trust-rule list",
					},
					{
						Description: "List one service account's rules.",
						Command:     "baseten org trust-rule list --service-account-name ci-deployer",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the rule IDs.",
					Command:     "baseten org trust-rule list --jq '.items[].id'",
				},
			},
		},
		{
			Name:        "describe",
			Summary:     "Describe a trust rule (PRE-RELEASE)",
			Description: "Describe a trust rule by name or ID.",
			Flags:       OrgTrustRuleDescribeFlags{},
			Output: &CommandOutput[managementapi.AccessTrustRule]{
				TextDescription: "Field-per-line summary of the trust rule.",
				Examples: []CommandExample{
					{
						Description: "Describe a trust rule by name.",
						Command:     "baseten org trust-rule describe --name models-main-deploy",
					},
				},
				JQExample: CommandExample{
					Description: "Print the rule's condition.",
					Command:     "baseten org trust-rule describe --name models-main-deploy --jq '.condition'",
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a trust rule (PRE-RELEASE)",
			Description: "Update a trust rule. Only the fields passed change. The identity " +
				"provider can't change.",
			Flags: OrgTrustRuleUpdateFlags{},
			Output: &CommandOutput[managementapi.AccessTrustRule]{
				TextDescription: "Prints \"Updated trust rule <name>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Point a rule at another service account.",
						Command:     "baseten org trust-rule update --name models-main-deploy --service-account-name ci-v2",
					},
					{
						Description: "Accept another Google Cloud service account instead.",
						CommandLines: []string{
							"baseten org trust-rule update --name gcp-deploy",
							"--condition \"claims.sub == '9876543210'\"",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print the rule's updated audience.",
					Command:     "baseten org trust-rule update --name deploy --audience https://api.baseten.co --jq '.audience'",
				},
			},
		},
		{
			Name:        "delete",
			Summary:     "Delete a trust rule (PRE-RELEASE)",
			Description: "Delete a trust rule by name or ID.",
			Flags:       OrgTrustRuleDeleteFlags{},
			Output: &CommandOutput[managementapi.DeletedAccessTrustRule]{
				TextDescription: "Prints \"Deleted trust rule <id>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Delete a trust rule by name.",
						Command:     "baseten org trust-rule delete --name models-main-deploy",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the deleted rule's ID.",
					Command:     "baseten org trust-rule delete --name models-main-deploy --jq '.id'",
				},
			},
		},
	},
}

// OrgTrustRuleList is the JSON output of `baseten org trust-rule list`: the rules
// aggregated across all pages.
type OrgTrustRuleList struct {
	Items []managementapi.AccessTrustRule `json:"items"`
}

// OrgTrustRuleRefFlags picks one trust rule.
type OrgTrustRuleRefFlags struct {
	ID   string `flag:"id" desc:"Trust rule ID." oneof:"trust-rule-ref"`
	Name string `flag:"name" desc:"Trust rule name." oneof:"trust-rule-ref"`
}

type OrgTrustRuleCreateFlags struct {
	CommandFlags

	Name                 string `flag:"name" desc:"Name, unique in the organization." required:"true"`
	IdentityProviderID   string `flag:"identity-provider-id" desc:"ID of the identity provider whose tokens the rule accepts. Can't be changed." oneof:"identity-provider"`
	IdentityProviderName string `flag:"identity-provider-name" desc:"Name of the identity provider whose tokens the rule accepts, or a predefined provider's value such as github-actions. Can't be changed." oneof:"identity-provider"`
	ServiceAccountID     string `flag:"service-account-id" desc:"ID of the service account the tokens act as." oneof:"service-account"`
	ServiceAccountName   string `flag:"service-account-name" desc:"Name of the service account the tokens act as." oneof:"service-account"`
	Description          string `flag:"description" desc:"Optional description."`
	Condition            string `flag:"condition" desc:"A CEL expression over the token's claims, available as claims, that must evaluate to true. It must pin a specific claims.sub in a top-level && clause." required:"true"`
	Audience             string `flag:"audience" desc:"Value the token's aud claim must equal, or contain when aud is a list." required:"true"`

	MaxAccessTokenLifetime time.Duration `flag:"max-access-token-lifetime" desc:"Longest lifetime of an access token the rule issues, from 1m to 1h (e.g. '15m'). Defaults to 1h."`
}

type OrgTrustRuleListFlags struct {
	CommandFlags

	IdentityProviderID   string `flag:"identity-provider-id" desc:"Only list rules for the identity provider with this ID. Mutually exclusive with --identity-provider-name."`
	IdentityProviderName string `flag:"identity-provider-name" desc:"Only list rules for the identity provider with this name, or of this predefined kind such as github-actions."`
	ServiceAccountID     string `flag:"service-account-id" desc:"Only list rules for the service account with this ID. Mutually exclusive with --service-account-name."`
	ServiceAccountName   string `flag:"service-account-name" desc:"Only list rules for the service account with this name."`
	Team                 string `flag:"team" desc:"Only list rules for this team's service accounts, by team name or ID."`
}

type OrgTrustRuleDescribeFlags struct {
	CommandFlags
	OrgTrustRuleRefFlags
}

type OrgTrustRuleUpdateFlags struct {
	CommandFlags
	OrgTrustRuleRefFlags

	NewName            string               `flag:"new-name" desc:"New name, unique in the organization."`
	Description        OptionalFlag[string] `flag:"description" desc:"New description. Pass an empty string to clear it."`
	ServiceAccountID   string               `flag:"service-account-id" desc:"ID of the service account the tokens act as from now on. Access tokens already issued keep the old account until they expire. Mutually exclusive with --service-account-name."`
	ServiceAccountName string               `flag:"service-account-name" desc:"Name of the service account the tokens act as from now on."`
	Condition          string               `flag:"condition" desc:"New CEL expression over the token's claims. It must pin a specific claims.sub in a top-level && clause."`
	Audience           string               `flag:"audience" desc:"New value the token's aud claim must equal, or contain when aud is a list."`

	MaxAccessTokenLifetime time.Duration `flag:"max-access-token-lifetime" desc:"New longest lifetime of an access token the rule issues, from 1m to 1h (e.g. '15m')."`
}

type OrgTrustRuleDeleteFlags struct {
	CommandFlags
	OrgTrustRuleRefFlags
}
