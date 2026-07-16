package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

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
					Description: "Show a billing usage summary for the organization, broken down into " +
						"dedicated deployments, model APIs, and training. Pass --since (relative " +
						"duration, e.g. 7d, 24h) for a sliding window ending now, or --start and --end " +
						"together for an explicit ISO 8601 range. The two modes are mutually exclusive. The " +
						"range cannot exceed 31 days and cannot start before 2026-01-01 UTC. Defaults " +
						"to --since 7d.",
					Flags: OrgBillingUsageFlags{},
					Output: &CommandOutput[managementapi.UsageSummary]{
						TextDescription: "The resolved window on stderr, then a table on stdout with one row " +
							"per category present (Dedicated, Model APIs, Training), plus an \"All\" total row " +
							"when more than one category is present, with columns CATEGORY, MINUTES, TOTAL, " +
							"CREDITS, SUBTOTAL. Costs are in USD; SUBTOTAL is the net cost after credits. Prints " +
							"\"No usage in the selected window.\" to stderr when every category is absent.",
						JSONDescription: "The usage summary: optional dedicated_usage, model_apis_usage, and " +
							"training_usage objects, each with total/credits_used/subtotal costs and a " +
							"per-resource breakdown whose items each carry an optional daily series.",
						Examples: []CommandExample{
							{
								Description: "Show usage over the last 7 days (default).",
								Command:     "baseten org billing usage",
							},
							{
								Description: "Show usage over the last 30 days.",
								Command:     "baseten org billing usage --since 30d",
							},
							{
								Description: "Show usage over an explicit ISO 8601 range.",
								Command:     "baseten org billing usage --start 2026-05-01 --end 2026-05-08",
							},
						},
						JQExample: CommandExample{
							Description: "Print the total cost of model API usage.",
							Command:     "baseten org billing usage --jq '.model_apis_usage.total'",
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
		{
			Name:        "team",
			Summary:     "View teams",
			Description: "List and inspect the teams in the organization.",
			Children: []Command{
				{
					Name:        "describe",
					Summary:     "Describe a team",
					Description: "Describe a single team by name or ID.",
					Flags:       OrgTeamDescribeFlags{},
					Output: &CommandOutput[managementapi.Team]{
						TextDescription: "Field-per-line summary of the team.",
						Examples: []CommandExample{
							{
								Description: "Describe a team by ID or name.",
								Command:     "baseten org team describe --team-id <team>",
							},
						},
						JQExample: CommandExample{
							Description: "Print the team's name.",
							Command:     "baseten org team describe --team-id <team> --jq '.name'",
						},
					},
				},
				{
					Name:        "list",
					Summary:     "List teams",
					Description: "List the teams in the organization.",
					Flags:       OrgTeamListFlags{},
					Output: &CommandOutput[managementapi.Teams]{
						TextDescription: "Table with columns: ID, NAME, DEFAULT, CREATED. When no teams " +
							"exist, prints \"No teams found.\" to stderr.",
						Examples: []CommandExample{
							{
								Description: "List all teams in the org.",
								Command:     "baseten org team list",
							},
						},
						JQExample: CommandExample{
							Description: "Print just the team names.",
							Command:     "baseten org team list --jq '.teams[].name'",
						},
					},
				},
			},
		},
		{
			Name:        "user",
			Summary:     "View users",
			Description: "List and inspect the users in the organization.",
			Children: []Command{
				{
					Name:        "describe",
					Summary:     "Describe a user",
					Description: "Describe a single user by ID. Pass 'me' for the authenticated user.",
					Flags:       OrgUserDescribeFlags{},
					Output: &CommandOutput[managementapi.UserInfo]{
						TextDescription: "Field-per-line summary of the user.",
						Examples: []CommandExample{
							{
								Description: "Describe the authenticated user.",
								Command:     "baseten org user describe --user-id me",
							},
							{
								Description: "Describe a user by ID.",
								Command:     "baseten org user describe --user-id <user-id>",
							},
						},
						JQExample: CommandExample{
							Description: "Print the user's email.",
							Command:     "baseten org user describe --user-id me --jq '.email'",
						},
					},
				},
				{
					Name:        "list",
					Summary:     "List users",
					Description: "List the users in the organization.",
					Flags:       OrgUserListFlags{},
					Output: &CommandOutput[OrgUserList]{
						TextDescription: "Table with columns: USER ID, EMAIL, NAME. When no users " +
							"exist, prints \"No users found.\" to stderr.",
						Examples: []CommandExample{
							{
								Description: "List all users in the org.",
								Command:     "baseten org user list",
							},
						},
						JQExample: CommandExample{
							Description: "Print just the user emails.",
							Command:     "baseten org user list --jq '.items[].email'",
						},
					},
				},
			},
		},
		{
			Name:    "audit-logs",
			Summary: "List audit-log entries",
			Description: "List audit-log entries for the workspace, newest first.\n\n" +
				"Returns up to --limit entries (default 20) across the full history by default. " +
				"Use --start/--end or --since to scope the time window, and the filter flags " +
				"(--event-type-group, --source, --user-id, --deployment-id, --environment, --search) " +
				"to narrow results.\n\n" +
				"For machine-readable streaming, prefer --output jsonl over --output json.",
			Flags: OrgAuditLogsFlags{},
			Output: &CommandOutput[managementapi.AuditLogEntry]{
				JSONArrayStreamed: true,
				TextDescription: "Table with columns: TIME, ACTOR, EVENT, SOURCE. When no entries " +
					"match, prints \"No audit-log entries found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List the 20 most recent audit-log entries.",
						Command:     "baseten org audit-logs",
					},
					{
						Description: "List deploy and promote events from the UI over the last 7 days.",
						Command:     "baseten org audit-logs --since 7d --event-type-group deployed --event-type-group promoted --source ui",
					},
				},
				JQExample: CommandExample{
					Description: "Stream each entry's event type as a JSONL stream.",
					Command:     "baseten org audit-logs --output jsonl --jq '.event_type'",
				},
			},
		},
	},
}

