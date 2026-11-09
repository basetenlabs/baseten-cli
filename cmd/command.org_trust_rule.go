package cmd

import (
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// commandOrgTrustRule groups the `baseten org trust-rule` subcommands.
var commandOrgTrustRule = Command{
	Name:    "trust-rule",
	Summary: "Manage trust rules",
	Description: "Manage trust rules, which let an identity provider's tokens act as a " +
		"service account when they match. Every match criterion that is set must pass.",
	Children: []Command{
		{
			Name:    "create",
			Summary: "Create a trust rule",
			Description: "Create a trust rule. A workload passes the rule's ID when it exchanges " +
				"a token. A GitHub Actions rule must set a repository_owner_id --claim; a Google " +
				"Cloud rule must set --subject-glob to the exact service account ID.",
			Flags: OrgTrustRuleCreateFlags{},
			Output: &CommandOutput[managementapi.TrustRule]{
				TextDescription: "Prints the new rule's ID on stdout, and \"Created trust rule " +
					"<name>\" on stderr.",
				Examples: []CommandExample{
					{
						Description: "Let pushes to main in one GitHub repository act as a service account.",
						CommandLines: []string{
							"baseten org trust-rule create --name models-main-deploy",
							"--identity-provider github-actions --service-account ci-deployer",
							"--subject-glob 'repo:acme/models:ref:refs/heads/main'",
							"--claim repository_owner_id=12345",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print just the new rule's ID.",
					CommandLines: []string{
						"baseten org trust-rule create --name deploy --identity-provider github-actions",
						"--service-account ci-deployer --claim repository_owner_id=12345 --jq '.id'",
					},
				},
			},
		},
		{
			Name:        "list",
			Summary:     "List trust rules",
			Description: "List the trust rules you can administer.",
			Flags:       OrgTrustRuleListFlags{},
			Output: &CommandOutput[TrustRuleList]{
				TextDescription: "Table with columns: ID, NAME, IDENTITY PROVIDER, SERVICE ACCOUNT " +
					"(IDs), CREATED, LAST USED. When none exist, prints \"No trust rules found.\" " +
					"to stderr.",
				Examples: []CommandExample{
					{
						Description: "List all trust rules.",
						Command:     "baseten org trust-rule list",
					},
					{
						Description: "List one service account's rules.",
						Command:     "baseten org trust-rule list --service-account ci-deployer",
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
			Summary:     "Describe a trust rule",
			Description: "Describe a trust rule by name or ID.",
			Flags:       OrgTrustRuleDescribeFlags{},
			Output: &CommandOutput[managementapi.TrustRule]{
				TextDescription: "Field-per-line summary of the trust rule.",
				Examples: []CommandExample{
					{
						Description: "Describe a trust rule by name.",
						Command:     "baseten org trust-rule describe --name models-main-deploy",
					},
				},
				JQExample: CommandExample{
					Description: "Print the rule's match.",
					Command:     "baseten org trust-rule describe --name models-main-deploy --jq '.match'",
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a trust rule",
			Description: "Update a trust rule. Match criteria not passed are kept; pass null to " +
				"--subject-glob or --condition to remove that criterion, or to --audience to " +
				"restore the default audience. --claim replaces every claim. The identity provider " +
				"and maximum token lifetime can't change. The rule is read, then replaced whole, " +
				"so a change made by someone else in between is overwritten.",
			Flags: OrgTrustRuleUpdateFlags{},
			Output: &CommandOutput[managementapi.TrustRule]{
				TextDescription: "Prints \"Updated trust rule <name>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Point a rule at another service account.",
						Command:     "baseten org trust-rule update --name models-main-deploy --service-account ci-v2",
					},
					{
						Description: "Also require the token to come from a push or release.",
						CommandLines: []string{
							"baseten org trust-rule update --name models-main-deploy",
							"--condition \"claims.event_name in ['push', 'release']\"",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print the rule's updated match.",
					Command:     "baseten org trust-rule update --name deploy --audience null --jq '.match'",
				},
			},
		},
		{
			Name:        "delete",
			Summary:     "Delete a trust rule",
			Description: "Delete a trust rule by name or ID.",
			Flags:       OrgTrustRuleDeleteFlags{},
			Output: &CommandOutput[managementapi.DeletedTrustRule]{
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

// TrustRuleList is the JSON output of `baseten org trust-rule list`: the rules
// aggregated across all pages.
type TrustRuleList struct {
	Items []managementapi.TrustRule `json:"items"`
}

// TrustRuleRefFlags picks one trust rule.
type TrustRuleRefFlags struct {
	ID   string `flag:"id" desc:"Trust rule ID." oneof:"trust-rule-ref"`
	Name string `flag:"name" desc:"Trust rule name." oneof:"trust-rule-ref"`
}

type OrgTrustRuleCreateFlags struct {
	CommandFlags

	Name             string   `flag:"name" desc:"Name, unique in the organization." required:"true"`
	IdentityProvider string   `flag:"identity-provider" desc:"Name or ID of the identity provider whose tokens the rule accepts, or a predefined provider's value such as github-actions. Can't be changed." required:"true"`
	ServiceAccount   string   `flag:"service-account" desc:"Name or ID of the service account the tokens act as." required:"true"`
	Description      string   `flag:"description" desc:"Optional description."`
	SubjectGlob      string   `flag:"subject-glob" desc:"Glob on the token's sub claim. * matches any run of characters, including ':'."`
	Claims           []string `flag:"claim" desc:"A top-level string claim the token must carry, as key=value. May be repeated."`
	Condition        string   `flag:"condition" desc:"A CEL expression over the token's claims, available as claims, that must evaluate to true."`
	Audience         string   `flag:"audience" desc:"Value the token's aud claim must equal or contain. The server defaults it to https://api.baseten.co."`

	MaxAccessTokenLifetimeSeconds OptionalFlag[int] `flag:"max-access-token-lifetime-seconds" desc:"Longest lifetime of an access token the rule issues, 60 to 3600 seconds. The server defaults it to 3600. Can't be changed."`
}

type OrgTrustRuleListFlags struct {
	CommandFlags

	IdentityProvider string `flag:"identity-provider" desc:"Only list this identity provider's rules, by name or ID."`
	ServiceAccount   string `flag:"service-account" desc:"Only list rules for this service account, by name or ID."`
	Team             string `flag:"team" desc:"Only list rules for this team's service accounts, by team name or ID."`
}

type OrgTrustRuleDescribeFlags struct {
	CommandFlags
	TrustRuleRefFlags
}

type OrgTrustRuleUpdateFlags struct {
	CommandFlags
	TrustRuleRefFlags

	NewName        string               `flag:"new-name" desc:"New name, unique in the organization."`
	Description    OptionalFlag[string] `flag:"description" desc:"New description. Pass an empty string to clear it."`
	ServiceAccount string               `flag:"service-account" desc:"Name or ID of the service account the tokens act as from now on. Access tokens already issued keep the old account until they expire."`
	SubjectGlob    OptionalFlag[string] `flag:"subject-glob" desc:"New glob on the token's sub claim." nullable:"true"`
	Claims         []string             `flag:"claim" desc:"A top-level string claim the token must carry, as key=value. May be repeated. Replaces every claim."`
	ClearClaims    bool                 `flag:"clear-claims" desc:"Remove every claim requirement."`
	Condition      OptionalFlag[string] `flag:"condition" desc:"New CEL expression over the token's claims." nullable:"true"`
	Audience       OptionalFlag[string] `flag:"audience" desc:"New value the token's aud claim must equal or contain. null restores the default." nullable:"true"`
}

type OrgTrustRuleDeleteFlags struct {
	CommandFlags
	TrustRuleRefFlags
}
