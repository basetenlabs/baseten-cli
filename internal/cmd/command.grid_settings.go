package cmd

import (
	"fmt"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("grid team-settings describe", commandGridTeamSettingsDescribe)
	Register("grid team-settings update", commandGridTeamSettingsUpdate)
	Register("grid user-settings list", commandGridUserSettingsList)
	Register("grid user-settings describe", commandGridUserSettingsDescribe)
	Register("grid user-settings update", commandGridUserSettingsUpdate)
}

func gridTeamID(ctx *CommandContext, api *managementapi.Client, team string) (*string, error) {
	id, err := ResolveTeam(ctx, api, team)
	if err != nil || id == "" {
		return nil, err
	}
	return &id, nil
}

func commandGridTeamSettingsDescribe(ctx *CommandContext, f *cmd.GridTeamSettingsDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	team, err := gridTeamID(ctx, cl.API(), f.Team)
	if err != nil {
		return err
	}
	settings, err := cl.API().GetGridTeamSettings(ctx, managementapi.GetV1GridTeamSettingsParams{TeamId: team})
	if err != nil {
		return fmt.Errorf("getting team settings: %w", err)
	}
	outputGridTeamSettings(ctx, settings)
	return nil
}

func gridOptional[T any](flag cmd.OptionalFlag[T]) managementapi.Optional[T] {
	if !flag.IsSet() {
		return managementapi.Optional[T]{}
	}
	return managementapi.NewOptional(flag.Pointer())
}

func gridTeamSettingsUpdate(f *cmd.GridTeamSettingsUpdateFlags) (managementapi.UpdateGridTeamSettingsRequest, error) {
	body := managementapi.UpdateGridTeamSettingsRequest{}
	roles := f.PrimaryRoute.IsSet() || f.BackgroundRoute.IsSet()
	if f.ClearHarnessDefaults && (f.Harness != "" || roles) {
		return body, cmd.NewErrUsagef("--clear-harness-defaults cannot be combined with --harness or route flags")
	}
	if roles != (f.Harness != "") {
		return body, cmd.NewErrUsagef("pass --harness together with --primary-route or --background-route")
	}
	if f.ClearSpendLimit && (f.PerMemberMonthlyLimitUSD.IsSet() || f.TeamWideMonthlyLimitUSD.IsSet()) {
		return body, cmd.NewErrUsagef("--clear-spend-limit cannot be combined with --per-member-monthly-limit-usd or --team-wide-monthly-limit-usd")
	}
	if !roles && !f.ClearHarnessDefaults && !f.ClearSpendLimit && !f.PerMemberMonthlyLimitUSD.IsSet() && !f.TeamWideMonthlyLimitUSD.IsSet() {
		return body, cmd.NewErrUsagef("pass a harness default or spend limit to update")
	}
	if f.ClearHarnessDefaults {
		body.HarnessDefaults = managementapi.NewOptional[managementapi.UpdateGridHarnessDefaults](nil)
	} else if roles {
		defaults := managementapi.UpdateGridHarnessDefaults{}
		primary := managementapi.UpdateGridPrimaryHarnessModels{Primary: gridOptional(f.PrimaryRoute)}
		background := managementapi.UpdateGridBackgroundHarnessModels{Primary: gridOptional(f.PrimaryRoute), Background: gridOptional(f.BackgroundRoute)}
		switch f.Harness {
		case "claude-code":
			defaults.ClaudeCode = managementapi.NewOptional(&background)
		case "opencode":
			defaults.Opencode = managementapi.NewOptional(&background)
		case "codex", "pi":
			if f.BackgroundRoute.IsSet() {
				return body, cmd.NewErrUsagef("%s does not support --background-route", f.Harness)
			}
			if f.Harness == "codex" {
				defaults.Codex = managementapi.NewOptional(&primary)
			} else {
				defaults.Pi = managementapi.NewOptional(&primary)
			}
		default:
			return body, cmd.NewErrUsagef("unsupported harness %q", f.Harness)
		}
		body.HarnessDefaults = managementapi.NewOptional(&defaults)
	}
	if f.ClearSpendLimit {
		body.SpendLimit = managementapi.NewOptional[managementapi.UpdateGridTeamSpendLimitSetting](nil)
	} else if f.PerMemberMonthlyLimitUSD.IsSet() || f.TeamWideMonthlyLimitUSD.IsSet() {
		body.SpendLimit = managementapi.NewOptional(&managementapi.UpdateGridTeamSpendLimitSetting{
			PerMemberMonthlyLimitUsd: gridOptional(f.PerMemberMonthlyLimitUSD),
			TeamWideMonthlyLimitUsd:  gridOptional(f.TeamWideMonthlyLimitUSD),
		})
	}
	return body, nil
}

func commandGridTeamSettingsUpdate(ctx *CommandContext, f *cmd.GridTeamSettingsUpdateFlags) error {
	body, err := gridTeamSettingsUpdate(f)
	if err != nil {
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
	settings, err := cl.API().PatchGridTeamSettings(ctx, body)
	if err != nil {
		return fmt.Errorf("updating team settings: %w", err)
	}
	outputGridTeamSettings(ctx, settings)
	return nil
}

func outputGridTeamSettings(ctx *CommandContext, settings *managementapi.GridTeamSettings) {
	if ctx.JSON {
		ctx.OutputJSON(settings)
		return
	}
	ctx.Outputf("Team ID:                       %s\n", settings.TeamId)
	ctx.Outputf("Team name:                     %s\n", settings.TeamName)
	ctx.Outputf("Per-member monthly limit USD:  %s\n", gridSettingText(settings.SpendLimit.PerMemberMonthlyLimitUsd))
	ctx.Outputf("Team-wide monthly limit USD:   %s\n", gridSettingText(settings.SpendLimit.TeamWideMonthlyLimitUsd))
	var rows [][]string
	for _, item := range gridHarnessDefaultItems(settings.HarnessDefaults, nil) {
		rows = append(rows, []string{item.Harness, gridHarnessDefaultLabel(item.Primary, true), gridHarnessDefaultLabel(item.Background, gridHarnessSupportsBackground(item.Harness))})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"HARNESS", "PRIMARY", "BACKGROUND"}, Rows: rows})
}

