package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("org identity-provider list-predefined", commandOrgIdentityProviderListPredefined)
	Register("org identity-provider create", commandOrgIdentityProviderCreate)
	Register("org identity-provider list", commandOrgIdentityProviderList)
	Register("org identity-provider describe", commandOrgIdentityProviderDescribe)
	Register("org identity-provider update", commandOrgIdentityProviderUpdate)
	Register("org identity-provider delete", commandOrgIdentityProviderDelete)
}

func commandOrgIdentityProviderListPredefined(ctx *CommandContext, flags *cmd.OrgIdentityProviderListPredefinedFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	predefined, err := cl.API().GetAccessPredefinedIdentityProviders(ctx)
	if err != nil {
		return fmt.Errorf("listing predefined identity providers: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(predefined)
		return nil
	}
	rows := make([][]string, 0, len(predefined.Items))
	for _, p := range predefined.Items {
		issuer := valueOrDash(p.IssuerUrl)
		if p.IssuerUrl == nil && p.IssuerUrlExample != nil {
			issuer = "yours, such as " + *p.IssuerUrlExample
		}
		rows = append(rows, []string{enumFromAPIValue(string(p.Kind)), p.Name, issuer})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"KIND", "NAME", "ISSUER URL"}, Rows: rows})
	return nil
}

func commandOrgIdentityProviderCreate(ctx *CommandContext, flags *cmd.OrgIdentityProviderCreateFlags) error {
	req := managementapi.CreateIdentityProviderRequest{
		Predefined: managementapi.CreatePredefinedIdentityProviderConfig{
			Kind:      managementapi.PredefinedIdentityProviderKind(enumToAPIValue(flags.Kind)),
			IssuerUrl: nonEmpty(flags.IssuerURL),
		},
		Name:        nonEmpty(flags.Name),
		Description: nonEmpty(flags.Description),
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	provider, err := cl.API().PostAccessIdentityProviders(ctx, req)
	if err != nil {
		return fmt.Errorf("creating identity provider: %w", err)
	}
	outputCreated(ctx, provider, "identity provider", provider.Name, provider.Id)
	return nil
}

func commandOrgIdentityProviderList(ctx *CommandContext, flags *cmd.OrgIdentityProviderListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	var params managementapi.GetV1AccessIdentityProvidersParams
	if flags.Kind != "" {
		kind := managementapi.PredefinedIdentityProviderKind(enumToAPIValue(flags.Kind))
		params.PredefinedKind = &kind
	}
	providers, err := listIdentityProviders(ctx, cl.API(), params)
	if err != nil {
		return err
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.IdentityProviderList{Items: normalizeJSONSlice(providers)})
		return nil
	}
	if len(providers) == 0 {
		ctx.LogLine("No identity providers found.")
		return nil
	}
	rows := make([][]string, 0, len(providers))
	for _, p := range providers {
		rows = append(rows, []string{p.Id, p.Name, identityProviderKind(p), valueOrDash(p.Predefined.IssuerUrl), p.CreatedAt.UTC().Format(time.RFC3339)})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "KIND", "ISSUER URL", "CREATED"},
		Rows:    rows,
	})
	return nil
}

// identityProviderKind renders a provider's kind as create --kind takes it.
func identityProviderKind(p managementapi.IdentityProvider) string {
	return enumFromAPIValue(string(p.Predefined.Kind))
}

func listIdentityProviders(ctx context.Context, api *managementapi.Client, params managementapi.GetV1AccessIdentityProvidersParams) ([]managementapi.IdentityProvider, error) {
	providers, err := listAllPages(func(cursor *string) ([]managementapi.IdentityProvider, managementapi.PaginationResponse, error) {
		params.Cursor = cursor
		page, err := api.GetAccessIdentityProviders(ctx, params)
		if err != nil {
			return nil, managementapi.PaginationResponse{}, err
		}
		return page.Items, page.Pagination, nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing identity providers: %w", err)
	}
	return providers, nil
}

func identityProviderID(p managementapi.IdentityProvider) string { return p.Id }

// identityProviderNameIs also matches a provider by its kind (github-actions),
// since its default name is a display name.
func identityProviderNameIs(p managementapi.IdentityProvider, name string) bool {
	return p.Name == name || identityProviderKind(p) == name
}

// resolveIdentityProviderRef resolves --id or --name to an identity provider ID.
func resolveIdentityProviderRef(ctx context.Context, api *managementapi.Client, ref cmd.IdentityProviderRefFlags) (string, error) {
	if ref.ID != "" {
		return ref.ID, nil
	}
	providers, err := listIdentityProviders(ctx, api, managementapi.GetV1AccessIdentityProvidersParams{})
	if err != nil {
		return "", err
	}
	provider, err := findByName("identity provider", ref.Name, providers, identityProviderNameIs)
	if err != nil {
		return "", err
	}
	return provider.Id, nil
}

// resolveIdentityProvider resolves a flag that takes an identity provider name
// or ID.
func resolveIdentityProvider(ctx context.Context, api *managementapi.Client, input string) (string, error) {
	providers, err := listIdentityProviders(ctx, api, managementapi.GetV1AccessIdentityProvidersParams{})
	if err != nil {
		return "", err
	}
	provider, err := findByIDOrName("identity provider", input, providers, identityProviderID, identityProviderNameIs)
	if err != nil {
		return "", err
	}
	return provider.Id, nil
}

func commandOrgIdentityProviderDescribe(ctx *CommandContext, flags *cmd.OrgIdentityProviderDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveIdentityProviderRef(ctx, cl.API(), flags.IdentityProviderRefFlags)
	if err != nil {
		return err
	}
	p, err := cl.API().GetAccessIdentityProvidersIdentityProviderId(ctx, id)
	if err != nil {
		return fmt.Errorf("describing identity provider %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(p)
		return nil
	}
	ctx.Outputf("ID:               %s\n", p.Id)
	ctx.Outputf("Name:             %s\n", p.Name)
	ctx.Outputf("Description:      %s\n", valueOrDash(p.Description))
	ctx.Outputf("Kind:             %s\n", identityProviderKind(*p))
	ctx.Outputf("Issuer URL:       %s\n", valueOrDash(p.Predefined.IssuerUrl))
	ctx.Outputf("Created:          %s\n", p.CreatedAt.UTC().Format(time.RFC3339))
	return nil
}

func commandOrgIdentityProviderUpdate(ctx *CommandContext, flags *cmd.OrgIdentityProviderUpdateFlags) error {
	var req managementapi.UpdateIdentityProviderRequest
	if flags.NewName != "" {
		req.Name = &flags.NewName
	}
	req.Description = flags.Description.Pointer()
	if req == (managementapi.UpdateIdentityProviderRequest{}) {
		return cmd.NewErrUsagef("nothing to update; pass --new-name or --description")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveIdentityProviderRef(ctx, cl.API(), flags.IdentityProviderRefFlags)
	if err != nil {
		return err
	}
	provider, err := cl.API().PatchAccessIdentityProviders(ctx, id, req)
	if err != nil {
		return fmt.Errorf("updating identity provider %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(provider)
		return nil
	}
	ctx.Logf("Updated identity provider %s\n", provider.Name)
	return nil
}

func commandOrgIdentityProviderDelete(ctx *CommandContext, flags *cmd.OrgIdentityProviderDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveIdentityProviderRef(ctx, cl.API(), flags.IdentityProviderRefFlags)
	if err != nil {
		return err
	}
	tomb, err := cl.API().DeleteAccessIdentityProviders(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting identity provider %s: %w", id, err)
	}
	outputDeleted(ctx, tomb, "identity provider", tomb.Id)
	return nil
}
