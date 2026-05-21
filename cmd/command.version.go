package cmd

var commandVersion = Command{
	Name:        "version",
	Summary:     "Print the baseten CLI version",
	Description: "Print the version of the baseten CLI.",
	Flags:       VersionFlags{},
	Output: &CommandOutput[VersionResult]{
		TextDescription: "Prints the CLI version on a single line.",
		Examples: []CommandExample{
			{
				Description: "Print the CLI version.",
				Command:     "baseten version",
			},
		},
		JQExample: CommandExample{
			Description: "Print just the version string from JSON.",
			Command:     "baseten version --jq '.version'",
		},
	},
}

// VersionFlags configures `baseten version`.
type VersionFlags struct {
	CommandFlags
}

// VersionResult is the JSON output of `baseten version`.
type VersionResult struct {
	Version string `json:"version"`
}
