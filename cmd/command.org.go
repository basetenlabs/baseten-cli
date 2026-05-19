package cmd

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
				},
				{
					Name:    "create",
					Summary: "Create an API key",
					Description: "Create a new API key. The key value is printed to stdout exactly once " +
						"and cannot be retrieved later; capture or pipe it on creation. --model-id may be " +
						"repeated to scope the key to specific models and is only valid with " +
						"--type workspace-export-metrics or --type workspace-invoke.",
					Flags: OrgAPIKeyCreateFlags{},
				},
				{
					Name:    "delete",
					Summary: "Delete an API key",
					Description: "Delete an API key. Exactly one of --name or --prefix is required: --name " +
						"matches the human-readable name, --prefix matches the leading characters shown in " +
						"`org api-key list`.",
					Flags: OrgAPIKeyDeleteFlags{},
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
				},
				{
					Name:    "set",
					Summary: "Create or update a secret",
					Description: "Create or update a secret. The value is read from stdin (or prompted " +
						"interactively on a TTY). --value is supported but discouraged: it leaks the secret " +
						"into shell history and `ps` output. Pass --team to target a specific team; without " +
						"it the organization's default team is used.",
					Flags: OrgSecretSetFlags{},
				},
				{
					Name:        "delete",
					Summary:     "Delete a secret",
					Description: "Delete a secret by name.",
					Flags:       OrgSecretDeleteFlags{},
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
