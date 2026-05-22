package cmd

var commandTruss = Command{
	Name:               "truss",
	Summary:            "Run truss commands",
	Description:        "Delegates to the truss CLI if found on PATH, otherwise shows install instructions.",
	ArgsUsage:          "[args...]",
	DisableFlagParsing: true,
	Flags:              TrussFlags{},
	Output: &CommandOutput[JSONUndefined]{
		TextDescription: "Whatever the truss CLI writes to stdout/stderr, passed through verbatim. " +
			"--output and --jq are not honored: all arguments after `truss` are forwarded to " +
			"the underlying truss binary. The exit code is propagated from truss.",
		Examples: []CommandExample{
			{
				Description: "Show truss help by passing --help to truss.",
				Command:     "baseten truss --help",
			},
		},
	},
}

type TrussFlags struct{}
