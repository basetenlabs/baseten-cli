package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func init() {
	Register("harness setup", commandHarnessSetup)
	Register("harness status", commandHarnessStatus)
	Register("harness teardown", commandHarnessTeardown)
}

type selectedHarness struct {
	adapter   harness.Harness
	detection harness.Detection
}

// Setup offers installed harnesses. Status and teardown discover configured
// integrations from their native settings, even after their executable is removed.
func selectHarnesses(ctx *CommandContext, flags cmd.HarnessFlags, setup bool) ([]selectedHarness, error) {
	if flags.Config != "" && len(flags.Harness) != 1 {
		return nil, cmd.NewErrUsagef("--config requires exactly one explicit --harness")
	}
	if setup && len(flags.Harness) == 0 && !ctx.IsInteractive() {
		return nil, cmd.NewErrUsagef("pass --harness when not interactive")
	}
	adapters := harness.All()
	if len(flags.Harness) > 0 {
		adapters = nil
		seen := map[string]bool{}
		for _, name := range flags.Harness {
			if seen[name] {
				continue
			}
			seen[name] = true
			adapter, err := harness.Find(name)
			if err != nil {
				return nil, cmd.NewErrUsage(err)
			}
			adapters = append(adapters, adapter)
		}
	}
	var selected []selectedHarness
	var options []huh.Option[string]
	for _, adapter := range adapters {
		d, err := adapter.Detect(ctx, ctx.Execer(), flags.Config)
		if err != nil {
			return nil, err
		}
		if setup && (!d.Installed || !d.Supported) {
			if len(flags.Harness) == 0 {
				continue
			}
			if !d.Installed {
				return nil, fmt.Errorf("%s is not installed or not on PATH", d.Name)
			}
			return nil, fmt.Errorf("%s setup is currently supported only on macOS", d.Name)
		}
		if !setup && len(flags.Harness) == 0 {
			configured, err := adapter.Configured(d.Path)
			if err != nil {
				// One broken file shouldn't hide the other harnesses. An explicit --harness still fails.
				ctx.Logf("warning: skipping %s: settings at %s are misconfigured: %v\n", d.Name, harnessDisplayPath(d.Path), err)
				continue
			}
			if !configured {
				continue
			}
		}
		selected = append(selected, selectedHarness{adapter, d})
		version := d.Version
		if version == "" {
			version = "version unavailable"
		}
		options = append(options, huh.NewOption(fmt.Sprintf("%s  %s  %s", d.Name, version, harnessDisplayPath(d.Path)), d.Name))
	}
	if setup && len(flags.Harness) == 0 {
		if len(options) == 0 {
			return nil, fmt.Errorf("no supported harnesses are installed")
		}
		var names []string
		if err := harnessPrompt(ctx, huh.NewMultiSelect[string]().Title("Detected harnesses: select which to configure").Options(options...).Value(&names).Validate(func(names []string) error {
			if len(names) == 0 {
				return fmt.Errorf("choose at least one harness")
			}
			return nil
		})); err != nil {
			return nil, err
		}
		chosen := selected[:0]
		for _, candidate := range selected {
			for _, name := range names {
				if candidate.adapter.Name() == name {
					chosen = append(chosen, candidate)
					break
				}
			}
		}
		selected = chosen
	}
	return selected, nil
}

func harnessConfirm(ctx *CommandContext, yes bool, question string) error {
	if yes {
		return nil
	}
	// Prompts use stderr even with JSON output. Interactivity depends on stdin.
	if !ctx.IsInteractive() {
		return cmd.NewErrUsagef("cannot confirm: stdin is not a terminal; pass --yes to skip the prompt")
	}
	var confirmed bool
	if err := harnessPrompt(ctx, huh.NewConfirm().Title(question).Value(&confirmed)); err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("aborted")
	}
	return nil
}

