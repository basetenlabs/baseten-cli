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

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func commandOrgTrustRuleCreate(ctx *CommandContext, flags *cmd.OrgTrustRuleCreateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	providerID, err := resolveIdentityProvider(ctx, cl.API(), flags.IdentityProvider)
	if err != nil {
		return err
	}
	accountID, err := resolveServiceAccount(ctx, cl.API(), flags.ServiceAccount)
	if err != nil {
		return err
	}
	rule, err := cl.API().PostAccessTrustRules(ctx, managementapi.CreateAccessTrustRuleRequest{
		Name:                          flags.Name,
		Description:                   nonEmpty(flags.Description),
		IdentityProviderId:            providerID,
		ServiceAccountId:              accountID,
		Condition:                     flags.Condition,
		Audience:                      flags.Audience,
		MaxAccessTokenLifetimeSeconds: flags.MaxAccessTokenLifetimeSeconds.Pointer(),
	})
	if err != nil {
		return fmt.Errorf("creating trust rule: %w", err)
	}
	outputCreated(ctx, rule, "trust rule", rule.Name, rule.Id)
	return nil
}

func commandOrgTrustRuleList(ctx *CommandContext, flags *cmd.OrgTrustRuleListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	var params managementapi.GetV1AccessTrustRulesParams
	if flags.IdentityProvider != "" {
		id, err := resolveIdentityProvider(ctx, cl.API(), flags.IdentityProvider)
		if err != nil {
			return err
		}
		params.IdentityProviderId = &id
	}
	if flags.ServiceAccount != "" {
		id, err := resolveServiceAccount(ctx, cl.API(), flags.ServiceAccount)
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
	rules, err := listTrustRules(ctx, cl.API(), params)
	if err != nil {
		return err
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.TrustRuleList{Items: normalizeJSONSlice(rules)})
		return nil
	}
	if len(rules) == 0 {
		ctx.LogLine("No trust rules found.")
		return nil
	}
	rows := make([][]string, 0, len(rules))
	for _, r := range rules {
		rows = append(rows, []string{
			r.Id, r.Name, r.IdentityProviderName, r.ServiceAccountName,
			r.CreatedAt.UTC().Format(time.RFC3339), timeOrDash(r.LastUsedAt),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "IDENTITY PROVIDER", "SERVICE ACCOUNT", "CREATED", "LAST USED"},
		Rows:    rows,
	})
	return nil
}

func listTrustRules(ctx context.Context, api *managementapi.Client, params managementapi.GetV1AccessTrustRulesParams) ([]managementapi.AccessTrustRule, error) {
	rules, err := listAllPages(func(cursor *string) ([]managementapi.AccessTrustRule, managementapi.PaginationResponse, error) {
		params.Cursor = cursor
		page, err := api.GetAccessTrustRules(ctx, params)
		if err != nil {
			return nil, managementapi.PaginationResponse{}, err
		}
		return page.Items, page.Pagination, nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing trust rules: %w", err)
	}
	return rules, nil
}

// resolveTrustRuleRef resolves --id or --name to a trust rule ID.
func resolveTrustRuleRef(ctx context.Context, api *managementapi.Client, ref cmd.TrustRuleRefFlags) (string, error) {
	if ref.ID != "" {
		return ref.ID, nil
	}
	rules, err := listTrustRules(ctx, api, managementapi.GetV1AccessTrustRulesParams{})
	if err != nil {
		return "", err
	}
	rule, err := findByName("trust rule", ref.Name, rules, func(r managementapi.AccessTrustRule, name string) bool { return r.Name == name })
	if err != nil {
		return "", err
	}
	return rule.Id, nil
}

func commandOrgTrustRuleDescribe(ctx *CommandContext, flags *cmd.OrgTrustRuleDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveTrustRuleRef(ctx, cl.API(), flags.TrustRuleRefFlags)
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
	ctx.Outputf("ID:                 %s\n", r.Id)
	ctx.Outputf("Name:               %s\n", r.Name)
	ctx.Outputf("Description:        %s\n", valueOrDash(r.Description))
	ctx.Outputf("Identity Provider:  %s (%s)\n", r.IdentityProviderName, r.IdentityProviderId)
	ctx.Outputf("Service Account:    %s (%s)\n", r.ServiceAccountName, r.ServiceAccountId)
	ctx.Outputf("Team:               %s\n", valueOrDash(r.TeamName))
	ctx.Outputf("Condition:          %s\n", r.Condition)
	ctx.Outputf("Audience:           %s\n", r.Audience)
	ctx.Outputf("Max Token Lifetime: %ds\n", r.MaxAccessTokenLifetimeSeconds)
	ctx.Outputf("Created:            %s\n", r.CreatedAt.UTC().Format(time.RFC3339))
	ctx.Outputf("Last Used:          %s\n", timeOrDash(r.LastUsedAt))
	return nil
}

func commandOrgTrustRuleUpdate(ctx *CommandContext, flags *cmd.OrgTrustRuleUpdateFlags) error {
	if flags.NewName == "" && !flags.Description.IsSet() && flags.ServiceAccount == "" &&
		flags.Condition == "" && flags.Audience == "" && !flags.MaxAccessTokenLifetimeSeconds.IsSet() {
		return cmd.NewErrUsagef("nothing to update; pass --new-name, --description, --service-account, --condition, --audience or --max-access-token-lifetime-seconds")
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveTrustRuleRef(ctx, cl.API(), flags.TrustRuleRefFlags)
	if err != nil {
		return err
	}
	req := managementapi.UpdateAccessTrustRuleRequest{
		Name:                          nonEmpty(flags.NewName),
		Description:                   flags.Description.Pointer(),
		Condition:                     nonEmpty(flags.Condition),
		Audience:                      nonEmpty(flags.Audience),
		MaxAccessTokenLifetimeSeconds: flags.MaxAccessTokenLifetimeSeconds.Pointer(),
	}
	if flags.ServiceAccount != "" {
		accountID, err := resolveServiceAccount(ctx, cl.API(), flags.ServiceAccount)
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
	id, err := resolveTrustRuleRef(ctx, cl.API(), flags.TrustRuleRefFlags)
	if err != nil {
		return err
	}
	tomb, err := cl.API().DeleteAccessTrustRules(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting trust rule %s: %w", id, err)
	}
	outputDeleted(ctx, tomb, "trust rule", tomb.Id)
	return nil
}
