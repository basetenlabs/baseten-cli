package cmd

var commandVersion = Command{
	Name:        "version",
	Summary:     "Print the baseten CLI version",
	Description: "Print the version of the baseten CLI.",
	Flags:       VersionFlags{},
}

// VersionFlags configures `baseten version`.
type VersionFlags struct {
	CommandFlags
}
