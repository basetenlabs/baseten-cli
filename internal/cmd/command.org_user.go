package cmd

import (
	"fmt"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("org user describe", commandOrgUserDescribe)
	Register("org user list", commandOrgUserList)
}

func commandOrgUserList(ctx *CommandContext, flags *cmd.OrgUserListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	// Walk every page and aggregate into one list rather than exposing
	// cursors, matching `model-api list`.
	var items []managementapi.UserInfo
	var params managementapi.GetV1UsersParams
	for {
		resp, err := cl.API().GetUsers(ctx, params)
		if err != nil {
			return fmt.Errorf("listing users: %w", err)
		}
		items = append(items, resp.Items...)
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			break
		}
		params.Cursor = resp.Pagination.Cursor
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.OrgUserList{Items: items})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No users found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, u := range items {
		rows = append(rows, []string{u.UserId, deref(u.Email), deref(u.Name)})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"USER ID", "EMAIL", "NAME"},
		Rows:    rows,
	})
	return nil
}

func commandOrgUserDescribe(ctx *CommandContext, flags *cmd.OrgUserDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	var user *managementapi.UserInfo
	if flags.UserEmail != "" {
		resp, err := cl.API().GetUsers(ctx, managementapi.GetV1UsersParams{Email: &flags.UserEmail})
		if err != nil {
			return fmt.Errorf("describe user %q: %w", flags.UserEmail, err)
		}
		if len(resp.Items) == 0 {
			return fmt.Errorf("no user with email %q", flags.UserEmail)
		} else if len(resp.Items) > 1 {
			return fmt.Errorf("multiple users with email %q", flags.UserEmail)
		}
		user = &resp.Items[0]
	} else if flags.UserID == "me" {
		// "me" routes to the dedicated authenticated-user endpoint.
		user, err = cl.API().GetUsersMe(ctx)
		if err != nil {
			return fmt.Errorf("describe user %s: %w", flags.UserID, err)
		}
	} else {
		user, err = cl.API().GetUsersUserId(ctx, flags.UserID)
		if err != nil {
			return fmt.Errorf("describe user %s: %w", flags.UserID, err)
		}
	}

	if ctx.JSON {
		ctx.OutputJSON(user)
		return nil
	}
	writeUserInfo(ctx, user)
	return nil
}

// writeUserInfo prints a UserInfo as a field-per-line summary. Shared by
// `org user describe` and `whoami`.
func writeUserInfo(ctx *CommandContext, user *managementapi.UserInfo) {
	ctx.Outputf("User ID:    %s\n", user.UserId)
	if email := deref(user.Email); email != "" {
		ctx.Outputf("Email:      %s\n", email)
	}
	if name := deref(user.Name); name != "" {
		ctx.Outputf("Name:       %s\n", name)
	}
	if ws := deref(user.WorkspaceName); ws != "" {
		ctx.Outputf("Workspace:  %s\n", ws)
	}
}
