package cmd

var commandTruss = Command{
	Name:               "truss",
	Summary:            "Run truss commands",
	Description:        "Delegates to the truss CLI if found on PATH, otherwise shows install instructions.",
	ArgsUsage:          "[args...]",
	DisableFlagParsing: true,
	Flags:              TrussFlags{},
}

type TrussFlags struct{}
