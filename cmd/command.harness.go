package cmd

const harnessPreRelease = "PRE-RELEASE: Harness commands are not GA yet. " +
	"Their arguments, flags, and output may change.\n\n"

var commandHarness = Command{
	Name:        "harness",
	Summary:     "Configure Baseten harness integrations (PRE-RELEASE)",
	Description: harnessPreRelease + "Configure Claude Code, Codex CLI, and OpenCode CLI to use Baseten routes.",
	Children: []Command{
		{
			Name:    "setup",
			Summary: "Set up harness authentication and configuration (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Configure compatible harnesses with your team's routes.\n\n" +
				"Setup offers a picker of installed harnesses when --harness is omitted, previews changes, and creates or reuses a team-scoped " +
				"routes API key. Repeat --harness to select multiple integrations. Setup overwrites integration settings without saving previous values; reruns refresh those settings.\n\n" +
				"Pass --team if you belong to multiple teams. All routes are added to the catalog. The first returned route is the default; " +
				"use --route to override it.\n\n" +
				"Setup replaces the complete model picker. Use --dry-run to preview or --yes to skip confirmation. OAuth refresh may update saved login credentials during previews. Restart the harness after setup.",
			Flags: HarnessSetupFlags{},
			Output: &CommandOutput[HarnessPlansResult]{
				TextDescription: "Preview available routes and configuration before setup. Use --verbose for setting names.",
				Examples: []CommandExample{
					{
						Description: "Configure installed harnesses interactively.",
						Command:     "baseten harness setup",
					},
					{
						Description: "Preview configuration for one harness.",
						CommandLines: []string{
							"baseten harness setup --harness claude-code",
							"--team <team> --route <route-name> --dry-run",
						},
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
					Description: "Print the configuration file path.",
					CommandLines: []string{
						"baseten harness setup --harness claude-code",
						"--team <team> --route <route-name> --dry-run --jq '.changes[].config'",
					},
				},
				JSONDescription: "Reports configuration changes as a collection, including for a single file.",
			},
		},
		{
			Name:    "status",
			Summary: "Inspect local harness configuration (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Show the installed version, configuration path, integration settings, and configured routes. " +
				"Inspect configured harnesses at their default paths, or pass --harness to narrow the selection. Status remains available " +
				"after an upgrade or executable removal.",
			Flags: HarnessStatusFlags{},
			Output: &CommandOutput[HarnessStatusesResult]{
				TextDescription: "Show local configuration, configured routes, and integration state. Use --verbose for managed setting names.",
				Examples: []CommandExample{
					{
						Description: "Inspect Codex configuration.",
						Command:     "baseten harness status --harness codex",
					},
				},
				JQExample: CommandExample{
					Description: "Inspect structured output.",
					Command:     "baseten harness status --harness codex --jq '.'",
				},
			},
		},
		{
			Name:    "teardown",
			Summary: "Remove Baseten harness settings and use native defaults (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Remove Baseten providers and clear their shared settings so native defaults apply. Previous values are not saved or restored. " +
				"Unrelated settings are preserved. Remove configured integrations at their default paths, or pass --harness to narrow the selection.\n\nUse --dry-run to preview removal or --yes to apply without confirmation. Saved " +
				"routes keys are retained and are not revoked. Teardown remains available after an upgrade or " +
				"executable removal.",
			Flags: HarnessTeardownFlags{},
			Output: &CommandOutput[HarnessPlansResult]{
				TextDescription: "Remove Baseten integration settings. Use --verbose for configuration paths and setting names.",
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
					Description: "Inspect structured output.",
					Command:     "baseten harness teardown --harness codex --dry-run --jq '.'",
				},
				JSONDescription: "Reports configuration changes as a collection, including for a single file.",
			},
		},
	},
}

type HarnessPlanResult struct {
	Harness          string   `json:"harness"`
	ReplacedSettings []string `json:"replaced_settings,omitempty"`
	Managed          bool     `json:"managed"`
	Config           string   `json:"config"`
	Settings         []string `json:"settings"`
	Changed          bool     `json:"changed"`
}

type HarnessPlansResult struct {
	Changes []HarnessPlanResult `json:"changes"`
}

type HarnessRouteSummary struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

type HarnessStatusResult struct {
	DefaultRoute    string                `json:"default_route,omitempty"`
	SmallTaskModel  string                `json:"small_task_model,omitempty"`
	RouteDetails    []HarnessRouteSummary `json:"route_details,omitempty"`
	Harness         string                `json:"harness"`
	Config          string                `json:"config"`
	Installed       bool                  `json:"installed"`
	Version         string                `json:"version"`
	Supported       bool                  `json:"supported"`
	State           string                `json:"state"`
	ManagedSettings []string              `json:"managed_settings,omitempty"`
	Routes          []string              `json:"routes,omitempty"`
	Note            string                `json:"note"`
}

type HarnessStatusesResult struct {
	Harnesses []HarnessStatusResult `json:"harnesses"`
}

type HarnessStatusFlags struct{ HarnessFlags }

type HarnessFlags struct {
	CommandFlags
	Harness []string `flag:"harness" desc:"Apply to specific harnesses" enum:"claude-code,codex,opencode"`
	Config  string   `flag:"config" desc:"Explicit settings file; requires one --harness"`
}

type HarnessSetupFlags struct {
	HarnessFlags
	Team            string `flag:"team" desc:"Team name or ID; required when multiple teams are available"`
	KeyName         string `flag:"key-name" desc:"Routes API key name stored in Baseten; defaults to baseten-harness-<normalized-hostname>"`
	Route           string `flag:"route" desc:"Default route name; defaults to the first returned route"`
	BackgroundRoute string `flag:"background-route" desc:"Route for OpenCode lightweight tasks (defaults to deepseek-ai/DeepSeek-V4.1-Flash)"`
	SubagentRoute   string `flag:"subagent-route" desc:"Optional subagent default route (Claude/OpenCode)"`
	FallbackRoute   string `flag:"fallback-route" desc:"Fallback route for Claude Code (defaults to initial route)"`
	DryRun          bool   `flag:"dry-run" desc:"Preview routes and configuration without changing harness configuration or creating an API key"`
	Yes             bool   `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

type HarnessTeardownFlags struct {
	HarnessFlags
	DryRun bool `flag:"dry-run" desc:"Preview removal without writing files"`
	Yes    bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}
