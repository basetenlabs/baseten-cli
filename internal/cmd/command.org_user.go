package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("org user access", commandOrgUserAccess)
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
		ctx.OutputJSON(cmd.OrgUserList{Items: normalizeJSONSlice(items)})
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
		user, err = orgUserByEmail(ctx, cl.API(), flags.UserEmail)
		if err != nil {
			return err
		}
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

// orgUserByEmail looks up the organization's user with this email.
func orgUserByEmail(ctx context.Context, api *managementapi.Client, email string) (*managementapi.UserInfo, error) {
	resp, err := api.GetUsers(ctx, managementapi.GetV1UsersParams{Email: &email})
	if err != nil {
		return nil, fmt.Errorf("looking up user %q: %w", email, err)
	}
	switch len(resp.Items) {
	case 0:
		return nil, cmd.NewErrNotFound(fmt.Errorf("no user with email %q", email))
	case 1:
		return &resp.Items[0], nil
	}
	return nil, fmt.Errorf("multiple users with email %q", email)
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

func commandOrgUserAccess(ctx *CommandContext, flags *cmd.OrgUserAccessFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	userID := flags.UserID
	if flags.UserEmail != "" {
		user, err := orgUserByEmail(ctx, cl.API(), flags.UserEmail)
		if err != nil {
			return err
		}
		userID = user.UserId
	}
	access, err := cl.API().GetUsersAccess(ctx, userID)
	if err != nil {
		return fmt.Errorf("describing access: %w", err)
	}
	return orgAccessDescribe(ctx, access)
}

// orgAccessDescribe prints the roles and permissions a principal holds.
func orgAccessDescribe(ctx *CommandContext, access *managementapi.EffectiveAccess) error {
	if ctx.JSON {
		access.Assignments = normalizeJSONSlice(access.Assignments)
		ctx.OutputJSON(access)
		return nil
	}
	ctx.Outputf("%s (%s)\n", orgAccessSubjectText(access.SubjectType, access.SubjectName), access.SubjectId)
	rows := make([][]string, 0, len(access.Assignments))
	for _, held := range access.Assignments {
		via := strings.ToLower(string(held.GrantedVia))
		if held.SubjectType == managementapi.AccessSubjectType_DIRECTORY_GROUP {
			via += " to group " + held.SubjectName
		}
		rows = append(rows, []string{
			held.RoleName, via, orgAccessOrDash(held.TeamName), orgAccessResourceSetsText(held.ResourceSets),
			orgAccessOrDash(held.Id),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ROLE", "HELD VIA", "TEAM", "RESOURCE SETS", "ASSIGNMENT"},
		Rows:    rows,
	})
	if access.Permissions == nil {
		ctx.LogLine("Effective permissions aren't available yet.")
		return nil
	}
	var grants [][]string
	for _, permission := range *access.Permissions {
		for _, grant := range permission.Grants {
			team := "organization"
			if grant.TeamName != nil {
				team = *grant.TeamName
			}
			grants = append(grants, []string{permission.Name, team, orgAccessResourceSetText(grant.ResourceSet, "all")})
		}
	}
	ctx.Outputf("\n")
	ctx.OutputTable(TableOutput{Headers: []string{"PERMISSION", "TEAM", "RESOURCE SET"}, Rows: grants})
	return nil
}
