package cmd

import (
	"fmt"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("org team describe", commandOrgTeamDescribe)
	Register("org team list", commandOrgTeamList)
}

func commandOrgTeamList(ctx *CommandContext, flags *cmd.OrgTeamListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	teams, err := cl.API().GetTeams(ctx, managementapi.GetV1TeamsParams{})
	if err != nil {
		return fmt.Errorf("listing teams: %w", err)
	}

	if ctx.JSON {
		ctx.OutputJSON(teams)
		return nil
	}
	if len(teams.Teams) == 0 {
		ctx.LogLine("No teams found.")
		return nil
	}
	rows := make([][]string, 0, len(teams.Teams))
	for _, t := range teams.Teams {
		def := ""
		if t.Default {
			def = "yes"
		}
		rows = append(rows, []string{t.Id, t.Name, def, t.CreatedAt.UTC().Format(time.RFC3339)})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "DEFAULT", "CREATED"},
		Rows:    rows,
	})
	return nil
}

func commandOrgTeamDescribe(ctx *CommandContext, flags *cmd.OrgTeamDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	var team *managementapi.Team
	if flags.TeamName != "" {
		resp, err := cl.API().GetTeams(ctx, managementapi.GetV1TeamsParams{Name: &flags.TeamName})
		if err != nil {
			return fmt.Errorf("describe team %q: %w", flags.TeamName, err)
		}
		if len(resp.Teams) == 0 {
			return fmt.Errorf("no team named %q", flags.TeamName)
		} else if len(resp.Teams) > 1 {
			return fmt.Errorf("multiple teams named %q; pass --team-id instead", flags.TeamName)
		}
		team = &resp.Teams[0]
	} else {
		team, err = cl.API().GetTeamsTeamId(ctx, flags.TeamID)
		if err != nil {
			return fmt.Errorf("describe team %s: %w", flags.TeamID, err)
		}
	}

	if ctx.JSON {
		ctx.OutputJSON(team)
		return nil
	}
	def := "no"
	if team.Default {
		def = "yes"
	}
	ctx.Outputf("ID:       %s\n", team.Id)
	ctx.Outputf("Name:     %s\n", team.Name)
	ctx.Outputf("Default:  %s\n", def)
	ctx.Outputf("Created:  %s\n", team.CreatedAt.UTC().Format(time.RFC3339))
	return nil
}