func commandHarnessSetup(ctx *CommandContext, f *cmd.HarnessSetupFlags) error {
	selected, err := selectHarnesses(ctx, f.HarnessFlags, true)
	if err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	transport, remote, err := ctx.AuthTransport()
	if err != nil {
		return err
	}
	credential, err := prepareHarnessAuth(ctx, f, cl.API(), transport, remote.ManagementURL())
	if err != nil {
		return err
	}
	listed, err := listRoutes(ctx, cl.API(), managementapi.GetV1RoutesParams{TeamId: &credential.scope.TeamID})
	if err != nil {
		return err
	}
	if len(listed) == 0 {
		return fmt.Errorf("no accessible routes in the selected team; create a route before running harness setup")
	}
	routes := make([]harness.Route, 0, len(listed))
	endpoint := strings.TrimRight(listed[0].InvokeUrl, "/")
	// One provider configuration needs a common inference endpoint. Names and
	// labels otherwise follow the API contract without revalidating each field.
	for _, route := range listed {
		if strings.TrimRight(route.InvokeUrl, "/") != endpoint {
			return fmt.Errorf("selected team has routes with different invoke URLs")
		}
		routes = append(routes, harness.Route{Name: route.Name, DisplayName: route.DisplayName})
	}
	// Avoid writing credentials into a configuration that sends them over HTTP,
	// except when both APIs are explicitly running on local development servers.
	if !strings.HasPrefix(endpoint, "https://") && !(harness.LocalEndpoint(endpoint) == nil && harness.LocalEndpoint(remote.ManagementURL()) == nil) {
		return fmt.Errorf("routes inference endpoint must use HTTPS")
	}
	token := credential.saved
	if token == "" {
		token = "baseten-harness-pending-credential"
	}
	var plans []*harness.Plan
	selections := make([]harness.Selection, len(selected))
	for i, choice := range selected {
		selection := harness.Selection{Primary: f.Route, Background: f.BackgroundRoute, Subagent: f.SubagentRoute, Fallback: f.FallbackRoute}
		if selection.Primary == "" {
			selection.Primary = routes[0].Name
		}
		selections[i] = selection
		current, err := choice.adapter.Prepare(choice.detection.Path, routes, selection, endpoint, token)
		if err != nil {
			return fmt.Errorf("%s: %w", choice.adapter.Name(), err)
		}
		for _, p := range current {
			p.Harness = choice.adapter.Name()
		}
		plans = append(plans, current...)
	}
	if !ctx.JSON {
		harnessSetupSummary(ctx, credential, routes, selected, selections, plans)
	}
	if f.DryRun {
		if ctx.JSON {
			outputHarnessPlansJSON(ctx, plans)
		} else {
			ctx.OutputLine("Preview only. No harness files changed or routes API key created.")
		}
		return nil
	}
	var paths []string
	for _, choice := range selected {
		paths = append(paths, choice.detection.Path)
	}
	if err := harnessConfirm(ctx, f.Yes, "Apply harness changes to "+strings.Join(paths, ", ")+"?"); err != nil {
		return err
	}
	token, _, err = credential.ensure(ctx)
	if err != nil {
		return err
	}
	if err := harness.ApplyPlans(plans, token); err != nil {
		return err
	}
	if ctx.JSON {
		outputHarnessPlansJSON(ctx, plans)
	} else {
		ctx.OutputLine("Configuration saved. Restart the configured harnesses to load the changes.")
		for _, choice := range selected {
			ctx.Outputf("Restore settings: %s\n", harnessFollowupCommand("teardown", choice.detection, f.Config != ""))
		}
	}
	return nil
}

