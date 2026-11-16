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
		issuer := deref(p.IssuerUrl)
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
			Kind: managementapi.PredefinedIdentityProviderKind(enumToAPIValue(flags.Kind)),
		},
	}
	if flags.IssuerURL != "" {
		req.Predefined.IssuerUrl = &flags.IssuerURL
	}
	if flags.Name != "" {
		req.Name = &flags.Name
	}
	if flags.Description != "" {
		req.Description = &flags.Description
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	provider, err := cl.API().PostAccessIdentityProviders(ctx, req)
	if err != nil {
		return fmt.Errorf("creating identity provider: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(provider)
		return nil
	}
	ctx.Logf("Created identity provider %s\n", provider.Name)
	ctx.OutputLine(provider.Id)
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
	var providers []managementapi.IdentityProvider
	for {
		page, err := cl.API().GetAccessIdentityProviders(ctx, params)
		if err != nil {
			return fmt.Errorf("listing identity providers: %w", err)
		}
		providers = append(providers, page.Items...)
		if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
			break
		}
		params.Cursor = page.Pagination.Cursor
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.OrgIdentityProviderList{Items: normalizeJSONSlice(providers)})
		return nil
	}
	if len(providers) == 0 {
		ctx.LogLine("No identity providers found.")
		return nil
	}
	rows := make([][]string, 0, len(providers))
	for _, p := range providers {
		rows = append(rows, []string{
			p.Id, p.Name, enumFromAPIValue(string(p.Predefined.Kind)), deref(p.Predefined.IssuerUrl),
			p.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "KIND", "ISSUER URL", "CREATED"},
		Rows:    rows,
	})
	return nil
}

// orgIdentityProviderIDByName looks up the ID of the identity provider with
// this name, or else of this predefined kind (github-actions), since a
// provider's default name is a display name.
func orgIdentityProviderIDByName(ctx context.Context, api *managementapi.Client, name string) (string, error) {
	page, err := api.GetAccessIdentityProviders(ctx, managementapi.GetV1AccessIdentityProvidersParams{Name: &name})
	if err != nil {
		return "", fmt.Errorf("looking up identity provider %q: %w", name, err)
	}
	// Names are unique in the organization, so the filter matches at most one.
	if len(page.Items) == 1 {
		return page.Items[0].Id, nil
	}
	kind := managementapi.PredefinedIdentityProviderKind(enumToAPIValue(name))
	if kind.Valid() {
		page, err = api.GetAccessIdentityProviders(ctx, managementapi.GetV1AccessIdentityProvidersParams{PredefinedKind: &kind})
		if err != nil {
			return "", fmt.Errorf("looking up identity provider %q: %w", name, err)
		}
		// A kind with a per-customer issuer can be added more than once.
		if len(page.Items) > 1 {
			return "", fmt.Errorf("multiple identity providers of kind %q; pass the name or ID instead", name)
		}
		if len(page.Items) == 1 {
			return page.Items[0].Id, nil
		}
	}
	return "", cmd.NewErrNotFound(fmt.Errorf("no identity provider named %q", name))
}

// resolveOrgIdentityProviderRef resolves --id or --name to an identity
// provider ID.
func resolveOrgIdentityProviderRef(ctx context.Context, api *managementapi.Client, ref cmd.OrgIdentityProviderRefFlags) (string, error) {
	if ref.ID != "" {
		return ref.ID, nil
	}
	return orgIdentityProviderIDByName(ctx, api, ref.Name)
}

func commandOrgIdentityProviderDescribe(ctx *CommandContext, flags *cmd.OrgIdentityProviderDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveOrgIdentityProviderRef(ctx, cl.API(), flags.OrgIdentityProviderRefFlags)
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
	ctx.Outputf("Description:      %s\n", deref(p.Description))
	ctx.Outputf("Kind:             %s\n", enumFromAPIValue(string(p.Predefined.Kind)))
	ctx.Outputf("Issuer URL:       %s\n", deref(p.Predefined.IssuerUrl))
	ctx.Outputf("Created:          %s\n", p.CreatedAt.UTC().Format(time.RFC3339))
	return nil
}

func commandOrgIdentityProviderUpdate(ctx *CommandContext, flags *cmd.OrgIdentityProviderUpdateFlags) error {
	req := managementapi.UpdateIdentityProviderRequest{Description: flags.Description.APIOptional()}
	if flags.NewName != "" {
		req.Name = &flags.NewName
	}
	if req == (managementapi.UpdateIdentityProviderRequest{}) {
		return cmd.NewErrUsagef("nothing to update; pass --new-name or --description")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveOrgIdentityProviderRef(ctx, cl.API(), flags.OrgIdentityProviderRefFlags)
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
	id, err := resolveOrgIdentityProviderRef(ctx, cl.API(), flags.OrgIdentityProviderRefFlags)
	if err != nil {
		return err
	}
	tomb, err := cl.API().DeleteAccessIdentityProviders(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting identity provider %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(tomb)
		return nil
	}
	ctx.Logf("Deleted identity provider %s\n", tomb.Id)
	return nil
}
