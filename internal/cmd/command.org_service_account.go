package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("org service-account create", commandOrgServiceAccountCreate)
	Register("org service-account list", commandOrgServiceAccountList)
	Register("org service-account describe", commandOrgServiceAccountDescribe)
	Register("org service-account update", commandOrgServiceAccountUpdate)
	Register("org service-account delete", commandOrgServiceAccountDelete)
	Register("org service-account access", commandOrgServiceAccountAccess)
}

// orgServiceAccountPermissions builds the access to send for --type and the
// permission flags. An unset list is omitted, which the server reads as no
// restriction; the server rejects a list the type doesn't take.
func orgServiceAccountPermissions(presetType string, flags cmd.OrgServiceAccountPermissionFlags) managementapi.ServiceAccountPresetPermissionsRequest {
	req := managementapi.ServiceAccountPresetPermissionsRequest{
		Type: managementapi.ServiceAccountPresetType(enumToAPIValue(presetType)),
	}
	if len(flags.ModelIDs) > 0 {
		req.ModelIds = &flags.ModelIDs
	}
	if len(flags.Environments) > 0 {
		req.EnvironmentNames = &flags.Environments
	}
	if len(flags.ModelAPISlugs) > 0 {
		req.ModelApiSlugs = &flags.ModelAPISlugs
	}
	return req
}

func commandOrgServiceAccountCreate(ctx *CommandContext, flags *cmd.OrgServiceAccountCreateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	req := managementapi.CreateServiceAccountRequest{
		Name:              flags.Name,
		PresetPermissions: orgServiceAccountPermissions(flags.Type, flags.OrgServiceAccountPermissionFlags),
	}
	if flags.Team != "" {
		teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
		if err != nil {
			return err
		}
		req.TeamId = &teamID
	}
	if flags.Description != "" {
		req.Description = &flags.Description
	}
	account, err := cl.API().PostServiceAccounts(ctx, req)
	if err != nil {
		return fmt.Errorf("creating service account: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(account)
		return nil
	}
	ctx.Logf("Created service account %s\n", account.Name)
	ctx.OutputLine(account.Id)
	return nil
}

func commandOrgServiceAccountList(ctx *CommandContext, flags *cmd.OrgServiceAccountListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	params := managementapi.GetV1ServiceAccountsParams{Disabled: flags.Disabled.Pointer()}
	if flags.Team != "" {
		teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
		if err != nil {
			return err
		}
		params.TeamId = &teamID
	}
	var accounts []managementapi.ServiceAccount
	for {
		page, err := cl.API().GetServiceAccounts(ctx, params)
		if err != nil {
			return fmt.Errorf("listing service accounts: %w", err)
		}
		accounts = append(accounts, page.Items...)
		if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
			break
		}
		params.Cursor = page.Pagination.Cursor
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.OrgServiceAccountList{Items: normalizeJSONSlice(accounts)})
		return nil
	}
	if len(accounts) == 0 {
		ctx.LogLine("No service accounts found.")
		return nil
	}
	rows := make([][]string, 0, len(accounts))
	for _, a := range accounts {
		lastUsed := "-"
		if a.LastUsedAt != nil {
			lastUsed = a.LastUsedAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{
			a.Id, a.Name, enumFromAPIValue(string(a.PresetPermissions.Type)), deref(a.TeamId),
			fmt.Sprint(a.Disabled), a.CreatedAt.UTC().Format(time.RFC3339), lastUsed,
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "TYPE", "TEAM", "DISABLED", "CREATED", "LAST USED"},
		Rows:    rows,
	})
	return nil
}

// orgServiceAccountIDByName looks up the ID of the service account with this
// name.
func orgServiceAccountIDByName(ctx context.Context, api *managementapi.Client, name string) (string, error) {
	page, err := api.GetServiceAccounts(ctx, managementapi.GetV1ServiceAccountsParams{Name: &name})
	if err != nil {
		return "", fmt.Errorf("looking up service account %q: %w", name, err)
	}
	// Names are unique in the organization, so the filter matches at most one.
	if len(page.Items) == 0 {
		return "", cmd.NewErrNotFound(fmt.Errorf("no service account named %q", name))
	}
	return page.Items[0].Id, nil
}

// resolveOrgServiceAccountRef resolves --id or --name to a service account ID.
func resolveOrgServiceAccountRef(ctx context.Context, api *managementapi.Client, ref cmd.OrgServiceAccountRefFlags) (string, error) {
	if ref.ID != "" {
		return ref.ID, nil
	}
	return orgServiceAccountIDByName(ctx, api, ref.Name)
}