func commandGridUserSettingsList(ctx *CommandContext, f *cmd.GridUserSettingsListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	team, err := gridTeamID(ctx, cl.API(), f.Team)
	if err != nil {
		return err
	}
	params := managementapi.GetV1GridTeamSettingsUsersParams{TeamId: team}
	var items []managementapi.GridTeamUserSettings
	for {
		page, err := cl.API().GetGridTeamSettingsUsers(ctx, params)
		if err != nil {
			return fmt.Errorf("listing user settings: %w", err)
		}
		items = append(items, page.Items...)
		if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
			break
		}
		params.Cursor = page.Pagination.Cursor
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.GridUserSettingsList{Items: normalizeJSONSlice(items)})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No user settings found.")
		return nil
	}
	var rows [][]string
	for _, user := range items {
		rows = append(rows, []string{user.UserId, gridSettingText(user.Email), user.TeamId, user.TeamName, user.MonthSpendUsd,
			gridSettingText(user.SpendLimit.Effective.MonthlyLimitUsd), gridSettingText(user.BlockedBy)})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"USER", "EMAIL", "TEAM ID", "TEAM NAME", "MONTH SPEND", "EFFECTIVE LIMIT", "BLOCKED BY"}, Rows: rows})
	return nil
}

func commandGridUserSettingsDescribe(ctx *CommandContext, f *cmd.GridUserSettingsDescribeFlags) error {
	if f.User == "" {
		return cmd.NewErrUsagef("--user-id must be a user ID or me")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	team, err := gridTeamID(ctx, cl.API(), f.Team)
	if err != nil {
		return err
	}
	settings, err := cl.API().GetGridTeamSettingsUsersUserId(ctx, f.User, managementapi.GetV1GridTeamSettingsUsersUserIdParams{TeamId: team})
	if err != nil {
		return fmt.Errorf("getting user settings: %w", err)
	}
	outputGridUserSettings(ctx, settings)
	return nil
}

func commandGridUserSettingsUpdate(ctx *CommandContext, f *cmd.GridUserSettingsUpdateFlags) error {
	if f.User == "" {
		return cmd.NewErrUsagef("--user-id must be a user ID or me")
	}
	if !f.UserMonthlyLimitUSD.IsSet() && !f.MonthOverrideUSD.IsSet() {
		return cmd.NewErrUsagef("pass --user-monthly-limit-usd or --month-override-usd")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	team, err := gridTeamID(ctx, cl.API(), f.Team)
	if err != nil {
		return err
	}
	settings, err := cl.API().PatchGridTeamSettingsUsers(ctx, f.User, managementapi.UpdateGridUserSettingsRequest{
		TeamId: team,
		SpendLimit: managementapi.NewOptional(&managementapi.UpdateGridSpendLimitSetting{
			UserMonthlyLimitUsd: gridOptional(f.UserMonthlyLimitUSD), MonthOverrideUsd: gridOptional(f.MonthOverrideUSD),
		}),
	})
	if err != nil {
		return fmt.Errorf("updating user settings: %w", err)
	}
	outputGridUserSettings(ctx, settings)
	return nil
}

func gridSettingText[T ~string](value *T) string {
	if value == nil {
		return "-"
	}
	return string(*value)
}

func outputGridUserSettings(ctx *CommandContext, settings *managementapi.GridTeamUserSettings) {
	if ctx.JSON {
		ctx.OutputJSON(settings)
		return
	}
	ctx.Outputf("User ID:                    %s\n", settings.UserId)
	ctx.Outputf("Email:                      %s\n", gridSettingText(settings.Email))
	ctx.Outputf("Team ID:                    %s\n", settings.TeamId)
	ctx.Outputf("Team name:                  %s\n", settings.TeamName)
	ctx.Outputf("Month spend USD:            %s\n", settings.MonthSpendUsd)
	ctx.Outputf("Standing monthly limit USD: %s\n", gridSettingText(settings.SpendLimit.UserMonthlyLimitUsd))
	ctx.Outputf("Month override USD:         %s\n", gridSettingText(settings.SpendLimit.MonthOverrideUsd))
	teamDefault := "-"
	if settings.SpendLimit.TeamDefault != nil {
		teamDefault = settings.SpendLimit.TeamDefault.PerMemberMonthlyLimitUsd
	}
	ctx.Outputf("Team default limit USD:     %s\n", teamDefault)
	ctx.Outputf("Effective limit USD:        %s\n", gridSettingText(settings.SpendLimit.Effective.MonthlyLimitUsd))
	ctx.Outputf("Effective limit source:     %s\n", gridSettingText(settings.SpendLimit.Effective.Source))
	ctx.Outputf("Team-wide limit USD:        %s\n", gridSettingText(settings.SpendLimit.TeamWideMonthlyLimitUsd))
	ctx.Outputf("Blocked:                    %t\n", settings.IsBlocked)
	ctx.Outputf("Blocked by:                 %s\n", gridSettingText(settings.BlockedBy))
}
