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
			Description: "List the identity providers Baseten supports. Pass a value from the " +
				"KIND column to 'baseten org identity-provider create --kind'.",
			Flags: OrgIdentityProviderListPredefinedFlags{},
			Output: &CommandOutput[managementapi.PredefinedIdentityProviders]{
				TextDescription: "Table with columns: KIND, NAME, ISSUER URL. ISSUER URL shows an " +
					"example when each account, tenant, or cluster has its own issuer, which you pass " +
					"as --issuer-url.",
				Examples: []CommandExample{
					{
						Description: "List the predefined identity providers.",
						Command:     "baseten org identity-provider list-predefined",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the issuer URLs.",
					Command:     "baseten org identity-provider list-predefined --jq '.items[].issuer_url // .items[].issuer_url_example'",
				},
			},
		},
		{
			Name:    "create",
			Summary: "Add an identity provider",
			Description: "Add one of the identity providers Baseten supports. AWS IAM, Amazon EKS, " +
				"Azure managed identity, and Azure Kubernetes Service also take your --issuer-url, " +
				"in the format 'baseten org identity-provider list-predefined' shows.",
			Flags: OrgIdentityProviderCreateFlags{},
			Output: &CommandOutput[managementapi.IdentityProvider]{
				TextDescription: "Prints the new provider's ID on stdout, and \"Created identity " +
					"provider <name>\" on stderr.",
				Examples: []CommandExample{
					{
						Description: "Add GitHub Actions.",
						Command:     "baseten org identity-provider create --kind github-actions",
					},
					{
						Description: "Add an Amazon EKS cluster's issuer.",
						CommandLines: []string{
							"baseten org identity-provider create --kind aws-eks --name prod-eks",
							"--issuer-url https://oidc.eks.us-west-2.amazonaws.com/id/EXAMPLE",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print just the new provider's ID.",
					Command:     "baseten org identity-provider create --kind gcp --jq '.id'",
				},
			},
		},
		{
			Name:        "list",
			Summary:     "List identity providers",
			Description: "List the organization's identity providers.",
			Flags:       OrgIdentityProviderListFlags{},
			Output: &CommandOutput[IdentityProviderList]{
				TextDescription: "Table with columns: ID, NAME, KIND, ISSUER URL, CREATED. When " +
					"none exist, prints \"No identity providers found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List all identity providers.",
						Command:     "baseten org identity-provider list",
					},
					{
						Description: "List only Amazon EKS providers.",
						Command:     "baseten org identity-provider list --kind aws-eks",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the issuer URLs.",
					Command:     "baseten org identity-provider list --jq '.items[].predefined.issuer_url'",
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
					Command:     "baseten org identity-provider describe --name github-actions --jq '.predefined.issuer_url'",
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

	Kind        string `flag:"kind" desc:"Which provider. Can't be changed." enum:"github-actions,gcp,baseten,aws-iam,aws-eks,azure-managed-identity-v1,azure-managed-identity-v2,azure-aks" required:"true"`
	Name        string `flag:"name" desc:"Name, unique in the organization. Defaults to the provider's display name, so pass one when adding a second issuer of the same provider."`
	IssuerURL   string `flag:"issuer-url" desc:"Your issuer, for aws-iam, aws-eks, azure-managed-identity-v1, azure-managed-identity-v2, and azure-aks: the exact iss claim of your tokens. Can't be passed for other providers, or changed."`
	Description string `flag:"description" desc:"Optional description."`
}

type OrgIdentityProviderListFlags struct {
	CommandFlags

	Kind string `flag:"kind" desc:"Only list providers of this kind." enum:"github-actions,gcp,baseten,aws-iam,aws-eks,azure-managed-identity-v1,azure-managed-identity-v2,azure-aks"`
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
