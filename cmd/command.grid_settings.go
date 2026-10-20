package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

const gridTeamDescription = "Omit --team to let the API select your only team; specify a team when you belong to several."

var commandGridTeamSettings = Command{
	Name: "team-settings", Summary: "Read and manage team Grid settings (PRE-RELEASE)",
	Children: []Command{
		{
			Name: "describe", Summary: "Show a team's harness defaults and spend limits",
			Description: gridPrereleaseNotice + gridTeamDescription,
			Flags:       GridTeamSettingsDescribeFlags{},
			Output: &CommandOutput[managementapi.GridTeamSettings]{
				TextDescription: "Team ID, monthly spend limits, and a table of harness default routes and their sources.",
				Examples:        []CommandExample{{Description: "Show your only team's settings.", Command: "baseten grid team-settings describe"}},
				JQExample:       CommandExample{Description: "Print the harness defaults.", Command: "baseten grid team-settings describe --jq '.harness_defaults'"},
			},
		},
		{
			Name: "update", Summary: "Update team harness defaults and spend limits",
			Description: gridPrereleaseNotice + "Changes only supplied fields. Use --harness with --primary-route or --background-route to set route IDs; pass null to clear a role. " +
				"--clear-harness-defaults clears every harness's configured defaults. Spend limits are exact USD amounts; null removes a limit. " +
				"Requires team admin; changing the team-wide limit requires organization admin. " + gridTeamDescription,
			Flags: GridTeamSettingsUpdateFlags{},
			Output: &CommandOutput[managementapi.GridTeamSettings]{
				TextDescription: "The updated team settings, in the same format as describe.",
				Examples: []CommandExample{
					{Description: "Set the default monthly limit per member.", Command: "baseten grid team-settings update --per-member-monthly-limit-usd 100"},
					{Description: "Set Codex's primary route.", CommandLines: []string{"baseten grid team-settings update", "--harness codex --primary-route <route-id>"}},
				},
				JQExample: CommandExample{Description: "Clear and print the team-wide limit.", CommandLines: []string{"baseten grid team-settings update --team-wide-monthly-limit-usd null", "--jq '.spend_limit'"}},
			},
		},
	},
}

var commandGridUserSettings = Command{
	Name: "user-settings", Summary: "Read and manage team members' Grid spend settings (PRE-RELEASE)",
	Children: []Command{
		{
			Name: "list", Summary: "List team members' spend settings",
			Description: gridPrereleaseNotice + "Requires team admin or organization admin. " + gridTeamDescription,
			Flags:       GridUserSettingsListFlags{},
			Output: &CommandOutput[managementapi.GridTeamUserSettingsList]{
				TextDescription: "Table with USER, EMAIL, MONTH SPEND, EFFECTIVE LIMIT, and BLOCKED BY columns. Empty teams print a message to stderr.",
				Examples:        []CommandExample{{Description: "List members' settings in a team.", Command: "baseten grid user-settings list --team <id>"}},
				JQExample:       CommandExample{Description: "Print each member's limits.", Command: "baseten grid user-settings list --jq '.users[].spend_limit'"},
			},
		},
		{
			Name: "describe", Summary: "Show a user's spend settings in a team",
			Description: gridPrereleaseNotice + "Defaults to your own settings (--user me). Reading other users requires team admin or organization admin. " + gridTeamDescription,
			Flags:       GridUserSettingsDescribeFlags{},
			Output: &CommandOutput[managementapi.GridTeamUserSettings]{
				TextDescription: "User and team identifiers, monthly spend, standing and override limits, effective limit, and blocked status.",
				Examples:        []CommandExample{{Description: "Show your own settings.", Command: "baseten grid user-settings describe"}},
				JQExample:       CommandExample{Description: "Print your effective monthly limit.", Command: "baseten grid user-settings describe --jq '.spend_limit.effective'"},
			},
		},
		{
			Name: "update", Summary: "Update a user's spend limits in a team",
			Description: gridPrereleaseNotice + "Defaults to --user me. Requires team admin or organization admin, even when changing your own limits. " +
				"Omitted limits are unchanged; pass null to clear a limit. Amounts are exact USD strings. " + gridTeamDescription,
			Flags: GridUserSettingsUpdateFlags{},
			Output: &CommandOutput[managementapi.GridTeamUserSettings]{
				TextDescription: "The updated user settings, in the same format as describe.",
				Examples:        []CommandExample{{Description: "Set a member's standing monthly limit.", CommandLines: []string{"baseten grid user-settings update --team <id> --user <id>", "--user-monthly-limit-usd 100"}}},
				JQExample:       CommandExample{Description: "Clear and print your current-month override.", CommandLines: []string{"baseten grid user-settings update --month-override-usd null", "--jq '.spend_limit'"}},
			},
		},
	},
}

type GridTeamFlags struct {
	Team string `flag:"team" desc:"Team name or ID. Omit to use your only team; required when you belong to several."`
}
type GridTeamSettingsDescribeFlags struct {
	CommandFlags
	GridTeamFlags
}
type GridTeamSettingsUpdateFlags struct {
	CommandFlags
	GridTeamFlags
	Harness                  string               `flag:"harness" desc:"Harness whose default route IDs to update." enum:"claude-code,codex,opencode,pi"`
	PrimaryRoute             OptionalFlag[string] `flag:"primary-route" desc:"Primary route ID; null clears the configured default. Requires --harness." nullable:"true"`
	BackgroundRoute          OptionalFlag[string] `flag:"background-route" desc:"Background route ID for Claude Code or OpenCode; null clears it. Requires --harness." nullable:"true"`
	ClearHarnessDefaults     bool                 `flag:"clear-harness-defaults" desc:"Clear all configured harness defaults. Cannot be combined with --harness or route flags."`
	PerMemberMonthlyLimitUSD OptionalFlag[string] `flag:"per-member-monthly-limit-usd" desc:"Default monthly USD limit per member; null removes it." nullable:"true"`
	TeamWideMonthlyLimitUSD  OptionalFlag[string] `flag:"team-wide-monthly-limit-usd" desc:"Team-wide monthly USD limit; null removes it. Requires organization admin." nullable:"true"`
}
type GridUserSettingsListFlags struct {
	CommandFlags
	GridTeamFlags
}
type GridUserSettingsUserFlags struct {
	User string `flag:"user" desc:"User ID, or me for yourself." default:"me"`
}
type GridUserSettingsDescribeFlags struct {
	CommandFlags
	GridTeamFlags
	GridUserSettingsUserFlags
}
type GridUserSettingsUpdateFlags struct {
	CommandFlags
	GridTeamFlags
	GridUserSettingsUserFlags
	UserMonthlyLimitUSD OptionalFlag[string] `flag:"user-monthly-limit-usd" desc:"Standing monthly USD limit for this user in the team; null removes it." nullable:"true"`
	MonthOverrideUSD    OptionalFlag[string] `flag:"month-override-usd" desc:"USD limit for the current UTC month; null removes the override." nullable:"true"`
}
