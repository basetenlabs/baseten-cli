package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/basetenlabs/baseten-cli/cmd"
)

func init() {
	Register("truss", commandTruss)
}

func commandTruss(ctx *CommandContext, flags *cmd.TrussFlags) error {
	trussPath, err := exec.LookPath("truss")
	if err != nil {
		return fmt.Errorf("truss not found on PATH, install with: uv tool install truss")
	}

	trussCmd := exec.CommandContext(ctx, trussPath, ctx.Args...)
	trussCmd.Stdin = os.Stdin
	trussCmd.Stdout = os.Stdout
	trussCmd.Stderr = os.Stderr
	if err := trussCmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &ErrSubprocess{Err: err, Code: exitErr.ExitCode()}
		}
		return err
	}
	return nil
}
