package cmd

const harnessPreRelease = "PRE-RELEASE: Harness commands are not GA yet. " +
	"Their arguments, flags, and output may change.\n\n"

var commandHarness = Command{
	Name:        "harness",
	Summary:     "Configure Baseten harness integrations (PRE-RELEASE)",
	Description: harnessPreRelease + "Configure Claude Code, Codex CLI, and OpenCode CLI to use Baseten routes.",
	Hidden:      true,
	Children: []Command{
		{
			Name:    "setup",
			Summary: "Set up harness authentication and configuration (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Configure compatible harnesses with your team's routes.\n\n" +
				"Setup detects installed harnesses, previews changes, and creates or reuses a team-scoped " +
				"routes API key. The first setup backs up existing settings; reruns preserve that restore point.\n\n" +
				"Pass --team if you belong to multiple teams. Each harness defaults to the first route; " +
				"use --model to override it.\n\n" +
				"Use --dry-run to preview or --yes to skip confirmation. Restart the harness after setup.",
			Flags: HarnessSetupFlags{},
			Output: &CommandOutput[HarnessPlanResult]{
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
							"--team <team> --model <route-name> --dry-run",
						},
					},
					{
						Description: "Apply a configuration without prompting.",
						CommandLines: []string{
							"baseten harness setup --harness codex",
							"--team <team> --model <route-name> --yes",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Inspect structured output.",
					CommandLines: []string{
						"baseten harness setup --harness claude-code",
						"--team <team> --model <route-name> --dry-run --jq '.'",
					},
				},
				JSONDescription: "Reports changes for one configuration file.",
				JSONAlternatives: []CommandOutputAlternative{
					JSONAlternativeFor[HarnessPlansResult]("changes include multiple files"),
				},
			},
		},
		{
			Name:    "status",
			Summary: "Inspect local harness configuration and drift (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Show the installed version, configuration path, managed settings, and changes made since setup. " +
				"Pass --harness to select which harness to inspect. Status remains available " +
				"after an upgrade or executable removal.",
			Flags: HarnessFlags{},
			Output: &CommandOutput[HarnessStatusResult]{
				TextDescription: "Show local configuration, configured routes, and changed settings. Use --verbose for managed setting names.",
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
			Summary: "Restore settings owned by Baseten harness (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Restore managed settings to their values before the first setup, including settings changed afterward. " +
				"Unrelated settings are preserved. Pass --harness to select which harness to restore.\n\nUse --dry-run to preview restoration or --yes to apply without confirmation. Saved " +
				"routes keys are retained and are not revoked. Teardown remains available after an upgrade or " +
				"executable removal.",
			Flags: HarnessTeardownFlags{},
			Output: &CommandOutput[HarnessPlanResult]{
				TextDescription: "Restore original managed settings. Use --verbose for configuration paths and setting names.",
				Examples: []CommandExample{
					{
						Description: "Preview restoration.",
						Command:     "baseten harness teardown --harness codex --dry-run",
					},
					{
						Description: "Restore configuration without prompting.",
						Command:     "baseten harness teardown --harness codex --yes",
					},
				},
				JQExample: CommandExample{
					Description: "Inspect structured output.",
					Command:     "baseten harness teardown --harness codex --dry-run --jq '.'",
				},
				JSONDescription: "Reports changes for one configuration file.",
				JSONAlternatives: []CommandOutputAlternative{
					JSONAlternativeFor[HarnessPlansResult]("changes include multiple files"),
				},
			},
		},
	},
}

type HarnessPlanResult struct {
	Replaced []string `json:"replaced_settings,omitempty"`
	Managed  bool     `json:"managed"`
	Path     string   `json:"config"`
	Keys     []string `json:"settings"`
	Changed  bool     `json:"changed"`
}

type HarnessPlansResult struct {
	Changes []HarnessPlanResult `json:"changes"`
}

type HarnessRouteSummary struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

type HarnessStatusResult struct {
	DefaultRoute   string                `json:"default_route,omitempty"`
	SmallTaskModel string                `json:"small_task_model,omitempty"`
	RouteDetails   []HarnessRouteSummary `json:"route_details,omitempty"`
	Name           string                `json:"harness"`
	Path           string                `json:"config"`
	Installed      bool                  `json:"installed"`
	Version        string                `json:"version"`
	Supported      bool                  `json:"supported"`
	State          string                `json:"state"`
	Managed        []string              `json:"managed_settings,omitempty"`
	Drift          []string              `json:"drift,omitempty"`
	Routes         []string              `json:"routes,omitempty"`
	Note           string                `json:"note"`
}

type HarnessFlags struct {
	CommandFlags
	Harness string `flag:"harness" desc:"Harness to inspect or restore" required:"true" enum:"claude-code,codex,opencode"`
	Config  string `flag:"config" desc:"Explicit harness settings file"`
}

type HarnessSetupFlags struct {
	CommandFlags
	Team            string `flag:"team" desc:"Team name or ID; required when multiple teams are available"`
	KeyName         string `flag:"key-name" desc:"routes key name; defaults to baseten-harness-<normalized-hostname>"`
	Harness         string `flag:"harness" desc:"Harness to configure; prompts when omitted" enum:"claude-code,codex,opencode"`
	Config          string `flag:"config" desc:"Explicit settings file for a single harness; defaults to its native config path"`
	Model           string `flag:"model" desc:"Default route name; defaults to the first returned route"`
	BackgroundModel string `flag:"background-model" desc:"route for OpenCode lightweight tasks (defaults to deepseek-ai/DeepSeek-V4.1-Flash)"`
	SubagentModel   string `flag:"subagent-model" desc:"Optional subagent default route (Claude/OpenCode)"`
	FallbackModel   string `flag:"fallback-model" desc:"Fallback route for Claude Code (defaults to initial route)"`
	DryRun          bool   `flag:"dry-run" desc:"Preview setting names without writing files"`
	Yes             bool   `flag:"yes" desc:"Apply the displayed configuration plan without prompting"`
}

type HarnessTeardownFlags struct {
	HarnessFlags
	DryRun bool `flag:"dry-run" desc:"Preview restoration without writing files"`
	Yes    bool `flag:"yes" desc:"Restore the selected config without prompting"`
}
