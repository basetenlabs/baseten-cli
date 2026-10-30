package cmd

import (
	"fmt"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("grid connection list", commandGridConnectionList)
	Register("grid connection describe", commandGridConnectionDescribe)
	Register("grid connection create", commandGridConnectionCreate)
	Register("grid connection update", commandGridConnectionUpdate)
	Register("grid connection delete", commandGridConnectionDelete)
}

func commandGridConnectionList(ctx *CommandContext, f *cmd.GridConnectionListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	team, err := gridTeamID(ctx, cl.API(), f.Team)
	if err != nil {
		return err
	}
	items, err := listGridConnections(ctx, cl.API(), managementapi.GetV1GridConnectionsParams{TeamId: team})
	if err != nil {
		return fmt.Errorf("listing connections: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.GridConnectionList{Items: normalizeJSONSlice(items)})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No connections found.")
		return nil
	}
	return outputGridConnections(ctx, items)
}

func commandGridConnectionDescribe(ctx *CommandContext, f *cmd.GridConnectionDescribeFlags) error {
	if f.ID == "" {
		return cmd.NewErrUsagef("--id must not be empty")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	connection, err := cl.API().GetGridConnectionsConnectionId(ctx, f.ID)
	if err != nil {
		return fmt.Errorf("getting connection: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(connection)
		return nil
	}
	provider, secretName, err := gridConnectionConfigFields(connection)
	if err != nil {
		return err
	}
	ctx.Outputf("ID:          %s\n", connection.Id)
	ctx.Outputf("Team Name:   %s\n", connection.TeamName)
	ctx.Outputf("Provider:    %s\n", enumFromAPIValue(provider))
	ctx.Outputf("Secret Name: %s\n", secretName)
	ctx.Outputf("Created:     %s\n", connection.CreatedAt.UTC().Format(time.RFC3339))
	ctx.Outputf("Updated:     %s\n", connection.UpdatedAt.UTC().Format(time.RFC3339))
	return nil
}

func setGridConnectionConfig(config interface {
	FromUpsertGridConnectionConfigAnthropic(managementapi.UpsertGridConnectionConfigAnthropic) error
	FromUpsertGridConnectionConfigOpenAI(managementapi.UpsertGridConnectionConfigOpenAI) error
	FromUpsertGridConnectionConfigXAI(managementapi.UpsertGridConnectionConfigXAI) error
}, f cmd.GridConnectionConfigFlags) error {
	if f.SecretName == "" {
		return cmd.NewErrUsagef("--secret-name must not be empty")
	}
	switch f.Provider {
	case "anthropic":
		return config.FromUpsertGridConnectionConfigAnthropic(managementapi.UpsertGridConnectionConfigAnthropic{Provider: "ANTHROPIC", SecretName: f.SecretName})
	case "openai":
		return config.FromUpsertGridConnectionConfigOpenAI(managementapi.UpsertGridConnectionConfigOpenAI{Provider: "OPENAI", SecretName: f.SecretName})
	case "xai":
		return config.FromUpsertGridConnectionConfigXAI(managementapi.UpsertGridConnectionConfigXAI{Provider: "XAI", SecretName: f.SecretName})
	default:
		return cmd.NewErrUsagef("unsupported provider %q", f.Provider)
	}
}

func commandGridConnectionCreate(ctx *CommandContext, f *cmd.GridConnectionCreateFlags) error {
	body := managementapi.CreateGridConnectionRequest{}
	if err := setGridConnectionConfig(&body.Config, f.GridConnectionConfigFlags); err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	body.TeamId, err = gridTeamID(ctx, cl.API(), f.Team)
	if err != nil {
		return err
	}
	connection, err := cl.API().PostGridConnections(ctx, body)
	if err != nil {
		return fmt.Errorf("creating connection: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(connection)
		return nil
	}
	ctx.Logf("Created connection %s\n", connection.Id)
	return nil
}

func commandGridConnectionUpdate(ctx *CommandContext, f *cmd.GridConnectionUpdateFlags) error {
	if f.ID == "" {
		return cmd.NewErrUsagef("--id must not be empty")
	}
	body := managementapi.UpdateGridConnectionRequest{}
	if err := setGridConnectionConfig(&body.Config, f.GridConnectionConfigFlags); err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	connection, err := cl.API().PatchGridConnections(ctx, f.ID, body)
	if err != nil {
		return fmt.Errorf("updating connection: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(connection)
		return nil
	}
	ctx.Logf("Updated connection %s\n", connection.Id)
	return nil
}

func commandGridConnectionDelete(ctx *CommandContext, f *cmd.GridConnectionDeleteFlags) error {
	if f.ID == "" {
		return cmd.NewErrUsagef("--id must not be empty")
	}
	if !f.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete connection %s?", f.ID)); err != nil {
			return err
		}
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	result, err := cl.API().DeleteGridConnections(ctx, f.ID)
	if err != nil {
		return fmt.Errorf("deleting connection: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(result)
	} else {
		ctx.Logf("Deleted connection %s\n", result.Id)
	}
	return nil
}

func outputGridConnections(ctx *CommandContext, connections []managementapi.GridConnection) error {
	var rows [][]string
	for _, connection := range connections {
		provider, secretName, err := gridConnectionConfigFields(&connection)
		if err != nil {
			return err
		}
		rows = append(rows, []string{connection.Id, connection.TeamId, connection.TeamName, enumFromAPIValue(provider), secretName})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"ID", "TEAM ID", "TEAM NAME", "PROVIDER", "SECRET NAME"}, Rows: rows})
	return nil
}

func gridConnectionConfigFields(connection *managementapi.GridConnection) (string, string, error) {
	value, err := connection.Config.ValueByDiscriminator()
	if err != nil {
		provider, discriminatorErr := connection.Config.Discriminator()
		// The SDK retains a typed value when a known variant fails to decode.
		if value != nil || discriminatorErr != nil || provider == "" {
			return "", "", fmt.Errorf("reading connection %s config: %w", connection.Id, err)
		}
	}
	switch config := value.(type) {
	case managementapi.GridConnectionConfigAnthropic:
		return config.Provider, config.SecretName, nil
	case managementapi.GridConnectionConfigOpenAI:
		return config.Provider, config.SecretName, nil
	case managementapi.GridConnectionConfigXAI:
		return config.Provider, config.SecretName, nil
	default:
		return "<unknown>", "<unknown>", nil
	}
}
