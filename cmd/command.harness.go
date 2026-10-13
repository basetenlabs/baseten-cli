package cmd

import (
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

const harnessPreRelease = "PRE-RELEASE: Harness commands are not GA yet and support only macOS and Linux for now. " +
	"Their arguments, flags, and output may change.\n\n"

var commandHarness = Command{
	Name:        "harness",
	Summary:     "Configure Baseten harness integrations (PRE-RELEASE)",
	Description: harnessPreRelease + "Configure Claude Code, Codex (CLI or ChatGPT desktop app), OpenCode (CLI or desktop app), and Pi to use Baseten routes.",
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
				"The routes API key is created on first setup and reused afterward. Setup records its team and options so 'harness sync' can replay them. Restart the harness after setup. " +
				"For Codex, setup also signs out of OpenAI/ChatGPT and disables ChatGPT login while the Baseten harness is configured, and restarts Codex's background server if one is running, asking first when Codex sessions are attached to it. " +
				"For OpenCode, setup writes the original settings format, which OpenCode 1 and 2 both read, unless the file already has OpenCode 2's native settings, in which case it writes those.",
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
				"Show the installed versions, configuration path, and configured routes of each harness. " +
				"A harness installed both as a CLI and as a desktop app lists both versions; they share one configuration file. " +
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
			Name:    "sync",
			Summary: "Refresh configured harnesses with their recorded setup (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Reapply the setup of every harness configured on this machine, using the team, location, and options recorded by its last 'harness setup'. " +
				"Sync takes no harness selection: to change what is configured, run 'harness setup' or 'harness teardown'. " +
				"Stored routes the team has removed fall back to their defaults with a warning. Only harnesses whose record matches the current profile are synced, " +
				"and a machine with no record needs 'harness setup' first.\n\n" +
				"When nothing changes, sync does nothing and exits 0, so it is safe to run from a schedule; pass --yes to skip the prompt when changes are found. " +
				"For Codex, sync restarts the app-server daemon like setup does, asking first when sessions are attached unless --yes is passed; an in-progress turn is interrupted, " +
				"and thread history is preserved. A declined or failed restart is retried on the next sync.",
			Flags: HarnessSyncFlags{},
			Output: &CommandOutput[HarnessPlanList]{
				TextDescription: "The refreshed configuration of each harness, like setup. Use --dry-run to preview, and --verbose for per-harness details.",
				Examples: []CommandExample{
					{
						Description: "Refresh every configured harness, as a scheduled run does.",
						Command:     "baseten harness sync --yes",
					},
					{
						Description: "Preview what a sync would change.",
						Command:     "baseten harness sync --dry-run",
					},
				},
				JQExample: CommandExample{
					Description: "Print the configuration files a sync would change.",
					Command:     "baseten harness sync --dry-run --jq '.items[] | select(.changed) | .config'",
				},
			},
		},
		{
			Name:    "usage",
			Summary: "Show your routes spend this month in one of your teams (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Show what you've spent on routes this UTC month so far, the period monthly spend limits apply to, in " +
				"one of your teams: your spend there against your limit there, a chart of spend per day, and each " +
				"model's spend. --team picks the team, and is needed unless you're in only one.\n\n" +
				"This is the same spend that spend limits are checked against, and it can lag by up to 15 minutes. " +
				"Model API costs use your prices and include tool calls. OpenAI, Anthropic, and xAI costs estimate what " +
				"those providers charge and are not Baseten charges. Vertex and OpenAI-compatible usage isn't included.\n\n" +
				"For other windows, filters, token counts, or other users' usage, use 'baseten route usage'. For " +
				"machine-readable streaming, prefer --output jsonl over --output json.",
			// TODO: Revisit this description, and null-cost handling in
			// internal/cmd/command.harness_usage.go, as server-side usage changes:
			// which providers are included (Vertex and OpenAI-compatible aren't
			// yet), whether costs are always present, and the 15-minute lag.
			Flags: HarnessUsageFlags{},
			Output: &CommandOutput[managementapi.RoutesUsageBucket]{
				JSONArrayStreamed: true,
				TextDescription: "The month and when limits reset go to stderr. Then a line with the team, what you spent " +
					"there this month and your limit there, with the team's default when yours differs from it, ending in " +
					"\"limit reached\" or \"team limit reached\" when your requests there are rejected; on a terminal, a " +
					"chart of spend per day per model from the first of the month; and a line per model with its spend and " +
					"share, most expensive first. A cost of \"-\" means some of that usage couldn't be priced. With no " +
					"teams, prints \"No usage this month.\" to stderr. For a past month, the team line is what you " +
					"spent there that month, with no limit, since limits are per month.",
				JSONDescription: "One record per UTC day, as returned by the API: its date and the per-team, per-model usage " +
					"in results, including days with no usage. cost_usd values are exact decimal strings.",
				Examples: []CommandExample{
					{
						Description: "Show your spend this month in your team.",
						Command:     "baseten harness usage",
					},
					{
						Description: "Show your spend this month in one of your teams.",
						Command:     "baseten harness usage --team <team>",
					},
				},
				JQExample: CommandExample{
					Description: "Stream each day's cost per team and model as a JSONL stream.",
					Command:     "baseten harness usage --output jsonl --jq '.results[] | {team_id, model, cost_usd}'",
				},
			},
		},
		{
			Name:    "teardown",
			Summary: "Remove Baseten harness settings (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Remove the Baseten integration settings so the harness's own defaults apply. Previous values are not restored, " +
				"and unrelated settings are kept. Without --harness, removes every integration configured at its default path.\n\n" +
				"Teardown also deletes the routes API key the removed harnesses use, once no other harness on this machine uses it, " +
				"and forgets the recorded setup so 'harness sync' no longer refreshes the removed harnesses. " +
				"It works after the harness is uninstalled. For Codex, teardown re-enables ChatGPT login and restarts Codex's background server if one is running, asking first when Codex sessions are attached to it. Run 'codex login' to sign back in.",
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

// HarnessInstall is one copy of a harness found on this machine.
type HarnessInstall struct {
	// Kind is "CLI" or "Desktop".
	Kind    string `json:"kind"`
	Binary  string `json:"binary"`
	Version string `json:"version"`
}

// HarnessStatus is the local integration state of one harness.
type HarnessStatus struct {
	Harness   string `json:"harness"`
	Config    string `json:"config"`
	Installed bool   `json:"installed"`
	// Installs lists every copy of the harness found, the one on PATH first.
	// They all share the configuration file at Config.
	Installs        []HarnessInstall `json:"installs,omitempty"`
	State           string           `json:"state"`
	DefaultRoute    string           `json:"default_route,omitempty"`
	BackgroundRoute string           `json:"background_route,omitempty"`
	Routes          []HarnessRoute   `json:"routes,omitempty"`
	ManagedSettings []string         `json:"managed_settings,omitempty"`
}

// HarnessStatusList is the JSON output of `baseten harness status`.
type HarnessStatusList struct {
	Items []HarnessStatus `json:"items"`
}

// HarnessUsageFlags are the flags for `baseten harness usage`.
type HarnessUsageFlags struct {
	CommandFlags

	Team  string `flag:"team" desc:"Team name or ID to show. Needed unless you're in only one team."`
	Month string `flag:"month" desc:"A past month to show instead of this one, as YYYY-MM."`
}

// HarnessFlags selects the harnesses a command applies to.
type HarnessFlags struct {
	Harness   []string `flag:"harness" desc:"Harness to apply to. May be repeated." enum:"claude-code,codex,opencode,pi"`
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
	Team            string `flag:"team" desc:"Team name or ID whose routes to use. Needed unless there is only one team. Run 'baseten org team list' to see teams."`
	KeyName         string `flag:"key-name" desc:"Name of the routes API key created in Baseten. Defaults to baseten-harness-<hostname>, numbered (-2, -3, ...) if one of your keys has that name."`
	Route           string `flag:"route" desc:"Default route. Defaults to the first route the harness lists."`
	BackgroundRoute string `flag:"background-route" desc:"Route for lightweight background tasks (Claude Code and OpenCode). Defaults to deepseek-ai/DeepSeek-V4.1-Flash."`
	SubagentRoute   string `flag:"subagent-route" desc:"Route for subagents (Claude Code and OpenCode). Defaults to the harness's own setting."`
	FallbackRoute   string `flag:"fallback-route" desc:"Route to fall back to when the default is unavailable (Claude Code). Defaults to the default route."`
	DryRun          bool   `flag:"dry-run" desc:"Preview the configuration without changing files or creating an API key."`
	Yes             bool   `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// HarnessSyncFlags are the flags for `baseten harness sync`.
type HarnessSyncFlags struct {
	CommandFlags
	DryRun bool `flag:"dry-run" desc:"Preview the refresh without changing files or creating an API key."`
	Yes    bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// HarnessTeardownFlags are the flags for `baseten harness teardown`.
type HarnessTeardownFlags struct {
	CommandFlags
	HarnessFlags
	DryRun bool `flag:"dry-run" desc:"Preview removal without changing files."`
	Yes    bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}
