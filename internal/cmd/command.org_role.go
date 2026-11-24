package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("org role create", commandOrgRoleCreate)
	Register("org role delete", commandOrgRoleDelete)
	Register("org role describe", commandOrgRoleDescribe)
	Register("org role list", commandOrgRoleList)
	Register("org role update", commandOrgRoleUpdate)
}

// orgListRoles follows the role listing to its end.
func orgListRoles(ctx context.Context, api *managementapi.Client, params managementapi.GetV1AccessRolesParams) ([]managementapi.AccessRole, error) {
	var roles []managementapi.AccessRole
	for {
		page, err := api.GetAccessRoles(ctx, params)
		if err != nil {
			return nil, err
		}
		roles = append(roles, page.Items...)
		if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
			return roles, nil
		}
		params.Cursor = page.Pagination.Cursor
	}
}

// orgRoleID resolves a role reference. Names are unique in the organization. A
// name is looked up in teamID's role list when given, which a team admin can
// read without organization-wide access to roles.
func orgRoleID(ctx context.Context, api *managementapi.Client, id, name string, teamID *string) (string, error) {
	if id != "" {
		return id, nil
	}
	roles, err := orgListRoles(ctx, api, managementapi.GetV1AccessRolesParams{Name: &name, TeamId: teamID})
	if err != nil {
		return "", fmt.Errorf("looking up role %q: %w", name, err)
	}
	var ids []string
	for _, role := range roles {
		if role.Name == name {
			ids = append(ids, role.Id)
		}
	}
	switch len(ids) {
	case 0:
		return "", cmd.NewErrNotFound(fmt.Errorf("no role named %q", name))
	case 1:
		return ids[0], nil
	}
	// A built-in role added later can share a custom role's name.
	return "", cmd.NewErrUsagef("more than one role is named %q (%s); pass its ID instead", name, strings.Join(ids, ", "))
}

func commandOrgRoleList(ctx *CommandContext, flags *cmd.OrgRoleListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	params := managementapi.GetV1AccessRolesParams{}
	if flags.Team != "" {
		teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
		if err != nil {
			return err
		}
		params.TeamId = &teamID
	}
	roles, err := orgListRoles(ctx, cl.API(), params)
	if err != nil {
		return fmt.Errorf("listing roles: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.OrgRoleList{Items: normalizeJSONSlice(roles)})
		return nil
	}
	if len(roles) == 0 {
		ctx.LogLine("No roles found.")
		return nil
	}
	rows := make([][]string, 0, len(roles))
	for _, r := range roles {
		rows = append(rows, []string{
			r.Id, r.Name, strings.ToLower(string(r.Kind)), orgAccessOrDash(r.TeamName), strconv.FormatBool(r.BuiltIn),
			strconv.Itoa(len(r.Permissions)), strconv.Itoa(len(r.IncludeRoleIds)),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"ID", "NAME", "KIND", "TEAM", "BUILT-IN", "PERMISSIONS", "INCLUDES"},
		Rows:                rows,
		RightAlignedColumns: []int{5, 6},
	})
	return nil
}

func commandOrgRoleDescribe(ctx *CommandContext, flags *cmd.OrgRoleDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	params := managementapi.GetV1AccessRolesRoleIdParams{}
	if flags.Team != "" {
		teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
		if err != nil {
			return err
		}
		params.TeamId = &teamID
	}
	id, err := orgRoleID(ctx, cl.API(), flags.ID, flags.Name, params.TeamId)
	if err != nil {
		return err
	}
	role, err := cl.API().GetAccessRolesRoleId(ctx, id, params)
	if err != nil {
		return fmt.Errorf("describing role %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(role)
		return nil
	}
	permissions, includes := "-", "-"
	if len(role.Permissions) > 0 {
		permissions = strings.Join(role.Permissions, ", ")
	}
	if len(role.IncludeRoleIds) > 0 {
		includes = strings.Join(role.IncludeRoleIds, ", ")
	}
	ctx.Outputf("ID:          %s\n", role.Id)
	ctx.Outputf("Name:        %s\n", role.Name)
	ctx.Outputf("Description: %s\n", role.Description)
	ctx.Outputf("Kind:        %s\n", strings.ToLower(string(role.Kind)))
	ctx.Outputf("Team:        %s\n", orgAccessOrDash(role.TeamName))
	ctx.Outputf("Built-in:    %t\n", role.BuiltIn)
	ctx.Outputf("Permissions: %s\n", permissions)
	ctx.Outputf("Includes:    %s\n", includes)
	return nil
}

func commandOrgRoleCreate(ctx *CommandContext, flags *cmd.OrgRoleCreateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	permissions, includes := normalizeJSONSlice(flags.Permissions), normalizeJSONSlice(flags.Includes)
	req := managementapi.CreateAccessRoleRequest{
		Name:           flags.Name,
		Kind:           managementapi.AccessRoleKind(strings.ToUpper(flags.Kind)),
		Description:    &flags.Description,
		Permissions:    &permissions,
		IncludeRoleIds: &includes,
	}
	if flags.Team != "" {
		teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
		if err != nil {
			return err
		}
		req.TeamId = &teamID
	}
	role, err := cl.API().PostAccessRoles(ctx, req)
	if err != nil {
		return fmt.Errorf("creating role: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(role)
		return nil
	}
	ctx.Logf("Created role %s\n", role.Name)
	ctx.OutputLine(role.Id)
	return nil
}

func commandOrgRoleUpdate(ctx *CommandContext, flags *cmd.OrgRoleUpdateFlags) error {
	nonEmpty := func(values []string) *[]string {
		if len(values) == 0 {
			return nil
		}
		return &values
	}
	req := managementapi.UpdateAccessRoleRequest{
		Description:            flags.Description.Pointer(),
		Permissions:            nonEmpty(flags.AddPermissions),
		PermissionsToDelete:    nonEmpty(flags.RemovePermissions),
		IncludeRoleIds:         nonEmpty(flags.AddIncludes),
		IncludeRoleIdsToDelete: nonEmpty(flags.RemoveIncludes),
	}
	if flags.NewName != "" {
		req.Name = &flags.NewName
	}
	if req == (managementapi.UpdateAccessRoleRequest{}) {
		return cmd.NewErrUsagef("pass at least one of --new-name, --description, --add-permission, " +
			"--remove-permission, --add-include and --remove-include")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	var teamID *string
	if flags.Team != "" {
		id, err := ResolveTeam(ctx, cl.API(), flags.Team)
		if err != nil {
			return err
		}
		teamID = &id
	}
	id, err := orgRoleID(ctx, cl.API(), flags.ID, flags.Name, teamID)
	if err != nil {
		return err
	}
	role, err := cl.API().PatchAccessRoles(ctx, id, req)
	if err != nil {
		return fmt.Errorf("updating role %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(role)
		return nil
	}
	ctx.Logf("Updated role %s\n", role.Name)
	return nil
}

func commandOrgRoleDelete(ctx *CommandContext, flags *cmd.OrgRoleDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	var teamID *string
	if flags.Team != "" {
		id, err := ResolveTeam(ctx, cl.API(), flags.Team)
		if err != nil {
			return err
		}
		teamID = &id
	}
	id, err := orgRoleID(ctx, cl.API(), flags.ID, flags.Name, teamID)
	if err != nil {
		return err
	}
	deleted, err := cl.API().DeleteAccessRoles(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting role %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(deleted)
		return nil
	}
	ctx.Logf("Deleted role %s\n", deleted.Id)
	return nil
}
