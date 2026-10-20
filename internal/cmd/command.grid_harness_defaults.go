package cmd

import (
	"fmt"
	"slices"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("grid harness-defaults", commandGridHarnessDefaults)
}

func commandGridHarnessDefaults(ctx *CommandContext, flags *cmd.GridHarnessDefaultsFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()
	team, err := resolveTeamOrOnly(ctx, api, flags.Team)
	if err != nil {
		return err
	}
	settings, err := api.GetGridTeamSettings(ctx, managementapi.GetV1GridTeamSettingsParams{TeamId: &team.Id})
	if err != nil {
		return fmt.Errorf("getting harness defaults for team %s: %w", team.Name, err)
	}
	items := harnessDefaultItems(settings.HarnessDefaults, flags.Harness)
	if ctx.JSON {
		ctx.OutputJSON(cmd.GridHarnessDefaultsList{
			TeamId:   team.Id,
			TeamName: team.Name,
			Items:    normalizeJSONSlice(items),
		})
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			item.Harness,
			harnessDefaultLabel(item.Primary, true),
			harnessDefaultLabel(item.Background, harnessSupportsBackground(item.Harness)),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"HARNESS", "PRIMARY", "BACKGROUND"},
		Rows:    rows,
	})
	return nil
}

func harnessDefaultItems(defaults managementapi.GridHarnessDefaults, selected []string) []cmd.GridHarnessDefault {
	items := make([]cmd.GridHarnessDefault, 0, len(harness.All()))
	for _, h := range harness.All() {
		if len(selected) > 0 && !slices.Contains(selected, h.Name()) {
			continue
		}
		models := harnessDefaultModels(&defaults, h.Name())
		items = append(items, cmd.GridHarnessDefault{
			Harness:    h.Name(),
			Primary:    models[harnessRolePrimary],
			Background: models[harnessRoleBackground],
		})
	}
	return items
}

func harnessSupportsBackground(name string) bool {
	for _, h := range harness.All() {
		if h.Name() == name {
			return h.BackgroundRoute(harness.Selection{}) != ""
		}
	}
	return false
}

func harnessDefaultLabel(model *managementapi.GridHarnessModel, supported bool) string {
	if !supported {
		return "n/a"
	}
	if model == nil {
		return "-"
	}
	return fmt.Sprintf("%s (%s)", model.Route.Name, model.Source)
}
