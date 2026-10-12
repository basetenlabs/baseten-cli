package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
	team, err := resolveTeamOrOnly(ctx, api, input)
	if err != nil {
		return "", err
	}
	return team.Id, nil
}

// resolveTeamOrOnly is ResolveTeam for callers that also need the team's name. An
// empty input selects the only team there is; with several, the user has to say
// which, the CLI never picks one.
func resolveTeamOrOnly(ctx context.Context, api *managementapi.Client, input string) (*managementapi.Team, error) {
	resp, err := api.GetTeams(ctx, managementapi.GetV1TeamsParams{})
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	if input == "" {
		switch len(resp.Teams) {
		case 1:
			return &resp.Teams[0], nil
		case 0:
			return nil, errors.New("no teams found; pass --team")
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
