package cmd

var commandHarness = Command{
	Name: "harness", Summary: "Configure Baseten harness integrations", Hidden: true,
	Children: []Command{
		commandHarnessAuth,
		harnessLeaf("setup", "Set up harness authentication and configuration", HarnessSetupFlags{}),
		harnessLeaf("status", "Inspect local harness configuration and drift", HarnessFlags{}),
		harnessLeaf("teardown", "Restore settings owned by Baseten harness", HarnessTeardownFlags{}),
	},
}

func harnessLeaf(name, summary string, flags any) Command {
	example := "baseten harness " + name + " --harness claude-code"
	if name == "setup" {
		example += " --team <team> --model <route-name> --dry-run"
	}
	examples := []CommandExample{{Description: summary + ".", Command: example}}
	jq := CommandExample{Description: "Inspect structured output.", Command: example + " --jq '.'"}
	if name == "setup" {
		lines := []string{"baseten harness setup --harness claude-code", "--team <team> --model <route-name> --dry-run"}
		examples[0].Command = ""
		examples[0].CommandLines = lines
		jq.Command = ""
		jq.CommandLines = append(append([]string{}, lines...), "--jq '.'")
	}
	description := summary + ". Values and credentials are omitted."
	var output CommandOutputSpec
	switch name {
	case "setup":
		output = &CommandOutput[HarnessPlanResult]{
			TextDescription: description, Examples: examples, JQExample: jq,
			JSONDescription:  "Reports a configuration plan for one file. Dry runs do not create credentials or write configs.",
			JSONAlternatives: []CommandOutputAlternative{JSONAlternativeFor[HarnessPlansResult]("setup changes multiple files")},
		}
	case "status":
		output = &CommandOutput[HarnessStatusResult]{TextDescription: description, Examples: examples, JQExample: jq}
	case "teardown":
		output = &CommandOutput[HarnessPlanResult]{
			TextDescription: description, Examples: examples, JQExample: jq,
			JSONDescription:  "Reports restoration for one file.",
			JSONAlternatives: []CommandOutputAlternative{JSONAlternativeFor[HarnessPlansResult]("restoration includes multiple files")},
		}
	}
	return Command{Name: name, Summary: summary, Flags: flags,
		Description: summary + ".\n\nSetup reads team Routes and their /v1/models metadata, creates or reuses a Routes key, and configures selected harnesses. Setup never sends inference requests.",
		Output:      output}
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
	BackgroundModel string `flag:"background-model" desc:"Background Route (defaults to initial Route)"`
	SubagentModel   string `flag:"subagent-model" desc:"Optional subagent default Route (Claude/OpenCode)"`
	FallbackModel   string `flag:"fallback-model" desc:"Fallback Route (defaults to initial Route)"`
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

var commandHarnessAuth = Command{
	Name: "auth", Summary: "Manage saved Routes keys for harnesses",
	Children: []Command{{
		Name: "setup", Summary: "Create and save a Routes key",
		Description: "Create a Routes key through the existing API and save it in the system keyring. Reuse a saved key for the same API endpoint, profile, user, team and name. Selects the only available team automatically; otherwise prompts when --team is omitted. Does not modify harness configs or validate a saved key against inference. There is no plaintext storage fallback.",
		Flags:       HarnessAuthSetupFlags{},
		Output: &CommandOutput[HarnessAuthSetupResult]{
			TextDescription: "Reports whether a Routes key was created or reused. Never prints the secret.",
			Examples:        []CommandExample{{Description: "Create or reuse a Routes key for a team.", Command: "baseten harness auth setup --team <team>"}},
			JQExample:       CommandExample{Description: "Check whether a key was created.", Command: "baseten harness auth setup --team <team> --jq '.created'"},
		},
	}},
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
