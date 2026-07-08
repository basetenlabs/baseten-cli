package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/charmbracelet/huh"
)

func init() {
	Register("model list", commandModelList)
	Register("model describe", commandModelDescribe)
	Register("model delete", commandModelDelete)
}

// ModelRef is the result of resolving [cmd.ModelRefFlags] against the
// management API. Team is the resolved team ID (empty when unscoped).
type ModelRef struct {
	ID   string
	Team string
}

// ResolveModelRef resolves [cmd.ModelRefFlags] into a [ModelRef]. When
// --model-id is set it returns immediately. When --model-name is set it
// resolves the optional team and looks up the model by name; absent or
// ambiguous matches return an error.
func ResolveModelRef(
	ctx context.Context, api *managementapi.Client, flags cmd.ModelRefFlags,
) (ModelRef, error) {
	if flags.ModelID != "" {
		if flags.Team != "" {
			return ModelRef{}, cmd.NewErrUsagef("--team is only valid with --model-name")
		}
		return ModelRef{ID: flags.ModelID}, nil
	}
	teamID, err := ResolveTeam(ctx, api, flags.Team)
	if err != nil {
		return ModelRef{}, err
	}
	id, err := findModelIDByName(ctx, api, flags.ModelName, teamID)
	if err != nil {
		return ModelRef{}, err
	}
	if id == "" {
		if teamID != "" {
			return ModelRef{}, fmt.Errorf("no model named %q in team %q", flags.ModelName, flags.Team)
		}
		return ModelRef{}, fmt.Errorf("no model named %q", flags.ModelName)
	}
	return ModelRef{ID: id, Team: teamID}, nil
}

// findModelIDByName returns the ID of the unique model with the given name,
// scoped to teamID when non-empty. The server filters by exact name, so this
// matches at most one model per team; the org-wide route may still return the
// same name from multiple teams. Returns "" with nil error when no model
// matches. Returns an error when multiple models match (only possible when
// teamID is empty, since (org, team, name) is unique server-side).
func findModelIDByName(
	ctx context.Context, api *managementapi.Client, name, teamID string,
) (string, error) {
	var models []managementapi.Model
	if teamID == "" {
		resp, err := api.GetModels(ctx, managementapi.GetV1ModelsParams{Name: &name})
		if err != nil {
			return "", fmt.Errorf("list models: %w", err)
		}
		models = resp.Models
	} else {
		resp, err := api.GetTeamsModels(ctx, teamID, managementapi.GetV1TeamsTeamIdModelsParams{Name: &name})
		if err != nil {
			return "", fmt.Errorf("list models for team %s: %w", teamID, err)
		}
		models = resp.Models
	}
	switch len(models) {
	case 0:
		return "", nil
	case 1:
		return models[0].Id, nil
	default:
		return "", fmt.Errorf("multiple models named %q across teams; pass --team to disambiguate", name)
	}
}

// listModels returns models, optionally scoped to a single team. teamID is the
// resolved team ID (not name); pass "" to list across all accessible teams.
func listModels(
	ctx context.Context, api *managementapi.Client, teamID string,
) ([]managementapi.Model, error) {
	if teamID == "" {
		resp, err := api.GetModels(ctx, managementapi.GetV1ModelsParams{})
		if err != nil {
			return nil, fmt.Errorf("list models: %w", err)
		}
		return resp.Models, nil
	}
	resp, err := api.GetTeamsModels(ctx, teamID, managementapi.GetV1TeamsTeamIdModelsParams{})
	if err != nil {
		return nil, fmt.Errorf("list models for team %s: %w", teamID, err)
	}
	return resp.Models, nil
}

func commandModelList(ctx *CommandContext, flags *cmd.ModelListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}
	models, err := listModels(ctx, cl.API(), teamID)
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(managementapi.Models{Models: models})
		return nil
	}
	if len(models) == 0 {
		ctx.LogLine("No models found.")
		return nil
	}
	rows := make([][]string, 0, len(models))
	for _, m := range models {
		rows = append(rows, []string{
			m.Id,
			m.Name,
			m.TeamName,
			fmt.Sprintf("%d", m.DeploymentsCount),
			m.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "TEAM", "DEPLOYMENTS", "CREATED"},
		Rows:    rows,
	})
	return nil
}

func commandModelDescribe(ctx *CommandContext, flags *cmd.ModelDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	model, err := cl.API().GetModelsModelId(ctx, ref.ID)
	if err != nil {
		return fmt.Errorf("describe model %s: %w", ref.ID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(model)
		return nil
	}
	ctx.Outputf("ID:           %s\n", model.Id)
	ctx.Outputf("Name:         %s\n", model.Name)
	ctx.Outputf("Team:         %s\n", model.TeamName)
	ctx.Outputf("Deployments:  %d\n", model.DeploymentsCount)
	ctx.Outputf("Instance:     %s\n", model.InstanceTypeName)
	if model.ProductionDeploymentId != nil {
		ctx.Outputf("Production:   %s\n", *model.ProductionDeploymentId)
	}
	if model.DevelopmentDeploymentId != nil {
		ctx.Outputf("Development:  %s\n", *model.DevelopmentDeploymentId)
	}
	ctx.Outputf("Created:      %s\n", model.CreatedAt.UTC().Format(time.RFC3339))
	return nil
}

func commandModelDelete(ctx *CommandContext, flags *cmd.ModelDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveModelRef(ctx, cl.API(), flags.ModelRefFlags)
	if err != nil {
		return err
	}
	model, err := cl.API().GetModelsModelId(ctx, ref.ID)
	if err != nil {
		return fmt.Errorf("fetch model %s: %w", ref.ID, err)
	}

	if !flags.Yes {
		if !ctx.IsInteractive() {
			return cmd.NewErrUsagef("cannot confirm deletion: stdin is not a terminal; pass --yes to skip the prompt")
		}
		var typed string
		err := huh.NewInput().
			Title(fmt.Sprintf("Type the model name %q to confirm deletion", model.Name)).
			Value(&typed).
			Run()
		if err != nil {
			return err
		}
		if typed != model.Name {
			return fmt.Errorf("typed name %q does not match model name %q; deletion aborted", typed, model.Name)
		}
	}

	tombstone, err := cl.API().DeleteModels(ctx, ref.ID)
	if err != nil {
		return fmt.Errorf("delete model %s: %w", ref.ID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(tombstone)
		return nil
	}
	ctx.Logf("Deleted model %s (%s)\n", model.Name, ref.ID)
	return nil
}
