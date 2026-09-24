package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

const harnessPreRelease = "PRE-RELEASE: Harness commands are not GA yet. " +
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
				"so it works after the harness is uninstalled.",
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
			Name:    "teardown",
			Summary: "Remove Baseten harness settings (PRE-RELEASE)",
			Description: harnessPreRelease +
				"Remove the Baseten integration settings so the harness's own defaults apply. Previous values are not restored, " +
				"and unrelated settings are kept. Without --harness, removes every integration configured at its default path.\n\n" +
				"Saved routes API keys are kept and not revoked. Teardown reads local files only, so it works after the harness is uninstalled.",
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
		{
			Name:        "key",
			Summary:     "Manage your routes API keys (PRE-RELEASE)",
			Description: harnessPreRelease + "Manage the routes API keys you created with harness setup, across all your machines and teams.",
			Children: []Command{
				{
					Name:        "list",
					Summary:     "List your routes API keys (PRE-RELEASE)",
					Description: harnessPreRelease + "List the routes API keys you created, across all your machines and teams. Key values are never shown.",
					Flags:       HarnessKeyListFlags{},
					Output: &CommandOutput[managementapi.APIKeys]{
						TextDescription: "Table with NAME, PREFIX, TEAM, CREATED, and LAST USED columns.",
						Examples:        []CommandExample{{Description: "List your routes API keys.", Command: "baseten harness key list"}},
						JQExample:       CommandExample{Description: "Print key prefixes.", Command: "baseten harness key list --jq '.keys[].prefix'"},
					},
				},
				{
					Name:    "revoke",
					Summary: "Revoke your routes API keys (PRE-RELEASE)",
					Description: harnessPreRelease + "Revoke one routes API key by prefix, or all of them with --all. Harnesses using a revoked key lose access " +
						"until you rerun harness setup on that machine, which creates a new key.",
					Flags: HarnessKeyRevokeFlags{},
					Output: &CommandOutput[HarnessKeyRevokeResult]{
						TextDescription: "Each revoked prefix, on stderr.",
						JSONDescription: "revoked lists the revoked prefixes and failed the ones that could not be revoked. The command fails if any revocation failed.",
						Examples: []CommandExample{
							{Description: "Revoke a lost machine's key.", Command: "baseten harness key revoke --prefix <prefix>"},
							{Description: "Revoke all your routes API keys without prompting.", Command: "baseten harness key revoke --all --yes"},
						},
						JQExample: CommandExample{Description: "Print revoked prefixes.", Command: "baseten harness key revoke --prefix <prefix> --yes --jq '.revoked[]'"},
					},
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

// HarnessKeyListFlags are the flags for `baseten harness key list`.
type HarnessKeyListFlags struct{ CommandFlags }

// HarnessKeyRevokeFlags are the flags for `baseten harness key revoke`.
type HarnessKeyRevokeFlags struct {
	CommandFlags
	Prefix string `flag:"prefix" desc:"Prefix of the key to revoke, as shown by harness key list." oneof:"key"`
	All    bool   `flag:"all" desc:"Revoke all your routes API keys, on every machine and team." oneof:"key"`
	Yes    bool   `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// HarnessKeyRevokeResult is the JSON output of `baseten harness key revoke`.
type HarnessKeyRevokeResult struct {
	Revoked []string `json:"revoked"`
	Failed  []string `json:"failed"`
}
