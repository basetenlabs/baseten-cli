package cmd

import (
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// commandOrgIdentityProvider groups the `baseten org identity-provider`
// subcommands.
var commandOrgIdentityProvider = Command{
	Name:    "identity-provider",
	Summary: "Manage identity providers",
	Description: "Manage the OIDC identity providers whose tokens the organization accepts " +
		"for workload identity federation.",
	Children: []Command{
		{
			Name:    "list-predefined",
			Summary: "List predefined identity providers",
			Description: "List the identity providers Baseten predefines. Pass a value from the " +
				"PREDEFINED column to 'baseten org identity-provider create --predefined'.",
			Flags: OrgIdentityProviderListPredefinedFlags{},
			Output: &CommandOutput[managementapi.PredefinedIdentityProviders]{
				TextDescription: "Table with columns: PREDEFINED, NAME, ISSUER URL.",
				Examples: []CommandExample{
					{
						Description: "List the predefined identity providers.",
						Command:     "baseten org identity-provider list-predefined",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the issuer URLs.",
					Command:     "baseten org identity-provider list-predefined --jq '.items[].issuer_url'",
				},
			},
		},
		{
			Name:    "create",
			Summary: "Add an identity provider",
			Description: "Add a predefined identity provider with --predefined, or register a " +
				"custom one with --name and --issuer-url. A custom provider's signing keys are " +
				"discovered from the issuer's /.well-known/openid-configuration.",
			Flags: OrgIdentityProviderCreateFlags{},
			Output: &CommandOutput[managementapi.IdentityProvider]{
				TextDescription: "Prints the new provider's ID on stdout, and \"Created identity " +
					"provider <name>\" on stderr.",
				Examples: []CommandExample{
					{
						Description: "Add GitHub Actions.",
						Command:     "baseten org identity-provider create --predefined github-actions",
					},
					{
						Description: "Register a custom provider.",
						CommandLines: []string{
							"baseten org identity-provider create --name prod-eks",
							"--issuer-url https://oidc.eks.us-west-2.amazonaws.com/id/EXAMPLE",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print just the new provider's ID.",
					Command:     "baseten org identity-provider create --predefined gcp --jq '.id'",
				},
			},
		},
		{
			Name:        "list",
			Summary:     "List identity providers",
			Description: "List the organization's identity providers.",
			Flags:       OrgIdentityProviderListFlags{},
			Output: &CommandOutput[IdentityProviderList]{
				TextDescription: "Table with columns: ID, NAME, TYPE, ISSUER URL, CREATED. When " +
					"none exist, prints \"No identity providers found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List all identity providers.",
						Command:     "baseten org identity-provider list",
					},
					{
						Description: "List only custom providers.",
						Command:     "baseten org identity-provider list --type custom",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the issuer URLs.",
					Command:     "baseten org identity-provider list --jq '.items[].issuer_url'",
				},
			},
		},
		{
			Name:    "describe",
			Summary: "Describe an identity provider",
			Description: "Describe an identity provider by name or ID. --name also accepts a " +
				"predefined provider's value, such as github-actions.",
			Flags: OrgIdentityProviderDescribeFlags{},
			Output: &CommandOutput[managementapi.IdentityProvider]{
				TextDescription: "Field-per-line summary of the identity provider.",
				Examples: []CommandExample{
					{
						Description: "Describe the GitHub Actions provider.",
						Command:     "baseten org identity-provider describe --name github-actions",
					},
				},
				JQExample: CommandExample{
					Description: "Print the provider's issuer URL.",
					Command:     "baseten org identity-provider describe --name github-actions --jq '.issuer_url'",
				},
			},
		},
		{
			Name:        "update",
			Summary:     "Update an identity provider",
			Description: "Update an identity provider's name or description.",
			Flags:       OrgIdentityProviderUpdateFlags{},
			Output: &CommandOutput[managementapi.IdentityProvider]{
				TextDescription: "Prints \"Updated identity provider <name>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Rename an identity provider.",
						Command:     "baseten org identity-provider update --name prod-eks --new-name eks",
					},
				},
				JQExample: CommandExample{
					Description: "Print the provider's new name.",
					Command:     "baseten org identity-provider update --name prod-eks --new-name eks --jq '.name'",
				},
			},
		},
		{
			Name:        "delete",
			Summary:     "Delete an identity provider",
			Description: "Delete an identity provider by name or ID.",
			Flags:       OrgIdentityProviderDeleteFlags{},
			Output: &CommandOutput[managementapi.DeletedIdentityProvider]{
				TextDescription: "Prints \"Deleted identity provider <id>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Delete an identity provider by name.",
						Command:     "baseten org identity-provider delete --name prod-eks",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the deleted provider's ID.",
					Command:     "baseten org identity-provider delete --name prod-eks --jq '.id'",
				},
			},
		},
		{
			Name:    "verify",
			Summary: "Check an identity provider's signing keys",
			Description: "Check that an issuer's signing keys can be fetched and parsed. Pass " +
				"--issuer-url to check an issuer before adding it, or --id or --name to re-check " +
				"a saved provider.",
			Flags: OrgIdentityProviderVerifyFlags{},
			Output: &CommandOutput[managementapi.VerifyIdentityProviderResponse]{
				TextDescription: "On success, prints the key IDs found, one per line, on stdout. On " +
					"failure, the command fails with the reason.",
				Examples: []CommandExample{
					{
						Description: "Check an issuer before adding it.",
						Command:     "baseten org identity-provider verify --issuer-url https://token.actions.githubusercontent.com",
					},
					{
						Description: "Re-check a saved provider.",
						Command:     "baseten org identity-provider verify --name prod-eks",
					},
				},
				JQExample: CommandExample{
					Description: "Print whether the check passed.",
					Command:     "baseten org identity-provider verify --name prod-eks --jq '.ok'",
				},
			},
		},
	},
}

