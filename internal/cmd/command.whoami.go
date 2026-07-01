package cmd

import (
	"fmt"

	"github.com/basetenlabs/baseten-cli/cmd"
)

func init() {
	Register("whoami", commandWhoami)
}

func commandWhoami(ctx *CommandContext, flags *cmd.WhoamiFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	user, err := cl.API().GetUsersMe(ctx)
	if err != nil {
		return fmt.Errorf("fetching authenticated user: %w", err)
	}

	if ctx.JSON {
		ctx.OutputJSON(user)
		return nil
	}
	writeUserInfo(ctx, user)
	return nil
}
