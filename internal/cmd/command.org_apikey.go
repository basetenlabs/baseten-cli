package cmd

import (
	"fmt"
	"time"

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
		"personal":                  managementapi.APIKeyCategory_PERSONAL,
		"workspace-export-metrics":  managementapi.APIKeyCategory_WORKSPACE_EXPORT_METRICS,
		"workspace-invoke":          managementapi.APIKeyCategory_WORKSPACE_INVOKE,
		"workspace-manage-all":      managementapi.APIKeyCategory_WORKSPACE_MANAGE_ALL,
		"workspace-manage-api-keys": managementapi.APIKeyCategory_WORKSPACE_MANAGE_API_KEYS,
		"grid":                      managementapi.APIKeyCategory_GRID,
	}
	apiKeyTypeFromBackend = map[managementapi.APIKeyCategory]string{
		managementapi.APIKeyCategory_PERSONAL:                  "personal",
		managementapi.APIKeyCategory_WORKSPACE_EXPORT_METRICS:  "workspace-export-metrics",
		managementapi.APIKeyCategory_WORKSPACE_INVOKE:          "workspace-invoke",
		managementapi.APIKeyCategory_WORKSPACE_MANAGE_ALL:      "workspace-manage-all",
		managementapi.APIKeyCategory_WORKSPACE_MANAGE_API_KEYS: "workspace-manage-api-keys",
		managementapi.APIKeyCategory_GRID:                      "grid",
	}
)

func commandOrgAPIKeyList(ctx *CommandContext, flags *cmd.OrgAPIKeyListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	var params managementapi.GetV1ApiKeysParams
	if flags.Type != "" {
		keyType := apiKeyTypeToBackend[flags.Type]
		params.Type = &keyType
	}
	keys, err := cl.API().GetApiKeys(ctx, params)
	if err != nil {
		return fmt.Errorf("listing API keys: %w", err)
	}
	outputAPIKeys(ctx, keys)
	return nil
}

// outputAPIKeys prints API key metadata, shared by every API key list command.
func outputAPIKeys(ctx *CommandContext, keys *managementapi.APIKeys) {
	if ctx.JSON {
		ctx.OutputJSON(keys)
		return
	}
	if len(keys.Keys) == 0 {
		ctx.LogLine("No API keys found.")
		return
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
		// A category the CLI does not know yet renders as its raw backend form
		// rather than an empty cell.
		keyType := apiKeyTypeFromBackend[k.Type]
		if keyType == "" {
			keyType = string(k.Type)
		}
		lastUsed := "-"
		if k.LastUsedAt != nil {
			lastUsed = k.LastUsedAt.UTC().Format(time.RFC3339)
		}
		expires := "never"
		if k.ExpiresAt != nil {
			expires = k.ExpiresAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{name, k.Prefix + "****", keyType, team, k.CreatedAt.UTC().Format(time.RFC3339), lastUsed, expires})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"NAME", "KEY", "TYPE", "TEAM", "CREATED", "LAST USED", "EXPIRES"},
		Rows:    rows,
	})
}

func commandOrgAPIKeyCreate(ctx *CommandContext, flags *cmd.OrgAPIKeyCreateFlags) error {
	// --model-id only makes sense for the two scopes that key on it.
	if len(flags.ModelIDs) > 0 && flags.Type != "workspace-export-metrics" && flags.Type != "workspace-invoke" {
		return cmd.NewErrUsagef("--model-id is only valid with --type workspace-export-metrics or workspace-invoke")
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
	if !flags.ExpiresAt.IsZero() {
		req.ExpiresAt = &flags.ExpiresAt
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

	outputCreatedAPIKey(ctx, key, key.ApiKey)
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
		keys, err := cl.API().GetApiKeys(ctx, managementapi.GetV1ApiKeysParams{})
		if err != nil {
			return fmt.Errorf("listing API keys: %w", err)
		}
		if prefix, err = apiKeyPrefixByName(keys.Keys, flags.Name); err != nil {
			return err
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

func apiKeyPrefixByName(keys []managementapi.APIKeyInfo, name string) (string, error) {
	prefix := ""
	for _, k := range keys {
		if k.Name != nil && *k.Name == name {
			if prefix != "" {
				return "", fmt.Errorf("multiple API keys named %q; pass --prefix instead", name)
			}
			prefix = k.Prefix
		}
	}
	if prefix == "" {
		return "", fmt.Errorf("no API key named %q", name)
	}
	return prefix, nil
}

// outputCreatedAPIKey prints a creation response, or under text output only its
// key value, which the API returns only once.
func outputCreatedAPIKey(ctx *CommandContext, response any, apiKey string) {
	if ctx.JSON {
		ctx.OutputJSON(response)
		return
	}
	// One-time warning to stderr so piping captures only the key value.
	ctx.LogLine("Save this key now. It will not be shown again.")
	ctx.OutputLine(apiKey)
}
