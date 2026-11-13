package cmd

import (
	"context"
	"fmt"
	"slices"
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
	Register("org service-account disable", commandOrgServiceAccountDisable)
	Register("org service-account enable", commandOrgServiceAccountEnable)
	Register("org service-account delete", commandOrgServiceAccountDelete)
}

// serviceAccountPermissionFlags lists, per access type, the permission flags it
// accepts, mirroring the server's check so a mistake fails before any request.
var serviceAccountPermissionFlags = map[string][]string{
	"workspace-export-metrics":    {"model-id"},
	"workspace-invoke":            {"model-id", "environment"},
	"workspace-invoke-model-apis": {"model-api-slug"},
	"workspace-manage-all":        {"environment"},
	"workspace-manage-api-keys":   {},
}

// serviceAccountPermissions builds the access to send for --type and the
// permission flags. An unset list is omitted, which the server reads as no
// restriction.
func serviceAccountPermissions(presetType string, flags cmd.ServiceAccountPermissionFlags) (managementapi.ServiceAccountPresetPermissionsRequest, error) {
	allowed, ok := serviceAccountPermissionFlags[presetType]
	if !ok {
		return managementapi.ServiceAccountPresetPermissionsRequest{}, fmt.Errorf("unhandled service account type %q", presetType)
	}
	req := managementapi.ServiceAccountPresetPermissionsRequest{
		Type: managementapi.ServiceAccountPresetType(enumToAPIValue(presetType)),
	}
	for _, f := range []struct {
		name   string
		values []string
		field  **[]string
	}{
		{"model-id", flags.ModelIDs, &req.ModelIds},
		{"environment", flags.Environments, &req.EnvironmentNames},
		{"model-api-slug", flags.ModelAPISlugs, &req.ModelApiSlugs},
	} {
		if len(f.values) == 0 {
			continue
		}
		if !slices.Contains(allowed, f.name) {
			return req, cmd.NewErrUsagef("--%s can't be used with --type %s", f.name, presetType)
		}
		values := append([]string(nil), f.values...)
		*f.field = &values
	}
	return req, nil
}

func commandOrgServiceAccountCreate(ctx *CommandContext, flags *cmd.OrgServiceAccountCreateFlags) error {
	permissions, err := serviceAccountPermissions(flags.Type, flags.ServiceAccountPermissionFlags)
	if err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	req := managementapi.CreateServiceAccountRequest{Name: flags.Name, PresetPermissions: permissions}
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
	outputCreated(ctx, account, "service account", account.Name, account.Id)
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
	accounts, err := listServiceAccounts(ctx, cl.API(), params)
	if err != nil {
		return err
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.ServiceAccountList{Items: normalizeJSONSlice(accounts)})
		return nil
	}
	if len(accounts) == 0 {
		ctx.LogLine("No service accounts found.")
		return nil
	}
	rows := make([][]string, 0, len(accounts))
	for _, a := range accounts {
		rows = append(rows, []string{
			a.Id, a.Name, enumFromAPIValue(string(a.PresetPermissions.Type)), valueOrDash(a.TeamId),
			fmt.Sprint(a.Disabled), a.CreatedAt.UTC().Format(time.RFC3339), timeOrDash(a.LastUsedAt),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "TYPE", "TEAM", "DISABLED", "CREATED", "LAST USED"},
		Rows:    rows,
	})
	return nil
}

