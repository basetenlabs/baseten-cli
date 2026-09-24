package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("route api-key list", commandRouteAPIKeyList)
	Register("route api-key delete", commandRouteAPIKeyDelete)
}

// listRouteAPIKeys returns the routes API keys the caller created, across all
// their machines and teams.
func listRouteAPIKeys(ctx context.Context, api *managementapi.Client) (*managementapi.APIKeys, error) {
	category, mine := managementapi.APIKeyCategory_ROUTES, true
	keys, err := api.GetApiKeys(ctx, managementapi.GetV1ApiKeysParams{Type: &category, CreatedByMe: &mine})
	if err != nil {
		return nil, fmt.Errorf("listing routes API keys: %w", err)
	}
	return keys, nil
}

// routesKeyScope identifies where a routes API key is saved on this machine.
func routesKeyScope(ctx *CommandContext, userID, teamID, name string) (auth.RoutesKeyScope, error) {
	remote, err := ctx.authInfo.Remote()
	if err != nil {
		return auth.RoutesKeyScope{}, err
	}
	session, err := ctx.authInfo.Session()
	if err != nil {
		return auth.RoutesKeyScope{}, err
	}
	return auth.RoutesKeyScope{
		ManagementURL: remote.ManagementURL(),
		Profile:       session.ProfileName(),
		UserID:        userID,
		TeamID:        teamID,
		Name:          name,
	}, nil
}

// forgetRouteAPIKeys removes this machine's saved copies of deleted keys, so
// harness setup creates new ones.
func forgetRouteAPIKeys(ctx *CommandContext, api *managementapi.Client, deleted []managementapi.APIKeyInfo) error {
	user, err := api.GetUsersMe(ctx)
	if err != nil {
		return fmt.Errorf("getting current user: %w", err)
	}
	teams, err := api.GetTeams(ctx, managementapi.GetV1TeamsParams{})
	if err != nil {
		return fmt.Errorf("list teams: %w", err)
	}
	store, err := NewAuthStore(false)
	if err != nil {
		return err
	}
	for _, k := range deleted {
		if k.Name == nil || k.TeamName == nil {
			continue
		}
		for _, team := range teams.Teams {
			if team.Name != *k.TeamName {
				continue
			}
			scope, err := routesKeyScope(ctx, user.UserId, team.Id, *k.Name)
			if err != nil {
				return err
			}
			saved, err := store.GetRoutesKey(scope)
			if err != nil {
				return err
			}
			if saved != "" && strings.HasPrefix(saved, k.Prefix) {
				if err := store.DeleteRoutesKey(scope); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func commandRouteAPIKeyList(ctx *CommandContext, _ *cmd.RouteAPIKeyListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	keys, err := listRouteAPIKeys(ctx, cl.API())
	if err != nil {
		return err
	}
	if ctx.JSON {
		ctx.OutputJSON(keys)
		return nil
	}
	if len(keys.Keys) == 0 {
		ctx.LogLine("No routes API keys found.")
		return nil
	}
	rows := make([][]string, 0, len(keys.Keys))
	for _, k := range keys.Keys {
		name, team, used := "", "", "never"
		if k.Name != nil {
			name = *k.Name
		}
		if k.TeamName != nil {
			team = *k.TeamName
		}
		if k.LastUsedAt != nil {
			used = k.LastUsedAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{name, k.Prefix, team, k.CreatedAt.UTC().Format(time.RFC3339), used})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"NAME", "PREFIX", "TEAM", "CREATED", "LAST USED"}, Rows: rows})
	return nil
}

func commandRouteAPIKeyDelete(ctx *CommandContext, flags *cmd.RouteAPIKeyDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	keys, err := listRouteAPIKeys(ctx, cl.API())
	if err != nil {
		return err
	}
	targets := keys.Keys
	if !flags.All {
		targets = slices.DeleteFunc(targets, func(k managementapi.APIKeyInfo) bool { return k.Prefix != flags.Prefix })
		if len(targets) == 0 {
			return fmt.Errorf("no routes API key with prefix %q; run 'baseten route api-key list'", flags.Prefix)
		}
	}
	result := cmd.RouteAPIKeyDeleteResult{Deleted: []string{}, Failed: []string{}}
	if len(targets) == 0 {
		if ctx.JSON {
			ctx.OutputJSON(result)
		} else {
			ctx.LogLine("No routes API keys found.")
		}
		return nil
	}
	if !flags.Yes {
		ctx.LogLine("Routes API keys to delete:")
		for _, k := range targets {
			name := ""
			if k.Name != nil {
				name = *k.Name
			}
			ctx.Logf("  %s  %s\n", k.Prefix, name)
		}
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete %d routes API keys? Harnesses using them lose access.", len(targets))); err != nil {
			return err
		}
	}
	var errs []error
	var deleted []managementapi.APIKeyInfo
	for _, k := range targets {
		if _, err := cl.API().DeleteApiKeys(ctx, k.Prefix); err != nil {
			result.Failed = append(result.Failed, k.Prefix)
			errs = append(errs, fmt.Errorf("deleting routes API key %s: %w", k.Prefix, err))
			continue
		}
		deleted = append(deleted, k)
		result.Deleted = append(result.Deleted, k.Prefix)
		ctx.Logf("Deleted routes API key %s\n", k.Prefix)
	}
	if len(deleted) > 0 {
		if err := forgetRouteAPIKeys(ctx, cl.API(), deleted); err != nil {
			errs = append(errs, fmt.Errorf("removing saved copies of deleted keys: %w", err))
		}
	}
	if ctx.JSON {
		ctx.OutputJSON(result)
		if len(errs) > 0 {
			ctx.SuppressJSONError()
		}
	} else if len(result.Deleted) > 0 {
		ctx.LogLine("Rerun 'baseten harness setup' on affected machines to create new keys.")
	}
	return errors.Join(errs...)
}