func commandOrgServiceAccountDescribe(ctx *CommandContext, flags *cmd.OrgServiceAccountDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveOrgServiceAccountRef(ctx, cl.API(), flags.OrgServiceAccountRefFlags)
	if err != nil {
		return err
	}
	a, err := cl.API().GetServiceAccountsServiceAccountId(ctx, id)
	if err != nil {
		return fmt.Errorf("describing service account %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(a)
		return nil
	}
	lastUsed := "-"
	if a.LastUsedAt != nil {
		lastUsed = a.LastUsedAt.UTC().Format(time.RFC3339)
	}
	p := a.PresetPermissions
	ctx.Outputf("ID:               %s\n", a.Id)
	ctx.Outputf("Name:             %s\n", a.Name)
	ctx.Outputf("Description:      %s\n", deref(a.Description))
	ctx.Outputf("Team:             %s\n", deref(a.TeamId))
	ctx.Outputf("Type:             %s\n", enumFromAPIValue(string(p.Type)))
	// A list is present only when it restricts the account.
	if p.ModelIds != nil {
		ctx.Outputf("Models:           %s\n", strings.Join(*p.ModelIds, ", "))
	}
	if p.EnvironmentNames != nil {
		ctx.Outputf("Environments:     %s\n", strings.Join(*p.EnvironmentNames, ", "))
	}
	if p.ModelApiSlugs != nil {
		ctx.Outputf("Model APIs:       %s\n", strings.Join(*p.ModelApiSlugs, ", "))
	}
	ctx.Outputf("Disabled:         %t\n", a.Disabled)
	ctx.Outputf("Created:          %s\n", a.CreatedAt.UTC().Format(time.RFC3339))
	ctx.Outputf("Last Used:        %s\n", lastUsed)
	return nil
}

func commandOrgServiceAccountUpdate(ctx *CommandContext, flags *cmd.OrgServiceAccountUpdateFlags) error {
	p := flags.OrgServiceAccountPermissionFlags
	// Access is replaced as a whole, so inferring the type from the current
	// account could silently drop a restriction the caller meant to keep.
	if flags.Type == "" && (len(p.ModelIDs) > 0 || len(p.Environments) > 0 || len(p.ModelAPISlugs) > 0) {
		return cmd.NewErrUsagef("--model-id, --environment and --model-api-slug replace the account's access; pass --type too")
	}
	req := managementapi.UpdateServiceAccountRequest{
		Description: flags.Description.APIOptional(),
		Disabled:    flags.Disabled.Pointer(),
	}
	if flags.Type != "" {
		permissions := orgServiceAccountPermissions(flags.Type, p)
		req.PresetPermissions = &permissions
	}
	if flags.NewName != "" {
		req.Name = &flags.NewName
	}
	if req == (managementapi.UpdateServiceAccountRequest{}) {
		return cmd.NewErrUsagef("nothing to update; pass --new-name, --description, --type or --disabled")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveOrgServiceAccountRef(ctx, cl.API(), flags.OrgServiceAccountRefFlags)
	if err != nil {
		return err
	}
	account, err := cl.API().PatchServiceAccounts(ctx, id, req)
	if err != nil {
		return fmt.Errorf("updating service account %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(account)
		return nil
	}
	ctx.Logf("Updated service account %s\n", account.Name)
	return nil
}

func commandOrgServiceAccountDelete(ctx *CommandContext, flags *cmd.OrgServiceAccountDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveOrgServiceAccountRef(ctx, cl.API(), flags.OrgServiceAccountRefFlags)
	if err != nil {
		return err
	}
	tomb, err := cl.API().DeleteServiceAccounts(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting service account %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(tomb)
		return nil
	}
	ctx.Logf("Deleted service account %s\n", tomb.Id)
	return nil
}

func commandOrgServiceAccountAccess(ctx *CommandContext, flags *cmd.OrgServiceAccountAccessFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveOrgServiceAccountRef(ctx, cl.API(), flags.OrgServiceAccountRefFlags)
	if err != nil {
		return err
	}
	access, err := cl.API().GetServiceAccountsAccess(ctx, id)
	if err != nil {
		return fmt.Errorf("describing access: %w", err)
	}
	return orgOutputSubjectAccess(ctx, access)
}
