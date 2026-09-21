package cmd

import (
	"context"
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
	team, err := resolveTeam(ctx, api, input)
	return team.Id, err
}

// resolveTeam retains the SDK team for callers that also display its name.
func resolveTeam(ctx context.Context, api *managementapi.Client, input string) (managementapi.Team, error) {
	if input == "" {
		return managementapi.Team{}, nil
	}
	resp, err := api.GetTeams(ctx, managementapi.GetV1TeamsParams{})
	if err != nil {
		return managementapi.Team{}, fmt.Errorf("list teams: %w", err)
	}
	var found managementapi.Team
	for _, t := range resp.Teams {
		if t.Id == input {
			return t, nil
		}
		if t.Name == input {
			if found.Id != "" {
				return managementapi.Team{}, fmt.Errorf("multiple teams named %q; pass the team ID instead", input)
			}
			found = t
		}
	}
	if found.Id == "" {
		return managementapi.Team{}, fmt.Errorf("no team matched %q", input)
	}
	return found, nil
}