func listServiceAccounts(ctx context.Context, api *managementapi.Client, params managementapi.GetV1ServiceAccountsParams) ([]managementapi.ServiceAccount, error) {
	accounts, err := listAllPages(func(cursor *string) ([]managementapi.ServiceAccount, managementapi.PaginationResponse, error) {
		params.Cursor = cursor
		page, err := api.GetServiceAccounts(ctx, params)
		if err != nil {
			return nil, managementapi.PaginationResponse{}, err
		}
		return page.Items, page.Pagination, nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing service accounts: %w", err)
	}
	return accounts, nil
}

func serviceAccountID(a managementapi.ServiceAccount) string { return a.Id }

func serviceAccountNameIs(a managementapi.ServiceAccount, name string) bool { return a.Name == name }

// resolveServiceAccountRef resolves --id or --name to a service account ID.
// Mutations then address the account by its stable ID.
func resolveServiceAccountRef(ctx context.Context, api *managementapi.Client, ref cmd.ServiceAccountRefFlags) (string, error) {
	if ref.ID != "" {
		return ref.ID, nil
	}
	accounts, err := listServiceAccounts(ctx, api, managementapi.GetV1ServiceAccountsParams{})
	if err != nil {
		return "", err
	}
	account, err := findByName("service account", ref.Name, accounts, serviceAccountNameIs)
	if err != nil {
		return "", err
	}
	return account.Id, nil
}

// resolveServiceAccount resolves a flag that takes a service account name or ID.
func resolveServiceAccount(ctx context.Context, api *managementapi.Client, input string) (string, error) {
	accounts, err := listServiceAccounts(ctx, api, managementapi.GetV1ServiceAccountsParams{})
	if err != nil {
		return "", err
	}
	account, err := findByIDOrName("service account", input, accounts, serviceAccountID, serviceAccountNameIs)
	if err != nil {
		return "", err
	}
	return account.Id, nil
}

func commandOrgServiceAccountDescribe(ctx *CommandContext, flags *cmd.OrgServiceAccountDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveServiceAccountRef(ctx, cl.API(), flags.ServiceAccountRefFlags)
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
	p := a.PresetPermissions
	ctx.Outputf("ID:               %s\n", a.Id)
	ctx.Outputf("Name:             %s\n", a.Name)
	ctx.Outputf("Description:      %s\n", valueOrDash(a.Description))
	ctx.Outputf("Team:             %s\n", valueOrDash(a.TeamId))
	ctx.Outputf("Type:             %s\n", enumFromAPIValue(string(p.Type)))
	allowed := serviceAccountPermissionFlags[enumFromAPIValue(string(p.Type))]
	for _, r := range []struct {
		flag, label string
		values      *[]string
	}{
		{"model-id", "Models:           ", p.ModelIds},
		{"environment", "Environments:     ", p.EnvironmentNames},
		{"model-api-slug", "Model APIs:       ", p.ModelApiSlugs},
	} {
		if slices.Contains(allowed, r.flag) {
			ctx.Outputf("%s%s\n", r.label, listOrAll(r.values))
		}
	}
	ctx.Outputf("Disabled:         %t\n", a.Disabled)
	ctx.Outputf("Created:          %s\n", a.CreatedAt.UTC().Format(time.RFC3339))
	ctx.Outputf("Last Used:        %s\n", timeOrDash(a.LastUsedAt))
	return nil
}

func commandOrgServiceAccountUpdate(ctx *CommandContext, flags *cmd.OrgServiceAccountUpdateFlags) error {
	var req managementapi.UpdateServiceAccountRequest
	p := flags.ServiceAccountPermissionFlags
	// Access is replaced as a whole, so inferring the type from the current
	// account could silently drop a restriction the caller meant to keep.
	if flags.Type == "" && (len(p.ModelIDs) > 0 || len(p.Environments) > 0 || len(p.ModelAPISlugs) > 0) {
		return cmd.NewErrUsagef("--model-id, --environment and --model-api-slug replace the account's access; pass --type too")
	}
	if flags.Type != "" {
		permissions, err := serviceAccountPermissions(flags.Type, p)
		if err != nil {
			return err
		}
		req.PresetPermissions = &permissions
	}
	if flags.NewName != "" {
		req.Name = &flags.NewName
	}
	req.Description = flags.Description.Pointer()
	if req == (managementapi.UpdateServiceAccountRequest{}) {
		return cmd.NewErrUsagef("nothing to update; pass --new-name, --description or --type")
	}
	return updateServiceAccount(ctx, flags.ServiceAccountRefFlags, req, "Updated")
}

func commandOrgServiceAccountDisable(ctx *CommandContext, flags *cmd.OrgServiceAccountDisableFlags) error {
	disabled := true
	return updateServiceAccount(ctx, flags.ServiceAccountRefFlags, managementapi.UpdateServiceAccountRequest{Disabled: &disabled}, "Disabled")
}

func commandOrgServiceAccountEnable(ctx *CommandContext, flags *cmd.OrgServiceAccountEnableFlags) error {
	disabled := false
	return updateServiceAccount(ctx, flags.ServiceAccountRefFlags, managementapi.UpdateServiceAccountRequest{Disabled: &disabled}, "Enabled")
}

// updateServiceAccount patches the referenced account and reports it with verb.
func updateServiceAccount(ctx *CommandContext, ref cmd.ServiceAccountRefFlags, req managementapi.UpdateServiceAccountRequest, verb string) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveServiceAccountRef(ctx, cl.API(), ref)
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
	ctx.Logf("%s service account %s\n", verb, account.Name)
	return nil
}

func commandOrgServiceAccountDelete(ctx *CommandContext, flags *cmd.OrgServiceAccountDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveServiceAccountRef(ctx, cl.API(), flags.ServiceAccountRefFlags)
	if err != nil {
		return err
	}
	tomb, err := cl.API().DeleteServiceAccounts(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting service account %s: %w", id, err)
	}
	outputDeleted(ctx, tomb, "service account", tomb.Id)
	return nil
}

// outputCreated prints a created resource: the response under JSON output,
// otherwise its ID on stdout, for scripts, and a confirmation on stderr.
func outputCreated(ctx *CommandContext, response any, kind, name, id string) {
	if ctx.JSON {
		ctx.OutputJSON(response)
		return
	}
	ctx.Logf("Created %s %s\n", kind, name)
	ctx.OutputLine(id)
}

// outputDeleted prints a deletion tombstone: the response under JSON output,
// otherwise a confirmation on stderr.
func outputDeleted(ctx *CommandContext, tomb any, kind, id string) {
	if ctx.JSON {
		ctx.OutputJSON(tomb)
		return
	}
	ctx.Logf("Deleted %s %s\n", kind, id)
}

func valueOrDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

func timeOrDash(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

// listOrAll renders an access restriction list; an omitted list means no
// restriction.
func listOrAll(values *[]string) string {
	if values == nil {
		return "all"
	}
	return strings.Join(*values, ", ")
}
