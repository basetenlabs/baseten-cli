package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("org trust-rule create", commandOrgTrustRuleCreate)
	Register("org trust-rule list", commandOrgTrustRuleList)
	Register("org trust-rule describe", commandOrgTrustRuleDescribe)
	Register("org trust-rule update", commandOrgTrustRuleUpdate)
	Register("org trust-rule delete", commandOrgTrustRuleDelete)
}

func commandOrgTrustRuleCreate(ctx *CommandContext, flags *cmd.OrgTrustRuleCreateFlags) error {
	req := managementapi.CreateAccessTrustRuleRequest{
		Name:               flags.Name,
		IdentityProviderId: flags.IdentityProviderID,
		ServiceAccountId:   flags.ServiceAccountID,
		Condition:          flags.Condition,
		Audience:           flags.Audience,
	}
	if flags.Description != "" {
		req.Description = &flags.Description
	}
	if flags.MaxAccessTokenLifetime != 0 {
		seconds := int(flags.MaxAccessTokenLifetime / time.Second)
		req.MaxAccessTokenLifetimeSeconds = &seconds
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	if flags.IdentityProviderName != "" {
		if req.IdentityProviderId, err = orgIdentityProviderIDByName(ctx, cl.API(), flags.IdentityProviderName); err != nil {
			return err
		}
	}
	if flags.ServiceAccountName != "" {
		if req.ServiceAccountId, err = orgServiceAccountIDByName(ctx, cl.API(), flags.ServiceAccountName); err != nil {
			return err
		}
	}
	rule, err := cl.API().PostAccessTrustRules(ctx, req)
	if err != nil {
		return fmt.Errorf("creating trust rule: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(rule)
		return nil
	}
	ctx.Logf("Created trust rule %s\n", rule.Name)
	ctx.OutputLine(rule.Id)
	return nil
}

func commandOrgTrustRuleList(ctx *CommandContext, flags *cmd.OrgTrustRuleListFlags) error {
	if flags.IdentityProviderID != "" && flags.IdentityProviderName != "" {
		return cmd.NewErrUsagef("pass --identity-provider-id or --identity-provider-name, not both")
	}
	if flags.ServiceAccountID != "" && flags.ServiceAccountName != "" {
		return cmd.NewErrUsagef("pass --service-account-id or --service-account-name, not both")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	var params managementapi.GetV1AccessTrustRulesParams
	if flags.IdentityProviderID != "" {
		params.IdentityProviderId = &flags.IdentityProviderID
	}
	if flags.IdentityProviderName != "" {
		id, err := orgIdentityProviderIDByName(ctx, cl.API(), flags.IdentityProviderName)
		if err != nil {
			return err
		}
		params.IdentityProviderId = &id
	}
	if flags.ServiceAccountID != "" {
		params.ServiceAccountId = &flags.ServiceAccountID
	}
	if flags.ServiceAccountName != "" {
		id, err := orgServiceAccountIDByName(ctx, cl.API(), flags.ServiceAccountName)
		if err != nil {
			return err
		}
		params.ServiceAccountId = &id
	}
	if flags.Team != "" {
		id, err := ResolveTeam(ctx, cl.API(), flags.Team)
		if err != nil {
			return err
		}
		params.TeamId = &id
	}
	var rules []managementapi.AccessTrustRule
	for {
		page, err := cl.API().GetAccessTrustRules(ctx, params)
		if err != nil {
			return fmt.Errorf("listing trust rules: %w", err)
		}
		rules = append(rules, page.Items...)
		if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
			break
		}
		params.Cursor = page.Pagination.Cursor
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.OrgTrustRuleList{Items: normalizeJSONSlice(rules)})
		return nil
	}
	if len(rules) == 0 {
		ctx.LogLine("No trust rules found.")
		return nil
	}
	rows := make([][]string, 0, len(rules))
	for _, r := range rules {
		lastUsed := "-"
		if r.LastUsedAt != nil {
			lastUsed = r.LastUsedAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{
			r.Id, r.Name, r.IdentityProviderName, r.ServiceAccountName, r.CreatedAt.UTC().Format(time.RFC3339), lastUsed,
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "IDENTITY PROVIDER", "SERVICE ACCOUNT", "CREATED", "LAST USED"},
		Rows:    rows,
	})
	return nil
}

// resolveOrgTrustRuleRef resolves --id or --name to a trust rule ID.
func resolveOrgTrustRuleRef(ctx context.Context, api *managementapi.Client, ref cmd.OrgTrustRuleRefFlags) (string, error) {
	if ref.ID != "" {
		return ref.ID, nil
	}
	page, err := api.GetAccessTrustRules(ctx, managementapi.GetV1AccessTrustRulesParams{Name: &ref.Name})
	if err != nil {
		return "", fmt.Errorf("looking up trust rule %q: %w", ref.Name, err)
	}
	// Names are unique in the organization, so the filter matches at most one.
	if len(page.Items) == 0 {
		return "", cmd.NewErrNotFound(fmt.Errorf("no trust rule named %q", ref.Name))
	}
	return page.Items[0].Id, nil
}

func commandOrgTrustRuleDescribe(ctx *CommandContext, flags *cmd.OrgTrustRuleDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveOrgTrustRuleRef(ctx, cl.API(), flags.OrgTrustRuleRefFlags)
	if err != nil {
		return err
	}
	r, err := cl.API().GetAccessTrustRulesTrustRuleId(ctx, id)
	if err != nil {
		return fmt.Errorf("describing trust rule %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(r)
		return nil
	}
	lastUsed := "-"
	if r.LastUsedAt != nil {
		lastUsed = r.LastUsedAt.UTC().Format(time.RFC3339)
	}
	ctx.Outputf("ID:                 %s\n", r.Id)
	ctx.Outputf("Name:               %s\n", r.Name)
	ctx.Outputf("Description:        %s\n", deref(r.Description))
	ctx.Outputf("Identity Provider:  %s (%s)\n", r.IdentityProviderName, r.IdentityProviderId)
	ctx.Outputf("Service Account:    %s (%s)\n", r.ServiceAccountName, r.ServiceAccountId)
	ctx.Outputf("Team:               %s\n", deref(r.TeamName))
	ctx.Outputf("Condition:          %s\n", r.Condition)
	ctx.Outputf("Audience:           %s\n", r.Audience)
	ctx.Outputf("Max Token Lifetime: %s\n", time.Duration(r.MaxAccessTokenLifetimeSeconds)*time.Second)
	ctx.Outputf("Created:            %s\n", r.CreatedAt.UTC().Format(time.RFC3339))
	ctx.Outputf("Last Used:          %s\n", lastUsed)
	return nil
}

func commandOrgTrustRuleUpdate(ctx *CommandContext, flags *cmd.OrgTrustRuleUpdateFlags) error {
	if flags.ServiceAccountID != "" && flags.ServiceAccountName != "" {
		return cmd.NewErrUsagef("pass --service-account-id or --service-account-name, not both")
	}
	req := managementapi.UpdateAccessTrustRuleRequest{Description: flags.Description.APIOptional()}
	if flags.NewName != "" {
		req.Name = &flags.NewName
	}
	if flags.ServiceAccountID != "" {
		req.ServiceAccountId = &flags.ServiceAccountID
	}
	if flags.Condition != "" {
		req.Condition = &flags.Condition
	}
	if flags.Audience != "" {
		req.Audience = &flags.Audience
	}
	if flags.MaxAccessTokenLifetime != 0 {
		seconds := int(flags.MaxAccessTokenLifetime / time.Second)
		req.MaxAccessTokenLifetimeSeconds = &seconds
	}
	if req == (managementapi.UpdateAccessTrustRuleRequest{}) && flags.ServiceAccountName == "" {
		return cmd.NewErrUsagef("nothing to update; pass --new-name, --description, --service-account-id, --service-account-name, --condition, --audience or --max-access-token-lifetime")
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveOrgTrustRuleRef(ctx, cl.API(), flags.OrgTrustRuleRefFlags)
	if err != nil {
		return err
	}
	if flags.ServiceAccountName != "" {
		accountID, err := orgServiceAccountIDByName(ctx, cl.API(), flags.ServiceAccountName)
		if err != nil {
			return err
		}
		req.ServiceAccountId = &accountID
	}
	rule, err := cl.API().PatchAccessTrustRules(ctx, id, req)
	if err != nil {
		return fmt.Errorf("updating trust rule %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(rule)
		return nil
	}
	ctx.Logf("Updated trust rule %s\n", rule.Name)
	return nil
}

func commandOrgTrustRuleDelete(ctx *CommandContext, flags *cmd.OrgTrustRuleDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveOrgTrustRuleRef(ctx, cl.API(), flags.OrgTrustRuleRefFlags)
	if err != nil {
		return err
	}
	tomb, err := cl.API().DeleteAccessTrustRules(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting trust rule %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(tomb)
		return nil
	}
	ctx.Logf("Deleted trust rule %s\n", tomb.Id)
	return nil
}