func commandHarnessStatus(ctx *CommandContext, f *cmd.HarnessStatusFlags) error {
	selected, err := selectHarnesses(ctx, f.HarnessFlags, false)
	if err != nil {
		return err
	}
	results := make([]cmd.HarnessStatusResult, 0, len(selected))
	for _, choice := range selected {
		d := choice.detection
		r, err := choice.adapter.Inspect(d)
		if err != nil {
			return err
		}
		if ctx.JSON {
			details := make([]cmd.HarnessRouteSummary, 0, len(r.RouteDetails))
			for _, route := range r.RouteDetails {
				details = append(details, cmd.HarnessRouteSummary{
					Name:        route.Name,
					DisplayName: route.DisplayName,
				})
			}
			results = append(results, cmd.HarnessStatusResult{
				DefaultRoute:    r.DefaultRoute,
				SmallTaskModel:  r.SmallTaskModel,
				RouteDetails:    details,
				Harness:         r.Name,
				Config:          r.Path,
				Installed:       r.Installed,
				Version:         r.Version,
				Supported:       r.Supported,
				State:           r.State,
				ManagedSettings: r.Managed,
				Routes:          r.Routes,
				Note:            r.Note,
			})
		} else {
			ctx.Outputf("%s: %s\n", r.Name, r.State)
			ctx.Outputf("  Config         %s\n  Version        %s\n", harnessDisplayPath(r.Path), r.Version)
			if !r.Installed {
				ctx.OutputLine("  Installation   Not found on PATH")
			} else if !r.Supported {
				ctx.OutputLine("  Compatibility  Platform not supported for setup")
			}
			if r.DefaultRoute != "" {
				ctx.Outputf("  Default route  %s\n", r.DefaultRoute)
			}
			if r.SmallTaskModel != "" {
				ctx.Outputf("  %s  %s\n", harnessSmallTaskLabel(r.Name), r.SmallTaskModel)
			}
			ctx.OutputLine("")
			harnessRouteTable(ctx, "Configured routes", r.RouteDetails)
			ctx.Outputf("\nLocal configuration only; key validity and live routes were not checked.\nRefresh: %s (add --team <team> if needed), then restart the harness.\n", harnessFollowupCommand("setup", d, f.Config != ""))
			if len(r.Managed) > 0 {
				ctx.VerboseLogf("Managed settings: %s\n", strings.Join(r.Managed, ", "))
			}
		}

	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.HarnessStatusesResult{Harnesses: results})
	} else if len(selected) == 0 {
		ctx.OutputLine("No configured Baseten harnesses found.")
	}
	return nil
}

func commandHarnessTeardown(ctx *CommandContext, f *cmd.HarnessTeardownFlags) error {
	selected, err := selectHarnesses(ctx, f.HarnessFlags, false)
	if err != nil {
		return err
	}
	var plans []*harness.Plan
	var names []string
	for _, choice := range selected {
		current, err := choice.adapter.Teardown(choice.detection.Path)
		if err != nil {
			return err
		}
		managed := false
		for _, p := range current {
			p.Harness = choice.adapter.Name()
			managed = managed || p.Managed
			if !ctx.JSON {
				ctx.VerboseLogf("Config: %s\nSettings: %s\nChanged: %t\n", p.Path, strings.Join(p.Keys, ", "), p.Changed)
			}
		}
		plans = append(plans, current...)
		if managed {
			names = append(names, choice.adapter.Name())
		}
	}
	if !ctx.JSON {
		if len(names) == 0 {
			ctx.OutputLine("No Baseten settings to remove.")
		} else {
			ctx.Outputf("Would remove %s integration settings and use native defaults. Previous values will not be restored; unrelated settings will be preserved.\n", strings.Join(names, ", "))
		}
	}
	if !f.DryRun && len(names) > 0 {
		if err := harnessConfirm(ctx, f.Yes, "Remove Baseten settings and use native defaults for "+strings.Join(names, ", ")+"?"); err != nil {
			return err
		}
		if err := harness.ApplyPlans(plans, ""); err != nil {
			return err
		}
		if !ctx.JSON {
			ctx.OutputLine("Baseten integration settings removed. Native defaults now apply.")
		}
	}
	if ctx.JSON {
		outputHarnessPlansJSON(ctx, plans)
	}
	return nil
}

