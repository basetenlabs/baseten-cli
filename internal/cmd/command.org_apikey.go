package cmd

import (
	"fmt"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("org api-key list", commandOrgAPIKeyList)
	Register("org api-key create", commandOrgAPIKeyCreate)
	Register("org api-key delete", commandOrgAPIKeyDelete)
}

// Maps between the lowercase-kebab CLI form and the ALL_CAPS backend form.
var (
	apiKeyTypeToBackend = map[string]managementapi.APIKeyCategory{
		"personal":                 managementapi.APIKeyCategory_PERSONAL,
		"workspace-export-metrics": managementapi.APIKeyCategory_WORKSPACE_EXPORT_METRICS,
		"workspace-invoke":         managementapi.APIKeyCategory_WORKSPACE_INVOKE,
		"workspace-manage-all":     managementapi.APIKeyCategory_WORKSPACE_MANAGE_ALL,
	}
	apiKeyTypeFromBackend = map[managementapi.APIKeyCategory]string{
		managementapi.APIKeyCategory_PERSONAL:                 "personal",
		managementapi.APIKeyCategory_WORKSPACE_EXPORT_METRICS: "workspace-export-metrics",
		managementapi.APIKeyCategory_WORKSPACE_INVOKE:         "workspace-invoke",
		managementapi.APIKeyCategory_WORKSPACE_MANAGE_ALL:     "workspace-manage-all",
	}
)

func commandOrgAPIKeyList(ctx *CommandContext, _ *cmd.OrgAPIKeyListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	keys, err := cl.API().GetApiKeys(ctx)
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
		name := ""
		if k.Name != nil {
			name = *k.Name
		}
		team := ""
		if k.TeamName != nil {
			team = *k.TeamName
		}
		rows = append(rows, []string{name, k.Prefix + "****", apiKeyTypeFromBackend[k.Type], team})
	}
	ctx.OutputTable([]string{"NAME", "KEY", "TYPE", "TEAM"}, rows)
	return nil
}

func commandOrgAPIKeyCreate(ctx *CommandContext, flags *cmd.OrgAPIKeyCreateFlags) error {
	// --model-id only makes sense for the two scopes that key on it.
	if len(flags.ModelIDs) > 0 && flags.Type != "workspace-export-metrics" && flags.Type != "workspace-invoke" {
		return &ErrUsage{Err: fmt.Errorf("--model-id is only valid with --type workspace-export-metrics or workspace-invoke")}
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}

	req := managementapi.CreateAPIKeyRequest{Type: apiKeyTypeToBackend[flags.Type]}
	if flags.Name != "" {
		req.Name = &flags.Name
	}
	if len(flags.ModelIDs) > 0 {
		ids := append([]string(nil), flags.ModelIDs...)
		req.ModelIds = &ids
	}

	var key *managementapi.APIKey
	if teamID == "" {
		key, err = cl.API().PostApiKeys(ctx, req)
	} else {
		key, err = cl.API().PostTeamsApiKeys(ctx, teamID, req)
	}
	if err != nil {
		return fmt.Errorf("creating API key: %w", err)
	}

	if ctx.JSON {
		ctx.OutputJSON(key)
		return nil
	}
	// One-time warning to stderr so piping captures only the key value.
	ctx.LogLine("Save this key now. It will not be shown again.")
	ctx.OutputLine(key.ApiKey)
	return nil
}

func commandOrgAPIKeyDelete(ctx *CommandContext, flags *cmd.OrgAPIKeyDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	// Resolve --name to a prefix by listing; --prefix is passed through.
	prefix := flags.Prefix
	if flags.Name != "" {
		keys, err := cl.API().GetApiKeys(ctx)
		if err != nil {
			return fmt.Errorf("listing API keys: %w", err)
		}
		for _, k := range keys.Keys {
			if k.Name != nil && *k.Name == flags.Name {
				if prefix != "" {
					return fmt.Errorf("multiple API keys named %q; pass --prefix instead", flags.Name)
				}
				prefix = k.Prefix
			}
		}
		if prefix == "" {
			return fmt.Errorf("no API key named %q", flags.Name)
		}
	}

	tomb, err := cl.API().DeleteApiKeys(ctx, prefix)
	if err != nil {
		return fmt.Errorf("deleting API key: %w", err)
	}

	if ctx.JSON {
		ctx.OutputJSON(tomb)
		return nil
	}
	ctx.Logf("Deleted API key %s\n", tomb.Prefix)
	return nil
}
