package cmd

const harnessPreRelease = "PRE-RELEASE: Harness commands are not GA yet and support only macOS for now. " +
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
				"All of the team's routes are added to each harness's model picker, replacing it; the first route is the default unless --route is set.\n\n" +
				"Setup overwrites the harness's integration settings without saving their previous values. Running it again refreshes them. " +
				"The routes API key is created on first setup and reused afterward. Restart the harness after setup.",
			Flags: HarnessSetupFlags{},
			Output: &CommandOutput[HarnessPlanList]{
				TextDescription: "Available routes and the configuration for each harness. Use --verbose for setting names.",
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
			Summary: "Show your routes spend, requests, and tokens for the month (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Show the estimated spend, request count, and token usage of the routes API keys you created, " +
				"across all teams, for one UTC calendar month. Defaults to the current month.\n\n" +
				"Costs are estimates. Model API costs use your prices; OpenAI, Anthropic, and xAI costs estimate what " +
				"those providers charge and are not Baseten charges. Vertex and OpenAI-compatible usage can't be priced, " +
				"so its cost is empty and the total spend leaves it out. Usage is retained for 92 days.",
			Flags: HarnessUsageFlags{},
			Output: &CommandOutput[HarnessUsage]{
				TextDescription: "Total spend, requests, and tokens, then a table with one row per --group-by value: " +
					"REQUESTS, then INPUT, CACHED, and OUTPUT token counts (abbreviated, like 1.2M), and COST. Routes show their display names. A cost of \"-\" means some of that usage " +
					"could not be priced. With no usage in the month, prints a message to stderr instead.",
				JSONDescription: "cost_usd values are exact decimal strings. An item's cost_usd is null when some of its " +
					"usage could not be priced; totals.cost_usd sums the priced usage, and totals.cost_complete is false " +
					"when anything was left out.",
				Examples: []CommandExample{
					{
						Description: "Show this month's usage by model.",
						Command:     "baseten harness usage",
					},
					{
						Description: "Show last month's usage by route.",
						Command:     "baseten harness usage --month 2026-08 --group-by route",
					},
				},
				JQExample: CommandExample{
					Description: "Print this month's total estimated spend.",
					Command:     "baseten harness usage --jq '.totals.cost_usd'",
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

// HarnessUsageCounts are the request and token counts of some routes usage.
type HarnessUsageCounts struct {
	RequestCount        int64 `json:"request_count"`
	InputTokens         int64 `json:"input_tokens"`
	CachedInputTokens   int64 `json:"cached_input_tokens"`
	UncachedInputTokens int64 `json:"uncached_input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
}

// HarnessUsageItem is the month's usage for one --group-by value. The
// dimension fields not being grouped by are omitted.
type HarnessUsageItem struct {
	Model            *string `json:"model,omitempty"`
	Provider         *string `json:"provider,omitempty"`
	RouteID          *string `json:"route_id,omitempty"`
	RouteName        *string `json:"route_name,omitempty"`
	RouteDisplayName *string `json:"route_display_name,omitempty"`
	// CostUSD is null when some of this usage could not be priced.
	CostUSD *string `json:"cost_usd"`
	HarnessUsageCounts
}

// HarnessUsageTotals is the month's usage across every item.
type HarnessUsageTotals struct {
	// CostUSD sums the usage that could be priced.
	CostUSD string `json:"cost_usd"`
	// CostComplete is false when some usage could not be priced and is left
	// out of CostUSD.
	CostComplete bool `json:"cost_complete"`
	HarnessUsageCounts
}

// HarnessUsage is the JSON output of `baseten harness usage`.
type HarnessUsage struct {
	Month string `json:"month"`
	// StartDate is inclusive and EndDate exclusive, both UTC calendar days.
	StartDate string             `json:"start_date"`
	EndDate   string             `json:"end_date"`
	GroupBy   string             `json:"group_by"`
	Totals    HarnessUsageTotals `json:"totals"`
	Items     []HarnessUsageItem `json:"items"`
}

// HarnessUsageFlags are the flags for `baseten harness usage`.
type HarnessUsageFlags struct {
	CommandFlags
	Month   string `flag:"month" desc:"UTC calendar month to show, as YYYY-MM. Defaults to the current month."`
	GroupBy string `flag:"group-by" desc:"Break usage down by model (with its provider), route, or provider." enum:"model,route,provider" default:"model"`
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
	KeyName         string `flag:"key-name" desc:"Name of the routes API key created in Baseten. Defaults to baseten-harness-<hostname>."`
	Route           string `flag:"route" desc:"Default route. Defaults to the team's first route."`
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