// OrgUserList is the JSON output of `baseten org user list`: the users
// aggregated across all pages.
type OrgUserList struct {
	Items []managementapi.UserInfo `json:"items"`
}

type OrgTeamListFlags struct {
	CommandFlags
}

type OrgTeamDescribeFlags struct {
	CommandFlags

	TeamID   string `flag:"team-id" desc:"Team ID to describe." oneof:"team-ref"`
	TeamName string `flag:"team-name" desc:"Team name to describe." oneof:"team-ref"`
}

type OrgUserListFlags struct {
	CommandFlags
}

type OrgUserDescribeFlags struct {
	CommandFlags

	UserID    string `flag:"user-id" desc:"User ID to describe. Pass 'me' for the authenticated user." oneof:"user-ref"`
	UserEmail string `flag:"user-email" desc:"Email of the user to describe." oneof:"user-ref"`
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

	Since time.Duration `flag:"since" desc:"Relative window ending now (e.g. 24h, 7d). Used when neither --start nor --end is given. Maximum 31d. Mutually exclusive with --start/--end."`
	Start time.Time     `flag:"start" desc:"Start of the window. Accepts ISO 8601 (e.g. '2026-05-01', '2026-05-01T12:00:00Z'); values without a timezone are interpreted in the local timezone. Requires --end. Mutually exclusive with --since."`
	End   time.Time     `flag:"end" desc:"End of the window. Accepts ISO 8601; values without a timezone are interpreted in the local timezone. Requires --start. Mutually exclusive with --since."`
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

// AuditLogFlags is the shared query flag set for `baseten org audit-logs` and
// `baseten model audit-logs`. Both commands accept the same window, filter, and
// paging flags; only the audit-log scope differs. Unlike the log-query flags,
// there is no time-window default (the full history is queried) and no maximum
// window: results are bounded by --limit.
type AuditLogFlags struct {
	Start time.Time     `flag:"start" desc:"Start of the time window. Accepts ISO 8601 (e.g. '2026-05-14', '2026-05-14T12:00:00', '2026-05-14T12:00:00Z'). Values without a timezone designator are interpreted in the local timezone. Defaults to the beginning of the audit-log history."`
	End   time.Time     `flag:"end" desc:"End of the time window. Accepts ISO 8601; values without a timezone designator are interpreted in the local timezone. Defaults to now."`
	Since time.Duration `flag:"since" desc:"Shortcut for a window from a relative time ago until now. Accepts a Go duration (e.g. '30m', '1h30m') or '<N>d' (e.g. '3d'). Mutually exclusive with --start and --end."`

	Limit int `flag:"limit" desc:"Maximum number of entries to return, paging as needed. Use 0 for no limit (every entry in the window)." default:"20"`

	// PageSize is the per-request fetch size while paging. Hidden; exists so
	// tests can force multiple pages without a full page of entries.
	PageSize int `flag:"page-size" hidden:"true" desc:"Entries fetched per backend request while paging." default:"200"`

	Direction string `flag:"direction" desc:"Sort order by the time the action occurred: 'desc' (newest first) or 'asc' (oldest first)." enum:"asc,desc" default:"desc"`
	Search    string `flag:"search" desc:"Case-insensitive substring matched against resource names and IDs in the entry."`

	EventTypeGroups []string `flag:"event-type-group" desc:"Only return entries whose event type falls in one of these groups. May be repeated. One of: activated-deactivated, api-keys, autoscaling-settings, deleted, deployed, directory-group-management, environment-settings, gateway, instance-type-changed, promoted, replica-terminated, secrets, ssh, user-management, webhook-signing-secrets."`
	Sources         []string `flag:"source" desc:"Only return entries issued from one of these surfaces. May be repeated. One of: ui, api, mcp, other."`
	UserIDs         []string `flag:"user-id" desc:"Only return entries whose acting user is one of these IDs. May be repeated."`
	DeploymentIDs   []string `flag:"deployment-id" desc:"Only return entries referencing one of these model deployment IDs. May be repeated."`
	Environments    []string `flag:"environment" desc:"Only return entries for one of these environments. May be repeated."`
}

// OrgAuditLogsFlags configures `baseten org audit-logs`.
type OrgAuditLogsFlags struct {
	CommandFlags
	AuditLogFlags
}
