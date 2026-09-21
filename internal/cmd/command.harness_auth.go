package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/charmbracelet/huh"
)

// Hostnames may include uppercase letters, dots, or characters the API rejects.
func normalizeHarnessHostname(hostname string) string {
	var name strings.Builder
	separator := false
	for _, r := range strings.ToLower(hostname) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if separator && name.Len() > 0 {
				name.WriteByte('-')
			}
			name.WriteRune(r)
			separator = false
		} else {
			separator = true
		}
	}
	return name.String()
}

func defaultHarnessKeyName(hostname string) string {
	name := normalizeHarnessHostname(strings.TrimSuffix(strings.ToLower(hostname), ".local"))
	if name == "" {
		name = "local-machine"
	}
	return "baseten-harness-" + name
}

// harnessAuth separates credential lookup from creation so dry runs do not mint keys.
type harnessAuth struct {
	store     *auth.Store
	scope     auth.RoutesKeyScope
	saved     string
	teamName  string
	transport *auth.Transport
}

func prepareHarnessAuth(ctx *CommandContext, flags *cmd.HarnessSetupFlags) (*harnessAuth, error) {
	if flags.Team != "" && strings.TrimSpace(flags.Team) == "" {
		return nil, cmd.NewErrUsagef("team cannot be blank")
	}
	var previousNames []string
	if flags.KeyName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, err
		}
		flags.KeyName = defaultHarnessKeyName(hostname)
		previous := "baseten-harness"
		if normalized := normalizeHarnessHostname(hostname); normalized != "" {
			previous += "-" + normalized
		}
		previousNames = []string{strings.TrimPrefix(flags.KeyName, "baseten-harness-"), previous, "baseten-harness-" + hostname}
	}
	if flags.KeyName == "" || strings.IndexFunc(flags.KeyName, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-')
	}) >= 0 {
		return nil, cmd.NewErrUsagef("routes key name can only contain lowercase letters, numbers, and hyphens")
	}
	transport, remote, err := ctx.AuthTransport()
	if err != nil {
		return nil, err
	}
	endpoint := remote.ManagementURL()
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil {
		return nil, cmd.NewErrUsagef("invalid management API endpoint")
	}
	if u.Scheme != "https" && harness.FixtureEndpoint(endpoint) != nil {
		return nil, cmd.NewErrUsagef("routes key creation requires HTTPS or an explicit loopback development endpoint")
	}
	if _, err := transport.Credential(ctx); err != nil {
		return nil, cmd.NewErrAuth(err)
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return nil, err
	}
	var teamID, teamName string
	if flags.Team == "" {
		teams, err := cl.API().GetTeams(ctx, managementapi.GetV1TeamsParams{})
		if err != nil {
			return nil, fmt.Errorf("list teams: %w", err)
		}
		if len(teams.Teams) == 0 {
			return nil, cmd.NewErrUsagef("no teams available for the current account; ask an organization admin to add you to a team")
		}
		if len(teams.Teams) == 1 {
			teamID = teams.Teams[0].Id
			teamName = teams.Teams[0].Name
		} else {
			available := make([]string, 0, len(teams.Teams))
			for _, team := range teams.Teams {
				available = append(available, fmt.Sprintf("  %s (%s)", team.Name, team.Id))
			}
			return nil, cmd.NewErrUsagef("multiple teams available; pass --team with a team name or ID:\n%s\n\nExample: baseten harness setup --team %s", strings.Join(available, "\n"), teams.Teams[0].Id)
		}
		flags.Team = teamID
	} else {
		team, err := resolveTeam(ctx, cl.API(), flags.Team)
		if err != nil {
			return nil, err
		}
		teamID, teamName = team.Id, team.Name
	}
	user, err := cl.API().GetUsersMe(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting current user: %w", err)
	}
	if user.UserId == "" {
		return nil, errors.New("user response is missing user_id")
	}
	store, err := NewAuthStore(false)
	if err != nil {
		return nil, err
	}
	scope := auth.RoutesKeyScope{ManagementURL: endpoint, Profile: transport.Session.ProfileName(), UserID: user.UserId, TeamID: teamID, Name: flags.KeyName}
	saved, err := store.GetRoutesKey(scope)
	if err != nil {
		return nil, err
	}
	// Existing labels remain valid identities; a nicer default must not mint
	// another key when this installation already has one saved.
	if saved == "" {
		for _, name := range previousNames {
			prior := scope
			prior.Name = name
			key, err := store.GetRoutesKey(prior)
			if err != nil {
				return nil, err
			}
			if key != "" {
				scope, saved, flags.KeyName = prior, key, name
				break
			}
		}
	}
	return &harnessAuth{store: store, scope: scope, saved: saved, teamName: teamName, transport: transport}, nil
}

func (a *harnessAuth) ensure(ctx context.Context) (string, bool, error) {
	if a.saved != "" {
		return a.saved, false, nil
	}
	createCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	created, err := a.store.EnsureRoutesKey(a.scope, func() (string, error) {
		return createRoutesKey(createCtx, a.transport, a.scope.ManagementURL, a.scope.Name, a.scope.TeamID)
	})
	if err != nil {
		return "", false, err
	}
	key, err := a.store.GetRoutesKey(a.scope)
	if err != nil {
		return "", false, err
	}
	a.saved = key
	return key, created, nil
}

// Keep prompts on stderr so structured output remains usable by scripts.
func harnessPrompt(ctx *CommandContext, fields ...huh.Field) error {
	if !ctx.IsInteractive() || ctx.JSON {
		return cmd.NewErrUsagef("interactive input requires a terminal and text output")
	}
	return huh.NewForm(huh.NewGroup(fields...)).WithInput(ctx.Stdin).WithOutput(ctx.Stderr).RunWithContext(ctx)
}
