package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

const gridPrereleaseNotice = "PRE-RELEASE: Grid commands are not GA yet and support only macOS and Linux for now. " +
	"Their arguments, flags, and output may change.\n\n"

var commandGrid = Command{
	Name:        "grid",
	Summary:     "Manage Baseten Grid (PRE-RELEASE)",
	Description: "Configure harness integrations and manage routes with Baseten Grid. These commands are pre-release; their arguments, flags, and output may change.",
	Children: []Command{
		{
			Name:    "setup",
			Summary: "Set up harness authentication and configuration (PRE-RELEASE)",
			Description: gridPrereleaseNotice +
				"Configure installed harnesses with your team's routes and a Grid API key.\n\n" +
				"Setup offers a picker of installed harnesses when --harness is omitted. Repeat --harness to select several. " +
				"The team's routes with model metadata are added to each harness's model picker, replacing it. Claude Code lists the routes that serve " +
				"the Anthropic Messages API, and Codex lists the routes that serve the OpenAI Responses API. The first listed route is the primary route unless --primary-route is set.\n\n" +
				"Setup overwrites the harness's integration settings without saving their previous values. Running it again refreshes them. " +
				"The Grid API key is created on first setup and reused afterward. With --use-session-credential, the session's API key is installed instead and never created or deleted by this CLI. " +
				"Setup records its team and options so 'grid sync' can replay them. Restart the harness after setup. " +
				"For Codex, setup also signs out of OpenAI/ChatGPT and disables ChatGPT login while the Baseten harness is configured, and restarts Codex's background server if one is running, asking first when Codex sessions are attached to it. " +
				"For OpenCode, setup writes the original settings format, which OpenCode 1 and 2 both read, unless the file already has OpenCode 2's native settings, in which case it writes those.",
			Flags: GridSetupFlags{},
			Output: &CommandOutput[GridPlanList]{
				TextDescription: "The created key and follow-up commands. Use --dry-run or --verbose for the configuration of each harness, and --verbose for setting names.",
				Examples: []CommandExample{
					{
						Description: "Configure installed harnesses interactively.",
						Command:     "baseten grid setup",
					},
					{
						Description: "Install a pre-minted key from the environment instead of creating one.",
						Command:     "baseten grid setup --harness claude-code --use-session-credential --yes",
					},
					{
						Description: "Preview configuration for one harness.",
						Command:     "baseten grid setup --harness claude-code --primary-route <route-name> --dry-run",
					},
					{
						Description: "Apply a configuration without prompting.",
						CommandLines: []string{
							"baseten grid setup --harness codex",
							"--team <team> --primary-route <route-name> --yes",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print the configuration file paths.",
					Command:     "baseten grid setup --harness claude-code --dry-run --jq '.items[].config'",
				},
			},
		},
		{
			Name:    "sync",
			Summary: "Refresh configured harnesses with their recorded setup (PRE-RELEASE)",
			Description: gridPrereleaseNotice +
				"Reapply the setup of every harness configured on this machine, using the team, location, and options recorded by its last 'grid setup'. " +
				"Sync takes no harness selection: to change what is configured, run 'grid setup' or 'grid teardown'. " +
				"Stored routes the team has removed fall back to their defaults with a warning. Only harnesses whose record matches the current profile are synced, " +
				"and a machine with no record needs 'grid setup' first. Harnesses set up with --use-session-credential re-resolve the session's API key; " +
				"when it is unavailable, sync errors instead of creating a replacement.\n\n" +
				"When nothing changes, sync does nothing and exits 0, so it is safe to run from a schedule; pass --yes to skip the prompt when changes are found. " +
				"For Codex, sync restarts the app-server daemon like setup does, asking first when sessions are attached unless --yes is passed; an in-progress turn is interrupted, " +
				"and thread history is preserved. A declined or failed restart is retried on the next sync.",
			Flags: GridSyncFlags{},
			Output: &CommandOutput[GridPlanList]{
				TextDescription: "The refreshed configuration of each harness, like setup. Use --dry-run to preview, and --verbose for per-harness details.",
				Examples: []CommandExample{
					{
						Description: "Refresh every configured harness, as a scheduled run does.",
						Command:     "baseten grid sync --yes",
					},
					{
						Description: "Preview what a sync would change.",
						Command:     "baseten grid sync --dry-run",
					},
				},
				JQExample: CommandExample{
					Description: "Print the configuration files a sync would change.",
					Command:     "baseten grid sync --dry-run --jq '.items[] | select(.changed) | .config'",
				},
			},
		},
		{
			Name:    "status",
			Summary: "Inspect local harness configuration (PRE-RELEASE)",
			Description: gridPrereleaseNotice +
				"Show the installed versions, configuration path, and configured routes of each harness. " +
				"A harness installed both as a CLI and as a desktop app lists both versions; they share one configuration file. " +
				"Without --harness, shows every harness configured at its default path. Status reads local files only, " +
				"so it works after the harness is uninstalled, but it doesn't check the key or live routes. Rerun setup to refresh routes.",
			Flags: GridStatusFlags{},
			Output: &CommandOutput[GridStatusList]{
				TextDescription: "Local configuration and configured routes. Use --verbose for setting names.",
				Examples: []CommandExample{
					{
						Description: "Inspect Codex configuration.",
						Command:     "baseten grid status --harness codex",
					},
				},
				JQExample: CommandExample{
					Description: "Print each harness's state.",
					Command:     "baseten grid status --jq '.items[] | {harness, state}'",
				},
			},
		},
		{
			Name:    "harness-defaults",
			Summary: "Show team default routes for harnesses (PRE-RELEASE)",
			Description: gridPrereleaseNotice +
				"Show the default routes for each harness. " +
				"Use --harness to narrow the output; without it, every harness is shown. " +
				"Defaults can be changed on app.baseten.co or by API by an admin; this command only reads them.",
			Flags: GridHarnessDefaultsFlags{},
			Output: &CommandOutput[GridHarnessDefaultsList]{
				TextDescription: "Table with columns: HARNESS, PRIMARY, BACKGROUND. " +
					"Each route is followed by its source in parentheses; '-' means no default is set. " +
					"JSON includes the team and each default's full route and source.",
				Examples: []CommandExample{
					{
						Description: "Show every harness's defaults for your only team.",
						Command:     "baseten grid harness-defaults",
					},
					{
						Description: "Show one team's defaults for one harness.",
						Command:     "baseten grid harness-defaults --team Engineering --harness claude-code",
					},
				},
				JQExample: CommandExample{
					Description: "Print the primary route for Claude Code.",
					Command:     "baseten grid harness-defaults --harness claude-code --jq '.items[0].primary.route.name'",
				},
			},
		},
		{
			Name:    "usage",
			Summary: "Show your routes spend in your teams, this month by default (PRE-RELEASE)",
			Description: gridPrereleaseNotice +
				"Show what you've spent on routes in each of your teams: your spend there against your limit there, a " +
				"chart of spend per day, and each model's spend. --team narrows it to one team. " +
				"The window is this UTC month so far, the period monthly spend limits apply to, unless " +
				"--start, --end, or --since narrows or moves it; the limit is only shown for this month so far.\n\n" +
				"This is the same spend that spend limits are checked against, and it can lag by up to 15 minutes. " +
				"Model API costs use your prices and include tool calls. OpenAI, Anthropic, and xAI costs estimate what " +
				"those providers charge and are not Baseten charges. Vertex and OpenAI-compatible usage isn't included.\n\n" +
				"For filters, token counts, or other users' usage, use 'baseten grid route usage'. For " +
				"machine-readable streaming, prefer --output jsonl over --output json.",
			// TODO: Revisit this description, and null-cost handling in
			// internal/cmd/command.grid_usage.go, as server-side usage changes:
			// which providers are included (Vertex and OpenAI-compatible aren't
			// yet), whether costs are always present, and the 15-minute lag.
			Flags: GridUsageFlags{},
			Output: &CommandOutput[managementapi.GridRouteUsageBucket]{
				JSONArrayStreamed: true,
				TextDescription: "The month and when limits reset go to stderr. Then, for each team, a line with the team, what you spent " +
					"there this month and your limit there, with the team's default when yours differs from it, ending in " +
					"\"limit reached\" or \"team limit reached\" when your requests there are rejected; on a terminal, a " +
					"chart of spend per day per model from the first of the month; and a line per model with its spend and " +
					"share, most expensive first. A cost of \"-\" means some of that usage couldn't be priced. With no " +
					"teams, prints \"No usage this month.\" to stderr. For a window other than this month so far, the " +
					"month line is the window instead, and the team line is what you spent there in it, with no limit.",
				JSONDescription: "One record per UTC day, as returned by the API: its date and the per-team, per-model usage " +
					"in results, including days with no usage. cost_usd values are exact decimal strings.",
				Examples: []CommandExample{
					{
						Description: "Show your spend this month in each of your teams.",
						Command:     "baseten grid usage",
					},
					{
						Description: "Show your spend this month in one of your teams.",
						Command:     "baseten grid usage --team <team>",
					},
					{
						Description: "Show your spend over the last week.",
						Command:     "baseten grid usage --since 7d",
					},
				},
				JQExample: CommandExample{
					Description: "Stream each day's cost per team and model as a JSONL stream.",
					Command:     "baseten grid usage --output jsonl --jq '.results[] | {team_id, model, cost_usd}'",
				},
			},
		},
		{
			Name:    "teardown",
			Summary: "Remove Baseten harness settings (PRE-RELEASE)",
			Description: gridPrereleaseNotice +
				"Remove the Baseten integration settings so the harness's own defaults apply. Previous values are not restored, " +
				"and unrelated settings are kept. Without --harness, removes every integration configured at its default path.\n\n" +
				"Teardown also deletes the Grid API key the removed harnesses use, once no other harness on this machine uses it, " +
				"except a key installed with --use-session-credential, whose lifecycle this CLI does not own. " +
				"It forgets the recorded setup so 'grid sync' no longer refreshes the removed harnesses. " +
				"It works after the harness is uninstalled. For Codex, teardown re-enables ChatGPT login and restarts Codex's background server if one is running, asking first when Codex sessions are attached to it. Run 'codex login' to sign back in.",
			Flags: GridTeardownFlags{},
			Output: &CommandOutput[GridPlanList]{
				TextDescription: "The settings removed from each harness. Use --verbose for configuration paths and setting names.",
				Examples: []CommandExample{
					{
						Description: "Preview removal.",
						Command:     "baseten grid teardown --harness codex --dry-run",
					},
					{
						Description: "Remove integration settings without prompting.",
						Command:     "baseten grid teardown --harness codex --yes",
					},
				},
				JQExample: CommandExample{
					Description: "List the settings teardown would remove.",
					Command:     "baseten grid teardown --dry-run --jq '.items[].settings'",
				},
			},
		},
		commandGridRoute,
		commandGridTeamSettings,
		commandGridUserSettings,
		commandGridConnection,
		commandGridClassifier,
	},
}

var commandGridClassifier = Command{
	Name:    "classifier",
	Summary: "Discover managed classifiers (PRE-RELEASE)",
	Children: []Command{{
		Name:        "list",
		Summary:     "List classifiers available to your organization (PRE-RELEASE)",
		Description: gridPrereleaseNotice + "Classifiers are managed by Baseten. Use their IDs when creating or updating classifier-backed routes.",
		Flags:       GridClassifierListFlags{},
		Output: &CommandOutput[GridClassifierList]{
			TextDescription: "Table with ID and NAME columns. Lists all pages; an empty catalog prints a message to stderr.",
			Examples: []CommandExample{{
				Description: "List available classifiers.",
				Command:     "baseten grid classifier list",
			}},
			JQExample: CommandExample{
				Description: "Print classifier IDs.",
				Command:     "baseten grid classifier list --jq '.items[].id'",
			},
		},
	}},
}

type GridClassifierList struct {
	Items []managementapi.GridClassifier `json:"items"`
}

type GridClassifierListFlags struct{ CommandFlags }

const gridTeamDescription = "Omit --team to let the API select your only team; specify a team when you belong to several."

var commandGridTeamSettings = Command{
	Name:    "team-settings",
	Summary: "Read and manage team Grid settings (PRE-RELEASE)",
	Children: []Command{
		{
			Name:        "describe",
			Summary:     "Show a team's harness defaults and spend limits (PRE-RELEASE)",
			Description: gridPrereleaseNotice + gridTeamDescription,
			Flags:       GridTeamSettingsDescribeFlags{},
			Output: &CommandOutput[managementapi.GridTeamSettings]{
				TextDescription: "Team ID and name, monthly spend limits, and a table of harness default routes and their sources.",
				Examples: []CommandExample{{
					Description: "Show your only team's settings.",
					Command:     "baseten grid team-settings describe",
				}},
				JQExample: CommandExample{
					Description: "Print the harness defaults.",
					Command:     "baseten grid team-settings describe --jq '.harness_defaults'",
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update team harness defaults and spend limits (PRE-RELEASE)",
			Description: gridPrereleaseNotice + "Changes only supplied fields. Use --harness with --primary-route or --background-route to set route IDs; pass null to clear a role. " +
				"--clear-harness-defaults clears every harness's configured defaults. Spend limits are exact USD amounts; null removes a limit, and --clear-spend-limit removes both. " +
				"Requires team admin; changing the team-wide limit requires organization admin. " + gridTeamDescription,
			Flags: GridTeamSettingsUpdateFlags{},
			Output: &CommandOutput[managementapi.GridTeamSettings]{
				TextDescription: "The updated team settings, in the same format as describe.",
				Examples: []CommandExample{
					{
						Description: "Set the default monthly limit per member.",
						Command:     "baseten grid team-settings update --per-member-monthly-limit-usd 100",
					},
					{
						Description: "Set Codex's primary route.",
						CommandLines: []string{
							"baseten grid team-settings update",
							"--harness codex --primary-route <route-id>",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Clear and print the team-wide limit.",
					CommandLines: []string{
						"baseten grid team-settings update --team-wide-monthly-limit-usd null",
						"--jq '.spend_limit'",
					},
				},
			},
		},
	},
}

var commandGridUserSettings = Command{
	Name:    "user-settings",
	Summary: "Read and manage team members' Grid spend settings (PRE-RELEASE)",
	Children: []Command{
		{
			Name:        "list",
			Summary:     "List team members' spend settings (PRE-RELEASE)",
			Description: gridPrereleaseNotice + "Lists members across teams you administer; use --team to select one. Requires team admin or organization admin.",
			Flags:       GridUserSettingsListFlags{},
			Output: &CommandOutput[GridUserSettingsList]{
				TextDescription: "Table with USER, EMAIL, TEAM ID, TEAM NAME, MONTH SPEND, EFFECTIVE LIMIT, and BLOCKED BY columns. Lists all pages; empty results print a message to stderr.",
				Examples: []CommandExample{{
					Description: "List members' settings in a team.",
					Command:     "baseten grid user-settings list --team <id>",
				}},
				JQExample: CommandExample{
					Description: "Print each member's limits.",
					Command:     "baseten grid user-settings list --jq '.items[].spend_limit'",
				},
			},
		},
		{
			Name:        "describe",
			Summary:     "Show a user's spend settings in a team (PRE-RELEASE)",
			Description: gridPrereleaseNotice + "Defaults to your own settings (--user-id me). Reading other users requires team admin or organization admin. " + gridTeamDescription,
			Flags:       GridUserSettingsDescribeFlags{},
			Output: &CommandOutput[managementapi.GridTeamUserSettings]{
				TextDescription: "User and team identifiers, monthly spend, standing and override limits, effective limit, and blocked status.",
				Examples: []CommandExample{{
					Description: "Show your own settings.",
					Command:     "baseten grid user-settings describe",
				}},
				JQExample: CommandExample{
					Description: "Print your effective monthly limit.",
					Command:     "baseten grid user-settings describe --jq '.spend_limit.effective'",
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a user's spend limits in a team (PRE-RELEASE)",
			Description: gridPrereleaseNotice + "Defaults to --user-id me. Requires team admin or organization admin, even when changing your own limits. " +
				"Omitted limits are unchanged; pass null to clear a limit. Amounts are exact USD strings. " + gridTeamDescription,
			Flags: GridUserSettingsUpdateFlags{},
			Output: &CommandOutput[managementapi.GridTeamUserSettings]{
				TextDescription: "The updated user settings, in the same format as describe.",
				Examples: []CommandExample{{
					Description: "Set a member's standing monthly limit.",
					CommandLines: []string{
						"baseten grid user-settings update --team <id> --user-id <id>",
						"--user-monthly-limit-usd 100",
					},
				}},
				JQExample: CommandExample{
					Description: "Clear and print your current-month override.",
					CommandLines: []string{
						"baseten grid user-settings update --month-override-usd null",
						"--jq '.spend_limit'",
					},
				},
			},
		},
	},
}

type GridTeamFlags struct {
	Team string `flag:"team" desc:"Team name or ID. Omit to use your only team; required when you belong to several."`
}

type GridTeamListFlags struct {
	Team string `flag:"team" desc:"Team name or ID. Omit to list across all accessible teams."`
}

type GridTeamSettingsDescribeFlags struct {
	CommandFlags
	GridTeamFlags
}

type GridTeamSettingsUpdateFlags struct {
	CommandFlags
	GridTeamFlags
	Harness                  string               `flag:"harness" desc:"Harness whose default route IDs to update." enum:"claude-code,codex,opencode,pi"`
	PrimaryRoute             OptionalFlag[string] `flag:"primary-route" desc:"Primary route ID; null clears the configured default. Requires --harness." nullable:"true"`
	BackgroundRoute          OptionalFlag[string] `flag:"background-route" desc:"Background route ID for Claude Code or OpenCode; null clears it. Requires --harness." nullable:"true"`
	ClearHarnessDefaults     bool                 `flag:"clear-harness-defaults" desc:"Clear all configured harness defaults. Cannot be combined with --harness or route flags."`
	ClearSpendLimit          bool                 `flag:"clear-spend-limit" desc:"Clear both team spend limits. Requires organization admin. Cannot be combined with --per-member-monthly-limit-usd or --team-wide-monthly-limit-usd."`
	PerMemberMonthlyLimitUSD OptionalFlag[string] `flag:"per-member-monthly-limit-usd" desc:"Default monthly USD limit per member; null removes it." nullable:"true"`
	TeamWideMonthlyLimitUSD  OptionalFlag[string] `flag:"team-wide-monthly-limit-usd" desc:"Team-wide monthly USD limit; null removes it. Requires organization admin." nullable:"true"`
}

type GridUserSettingsList struct {
	Items []managementapi.GridTeamUserSettings `json:"items"`
}

type GridUserSettingsListFlags struct {
	CommandFlags
	GridTeamListFlags
}

type GridUserSettingsUserFlags struct {
	User string `flag:"user-id" desc:"User ID, or me for yourself." default:"me"`
}

type GridUserSettingsDescribeFlags struct {
	CommandFlags
	GridTeamFlags
	GridUserSettingsUserFlags
}

type GridUserSettingsUpdateFlags struct {
	CommandFlags
	GridTeamFlags
	GridUserSettingsUserFlags
	UserMonthlyLimitUSD OptionalFlag[string] `flag:"user-monthly-limit-usd" desc:"Standing monthly USD limit for this user in the team; null removes it." nullable:"true"`
	MonthOverrideUSD    OptionalFlag[string] `flag:"month-override-usd" desc:"USD limit for the current UTC month; null removes the override." nullable:"true"`
}

var commandGridConnection = Command{
	Name:    "connection",
	Summary: "Manage provider connections (PRE-RELEASE)",
	Children: []Command{
		{
			Name:        "list",
			Summary:     "List provider connections (PRE-RELEASE)",
			Description: gridPrereleaseNotice + "List connections across your teams; use --team to select one.",
			Flags:       GridConnectionListFlags{},
			Output: &CommandOutput[GridConnectionList]{
				TextDescription: "Table with ID, TEAM ID, TEAM NAME, PROVIDER, SECRET ID, and SECRET NAME columns. Lists all pages; secret values are never returned.",
				Examples: []CommandExample{{
					Description: "List connections across your teams.",
					Command:     "baseten grid connection list",
				}},
				JQExample: CommandExample{
					Description: "Print connection IDs.",
					Command:     "baseten grid connection list --jq '.items[].id'",
				},
			},
		},
		{
			Name:        "create",
			Summary:     "Create a provider connection using an existing team secret (PRE-RELEASE)",
			Description: gridPrereleaseNotice + "The secret must belong to the connection's team. Requires team admin. " + gridTeamDescription,
			Flags:       GridConnectionCreateFlags{},
			Output: &CommandOutput[managementapi.GridConnection]{
				TextDescription: "The created connection's ID, team, provider, and secret metadata.",
				Examples: []CommandExample{{
					Description: "Connect Anthropic using a team secret.",
					Command:     "baseten grid connection create --provider anthropic --secret-name <name>",
				}},
				JQExample: CommandExample{
					Description: "Print the new connection ID.",
					CommandLines: []string{
						"baseten grid connection create --provider openai --secret-name <name>",
						"--jq '.id'",
					},
				},
			},
		},
		{
			Name:        "update",
			Summary:     "Point a connection at another team secret (PRE-RELEASE)",
			Description: gridPrereleaseNotice + "Requires team admin. The provider must match the existing connection; provider and owning team cannot change.",
			Flags:       GridConnectionUpdateFlags{},
			Output: &CommandOutput[managementapi.GridConnection]{
				TextDescription: "The updated connection's ID, team, provider, and secret metadata.",
				Examples: []CommandExample{{
					Description: "Change the secret used by a connection.",
					CommandLines: []string{
						"baseten grid connection update --id <id>",
						"--provider anthropic --secret-name <name>",
					},
				}},
				JQExample: CommandExample{
					Description: "Print the updated secret metadata.",
					CommandLines: []string{
						"baseten grid connection update --id <id>",
						"--provider anthropic --secret-name <name> --jq '.config'",
					},
				},
			},
		},
		{
			Name:        "delete",
			Summary:     "Delete a provider connection (PRE-RELEASE)",
			Description: gridPrereleaseNotice + "Requires team admin. Leaves the referenced secret in place. Pass --yes to skip confirmation.",
			Flags:       GridConnectionDeleteFlags{},
			Output: &CommandOutput[managementapi.GridConnectionTombstone]{
				TextDescription: "Confirmation on stderr; JSON contains the deleted connection ID.",
				Examples: []CommandExample{{
					Description: "Delete a connection.",
					Command:     "baseten grid connection delete --id <id> --yes",
				}},
				JQExample: CommandExample{
					Description: "Print the deleted ID.",
					Command:     "baseten grid connection delete --id <id> --yes --jq '.id'",
				},
			},
		},
	},
}

type GridConnectionList struct {
	Items []managementapi.GridConnection `json:"items"`
}

type GridConnectionListFlags struct {
	CommandFlags
	GridTeamListFlags
}

type GridConnectionConfigFlags struct {
	Provider   string `flag:"provider" desc:"Upstream provider." enum:"anthropic,openai,xai" required:"true"`
	SecretName string `flag:"secret-name" desc:"Name of an existing secret in the connection's team." required:"true"`
}

type GridConnectionCreateFlags struct {
	CommandFlags
	GridTeamFlags
	GridConnectionConfigFlags
}

type GridConnectionUpdateFlags struct {
	CommandFlags
	GridConnectionConfigFlags
	ID string `flag:"id" desc:"Connection ID." required:"true"`
}

type GridConnectionDeleteFlags struct {
	CommandFlags
	ID  string `flag:"id" desc:"Connection ID." required:"true"`
	Yes bool   `flag:"yes" desc:"Skip confirmation; required when stdin is not a terminal."`
}

// GridPlan is one configuration file that setup or teardown changes.
// Credentials are never included.
type GridPlan struct {
	Harness          string   `json:"harness"`
	Config           string   `json:"config"`
	Settings         []string `json:"settings"`
	ReplacedSettings []string `json:"replaced_settings,omitempty"`
	Managed          bool     `json:"managed"`
	Changed          bool     `json:"changed"`
}

// GridPlanList is the JSON output of `baseten grid setup` and `baseten grid teardown`.
type GridPlanList struct {
	Items []GridPlan `json:"items"`
	// DeletedAPIKeys lists the prefixes of Grid API keys teardown deleted.
	DeletedAPIKeys []string `json:"deleted_api_keys,omitempty"`
}

// GridHarnessDefault is one harness's default routes.
type GridHarnessDefault struct {
	Harness    string                          `json:"harness"`
	Primary    *managementapi.GridHarnessModel `json:"primary"`
	Background *managementapi.GridHarnessModel `json:"background"`
}

// GridHarnessDefaultsList is the JSON output of `baseten grid harness-defaults`.
type GridHarnessDefaultsList struct {
	TeamId   string               `json:"team_id"`
	TeamName string               `json:"team_name"`
	Items    []GridHarnessDefault `json:"items"`
}

// GridHarnessRoute is a route configured in a harness.
type GridHarnessRoute struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// GridHarnessInstall is one copy of a harness found on this machine.
type GridHarnessInstall struct {
	// Kind is "CLI" or "Desktop".
	Kind    string `json:"kind"`
	Binary  string `json:"binary"`
	Version string `json:"version"`
}

// GridHarnessStatus is the local integration state of one harness.
type GridHarnessStatus struct {
	Harness   string `json:"harness"`
	Config    string `json:"config"`
	Installed bool   `json:"installed"`
	// Installs lists every copy of the harness found, the one on PATH first.
	// They all share the configuration file at Config.
	Installs        []GridHarnessInstall `json:"installs,omitempty"`
	State           string               `json:"state"`
	DefaultRoute    string               `json:"default_route,omitempty"`
	BackgroundRoute string               `json:"background_route,omitempty"`
	Routes          []GridHarnessRoute   `json:"routes,omitempty"`
	ManagedSettings []string             `json:"managed_settings,omitempty"`
}

// GridStatusList is the JSON output of `baseten grid status`.
type GridStatusList struct {
	Items []GridHarnessStatus `json:"items"`
}

// GridUsageFlags are the flags for `baseten grid usage`.
type GridUsageFlags struct {
	CommandFlags

	Team string `flag:"team" desc:"Team name or ID to show. Without it, every team you're in."`

	Start time.Time     `flag:"start" desc:"Start of the range, inclusive, snapped down to its UTC day. ISO 8601, local when no timezone is given. Defaults to the start of the current UTC month."`
	End   time.Time     `flag:"end" desc:"End of the range, exclusive, rounded up to the end of its UTC day. ISO 8601, local when no timezone is given. Defaults to now."`
	Since time.Duration `flag:"since" desc:"Window from a relative time ago until now (e.g. '7d'). Mutually exclusive with --start and --end."`
}

// GridHarnessFlags selects the harnesses a command applies to.
type GridHarnessFlags struct {
	Harness   []string `flag:"harness" desc:"Harness to apply to. May be repeated." enum:"claude-code,codex,opencode,pi"`
	ConfigDir string   `flag:"config-dir" desc:"Harness configuration directory. Requires exactly one --harness. Defaults to the harness's own location."`
}

// GridStatusFlags are the flags for `baseten grid status`.
type GridStatusFlags struct {
	CommandFlags
	GridHarnessFlags
}

// GridSetupFlags are the flags for `baseten grid setup`.
type GridSetupFlags struct {
	CommandFlags
	GridHarnessFlags
	Team                 string `flag:"team" desc:"Team name or ID whose routes to use. Omit to let the API select your only team. Run 'baseten org team list' to see teams."`
	KeyName              string `flag:"key-name" desc:"Name of the Grid API key, if setup creates one. Defaults to baseten-harness-<hostname>-<random suffix>."`
	PrimaryRoute         string `flag:"primary-route" desc:"Primary route. Defaults to the team's default for the harness, else the first route the harness lists."`
	BackgroundRoute      string `flag:"background-route" desc:"Route for lightweight background tasks (Claude Code and OpenCode). Defaults to the team's default for the harness, else deepseek-ai/DeepSeek-V4.1-Flash."`
	SubagentRoute        string `flag:"subagent-route" desc:"Route for subagents (Claude Code and OpenCode). Defaults to the harness's own setting."`
	FallbackRoute        string `flag:"fallback-route" desc:"Route to fall back to when the default is unavailable (Claude Code). Defaults to the primary route."`
	UseSessionCredential bool   `flag:"use-session-credential" desc:"Install the session's API key (BASETEN_API_KEY or an API-key profile) as the harness credential instead of creating a Grid API key. The key is never created, rotated, or deleted by this CLI. Mutually exclusive with --key-name."`
	DryRun               bool   `flag:"dry-run" desc:"Preview the configuration without changing files or creating an API key."`
	Yes                  bool   `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// GridHarnessDefaultsFlags are the flags for `baseten grid harness-defaults`.
type GridHarnessDefaultsFlags struct {
	CommandFlags
	Team    string   `flag:"team" desc:"Team name or ID whose defaults to show. Defaults to your only team; required if you belong to more than one. Run 'baseten org team list' to see teams."`
	Harness []string `flag:"harness" desc:"Harness to show. May be repeated. Defaults to every harness." enum:"claude-code,codex,opencode,pi"`
}

// GridSyncFlags are the flags for `baseten grid sync`.
type GridSyncFlags struct {
	CommandFlags
	DryRun bool `flag:"dry-run" desc:"Preview the refresh without changing files or creating an API key."`
	Yes    bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// GridTeardownFlags are the flags for `baseten grid teardown`.
type GridTeardownFlags struct {
	CommandFlags
	GridHarnessFlags
	DryRun bool `flag:"dry-run" desc:"Preview removal without changing files."`
	Yes    bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}
