package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

func init() { Register("harness auth setup", commandHarnessAuthSetup) }

// The pinned management SDK's CreateAPIKeyRequest does not yet include team_id.
// Keep this compatibility request narrow until the SDK exposes that field.
// Only the API error message is surfaced, never raw bodies or details. Never retry
// this POST automatically: creation is not known to be idempotent.
func createRoutesKey(ctx context.Context, transport *auth.Transport, endpoint, name, teamID string) (string, error) {
	body := struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		TeamID string `json:"team_id"`
	}{Type: "ROUTES", Name: name, TeamID: teamID}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/api_keys", bytes.NewReader(encoded))
	if err != nil {
		return "", errors.New("invalid Routes key API request")
	}
	// Resolve once so error redaction uses the exact credential sent, even if
	// an OAuth token expires while the request is in flight.
	token, err := transport.Credential(ctx)
	if err != nil {
		return "", cmd.NewErrAuth(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := transport.Do(req)
	if err != nil {
		return "", cmd.NewErrServer(errors.New("Routes key API request failed; check your login and connection"))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Match the REST ApiError contract. Do not dump arbitrary JSON fields,
		// HTML errors, or the response to a successful credential request.
		var apiError struct {
			Message string `json:"message"`
		}
		detail := ""
		if json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&apiError) == nil && apiError.Message != "" {
			message := apiError.Message
			// A faulty server can echo the authentication header in its error.
			if token != "" {
				message = strings.ReplaceAll(message, token, "[REDACTED]")
			}
			if len(message) > 2000 {
				message = message[:2000] + "..."
			}
			detail = fmt.Sprintf(": %q", message)
		}
		switch resp.StatusCode {
		case 400, 401, 403, 404, 422:
			return "", cmd.WrapHTTPStatus(resp.StatusCode, fmt.Errorf("%w: POST /v1/api_keys returned HTTP %d%s", auth.ErrRoutesKeyRejected, resp.StatusCode, detail))
		}
		return "", cmd.WrapHTTPStatus(resp.StatusCode, fmt.Errorf("Routes key API: POST /v1/api_keys returned HTTP %d%s", resp.StatusCode, detail))
	}
	var result struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", cmd.NewErrServer(errors.New("invalid Routes key API response"))
	}
	return result.APIKey, nil
}

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
	transport *auth.Transport
}

func prepareHarnessAuth(ctx *CommandContext, flags *cmd.HarnessAuthSetupFlags) (*harnessAuth, error) {
	if flags.Team != "" && strings.TrimSpace(flags.Team) == "" {
		return nil, cmd.NewErrUsagef("team cannot be blank")
	}
	var previousNames []string
	if flags.Name == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, err
		}
		flags.Name = defaultHarnessKeyName(hostname)
		previous := "baseten-harness"
		if normalized := normalizeHarnessHostname(hostname); normalized != "" {
			previous += "-" + normalized
		}
		previousNames = []string{strings.TrimPrefix(flags.Name, "baseten-harness-"), previous, "baseten-harness-" + hostname}
	}
	if flags.Name == "" || strings.IndexFunc(flags.Name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-')
	}) >= 0 {
		return nil, cmd.NewErrUsagef("Routes key name can only contain lowercase letters, numbers, and hyphens")
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
		return nil, cmd.NewErrUsagef("Routes key creation requires HTTPS or an explicit loopback development endpoint")
	}
	if _, err := transport.Credential(ctx); err != nil {
		return nil, cmd.NewErrAuth(err)
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return nil, err
	}
	var teamID string
	if flags.Team == "" {
		teams, err := cl.API().GetTeams(ctx, managementapi.GetV1TeamsParams{})
		if err != nil {
			return nil, fmt.Errorf("list teams: %w", err)
		}
		if len(teams.Teams) == 0 {
			return nil, fmt.Errorf("no teams available for the current account")
		}
		if len(teams.Teams) == 1 {
			teamID = teams.Teams[0].Id
		} else {
			if !ctx.IsInteractive() || ctx.JSON {
				return nil, cmd.NewErrUsagef("multiple teams available; pass --team when not interactive")
			}
			options := make([]huh.Option[string], 0, len(teams.Teams))
			for _, team := range teams.Teams {
				options = append(options, huh.NewOption(team.Name+" ("+team.Id+")", team.Id))
			}
			if err := harnessPrompt(ctx, huh.NewSelect[string]().Title("Which team should the Routes key belong to?").Options(options...).Value(&teamID)); err != nil {
				return nil, err
			}
		}
		flags.Team = teamID
	} else {
		teamID, err = ResolveTeam(ctx, cl.API(), flags.Team)
		if err != nil {
			return nil, err
		}
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
	scope := auth.RoutesKeyScope{ManagementURL: endpoint, Profile: transport.Session.ProfileName(), UserID: user.UserId, TeamID: teamID, Name: flags.Name}
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
				scope, saved, flags.Name = prior, key, name
				break
			}
		}
	}
	return &harnessAuth{store: store, scope: scope, saved: saved, transport: transport}, nil
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

func setupHarnessAuth(ctx *CommandContext, flags *cmd.HarnessAuthSetupFlags) (cmd.HarnessAuthSetupResult, error) {
	credential, err := prepareHarnessAuth(ctx, flags)
	if err != nil {
		return cmd.HarnessAuthSetupResult{}, err
	}

	result := cmd.HarnessAuthSetupResult{Name: flags.Name, TeamID: credential.scope.TeamID, Storage: "system-keyring", Reused: credential.saved != "", DryRun: flags.DryRun}
	if !flags.DryRun && credential.saved == "" {
		_, result.Created, err = credential.ensure(ctx)
		if err != nil {
			return cmd.HarnessAuthSetupResult{}, err
		}
		result.Reused = !result.Created
	}
	return result, nil
}

func commandHarnessAuthSetup(ctx *CommandContext, flags *cmd.HarnessAuthSetupFlags) error {
	result, err := setupHarnessAuth(ctx, flags)
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(result)
		return nil
	}
	switch {
	case result.DryRun:
		ctx.Outputf("Routes key %s for team %s: saved=%t. Dry run; no key created.\n", result.Name, result.TeamID, result.Reused)
	case result.Created:
		ctx.OutputLine("Routes key created and saved in the system keyring.")
	default:
		ctx.OutputLine("Using the saved Routes key in the system keyring.")
	}
	ctx.OutputLine("Harness configuration is unchanged. Run baseten harness setup to configure a harness.")
	return nil
}

// Keep prompts on stderr so structured output remains usable by scripts.
func harnessPrompt(ctx *CommandContext, fields ...huh.Field) error {
	if !ctx.IsInteractive() || ctx.JSON {
		return cmd.NewErrUsagef("interactive input requires a terminal and text output")
	}
	return huh.NewForm(huh.NewGroup(fields...)).WithInput(ctx.Stdin).WithOutput(ctx.Stderr).RunWithContext(ctx)
}
