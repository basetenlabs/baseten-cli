package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

var commandOrg = Command{
	Name:        "org",
	Summary:     "Manage organization resources",
	Description: "Manage API keys, secrets, and billing for the active organization.",
	Children: []Command{
		{
			Name:    "api-key",
			Summary: "Manage API keys",
			Children: []Command{
				{
					Name:        "list",
					Summary:     "List API keys",
					Description: "List API keys (metadata only; key values are never returned).",
					Flags:       OrgAPIKeyListFlags{},
					Output: &CommandOutput[managementapi.APIKeys]{
						TextDescription: "Table with columns: NAME, KEY (prefix + ****), TYPE, TEAM. When no " +
							"keys exist, prints \"No API keys found.\" to stderr.",
						Examples: []CommandExample{
							{
								Description: "List all API keys in the org.",
								Command:     "baseten org api-key list",
							},
						},
						JQExample: CommandExample{
							Description: "Print just the prefixes of personal keys.",
							Command:     `baseten org api-key list --jq '.keys[] | select(.type == "PERSONAL") | .prefix'`,
						},
					},
				},
				{
					Name:    "create",
					Summary: "Create an API key",
					Description: "Create a new API key. The key value is printed to stdout exactly once " +
						"and cannot be retrieved later; capture or pipe it on creation. --model-id may be " +
						"repeated to scope the key to specific models and is only valid with " +
						"--type workspace-export-metrics or --type workspace-invoke.",
					Flags: OrgAPIKeyCreateFlags{},
					Output: &CommandOutput[managementapi.APIKey]{
						TextDescription: "Prints the raw API key value on stdout (one line). Also prints " +
							"\"Save this key now. It will not be shown again.\" to stderr.",
						Examples: []CommandExample{
							{
								Description: "Create a personal API key.",
								Command:     "baseten org api-key create --type personal --name <label>",
							},
							{
								Description: "Create a workspace-invoke key scoped to specific models.",
								Command:     "baseten org api-key create --type workspace-invoke --model-id <id-1> --model-id <id-2>",
							},
						},
						JQExample: CommandExample{
							Description: "Print just the raw key value.",
							Command:     "baseten org api-key create --type personal --jq '.api_key'",
						},
					},
				},
				{
					Name:    "delete",
					Summary: "Delete an API key",
					Description: "Delete an API key. Exactly one of --name or --prefix is required: --name " +
						"matches the human-readable name, --prefix matches the leading characters shown in " +
						"`org api-key list`.",
					Flags: OrgAPIKeyDeleteFlags{},
					Output: &CommandOutput[managementapi.APIKeyTombstone]{
						TextDescription: "Prints \"Deleted API key <prefix>\" to stderr on success; no " +
							"stdout output.",
						Examples: []CommandExample{
							{
								Description: "Delete an API key by name.",
								Command:     "baseten org api-key delete --name <label>",
							},
							{
								Description: "Delete by visible prefix.",
								Command:     "baseten org api-key delete --prefix <prefix>",
							},
						},
						JQExample: CommandExample{
							Description: "Print just the deleted key's prefix.",
							Command:     "baseten org api-key delete --name <label> --jq '.prefix'",
						},
					},
				},
			},
		},
		{
			Name:    "billing",
			Summary: "View billing information",
			Children: []Command{
				{
					Name:    "usage",
					Summary: "Show billing usage summary",
					Description: "Show a billing usage summary for the organization. Pass --since (relative " +
						"duration, e.g. 7d, 24h) for a sliding window ending now, or --start and --end " +
						"together for an explicit ISO 8601 range. The two modes are mutually exclusive. The " +
						"range cannot exceed 31 days. Defaults to --since 7d.",
					Flags: OrgBillingUsageFlags{},
					Output: &CommandOutput[JSONAny]{
						TextDescription: "Not yet implemented. The output shape is TBD.",
						Examples: []CommandExample{
							{
								Description: "Show usage over the last 7 days (default).",
								Command:     "baseten org billing usage",
							},
							{
								Description: "Show usage over an explicit ISO 8601 range.",
								Command:     "baseten org billing usage --start 2026-05-01 --end 2026-05-08",
							},
						},
						JQExample: CommandExample{
							Description: "Print a top-level total (shape TBD).",
							Command:     "baseten org billing usage --jq '.total'",
						},
					},
				},
			},
		},
		{
			Name:    "secret",
			Summary: "Manage secrets",
			Children: []Command{
				{
					Name:        "list",
					Summary:     "List secrets",
					Description: "List secrets (metadata only; values are never returned).",
					Flags:       OrgSecretListFlags{},
					Output: &CommandOutput[managementapi.Secrets]{
						TextDescription: "Table with columns: NAME, TEAM, CREATED. When no secrets exist, " +
							"prints \"No secrets found.\" to stderr.",
						Examples: []CommandExample{
							{
								Description: "List secrets across all accessible teams.",
								Command:     "baseten org secret list",
							},
							{
								Description: "List secrets in a specific team.",
								Command:     "baseten org secret list --team <team>",
							},
						},
						JQExample: CommandExample{
							Description: "Print just the secret names.",
							Command:     "baseten org secret list --jq '.secrets[].name'",
						},
					},
				},
				{
					Name:    "set",
					Summary: "Create or update a secret",
					Description: "Create or update a secret. The value is read from stdin (or prompted " +
						"interactively on a TTY). --value is supported but discouraged: it leaks the secret " +
						"into shell history and `ps` output. Pass --team to target a specific team; without " +
						"it the organization's default team is used.",
					Flags: OrgSecretSetFlags{},
					Output: &CommandOutput[managementapi.Secret]{
						TextDescription: "Prints \"Set secret <name>\" to stderr on success; no stdout output.",
						Examples: []CommandExample{
							{
								Description: "Set a secret by piping its value via stdin.",
								Command:     "echo $TOKEN | baseten org secret set --name <name>",
							},
							{
								Description: "Set a secret scoped to a specific team.",
								Command:     "echo $TOKEN | baseten org secret set --name <name> --team <team>",
							},
						},
						JQExample: CommandExample{
							Description: "Print the secret's team.",
							Command:     "echo $TOKEN | baseten org secret set --name <name> --jq '.team_name'",
						},
					},
				},
				{
					Name:        "delete",
					Summary:     "Delete a secret",
					Description: "Delete a secret by name.",
					Flags:       OrgSecretDeleteFlags{},
					Output: &CommandOutput[managementapi.SecretTombstone]{
						TextDescription: "Prints \"Deleted secret <name>\" to stderr on success; no stdout " +
							"output.",
						Examples: []CommandExample{
							{
								Description: "Delete a secret by name.",
								Command:     "baseten org secret delete --name <name>",
							},
						},
						JQExample: CommandExample{
							Description: "Print just the deleted secret name.",
							Command:     "baseten org secret delete --name <name> --jq '.name'",
						},
					},
				},
			},
		},
	},
}

