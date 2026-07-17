package cmd

import (
	"fmt"
	"os/exec"

	"github.com/basetenlabs/baseten-cli/cmd"
)

func init() {
	Register("truss", commandTruss)
}

func commandTruss(ctx *CommandContext, flags *cmd.TrussFlags) error {
	if _, err := ctx.Execer().LookPath("truss"); err != nil {
		return fmt.Errorf("truss not found on PATH, install with: uv tool install truss")
	}

	trussCmd := exec.CommandContext(ctx, "truss", ctx.Args...)
	trussCmd.Stdin = ctx.Stdin
	trussCmd.Stdout = ctx.Stdout
	trussCmd.Stderr = ctx.Stderr
	return ctx.Execer().Exec(trussCmd)
}
