package cmd

var commandHarness = Command{
	Name: "harness", Summary: "Configure Baseten harness integrations", Hidden: true,
	Children: []Command{commandHarnessAuth},
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