type OrgAPIKeyListFlags struct {
	CommandFlags
}

type OrgAPIKeyCreateFlags struct {
	CommandFlags

	Type     string   `flag:"type" desc:"API key category." required:"true" enum:"personal,workspace-export-metrics,workspace-invoke,workspace-manage-all"`
	Name     string   `flag:"name" desc:"Optional human-readable name for the key."`
	ModelIDs []string `flag:"model-id" desc:"Restrict the key to a specific model. May be repeated. Only valid with --type workspace-export-metrics or workspace-invoke."`
	Team     string   `flag:"team" desc:"Team name or ID to create the key in. Defaults to the organization's default team."`
}

type OrgAPIKeyDeleteFlags struct {
	CommandFlags

	Name   string `flag:"name" desc:"Human-readable name of the API key to delete." oneof:"identifier"`
	Prefix string `flag:"prefix" desc:"Prefix of the API key to delete (as shown in list)." oneof:"identifier"`
}

type OrgBillingUsageFlags struct {
	CommandFlags

	Since string `flag:"since" desc:"Relative window ending now, as a Go duration (e.g. 7d, 24h). Mutually exclusive with --start/--end." default:"7d"`
	Start string `flag:"start" desc:"Start of the window (ISO 8601). Requires --end. Mutually exclusive with --since."`
	End   string `flag:"end" desc:"End of the window (ISO 8601). Requires --start. Mutually exclusive with --since."`
}

type OrgSecretListFlags struct {
	CommandFlags

	Team string `flag:"team" desc:"Filter to a specific team by name or ID. Defaults to all teams the caller belongs to."`
}

type OrgSecretSetFlags struct {
	CommandFlags

	Name  string `flag:"name" desc:"Name of the secret." required:"true"`
	Value string `flag:"value" desc:"Secret value. Discouraged: leaks into shell history and process list. Prefer stdin or prompt."`
	Team  string `flag:"team" desc:"Team name or ID the secret belongs to. Defaults to the organization's default team."`
}

type OrgSecretDeleteFlags struct {
	CommandFlags

	Name string `flag:"name" desc:"Name of the secret to delete." required:"true"`
	Team string `flag:"team" desc:"Team name or ID the secret belongs to. Defaults to the organization's default team."`
}
