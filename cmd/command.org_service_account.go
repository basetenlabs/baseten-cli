package cmd

import (
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// commandOrgServiceAccount groups the `baseten org service-account` subcommands.
var commandOrgServiceAccount = Command{
	Name:    "service-account",
	Summary: "Manage service accounts",
	Description: "Manage service accounts, the identities that workload identity federation " +
		"tokens act as. Trust rules map a workload's tokens to a service account.",
	Children: []Command{
		{
			Name:    "create",
			Summary: "Create a service account",
			Description: "Create a service account. Omit --team for an organization account. " +
				"--type takes the same access types as 'baseten org api-key create', plus " +
				"workspace-invoke-model-apis. --model-id, --model-api-slug and, with workspace-invoke, " +
				"--environment restrict the access; omit them for no restriction. With " +
				"workspace-manage-all, --environment instead adds a restricted environment the " +
				"account may also manage.",
			Flags: OrgServiceAccountCreateFlags{},
			Output: &CommandOutput[managementapi.ServiceAccount]{
				TextDescription: "Prints the new service account's ID on stdout, and " +
					"\"Created service account <name>\" on stderr.",
				Examples: []CommandExample{
					{
						Description: "Create a team account that manages everything in the team.",
						Command:     "baseten org service-account create --name ci-deployer --team <team> --type workspace-manage-all",
					},
					{
						Description: "Create an account that may only call one model's production environment.",
						CommandLines: []string{
							"baseten org service-account create --name invoker --type workspace-invoke",
							"--model-id <model-id> --environment production",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print just the new account's ID.",
					Command:     "baseten org service-account create --name ci --type workspace-manage-all --jq '.id'",
				},
			},
		},
		{
			Name:        "list",
			Summary:     "List service accounts",
			Description: "List the service accounts you can administer.",
			Flags:       OrgServiceAccountListFlags{},
			Output: &CommandOutput[ServiceAccountList]{
				TextDescription: "Table with columns: ID, NAME, TYPE, TEAM (a team ID, or - for an " +
					"organization account), DISABLED, CREATED, LAST USED. When none exist, prints " +
					"\"No service accounts found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List all service accounts.",
						Command:     "baseten org service-account list",
					},
					{
						Description: "List one team's disabled accounts.",
						Command:     "baseten org service-account list --team <team> --disabled true",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the account names.",
					Command:     "baseten org service-account list --jq '.items[].name'",
				},
			},
		},
		{
			Name:        "describe",
			Summary:     "Describe a service account",
			Description: "Describe a service account by name or ID.",
			Flags:       OrgServiceAccountDescribeFlags{},
			Output: &CommandOutput[managementapi.ServiceAccount]{
				TextDescription: "Field-per-line summary of the service account.",
				Examples: []CommandExample{
					{
						Description: "Describe a service account by name.",
						Command:     "baseten org service-account describe --name ci-deployer",
					},
				},
				JQExample: CommandExample{
					Description: "Print the account's access type.",
					Command:     "baseten org service-account describe --name ci-deployer --jq '.preset_permissions.type'",
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a service account",
			Description: "Update a service account's name, description or access. Access is " +
				"replaced as a whole: to change it, pass --type together with every --model-id, " +
				"--environment and --model-api-slug the account should keep. The owning team " +
				"can't change.",
			Flags: OrgServiceAccountUpdateFlags{},
			Output: &CommandOutput[managementapi.ServiceAccount]{
				TextDescription: "Prints \"Updated service account <name>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Rename a service account.",
						Command:     "baseten org service-account update --name ci-deployer --new-name ci",
					},
					{
						Description: "Restrict an account to calling the production environment.",
						Command:     "baseten org service-account update --name invoker --type workspace-invoke --environment production",
					},
				},
				JQExample: CommandExample{
					Description: "Print the account's updated access.",
					Command:     "baseten org service-account update --name ci --type workspace-manage-all --jq '.preset_permissions'",
				},
			},
		},
		{
			Name:    "disable",
			Summary: "Disable a service account",
			Description: "Disable a service account so no token can be exchanged for it. Access " +
				"tokens already issued keep working until they expire. Undo with 'baseten org " +
				"service-account enable'.",
			Flags: OrgServiceAccountDisableFlags{},
			Output: &CommandOutput[managementapi.ServiceAccount]{
				TextDescription: "Prints \"Disabled service account <name>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Disable a service account.",
						Command:     "baseten org service-account disable --name ci-deployer",
					},
				},
				JQExample: CommandExample{
					Description: "Print whether the account is now disabled.",
					Command:     "baseten org service-account disable --name ci-deployer --jq '.disabled'",
				},
			},
		},
		{
			Name:        "enable",
			Summary:     "Enable a service account",
			Description: "Enable a disabled service account again.",
			Flags:       OrgServiceAccountEnableFlags{},
			Output: &CommandOutput[managementapi.ServiceAccount]{
				TextDescription: "Prints \"Enabled service account <name>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Enable a service account.",
						Command:     "baseten org service-account enable --name ci-deployer",
					},
				},
				JQExample: CommandExample{
					Description: "Print whether the account is still disabled.",
					Command:     "baseten org service-account enable --name ci-deployer --jq '.disabled'",
				},
			},
		},
		{
			Name:        "delete",
			Summary:     "Delete a service account",
			Description: "Delete a service account by name or ID.",
			Flags:       OrgServiceAccountDeleteFlags{},
			Output: &CommandOutput[managementapi.DeletedServiceAccount]{
				TextDescription: "Prints \"Deleted service account <id>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Delete a service account by name.",
						Command:     "baseten org service-account delete --name ci-deployer",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the deleted account's ID.",
					Command:     "baseten org service-account delete --name ci-deployer --jq '.id'",
				},
			},
		},
	},
}

