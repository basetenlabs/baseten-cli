package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("grid machine-user list", commandGridMachineUserList)
	Register("grid machine-user create", commandGridMachineUserCreate)
	Register("grid machine-user describe", commandGridMachineUserDescribe)
	Register("grid machine-user update", commandGridMachineUserUpdate)
	Register("grid machine-user delete", commandGridMachineUserDelete)
	Register("grid machine-user api-key list", commandGridMachineUserAPIKeyList)
	Register("grid machine-user api-key create", commandGridMachineUserAPIKeyCreate)
	Register("grid machine-user api-key revoke", commandGridMachineUserAPIKeyRevoke)
}

func commandGridMachineUserList(ctx *CommandContext, f *cmd.GridMachineUserListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	team, err := gridTeamID(ctx, cl.API(), f.Team)
	if err != nil {
		return err
	}
	items, err := listGridMachineUsers(ctx, cl.API(), managementapi.GetV1GridMachineUsersParams{TeamId: team})
	if err != nil {
		return err
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.GridMachineUserList{Items: normalizeJSONSlice(items)})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No machine users found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, u := range items {
		rows = append(rows, []string{u.Id, u.Name, u.TeamName, u.CreatedAt.UTC().Format(time.RFC3339)})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"ID", "NAME", "TEAM", "CREATED"}, Rows: rows})
	return nil
}

func listGridMachineUsers(ctx context.Context, api *managementapi.Client, params managementapi.GetV1GridMachineUsersParams) ([]managementapi.GridMachineUser, error) {
	var users []managementapi.GridMachineUser
	for {
		resp, err := api.GetGridMachineUsers(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("listing machine users: %w", err)
		}
		users = append(users, resp.Items...)
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			return users, nil
		}
		params.Cursor = resp.Pagination.Cursor
	}
}

func commandGridMachineUserCreate(ctx *CommandContext, f *cmd.GridMachineUserCreateFlags) error {
	if f.Name == "" {
		return cmd.NewErrUsagef("--name must not be empty")
	}
	if f.NoAPIKey && f.APIKeyName != "" {
		return cmd.NewErrUsagef("--api-key-name cannot be combined with --no-api-key")
	}
	body := managementapi.CreateGridMachineUserRequest{Name: f.Name}
	if !f.NoAPIKey {
		body.ApiKey = &managementapi.CreateGridMachineUserAPIKeyRequest{}
		if f.APIKeyName != "" {
			body.ApiKey.Name = &f.APIKeyName
		}
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	body.TeamId, err = gridTeamID(ctx, cl.API(), f.Team)
	if err != nil {
		return err
	}
	created, err := cl.API().PostGridMachineUsers(ctx, body)
	if err != nil {
		return fmt.Errorf("creating machine user: %w", err)
	}
	if !ctx.JSON {
		ctx.Logf("Created machine user %s (%s) in team %s\n", created.Name, created.Id, created.TeamName)
	}
	if f.NoAPIKey {
		if ctx.JSON {
			ctx.OutputJSON(created)
		}
		return nil
	}
	if created.ApiKey == nil {
		return fmt.Errorf("created machine user %s, but the response has no API key; create one with 'baseten grid machine-user api-key create --machine-user-id %s'", created.Id, created.Id)
	}
	outputCreatedAPIKey(ctx, created, *created.ApiKey)
	return nil
}

func commandGridMachineUserDescribe(ctx *CommandContext, f *cmd.GridMachineUserDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	user, err := cl.API().GetGridMachineUsersMachineUserId(ctx, f.ID)
	if err != nil {
		return fmt.Errorf("getting machine user: %w", err)
	}
	outputGridMachineUser(ctx, user)
	return nil
}

func commandGridMachineUserUpdate(ctx *CommandContext, f *cmd.GridMachineUserUpdateFlags) error {
	if f.Name == "" {
		return cmd.NewErrUsagef("--name must not be empty")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	user, err := cl.API().PatchGridMachineUsers(ctx, f.ID, managementapi.UpdateGridMachineUserRequest{Name: &f.Name})
	if err != nil {
		return fmt.Errorf("updating machine user: %w", err)
	}
	outputGridMachineUser(ctx, user)
	return nil
}

func commandGridMachineUserDelete(ctx *CommandContext, f *cmd.GridMachineUserDeleteFlags) error {
	if !f.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete machine user %s and revoke its API keys?", f.ID)); err != nil {
			return err
		}
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	result, err := cl.API().DeleteGridMachineUsers(ctx, f.ID)
	if err != nil {
		return fmt.Errorf("deleting machine user: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(result)
	} else {
		ctx.Logf("Deleted machine user %s\n", result.Id)
	}
	return nil
}

func outputGridMachineUser(ctx *CommandContext, user *managementapi.GridMachineUser) {
	if ctx.JSON {
		ctx.OutputJSON(user)
		return
	}
	ctx.Outputf("ID:         %s\n", user.Id)
	ctx.Outputf("Name:       %s\n", user.Name)
	ctx.Outputf("Team:       %s (%s)\n", user.TeamName, user.TeamId)
	ctx.Outputf("Created by: %s\n", gridSettingText(user.CreatedBy))
	ctx.Outputf("Created:    %s\n", user.CreatedAt.UTC().Format(time.RFC3339))
}

func commandGridMachineUserAPIKeyList(ctx *CommandContext, f *cmd.GridMachineUserAPIKeyListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	keys, err := cl.API().GetGridMachineUsersApiKeys(ctx, f.MachineUserID)
	if err != nil {
		return fmt.Errorf("listing API keys: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(keys)
		return nil
	}
	if len(keys.Keys) == 0 {
		ctx.LogLine("No API keys found.")
		return nil
	}
	rows := make([][]string, 0, len(keys.Keys))
	for _, k := range keys.Keys {
		lastUsed := "-"
		if k.LastUsedAt != nil {
			lastUsed = k.LastUsedAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{gridSettingText(k.Name), k.Prefix + "****", k.CreatedAt.UTC().Format(time.RFC3339), lastUsed})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"NAME", "KEY", "CREATED", "LAST USED"}, Rows: rows})
	return nil
}

func commandGridMachineUserAPIKeyCreate(ctx *CommandContext, f *cmd.GridMachineUserAPIKeyCreateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	body := managementapi.CreateGridMachineUserAPIKeyRequest{}
	if f.Name != "" {
		body.Name = &f.Name
	}
	key, err := cl.API().PostGridMachineUsersApiKeys(ctx, f.MachineUserID, body)
	if err != nil {
		return fmt.Errorf("creating API key: %w", err)
	}
	outputCreatedAPIKey(ctx, key, key.ApiKey)
	return nil
}

func commandGridMachineUserAPIKeyRevoke(ctx *CommandContext, f *cmd.GridMachineUserAPIKeyRevokeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	prefix := f.Prefix
	if f.Name != "" {
		keys, err := cl.API().GetGridMachineUsersApiKeys(ctx, f.MachineUserID)
		if err != nil {
			return fmt.Errorf("listing API keys: %w", err)
		}
		if prefix, err = apiKeyPrefixByName(keys.Keys, f.Name); err != nil {
			return err
		}
	}
	tomb, err := cl.API().DeleteGridMachineUsersApiKeys(ctx, f.MachineUserID, prefix)
	if err != nil {
		return fmt.Errorf("revoking API key: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(tomb)
		return nil
	}
	ctx.Logf("Revoked API key %s\n", tomb.Prefix)
	return nil
}