// IdentityProviderList is the JSON output of `baseten org identity-provider
// list`: the providers aggregated across all pages.
type IdentityProviderList struct {
	Items []managementapi.IdentityProvider `json:"items"`
}

// IdentityProviderRefFlags picks one identity provider.
type IdentityProviderRefFlags struct {
	ID   string `flag:"id" desc:"Identity provider ID." oneof:"identity-provider-ref"`
	Name string `flag:"name" desc:"Identity provider name, or a predefined provider's value such as github-actions." oneof:"identity-provider-ref"`
}

type OrgIdentityProviderListPredefinedFlags struct {
	CommandFlags
}

type OrgIdentityProviderCreateFlags struct {
	CommandFlags

	Predefined  string `flag:"predefined" desc:"Add this predefined provider. Mutually exclusive with --issuer-url." enum:"github-actions,gcp"`
	Name        string `flag:"name" desc:"Name, unique in the organization. Required for a custom provider; defaults to the display name for a predefined one."`
	IssuerURL   string `flag:"issuer-url" desc:"A custom provider's issuer: the exact iss claim of its tokens, an https URL. Can't be changed."`
	Description string `flag:"description" desc:"Optional description."`
}

type OrgIdentityProviderListFlags struct {
	CommandFlags

	Type string `flag:"type" desc:"Only list predefined or only custom providers." enum:"predefined,custom"`
}

type OrgIdentityProviderDescribeFlags struct {
	CommandFlags
	IdentityProviderRefFlags
}

type OrgIdentityProviderUpdateFlags struct {
	CommandFlags
	IdentityProviderRefFlags

	NewName     string               `flag:"new-name" desc:"New name, unique in the organization."`
	Description OptionalFlag[string] `flag:"description" desc:"New description. Pass an empty string to clear it."`
}

type OrgIdentityProviderDeleteFlags struct {
	CommandFlags
	IdentityProviderRefFlags
}

type OrgIdentityProviderVerifyFlags struct {
	CommandFlags

	IssuerURL string `flag:"issuer-url" desc:"Issuer to check, an https URL." oneof:"identity-provider-verify"`
	ID        string `flag:"id" desc:"ID of a saved identity provider to re-check." oneof:"identity-provider-verify"`
	Name      string `flag:"name" desc:"Name of a saved identity provider to re-check, or a predefined provider's value such as github-actions." oneof:"identity-provider-verify"`
}