// ServiceAccountList is the JSON output of `baseten org service-account list`:
// the accounts aggregated across all pages.
type ServiceAccountList struct {
	Items []managementapi.ServiceAccount `json:"items"`
}

// ServiceAccountRefFlags picks one service account.
type ServiceAccountRefFlags struct {
	ID   string `flag:"id" desc:"Service account ID." oneof:"service-account-ref"`
	Name string `flag:"name" desc:"Service account name." oneof:"service-account-ref"`
}

// ServiceAccountPermissionFlags set a service account's access.
type ServiceAccountPermissionFlags struct {
	ModelIDs      []string `flag:"model-id" desc:"Restrict the account to a model. May be repeated. Only valid with --type workspace-invoke or workspace-export-metrics."`
	Environments  []string `flag:"environment" desc:"With --type workspace-invoke, an environment the account may call; with workspace-manage-all, a restricted environment it may also manage. May be repeated."`
	ModelAPISlugs []string `flag:"model-api-slug" desc:"Restrict the account to a Model API. May be repeated. Only valid with --type workspace-invoke-model-apis."`
}

type OrgServiceAccountCreateFlags struct {
	CommandFlags
	ServiceAccountPermissionFlags

	Name        string `flag:"name" desc:"Name, unique in the organization." required:"true"`
	Type        string `flag:"type" desc:"What the account may do." required:"true" enum:"workspace-export-metrics,workspace-invoke,workspace-invoke-model-apis,workspace-manage-all,workspace-manage-api-keys"`
	Team        string `flag:"team" desc:"Team name or ID that owns the account. Omit for an organization account. Can't be changed. Run 'baseten org team list' to see teams."`
	Description string `flag:"description" desc:"Optional description."`
}

type OrgServiceAccountListFlags struct {
	CommandFlags

	Team     string             `flag:"team" desc:"Only list this team's accounts, by team name or ID."`
	Disabled OptionalFlag[bool] `flag:"disabled" desc:"Only list disabled (true) or enabled (false) accounts."`
}

type OrgServiceAccountDescribeFlags struct {
	CommandFlags
	ServiceAccountRefFlags
}

type OrgServiceAccountUpdateFlags struct {
	CommandFlags
	ServiceAccountRefFlags
	ServiceAccountPermissionFlags

	NewName     string               `flag:"new-name" desc:"New name, unique in the organization."`
	Description OptionalFlag[string] `flag:"description" desc:"New description. Pass an empty string to clear it."`
	Type        string               `flag:"type" desc:"New access type. Required with --model-id, --environment or --model-api-slug." enum:"workspace-export-metrics,workspace-invoke,workspace-invoke-model-apis,workspace-manage-all,workspace-manage-api-keys"`
}

type OrgServiceAccountDisableFlags struct {
	CommandFlags
	ServiceAccountRefFlags
}

type OrgServiceAccountEnableFlags struct {
	CommandFlags
	ServiceAccountRefFlags
}

type OrgServiceAccountDeleteFlags struct {
	CommandFlags
	ServiceAccountRefFlags
}