// harnessFollowupCommand keeps commands pointed at an explicit configuration.
// Quote paths for the supported macOS shells, including literal apostrophes.
func harnessFollowupCommand(action string, d harness.Detection, explicitConfig bool) string {
	command := "baseten harness " + action + " --harness " + d.Name
	if explicitConfig {
		command += " --config '" + strings.ReplaceAll(d.Path, "'", "'\\''") + "'"
	}
	return command
}

func outputHarnessPlansJSON(ctx *CommandContext, plans []*harness.Plan) {
	results := make([]cmd.HarnessPlanResult, 0, len(plans))
	for _, p := range plans {
		results = append(results, cmd.HarnessPlanResult{
			Harness: p.Harness, ReplacedSettings: p.Replaced, Managed: p.Managed, Config: p.Path, Settings: p.Keys, Changed: p.Changed,
		})
	}
	ctx.OutputJSON(cmd.HarnessPlansResult{Changes: results})
}

func harnessDisplayPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		relative, err := filepath.Rel(home, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			return filepath.Join("~", relative)
		}
	}
	return path
}

func harnessSmallTaskLabel(name string) string {
	if name == harness.ClaudeCode {
		return "Haiku model  "
	}
	return "Small tasks  "
}

func harnessRouteTable(ctx *CommandContext, title string, routes []harness.Route) {
	ctx.Outputf("%s: %d\n", title, len(routes))
	if len(routes) == 0 {
		return
	}
	rows := make([][]string, 0, len(routes))
	for _, route := range routes {
		rows = append(rows, []string{route.Name, route.DisplayName})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"NAME", "DISPLAY NAME"},
		Rows:    rows,
	})
}

func harnessSetupSummary(ctx *CommandContext, credential *harnessAuth, routes []harness.Route, selected []selectedHarness, selections []harness.Selection, plans []*harness.Plan) {
	renderer := lipgloss.NewRenderer(ctx.Stdout)
	accent := renderer.NewStyle().Inherit(inlineCodeStyle)
	heading := renderer.NewStyle().Bold(true)
	team := credential.teamName
	if team == "" {
		team = credential.scope.TeamID
	}
	ctx.Outputf("Team: %s\n\n", accent.Render(team))
	harnessRouteTable(ctx, "Available routes", routes)
	keyAction := "Reuse saved key"
	if credential.saved == "" {
		keyAction = "Create on confirmation"
	}
	for i, choice := range selected {
		d := choice.detection
		ctx.Outputf("\n%s\n  Config         %s\n  Default route  %s\n", heading.Render(d.Name), harnessDisplayPath(d.Path), accent.Render(selections[i].Primary))
		if small := selected[i].adapter.SmallTaskModel(selections[i]); small != "" {
			ctx.Outputf("  %s  %s\n", harnessSmallTaskLabel(d.Name), accent.Render(small))
		}
		if selections[i].Subagent != "" {
			ctx.Outputf("  Subagents      %s\n", accent.Render(selections[i].Subagent))
		}
		if d.Name == harness.ClaudeCode {
			fallback := selections[i].Fallback
			if fallback == "" {
				fallback = selections[i].Primary
			}
			ctx.Outputf("  Fallback route %s\n", accent.Render(fallback))
		}
		ctx.Outputf("  Routes API key %s\n", keyAction)
		changed, replaced := false, false
		for _, p := range plans {
			if p.Harness != d.Name {
				continue
			}
			changed = changed || p.Changed
			replaced = replaced || len(p.Replaced) > 0
			ctx.VerboseLogf("Config: %s\nSettings: %s\n", p.Path, strings.Join(p.Keys, ", "))
			if len(p.Replaced) > 0 {
				ctx.VerboseLogf("Settings to replace: %s\n", strings.Join(p.Replaced, ", "))
			}
		}
		result := "Already configured"
		if changed {
			result = "Changes ready to apply"
		}
		ctx.Outputf("  Result         %s\n", accent.Render(result))
		if replaced {
			ctx.OutputLine("  Existing integration settings will be replaced. Teardown uses native defaults; previous values are not saved or restored.")
		}
	}
	ctx.VerboseLogf("Routes API key name: %s\n", credential.scope.Name)
	ctx.OutputLine("")
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
	teamName  string
	transport *auth.Transport
	api       *managementapi.Client
}

