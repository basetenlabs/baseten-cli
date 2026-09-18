package cmd

const harnessPreRelease = "PRE-RELEASE: Harness commands are not GA yet. " +
	"Their arguments, flags, and output may change.\n\n"

var commandHarness = Command{
	Name:        "harness",
	Summary:     "Configure Baseten harness integrations (PRE-RELEASE)",
	Description: harnessPreRelease + "Configure Claude Code, Codex CLI, and OpenCode CLI to use Baseten Routes.",
	Hidden:      true,
	Children: []Command{
		commandHarnessAuth,
		{
			Name:    "setup",
			Summary: "Set up harness authentication and configuration (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Select one or more installed harnesses, a team, and an initial Route for each harness. Setup " +
				"shows installed versions and config paths, then previews changes before asking for " +
				"confirmation. A sole available team is selected automatically.\n\nExisting prompts, plugins, " +
				"permissions, and unrelated settings are preserved. Conflicting integration settings require " +
				"--replace-existing. Routes without usable model metadata are omitted.\n\nUse --dry-run to " +
				"preview without creating a key or writing files. For scripts, pass --harness, --model, and " +
				"--yes; also pass --team when multiple teams are available. A missing Routes key is created " +
				"automatically.\n\nRestart the harness after setup. Rerun setup to refresh available Routes. " +
				"Supported harnesses are Claude Code, Codex CLI, and OpenCode CLI on supported versions of " +
				"macOS.",
			Flags: HarnessSetupFlags{},
			Output: &CommandOutput[HarnessPlanResult]{
				TextDescription: "Set up harness authentication and configuration. Reports paths and setting names without credential values.",
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
				"Defaults to Claude Code; pass --harness to inspect Codex or OpenCode. Status remains available " +
				"after an upgrade or executable removal.",
			Flags: HarnessFlags{},
			Output: &CommandOutput[HarnessStatusResult]{
				TextDescription: "Inspect local harness configuration and drift. Reports paths and setting names without credential values.",
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
				"Restore settings changed by setup while preserving subsequent user edits. Conflicts are " +
				"reported for manual resolution. Defaults to Claude Code; pass --harness to restore Codex or " +
				"OpenCode.\n\nUse --dry-run to preview restoration or --yes to apply without confirmation. Saved " +
				"Routes keys are retained and are not revoked. Teardown remains available after an upgrade or " +
				"executable removal.",
			Flags: HarnessTeardownFlags{},
			Output: &CommandOutput[HarnessPlanResult]{
				TextDescription: "Restore settings owned by Baseten harness. Reports paths and setting names without credential values.",
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

var commandHarnessAuth = Command{
	Name:        "auth",
	Summary:     "Manage saved Routes keys for harnesses (PRE-RELEASE)",
	Description: harnessPreRelease + "Set up a saved Routes key without changing harness configuration.",
	Children: []Command{
		{
			Name:    "setup",
			Summary: "Create and save a Routes key (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Create or reuse a saved Routes key for the selected team. Selects the only available team " +
				"automatically; otherwise prompts when --team is omitted.\n\nThe key is saved securely and is " +
				"never printed. Reuse does not verify that a saved key is still valid. Use --dry-run to check " +
				"for a saved key without creating one. Run baseten harness setup to configure a harness.",
			Flags: HarnessAuthSetupFlags{},
			Output: &CommandOutput[HarnessAuthSetupResult]{
				TextDescription: "Reports whether a Routes key was created or reused. Never prints the secret.",
				Examples: []CommandExample{
					{
						Description: "Create or reuse a Routes key for a team.",
						Command:     "baseten harness auth setup --team <team>",
					},
				},
				JQExample: CommandExample{
					Description: "Check whether a key was created.",
					Command:     "baseten harness auth setup --team <team> --jq '.created'",
				},
			},
		},
	},
}

type HarnessPlanResult struct {
	Managed   bool     `json:"managed"`
	Path      string   `json:"config"`
	Keys      []string `json:"settings"`
	Changed   bool     `json:"changed"`
	Conflicts []string `json:"conflicts,omitempty"`
}

type HarnessPlansResult struct {
	Changes []HarnessPlanResult `json:"changes"`
}

type HarnessStatusResult struct {
	Name      string   `json:"harness"`
	Path      string   `json:"config"`
	Installed bool     `json:"installed"`
	Version   string   `json:"version"`
	Supported bool     `json:"supported"`
	State     string   `json:"state"`
	Managed   []string `json:"managed_settings,omitempty"`
	Drift     []string `json:"drift,omitempty"`
	Routes    []string `json:"routes,omitempty"`
	Note      string   `json:"note"`
}

type HarnessFlags struct {
	CommandFlags
	Harness string `flag:"harness" desc:"Harness to configure or inspect" default:"claude-code" enum:"claude-code,codex,opencode"`
	Config  string `flag:"config" desc:"Explicit harness settings file"`
}

type HarnessSetupFlags struct {
	CommandFlags
	Team            string `flag:"team" desc:"Team name or ID for the Routes key; automatically selects a sole team or prompts"`
	KeyName         string `flag:"key-name" desc:"Routes key name; defaults to baseten-harness-<normalized-hostname>"`
	Harness         string `flag:"harness" desc:"Harness to configure; prompts when omitted" enum:"claude-code,codex,opencode"`
	Config          string `flag:"config" desc:"Explicit settings file for a single harness; defaults to its native config path"`
	Model           string `flag:"model" desc:"Initial Route name"`
	BackgroundModel string `flag:"background-model" desc:"Background Route for Claude/OpenCode (defaults to initial Route)"`
	SubagentModel   string `flag:"subagent-model" desc:"Optional subagent default Route (Claude/OpenCode)"`
	FallbackModel   string `flag:"fallback-model" desc:"Fallback Route for Claude Code (defaults to initial Route)"`
	ReplacePicker   bool   `flag:"replace-picker" desc:"Hide built-in picker choices; preserve existing custom entries"`
	ReplaceExisting bool   `flag:"replace-existing" desc:"Back up and replace conflicting integration settings"`
	DryRun          bool   `flag:"dry-run" desc:"Preview setting names without writing files"`
	Yes             bool   `flag:"yes" desc:"Apply the displayed configuration plan without prompting"`
}

type HarnessTeardownFlags struct {
	HarnessFlags
	DryRun bool `flag:"dry-run" desc:"Preview restoration without writing files"`
	Yes    bool `flag:"yes" desc:"Restore the selected config without prompting"`
}

type HarnessAuthSetupFlags struct {
	CommandFlags
	Team   string `flag:"team" desc:"Team name or ID for the saved Routes key; prompts with available teams when omitted."`
	Name   string `flag:"name" desc:"Routes key name (defaults to baseten-harness-<normalized-hostname>); also identifies the local saved key"`
	DryRun bool   `flag:"dry-run" desc:"Check for a saved key without creating one"`
}

type HarnessAuthSetupResult struct {
	Name    string `json:"name"`
	TeamID  string `json:"team_id"`
	Storage string `json:"storage"`
	Created bool   `json:"created"`
	Reused  bool   `json:"reused"`
	DryRun  bool   `json:"dry_run"`
}
