package cmd

import "github.com/basetenlabs/baseten-cli/cmd"

// Version is the CLI version reported by `baseten version` and `baseten
// --version`. Overridden at release time via -ldflags:
//
//	-X github.com/basetenlabs/baseten-cli/internal/cmd.Version=<semver>
var Version = "dev"

func init() {
	Register("version", commandVersion)
}

func commandVersion(ctx *CommandContext, _ *cmd.VersionFlags) error {
	if ctx.JSON {
		ctx.OutputJSON(map[string]string{"version": Version})
		return nil
	}
	ctx.OutputLine(Version)
	return nil
}
