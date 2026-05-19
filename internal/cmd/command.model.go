package cmd

import (
	"github.com/basetenlabs/baseten-cli/cmd"
)

func init() {
	Register("model list", commandModelList)
	Register("model fetch", commandModelFetch)
}

func commandModelList(_ *CommandContext, _ *cmd.ModelListFlags) error {
	panic("TODO: implement model list")
}

func commandModelFetch(_ *CommandContext, _ *cmd.ModelFetchFlags) error {
	panic("TODO: implement model fetch")
}
