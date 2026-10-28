package cmd

import (
	"errors"

	"github.com/basetenlabs/baseten-cli/cmd"
)

func init() {
	Register("grid token", commandGridToken)
}

// commandGridToken prints a short-lived Grid token for the team a harness was
// set up with, or the team --team names. Implemented in the next PR.
func commandGridToken(ctx *CommandContext, flags *cmd.GridTokenFlags) error {
	return errors.New("not implemented")
}
