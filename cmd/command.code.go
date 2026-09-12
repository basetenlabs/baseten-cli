package cmd

// Keep every node hidden independently. Hiding a parent alone does not hide
// children when a user explicitly completes or asks for help beneath it.
var commandCode = Command{
	Name: "code", Summary: "Configure Baseten Code", Hidden: true,
	Children: []Command{
		codeLeaf("init", "Preview or configure installed coding harnesses", CodeInitFlags{}, "", 0),
		codeLeaf("status", "Show local Code installation status", CodeHarnessFlags{}, "", 0),
		codeLeaf("models", "Fetch the current authenticated Route catalog", CodeHarnessFlags{}, "", 0),
		codeLeaf("spend", "Show the current user's Code spend", CodeSpendFlags{}, "", 0),
		codeLeaf("doctor", "Check setup; --test sends billable inference requests", CodeDoctorFlags{}, "[harness]", 1),
		codeLeaf("sync", "Manually refresh configured harnesses from the catalog", CodeSyncFlags{}, "[harness]", 1),
		codeLeaf("teardown", "Restore settings still owned by this installation", CodeTeardownFlags{}, "[harness]", 1),
		codeLeaf("logout", "Revoke this installation's Code key before clearing it", CodeConfirmFlags{}, "", 0),
		{Name: "keys", Summary: "Manage your Code keys", Hidden: true, Children: []Command{
			codeLeaf("list", "List Code key metadata", CodeKeysListFlags{}, "", 0),
			codeLeaf("revoke", "Revoke one Code key, or explicitly select --all", CodeKeysRevokeFlags{}, "[id]", 1),
		}},
		{Name: "auth", Summary: "Internal Code authentication", Hidden: true, Children: []Command{
			{Name: "token", RawOutput: true, Summary: "Print only this installation's Code credential", Hidden: true,
				Flags: CodeTokenFlags{}, Output: &CommandOutput[JSONUndefined]{
					TextDescription:       "Writes only the Code credential and a newline. Errors go to stderr. Output formatting is not supported.",
					JSONOutputUnimportant: true,
					Examples:              []CommandExample{{Description: "Fetch the credential for a harness.", Command: "baseten code auth token"}},
				}},
		}},
	},
}

func codeLeaf(name, summary string, flags any, args string, maxArgs int) Command {
	example := "baseten code " + name
	if name == "list" || name == "revoke" {
		example = "baseten code keys " + name
	}
	if name == "init" {
		example += " --dry-run --harness codex"
	}
	if name == "teardown" {
		example += " codex --dry-run"
	}
	if name == "revoke" {
		example += " <id> --yes"
	}
	return Command{Name: name, Summary: summary, Description: summary + ".\n\nExperimental. Dedicated Code credential endpoints and active-session catalog refresh are not yet integrated. Unsupported operations fail explicitly.", Hidden: true, Flags: flags, ArgsUsage: args, MaxArgs: maxArgs,
		Output: &CommandOutput[JSONAny]{TextDescription: summary + ". No credential values are included.", Examples: []CommandExample{{Description: summary + ".", Command: example}}, JQExample: CommandExample{Description: "Read structured output.", Command: example + " --jq '.'"}},
	}
}

type CodeFlags struct {
	CommandFlags
	Org           string `flag:"org" desc:"Organization ID or slug (defaults to saved installation)"`
	NoInteractive bool   `flag:"no-interactive" desc:"Disable prompts; never bypasses authentication"`
}

// The absolute state directory lets GUI launches use the same credential even
// when BASETEN_CONFIG_DIR is absent from their environment.
type CodeTokenFlags struct {
	CodeFlags
	ConfigDir string `flag:"config-dir" hidden:"true" desc:"Internal absolute Code state directory"`
}

type CodeOutputFlags struct {
	CodeFlags
	JSON bool `flag:"json" desc:"Alias for --output json"`
}

type CodeHarnessFlags struct {
	CodeOutputFlags
	Harness string `flag:"harness" desc:"Harness to inspect" enum:"codex,codex-desktop,claude-code,claude-desktop,opencode"`
}

type CodeInitFlags struct {
	CodeOutputFlags
	Harness   []string `flag:"harness" desc:"Harness to configure (repeatable)" enum:"codex,codex-desktop,claude-code,claude-desktop,opencode"`
	Model     string   `flag:"model" desc:"Initial Route slug from the authenticated catalog"`
	Label     string   `flag:"label" desc:"Installation label for a new Code credential"`
	NoBrowser bool     `flag:"no-browser" desc:"Display device authorization URL without opening a browser"`
	DryRun    bool     `flag:"dry-run" desc:"Preview without writing configuration or issuing credentials"`
}

type CodeSpendFlags struct {
	CodeOutputFlags
	From    string   `flag:"from" desc:"Inclusive start date in UTC (YYYY-MM-DD)"`
	To      string   `flag:"to" desc:"Exclusive end date in UTC (YYYY-MM-DD)"`
	GroupBy []string `flag:"group-by" desc:"Cost dimension (repeatable)" enum:"day,route,model,api-key-prefix"`
}

type CodeDoctorFlags struct {
	CodeOutputFlags
	Test  bool   `flag:"test" desc:"Send streaming and tool round-trip probes (incurs usage)"`
	Model string `flag:"model" desc:"Route slug for --test"`
}

type CodeSyncFlags struct {
	CodeOutputFlags
	DryRun bool `flag:"dry-run" desc:"Preview without changing configuration"`
}

type CodeConfirmFlags struct {
	CodeOutputFlags
	Yes bool `flag:"yes" desc:"Confirm the explicitly selected scope"`
}

type CodeTeardownFlags struct {
	CodeConfirmFlags
	All    bool `flag:"all" desc:"Remove all configured harness integrations"`
	DryRun bool `flag:"dry-run" desc:"Preview without changing configuration"`
}

type CodeKeysListFlags struct {
	CodeOutputFlags
	IncludeRevoked bool `flag:"include-revoked" desc:"Include revoked Code key metadata"`
}

type CodeKeysRevokeFlags struct {
	CodeConfirmFlags
	All bool `flag:"all" desc:"Revoke all of your Code keys"`
}
