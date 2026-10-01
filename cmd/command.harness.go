package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

const harnessPreRelease = "PRE-RELEASE: Harness commands are not GA yet and support only macOS and Linux for now. " +
	"Their arguments, flags, and output may change.\n\n"

var commandHarness = Command{
	Name:        "harness",
	Summary:     "Configure Baseten harness integrations (PRE-RELEASE)",
	Description: harnessPreRelease + "Configure Claude Code, Codex (CLI or ChatGPT desktop app), and OpenCode CLI to use Baseten routes.",
	Children: []Command{
		{
			Name:    "setup",
			Summary: "Set up harness authentication and configuration (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Configure installed harnesses with your team's routes and a routes API key.\n\n" +
				"Setup offers a picker of installed harnesses when --harness is omitted. Repeat --harness to select several. " +
				"The team's routes with model metadata are added to each harness's model picker, replacing it. Claude Code lists the routes that serve " +
				"the Anthropic Messages API, and Codex lists the routes that serve the OpenAI Responses API. The first listed route is the default unless --route is set.\n\n" +
				"Setup overwrites the harness's integration settings without saving their previous values. Running it again refreshes them. " +
				"The routes API key is created on first setup and reused afterward. Restart the harness after setup.",
			Flags: HarnessSetupFlags{},
			Output: &CommandOutput[HarnessPlanList]{
				TextDescription: "The created key and follow-up commands. Use --dry-run or --verbose for the configuration of each harness, and --verbose for setting names.",
				Examples: []CommandExample{
					{
						Description: "Configure installed harnesses interactively.",
						Command:     "baseten harness setup",
					},
					{
						Description: "Preview configuration for one harness.",
						Command:     "baseten harness setup --harness claude-code --route <route-name> --dry-run",
					},
					{
						Description: "Apply a configuration without prompting.",
						CommandLines: []string{
							"baseten harness setup --harness codex",
							"--team <team> --route <route-name> --yes",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print the configuration file paths.",
					Command:     "baseten harness setup --harness claude-code --dry-run --jq '.items[].config'",
				},
			},
		},
		{
			Name:    "status",
			Summary: "Inspect local harness configuration (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Show the installed version, configuration path, and configured routes of each harness. " +
				"Without --harness, shows every harness configured at its default path. Status reads local files only, " +
				"so it works after the harness is uninstalled, but it doesn't check the key or live routes. Rerun setup to refresh routes.",
			Flags: HarnessStatusFlags{},
			Output: &CommandOutput[HarnessStatusList]{
				TextDescription: "Local configuration and configured routes. Use --verbose for setting names.",
				Examples: []CommandExample{
					{
						Description: "Inspect Codex configuration.",
						Command:     "baseten harness status --harness codex",
					},
				},
				JQExample: CommandExample{
					Description: "Print each harness's state.",
					Command:     "baseten harness status --jq '.items[] | {harness, state}'",
				},
			},
		},
		{
			Name:    "usage",
			Summary: "Show your routes spend and tokens, month to date by default (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Show your routes spend and token usage for the month so far, the period monthly spend limits apply to.\n\n" +
				"This is a shortcut for 'baseten route usage --user-id <your user ID> --start <first of this month, UTC>' " +
				"and takes " +
				"the same flags. The only other difference is the text table, which totals each --group-by combination " +
				"over the window instead of listing each day; JSON output is identical. Organization admins can pass " +
				"--user-id for other users; other members only ever see usage from keys they created.\n\n" +
				"Usage comes in whole UTC days: --start is snapped down to its day and --end is rounded up to the end of " +
				"its day. Every bucket in the window is fetched, paging as needed, until --limit buckets are collected.\n\n" +
				"This is the same spend that spend limits are checked against, and it can lag by up to 15 minutes. " +
				"Model API costs use your prices and include tool calls. OpenAI, Anthropic, and xAI costs estimate what " +
				"those providers charge and are not Baseten charges. Vertex and OpenAI-compatible usage isn't included. " +
				"Usage is retained for 92 days.\n\n" +
				"For machine-readable streaming, prefer --output jsonl over --output json.",
			// TODO: Revisit this description, and null-cost handling in
			// internal/cmd/command.harness_usage.go, as server-side usage changes:
			// which providers are included (Vertex and OpenAI-compatible aren't
			// yet), whether costs are always present, the 15-minute lag, and the
			// 92-day retention.
			Flags: HarnessUsageFlags{},
			Output: &CommandOutput[managementapi.RoutesUsageBucket]{
				JSONArrayStreamed: true,
				TextDescription: "Table with one column per --group-by dimension, then INPUT, CACHED, and OUTPUT token " +
					"counts and COST, totaled over the window, most expensive first, followed by an ALL totals row. A cost " +
					"of \"-\" means some of that usage couldn't be priced. The window goes to stderr. With no usage in the " +
					"window, prints \"No usage in the selected window.\" to stderr instead of a table.",
				JSONDescription: "One record per UTC day, as returned by the API: its date and the per-dimension usage in " +
					"results, including days with no usage. cost_usd values are exact decimal strings.",
				Examples: []CommandExample{
					{
						Description: "Show your usage this month so far, by model.",
						Command:     "baseten harness usage",
					},
					{
						Description: "Show usage over the last 7 days by provider.",
						Command:     "baseten harness usage --since 7d --group-by provider",
					},
					{
						Description: "Break August's usage down by user and model for two users (admins).",
						CommandLines: []string{
							"baseten harness usage --start 2026-08-01T00:00:00Z --end 2026-09-01T00:00:00Z",
							"--group-by user --group-by model --user-id <id> --user-id <id>",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Stream each day's cost per model as a JSONL stream.",
					Command:     "baseten harness usage --output jsonl --jq '.results[] | {model, cost_usd}'",
				},
			},
		},
		{
			Name:    "teardown",
			Summary: "Remove Baseten harness settings (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Remove the Baseten integration settings so the harness's own defaults apply. Previous values are not restored, " +
				"and unrelated settings are kept. Without --harness, removes every integration configured at its default path.\n\n" +
				"Teardown also deletes the routes API key the removed harnesses use, once no other harness on this machine uses it. " +
				"It works after the harness is uninstalled.",
			Flags: HarnessTeardownFlags{},
			Output: &CommandOutput[HarnessPlanList]{
				TextDescription: "The settings removed from each harness. Use --verbose for configuration paths and setting names.",
				Examples: []CommandExample{
					{
						Description: "Preview removal.",
						Command:     "baseten harness teardown --harness codex --dry-run",
					},
					{
						Description: "Remove integration settings without prompting.",
						Command:     "baseten harness teardown --harness codex --yes",
					},
				},
				JQExample: CommandExample{
					Description: "List the settings teardown would remove.",
					Command:     "baseten harness teardown --dry-run --jq '.items[].settings'",
				},
			},
		},
	},
}

// HarnessPlan is one configuration file that setup or teardown changes.
// Credentials are never included.
type HarnessPlan struct {
	Harness          string   `json:"harness"`
	Config           string   `json:"config"`
	Settings         []string `json:"settings"`
	ReplacedSettings []string `json:"replaced_settings,omitempty"`
	Managed          bool     `json:"managed"`
	Changed          bool     `json:"changed"`
}

// HarnessPlanList is the JSON output of `baseten harness setup` and `baseten harness teardown`.
type HarnessPlanList struct {
	Items []HarnessPlan `json:"items"`
	// DeletedAPIKeys lists the prefixes of routes API keys teardown deleted.
	DeletedAPIKeys []string `json:"deleted_api_keys,omitempty"`
}

// HarnessRoute is a route configured in a harness.
type HarnessRoute struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// HarnessStatus is the local integration state of one harness.
type HarnessStatus struct {
	Harness         string         `json:"harness"`
	Config          string         `json:"config"`
	Installed       bool           `json:"installed"`
	Version         string         `json:"version"`
	State           string         `json:"state"`
	DefaultRoute    string         `json:"default_route,omitempty"`
	BackgroundRoute string         `json:"background_route,omitempty"`
	Routes          []HarnessRoute `json:"routes,omitempty"`
	ManagedSettings []string       `json:"managed_settings,omitempty"`
}

// HarnessStatusList is the JSON output of `baseten harness status`.
type HarnessStatusList struct {
	Items []HarnessStatus `json:"items"`
}

// HarnessUsageFlags are the flags for `baseten harness usage`.
type HarnessUsageFlags struct {
	CommandFlags

	Start time.Time     `flag:"start" desc:"Start of the range, inclusive, snapped down to its UTC day. ISO 8601, local when no timezone is given. Defaults to the start of the current UTC month."`
	End   time.Time     `flag:"end" desc:"End of the range, exclusive, rounded up to the end of its UTC day. ISO 8601, local when no timezone is given. Defaults to now."`
	Since time.Duration `flag:"since" desc:"Window from a relative time ago until now (e.g. '7d'). Mutually exclusive with --start and --end."`

	UserIDs []string `flag:"user-id" desc:"Only return usage from routes API keys created by these user IDs. May be repeated. Defaults to your own user ID."`

	RouteUsageQueryFlags
}

// HarnessFlags selects the harnesses a command applies to.
type HarnessFlags struct {
	Harness   []string `flag:"harness" desc:"Harness to apply to. May be repeated." enum:"claude-code,codex,opencode"`
	ConfigDir string   `flag:"config-dir" desc:"Harness configuration directory. Requires exactly one --harness. Defaults to the harness's own location."`
}

// HarnessStatusFlags are the flags for `baseten harness status`.
type HarnessStatusFlags struct {
	CommandFlags
	HarnessFlags
}

// HarnessSetupFlags are the flags for `baseten harness setup`.
type HarnessSetupFlags struct {
	CommandFlags
	HarnessFlags
	Team            string `flag:"team" desc:"Team name or ID whose routes to use. Defaults to the organization's default team. Run 'baseten org team list' to see teams."`
	KeyName         string `flag:"key-name" desc:"Name of the routes API key created in Baseten. Defaults to baseten-harness-<hostname>, numbered (-2, -3, ...) if one of your keys has that name."`
	Route           string `flag:"route" desc:"Default route. Defaults to the first route the harness lists."`
	BackgroundRoute string `flag:"background-route" desc:"Route for lightweight background tasks (Claude Code and OpenCode). Defaults to deepseek-ai/DeepSeek-V4.1-Flash."`
	SubagentRoute   string `flag:"subagent-route" desc:"Route for subagents (Claude Code and OpenCode). Defaults to the harness's own setting."`
	FallbackRoute   string `flag:"fallback-route" desc:"Route to fall back to when the default is unavailable (Claude Code). Defaults to the default route."`
	DryRun          bool   `flag:"dry-run" desc:"Preview the configuration without changing files or creating an API key."`
	Yes             bool   `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// HarnessTeardownFlags are the flags for `baseten harness teardown`.
type HarnessTeardownFlags struct {
	CommandFlags
	HarnessFlags
	DryRun bool `flag:"dry-run" desc:"Preview removal without changing files."`
	Yes    bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}
