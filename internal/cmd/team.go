package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// ResolveTeam translates a --team flag value (team name or team ID) into a
// team ID by listing teams from the management API. Returns "" when input
// is "" so the server can route to the org's default team.
//
// An exact match on Id wins over a name match: this lets users always pass
// an ID without colliding with a same-spelled name.
func ResolveTeam(ctx context.Context, api *managementapi.Client, input string) (string, error) {
	if input == "" {
		return "", nil
	}
	team, err := ResolveTeamOrOnly(ctx, api, input)
	if err != nil {
		return "", err
	}
	return team.Id, nil
}

// ResolveTeamOrOnly is ResolveTeam for callers that also need the team's name. An
// empty input selects the only team there is; with several, the user has to say
// which, the CLI never picks one.
func ResolveTeamOrOnly(ctx context.Context, api *managementapi.Client, input string) (*managementapi.Team, error) {
	resp, err := api.GetTeams(ctx, managementapi.GetV1TeamsParams{})
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	if input == "" {
		switch len(resp.Teams) {
		case 1:
			return &resp.Teams[0], nil
		case 0:
			return nil, errors.New("no teams found")
		}
		names := make([]string, len(resp.Teams))
		for i, t := range resp.Teams {
			names[i] = strconv.Quote(t.Name)
		}
		return nil, cmd.NewErrUsagef("there are %d teams; pass --team with one of %s", len(resp.Teams), strings.Join(names, ", "))
	}
	var found *managementapi.Team
	for i := range resp.Teams {
		t := &resp.Teams[i]
		switch {
		case t.Id == input:
			return t, nil
		case t.Name == input:
			if found != nil {
				return nil, fmt.Errorf("multiple teams named %q; pass the team ID instead", input)
			}
			found = t
		}
	}
	if found != nil {
		return found, nil
	}
	return nil, fmt.Errorf("no team matched %q", input)
}

// CallWithTeam runs call for the team a --team flag names, or with nil when the
// flag is empty so the server uses the caller's only team. When the caller
// belongs to several, the server's error lists them: on a terminal the user
// picks one and call runs again with it, otherwise the error names the teams
// to pass as --team. The list comes from the error, so no request is spent
// listing teams for callers who have only one.
func CallWithTeam[T any](ctx *CommandContext, api *managementapi.Client, team string, call func(teamID *string) (T, error)) (T, error) {
	id, err := ResolveTeam(ctx, api, team)
	if err != nil {
		var zero T
		return zero, err
	}
	var teamID *string
	if id != "" {
		teamID = &id
	}
	out, err := call(teamID)
	var mre *managementapi.ResponseError
	if !errors.As(err, &mre) {
		return out, err
	}
	// Older servers send no details, and a body we can't decode leaves them
	// empty, so both return the error unchanged.
	var body struct {
		Details struct {
			Reason string `json:"reason"`
			Teams  []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"teams"`
		} `json:"details"`
	}
	_ = json.Unmarshal([]byte(mre.Body), &body)
	if body.Details.Reason != "AMBIGUOUS_TEAM" {
		return out, err
	}
	teams := body.Details.Teams
	if !ctx.IsInteractive() {
		names := make([]string, len(teams))
		for i, t := range teams {
			names[i] = strconv.Quote(t.Name)
		}
		// The API error stays in the chain for --output json, but its raw body
		// stays out of the message.
		msg := fmt.Sprintf("you belong to %d teams; pass --team with one of %s", len(teams), strings.Join(names, ", "))
		return out, &cmd.ErrValidation{CommandErrorMeta: cmd.NewCommandErrorMeta(msg, err)}
	}
	options := make([]huh.Option[string], len(teams))
	for i, t := range teams {
		options[i] = huh.NewOption(t.Name, t.ID)
	}
	var chosen string
	if err := ctx.RunPrompt(huh.NewSelect[string]().Title("Use which team?").Options(options...).Value(&chosen)); err != nil {
		var zero T
		return zero, err
	}
	return call(&chosen)
}
