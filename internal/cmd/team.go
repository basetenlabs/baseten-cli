package cmd

import (
	"context"
	"errors"
	"fmt"

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
	team, err := resolveTeam(ctx, api, input)
	if err != nil {
		return "", err
	}
	return team.Id, nil
}

// resolveTeam is ResolveTeam for callers that also need the team's name. An
// empty input selects the organization's default team.
func resolveTeam(ctx context.Context, api *managementapi.Client, input string) (*managementapi.Team, error) {
	resp, err := api.GetTeams(ctx, managementapi.GetV1TeamsParams{})
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	var found *managementapi.Team
	for i := range resp.Teams {
		t := &resp.Teams[i]
		switch {
		case input == "" && t.Default, input != "" && t.Id == input:
			return t, nil
		case input != "" && t.Name == input:
			if found != nil {
				return nil, fmt.Errorf("multiple teams named %q; pass the team ID instead", input)
			}
			found = t
		}
	}
	if found != nil {
		return found, nil
	}
	if input == "" {
		return nil, errors.New("the organization has no default team; pass --team")
	}
	return nil, fmt.Errorf("no team matched %q", input)
}