func prepareHarnessAuth(ctx *CommandContext, flags *cmd.HarnessSetupFlags, api *managementapi.Client, transport *auth.Transport, endpoint string) (*harnessAuth, error) {
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
	var teamID, teamName string
	if flags.Team == "" {
		teams, err := api.GetTeams(ctx, managementapi.GetV1TeamsParams{})
		if err != nil {
			return nil, fmt.Errorf("list teams: %w", err)
		}
		if len(teams.Teams) == 0 {
			return nil, fmt.Errorf("no teams available for the current account; ask an organization admin to add you to a team")
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
		team, err := resolveTeam(ctx, api, flags.Team)
		if err != nil {
			return nil, err
		}
		teamID, teamName = team.Id, team.Name
	}
	user, err := api.GetUsersMe(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting current user: %w", err)
	}
	store, err := NewAuthStore(false)
	if err != nil {
		return nil, err
	}
	scope := auth.RoutesKeyScope{
		ManagementURL: endpoint,
		Profile:       transport.Session.ProfileName(),
		UserID:        user.UserId,
		TeamID:        teamID,
		Name:          flags.KeyName,
	}
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
	return &harnessAuth{
		store:     store,
		scope:     scope,
		saved:     saved,
		teamName:  teamName,
		transport: transport,
		api:       api,
	}, nil
}

func (a *harnessAuth) ensure(ctx context.Context) (string, bool, error) {
	if a.saved != "" {
		return a.saved, false, nil
	}
	createCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	token, err := a.transport.Credential(createCtx)
	if err != nil {
		return "", false, cmd.NewErrAuth(err)
	}
	created, err := a.store.EnsureRoutesKey(a.scope, func() (string, bool, error) {
		return createRoutesKey(createCtx, a.api, token, a.scope.Name, a.scope.TeamID)
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
	if !ctx.IsInteractive() {
		return cmd.NewErrUsagef("interactive input requires a terminal")
	}
	return huh.NewForm(huh.NewGroup(fields...)).WithInput(ctx.Stdin).WithOutput(ctx.Stderr).RunWithContext(ctx)
}

// Creation is not known to be idempotent. Only definitive API rejections allow
// the credential store to clear its pending record and permit another attempt.
func createRoutesKey(ctx context.Context, api *managementapi.Client, token, name, teamID string) (string, bool, error) {
	result, err := api.PostApiKeys(ctx, managementapi.CreateAPIKeyRequest{
		Type:   managementapi.APIKeyCategory_ROUTES,
		Name:   &name,
		TeamId: &teamID,
	})
	if err == nil {
		return result.ApiKey, false, nil
	}
	var response *managementapi.ResponseError
	if !errors.As(err, &response) {
		return "", false, cmd.NewErrServer(errors.New("routes key creation failed; check your login and connection"))
	}
	// Only retain the API's message, never arbitrary response details that may
	// include credentials. Keep the SDK error type for standard CLI classification.
	var detail struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal([]byte(response.Body), &detail)
	detail.Message = strings.ReplaceAll(detail.Message, token, "[REDACTED]")
	if len(detail.Message) > 2000 {
		detail.Message = detail.Message[:2000] + "..."
	}
	body, _ := json.Marshal(detail)
	safe := &managementapi.ResponseError{StatusCode: response.StatusCode, Body: string(body)}
	switch response.StatusCode {
	case 400, 401, 403, 404, 422:
		return "", true, cmd.WrapHTTPStatus(response.StatusCode, safe)
	default:
		return "", false, cmd.WrapHTTPStatus(response.StatusCode, safe)
	}
}
