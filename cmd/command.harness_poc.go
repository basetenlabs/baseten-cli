package cmd

// commandHarnessPOC groups the `baseten harness-poc` subcommands. This is a
// proof of concept for the Routes harness work: it explores how far the model
// list of each agentic harness can be controlled, and it never ships. Every
// file it writes lives under its own root, so a developer's real harness
// installs are untouched.
var commandHarnessPOC = Command{
	Name:    "harness-poc",
	Hidden:  true,
	Summary: "Experiment with harness model lists (proof of concept)",
	Description: "Point agentic harnesses at a local mock gateway and see how far their model " +
		"lists can be controlled.\n\n" +
		"Nothing is written to a harness's real configuration. Every file lands under " +
		"--root, and setup prints the command that launches each harness against it.",
	Children: []Command{
		{
			Name:    "setup",
			Summary: "Write isolated harness configuration for the model list",
			Description: "Writes an isolated configuration for each selected harness, pointing it at " +
				"the mock gateway and populating its model list from the models YAML.\n\n" +
				"--mode local writes the harness's own model list. --mode remote leaves the list " +
				"to the harness's discovery against the gateway, where the harness supports it.",
			Flags: HarnessPOCSetupFlags{},
			Output: &CommandOutput[HarnessPOCSetupResult]{
				TextDescription: "Prints the files written and the launch command for each harness.",
				JSONDescription: "A JSON object with one entry per configured harness, each carrying the " +
					"paths written and the launch command.",
				Examples: []CommandExample{
					{
						Description: "Configure every supported harness with a local model list.",
						Command:     "baseten harness-poc setup --models models.yaml",
					},
					{
						Description: "Configure Claude Code alone, replacing its built-in model list.",
						Command: "baseten harness-poc setup --models models.yaml " +
							"--harness claude-code --replace-builtins",
					},
					{
						Description: "Configure Claude Code to discover models from the gateway instead.",
						Command:     "baseten harness-poc setup --models models.yaml --mode remote",
					},
				},
				JQExample: CommandExample{
					Description: "Print the launch command for each configured harness.",
					Command:     "baseten harness-poc setup --models models.yaml --jq '.harnesses[].launch'",
				},
			},
		},
		{
			Name:    "serve",
			Summary: "Serve the mock gateway the configured harnesses call",
			Description: "Runs a local HTTP server that answers the model list and inference endpoints " +
				"of Anthropic, OpenAI, and Codex, using the models YAML as its catalog.\n\n" +
				"Inference replies are canned and name the model the harness asked for, so the log " +
				"shows exactly which model name each harness sends and when. Runs until interrupted.",
			Flags: HarnessPOCServeFlags{},
			Output: &CommandOutput[JSONUndefined]{
				JSONOutputUnimportant: true,
				TextDescription:       "Logs every request the harnesses make until interrupted.",
				Examples: []CommandExample{
					{
						Description: "Serve the mock gateway on the default port.",
						Command:     "baseten harness-poc serve --models models.yaml",
					},
				},
			},
		},
	},
}

// HarnessPOCModelFlags are the flags shared by the commands that read the
// models YAML.
type HarnessPOCModelFlags struct {
	CommandFlags

	Models string `flag:"models" desc:"Path to the YAML file listing the models to expose"`
}

// HarnessPOCSetupFlags configures `baseten harness-poc setup`.
type HarnessPOCSetupFlags struct {
	HarnessPOCModelFlags

	Root            string   `flag:"root" desc:"Directory holding the isolated harness configuration" default:"~/.baseten-harness-poc"`
	Harness         []string `flag:"harness" desc:"Harness to configure: claude-code, claude-desktop, codex, opencode. Repeatable; defaults to all."`
	Mode            string   `flag:"mode" desc:"Where the model list comes from" default:"local" enum:"local,remote"`
	BaseURL         string   `flag:"base-url" desc:"Base URL of the mock gateway the harnesses call" default:"http://localhost:8010"`
	APIKey          string   `flag:"api-key" desc:"Credential written into each harness configuration" default:"harness-poc-key"`
	ReplaceBuiltins bool     `flag:"replace-builtins" desc:"Hide the harness's built-in models where the harness allows a choice"`
}

// HarnessPOCServeFlags configures `baseten harness-poc serve`.
type HarnessPOCServeFlags struct {
	HarnessPOCModelFlags

	Addr               string `flag:"addr" desc:"Address to listen on" default:"localhost:8010"`
	LogBodies          string `flag:"log-bodies" desc:"Directory to write one file per request body, numbered in call order"`
	WithModelDiscovery bool   `flag:"with-model-discovery" desc:"Serve GET /v1/models. Left off, the endpoint does not exist, so a harness can only get its model list from what setup wrote locally"`
}

// HarnessPOCSetupResult is the JSON output of `baseten harness-poc setup`.
type HarnessPOCSetupResult struct {
	Harnesses []HarnessPOCSetupHarness `json:"harnesses"`
}

// HarnessPOCSetupHarness is one harness's share of the setup result.
type HarnessPOCSetupHarness struct {
	Harness string   `json:"harness"`
	Paths   []string `json:"paths"`
	Launch  string   `json:"launch"`
	Notes   []string `json:"notes,omitempty"`
}
