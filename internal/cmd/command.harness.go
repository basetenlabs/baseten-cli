package cmd

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/charmbracelet/lipgloss"
)

func init() {
	Register("harness setup", commandHarnessSetup)
	Register("harness status", commandHarnessStatus)
	Register("harness teardown", commandHarnessTeardown)
}

type selectedHarness struct {
	harness.Harness
	detection harness.Detection
}

// selectHarnesses returns the harnesses a command applies to. Setup offers
// installed harnesses; status and teardown find configured ones, even when
// the harness is no longer installed.
func selectHarnesses(ctx *CommandContext, flags cmd.HarnessFlags, setup bool) ([]selectedHarness, error) {
	// Harness settings locations are only verified on macOS so far.
	if runtime.GOOS != "darwin" {
		return nil, errors.New("harness commands support only macOS for now")
	}
	explicit := len(flags.Harness) > 0
	if flags.ConfigDir != "" && len(flags.Harness) != 1 {
		return nil, cmd.NewErrUsagef("--config-dir requires exactly one --harness")
	}
	if setup && !explicit && !ctx.IsInteractive() {
		return nil, cmd.NewErrUsagef("pass --harness when stdin is not a terminal")
	}
	var selected []selectedHarness
	for _, h := range harness.All() {
		if explicit && !slices.Contains(flags.Harness, h.Name()) {
			continue
		}
		d, err := h.Detect(ctx, ctx.Execer(), flags.ConfigDir)
		if err != nil {
			return nil, err
		}
		switch {
		case setup && !d.Installed && explicit:
			searched := "not on PATH"
			if h.Name() == harness.Codex && runtime.GOOS == "darwin" {
				searched = "not on PATH or in ChatGPT.app or Codex.app"
			}
			return nil, fmt.Errorf("%s is not installed or %s", h.Name(), searched)
		case setup && !d.Installed:
			continue
		case !setup && !explicit:
			status, err := h.Inspect(d)
			if err != nil {
				// One broken file shouldn't hide the other harnesses. An explicit --harness still fails.
				ctx.Logf("warning: skipping %s: settings at %s are misconfigured: %v\n", h.Name(), harnessDisplayPath(d.Path), err)
				continue
			}
			if status.State == harness.StateNotConfigured {
				continue
			}
		}
		selected = append(selected, selectedHarness{h, d})
	}
	if !setup || explicit {
		return selected, nil
	}
	if len(selected) == 0 {
		return nil, errors.New("no supported harnesses are installed")
	}
	options := make([]huh.Option[string], 0, len(selected))
	for _, s := range selected {
		label := fmt.Sprintf("%s  %s  %s", s.Name(), cmp.Or(s.detection.Version, "version unavailable"), harnessDisplayPath(s.detection.Path))
		options = append(options, huh.NewOption(label, s.Name()))
	}
	var names []string
	err := huh.NewMultiSelect[string]().
		Title("Select harnesses to configure").
		Options(options...).
		Value(&names).
		Validate(func(names []string) error {
			if len(names) == 0 {
				return errors.New("choose at least one harness")
			}
			return nil
		}).
		Run()
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(selected, func(s selectedHarness) bool { return !slices.Contains(names, s.Name()) }), nil
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
	api := cl.API()
	team, err := resolveTeam(ctx, api, f.Team)
	if err != nil {
		return err
	}
	listed, err := listRoutes(ctx, api, managementapi.GetV1RoutesParams{TeamId: &team.Id})
	if err != nil {
		return err
	}
	if len(listed) == 0 {
		return fmt.Errorf("team %s has no routes; create one with 'baseten route create'", team.Name)
	}
	// One provider configuration serves every route, so they must share an endpoint.
	endpoint := strings.TrimRight(listed[0].InvokeUrl, "/")
	routes := make([]harness.Route, 0, len(listed))
	for _, r := range listed {
		if strings.TrimRight(r.InvokeUrl, "/") != endpoint {
			return fmt.Errorf("team %s has routes with different invoke URLs", team.Name)
		}
		routes = append(routes, harness.Route{Name: r.Name, DisplayName: r.DisplayName})
	}
	selection := harness.Selection{
		Primary:    cmp.Or(f.Route, routes[0].Name),
		Background: f.BackgroundRoute,
		Subagent:   f.SubagentRoute,
		Fallback:   f.FallbackRoute,
	}
	var plans []*harness.Plan
	for _, choice := range selected {
		current, err := choice.Prepare(choice.detection.Path, routes, selection, endpoint)
		if err != nil {
			return fmt.Errorf("%s: %w", choice.Name(), err)
		}
		for _, p := range current {
			p.Harness = choice.Name()
		}
		plans = append(plans, current...)
	}
	if !ctx.JSON {
		harnessSetupSummary(ctx, team, routes, selected, selection, plans)
	}
	if f.DryRun {
		if ctx.JSON {
			outputHarnessPlansJSON(ctx, plans, nil)
		} else {
			ctx.LogLine("Preview only. No files changed and no routes API key created.")
		}
		return nil
	}
	if !f.Yes {
		if err := ctx.ConfirmYesNo("Apply these harness changes?"); err != nil {
			return err
		}
	}
	// The routes API key is only read or created once the user has confirmed.
	key, err := loadHarnessKey(ctx, api, team.Id, f.KeyName)
	if err != nil {
		return err
	}
	token, err := key.ensure(ctx, api)
	if err != nil {
		return err
	}
	if err := harness.ApplyPlans(plans, token); err != nil {
		return err
	}
	if ctx.JSON {
		outputHarnessPlansJSON(ctx, plans, nil)
		return nil
	}
	ctx.LogLine("Configuration saved. Restart the configured harnesses to load the changes.")
	for _, choice := range selected {
		ctx.Logf("Undo with: %s\n", harnessFollowupCommand("teardown", choice, f.ConfigDir))
	}
	return nil
}

func commandHarnessStatus(ctx *CommandContext, f *cmd.HarnessStatusFlags) error {
	selected, err := selectHarnesses(ctx, f.HarnessFlags, false)
	if err != nil {
		return err
	}
	results := make([]cmd.HarnessStatus, 0, len(selected))
	for _, choice := range selected {
		r, err := choice.Inspect(choice.detection)
		if err != nil {
			return fmt.Errorf("%s: %w", choice.Name(), err)
		}
		routes := make([]cmd.HarnessRoute, 0, len(r.Routes))
		for _, route := range r.Routes {
			routes = append(routes, cmd.HarnessRoute{Name: route.Name, DisplayName: route.DisplayName})
		}
		results = append(results, cmd.HarnessStatus{
			Harness:         r.Name,
			Config:          r.Path,
			Installed:       r.Installed,
			Version:         r.Version,
			State:           r.State,
			DefaultRoute:    r.DefaultRoute,
			BackgroundRoute: r.BackgroundRoute,
			Routes:          routes,
			ManagedSettings: r.Managed,
		})
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.HarnessStatusList{Items: results})
		return nil
	}
	if len(results) == 0 {
		ctx.LogLine("No configured Baseten harnesses found.")
		return nil
	}
	for _, r := range results {
		ctx.Outputf("%s: %s\n", r.Harness, r.State)
		ctx.Outputf("  Config            %s\n", harnessDisplayPath(r.Config))
		if !r.Installed {
			ctx.OutputLine("  Version           not found")
		} else {
			ctx.Outputf("  Version           %s\n", cmp.Or(r.Version, "unavailable"))
		}
		if r.DefaultRoute != "" {
			ctx.Outputf("  Default route     %s\n", r.DefaultRoute)
		}
		if r.BackgroundRoute != "" {
			ctx.Outputf("  Background route  %s\n", r.BackgroundRoute)
		}
		ctx.OutputLine("")
		rows := make([]harness.Route, 0, len(r.Routes))
		for _, route := range r.Routes {
			rows = append(rows, harness.Route{Name: route.Name, DisplayName: route.DisplayName})
		}
		harnessRouteTable(ctx, "Configured routes", rows)
		ctx.OutputLine("")
		ctx.VerboseLogf("%s settings: %s\n", r.Harness, strings.Join(r.ManagedSettings, ", "))
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
		current, err := choice.Teardown(choice.detection.Path)
		if err != nil {
			return fmt.Errorf("%s: %w", choice.Name(), err)
		}
		for _, p := range current {
			p.Harness = choice.Name()
			if len(p.Keys) > 0 {
				ctx.VerboseLogf("Config: %s\nSettings: %s\n", p.Path, strings.Join(p.Keys, ", "))
			}
		}
		if slices.ContainsFunc(current, func(p *harness.Plan) bool { return p.Managed }) {
			names = append(names, choice.Name())
		}
		plans = append(plans, current...)
	}
	if len(names) == 0 {
		if ctx.JSON {
			outputHarnessPlansJSON(ctx, plans, nil)
		} else {
			ctx.LogLine("No Baseten settings to remove.")
		}
		return nil
	}
	unused, err := unusedHarnessKeys(ctx, selected)
	if err != nil {
		return err
	}
	list := strings.Join(names, ", ")
	question := "Remove Baseten settings from " + list + "?"
	if !ctx.JSON {
		ctx.Outputf("Remove Baseten settings from %s. Native defaults will apply and unrelated settings are kept.\n", list)
	}
	if len(unused) > 0 {
		question = "Remove Baseten settings from " + list + " and delete the routes API key they use?"
		if !ctx.JSON {
			ctx.OutputLine("Also delete the routes API key they use, since no other harness on this machine uses it.")
		}
	}
	if f.DryRun {
		if ctx.JSON {
			outputHarnessPlansJSON(ctx, plans, nil)
		} else {
			ctx.LogLine("Preview only. No files changed and no key deleted.")
		}
		return nil
	}
	if !f.Yes {
		if err := ctx.ConfirmYesNo(question); err != nil {
			return err
		}
	}
	// Delete the key first: once the settings are gone, teardown can no longer
	// find it to retry.
	var deleted []string
	if len(unused) > 0 {
		if deleted, err = deleteHarnessKeys(ctx, unused); err != nil {
			return err
		}
	}
	if err := harness.ApplyPlans(plans, ""); err != nil {
		return err
	}
	if ctx.JSON {
		outputHarnessPlansJSON(ctx, plans, deleted)
	} else {
		ctx.LogLine("Baseten settings removed. Restart the harnesses to load the changes.")
	}
	return nil
}

// harnessFollowupCommand repeats --config-dir so follow-ups target the same files.
func harnessFollowupCommand(action string, h selectedHarness, configDir string) string {
	command := "baseten harness " + action + " --harness " + h.Name()
	if configDir != "" {
		command += " --config-dir '" + strings.ReplaceAll(configDir, "'", `'\''`) + "'"
	}
	return command
}

func outputHarnessPlansJSON(ctx *CommandContext, plans []*harness.Plan, deletedKeys []string) {
	items := make([]cmd.HarnessPlan, 0, len(plans))
	for _, p := range plans {
		items = append(items, cmd.HarnessPlan{
			Harness:          p.Harness,
			Config:           p.Path,
			Settings:         append([]string{}, p.Keys...),
			ReplacedSettings: p.Replaced,
			Managed:          p.Managed,
			Changed:          p.Changed,
		})
	}
	ctx.OutputJSON(cmd.HarnessPlanList{Items: items, DeletedAPIKeys: deletedKeys})
}

func harnessDisplayPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(home, path); err == nil && filepath.IsLocal(rel) {
		return filepath.Join("~", rel)
	}
	return path
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
	ctx.OutputTable(TableOutput{Headers: []string{"NAME", "DISPLAY NAME"}, Rows: rows})
}

func harnessSetupSummary(ctx *CommandContext, team *managementapi.Team, routes []harness.Route, selected []selectedHarness, s harness.Selection, plans []*harness.Plan) {
	renderer := lipgloss.NewRenderer(ctx.Stdout)
	accent := renderer.NewStyle().Inherit(inlineCodeStyle)
	heading := renderer.NewStyle().Bold(true)
	ctx.Outputf("Team: %s\n\n", accent.Render(team.Name))
	harnessRouteTable(ctx, "Available routes", routes)
	for _, choice := range selected {
		ctx.Outputf("\n%s\n", heading.Render(choice.Name()))
		ctx.Outputf("  Config            %s\n", harnessDisplayPath(choice.detection.Path))
		ctx.Outputf("  Default route     %s\n", accent.Render(s.Primary))
		if background := choice.BackgroundRoute(s); background != "" {
			ctx.Outputf("  Background route  %s\n", accent.Render(background))
		}
		if s.Subagent != "" {
			ctx.Outputf("  Subagent route    %s\n", accent.Render(s.Subagent))
		}
		if choice.Name() == harness.ClaudeCode {
			ctx.Outputf("  Fallback route    %s\n", accent.Render(cmp.Or(s.Fallback, s.Primary)))
		}
		changed, replaced := false, false
		for _, p := range plans {
			if p.Harness != choice.Name() {
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
		ctx.Outputf("  Result            %s\n", accent.Render(result))
		if replaced {
			ctx.OutputLine("  Existing integration settings will be overwritten; teardown does not restore them.")
		}
	}
	ctx.OutputLine("")
}

// unusedHarnessKeys returns the routes API keys the selected harnesses use that
// no other harness on this machine still uses.
func unusedHarnessKeys(ctx *CommandContext, selected []selectedHarness) ([]string, error) {
	keys := map[string]bool{}
	for _, s := range selected {
		token, err := s.Credential(s.detection.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Name(), err)
		}
		if token != "" {
			keys[token] = true
		}
	}
	for _, h := range harness.All() {
		if len(keys) == 0 {
			break
		}
		d, err := h.Detect(ctx, ctx.Execer(), "")
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(selected, func(s selectedHarness) bool { return s.Name() == h.Name() && s.detection.Path == d.Path }) {
			continue
		}
		token, err := h.Credential(d.Path)
		if err != nil {
			ctx.Logf("warning: keeping the routes API key, since %s settings at %s can't be read: %v\n", h.Name(), harnessDisplayPath(d.Path), err)
			return nil, nil
		}
		delete(keys, token)
	}
	return slices.Sorted(maps.Keys(keys)), nil
}

// deleteHarnessKeys deletes the routes API keys with these values, and this
// machine's saved copies of them.
func deleteHarnessKeys(ctx *CommandContext, tokens []string) ([]string, error) {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return nil, err
	}
	keys, err := listRouteAPIKeys(ctx, cl.API())
	if err != nil {
		return nil, err
	}
	var deleted []managementapi.APIKeyInfo
	prefixes := []string{}
	for _, k := range keys.Keys {
		if !slices.ContainsFunc(tokens, func(token string) bool { return strings.HasPrefix(token, k.Prefix) }) {
			continue
		}
		if _, err := cl.API().DeleteApiKeys(ctx, k.Prefix); err != nil {
			return nil, fmt.Errorf("deleting routes API key %s: %w", k.Prefix, err)
		}
		deleted = append(deleted, k)
		prefixes = append(prefixes, k.Prefix)
		ctx.Logf("Deleted routes API key %s\n", k.Prefix)
	}
	if len(deleted) > 0 {
		if err := forgetRouteAPIKeys(ctx, cl.API(), deleted); err != nil {
			return nil, err
		}
	}
	return prefixes, nil
}

// defaultHarnessKeyName derives a key name from the hostname, which may contain
// characters API key names don't allow.
func defaultHarnessKeyName(hostname string) string {
	parts := strings.FieldsFunc(strings.TrimSuffix(strings.ToLower(hostname), ".local"), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	return "baseten-harness-" + cmp.Or(strings.Join(parts, "-"), "local-machine")
}

// harnessKey is the routes API key setup installs. It is created on first
// setup and saved so later setups reuse it.
type harnessKey struct {
	store *auth.Store
	scope auth.RoutesKeyScope
	saved string
}

func loadHarnessKey(ctx *CommandContext, api *managementapi.Client, teamID, name string) (*harnessKey, error) {
	if name == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, err
		}
		name = defaultHarnessKeyName(hostname)
	}
	user, err := api.GetUsersMe(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting current user: %w", err)
	}
	scope, err := routesKeyScope(ctx, user.UserId, teamID, name)
	if err != nil {
		return nil, err
	}
	store, err := NewAuthStore(false)
	if err != nil {
		return nil, err
	}
	k := &harnessKey{store: store, scope: scope}
	if k.saved, err = store.GetRoutesKey(k.scope); err != nil || k.saved == "" {
		return k, err
	}
	// The saved key may have been deleted, here or on another machine; setup
	// then creates a new one.
	keys, err := listRouteAPIKeys(ctx, api)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(keys.Keys, func(key managementapi.APIKeyInfo) bool { return strings.HasPrefix(k.saved, key.Prefix) }) {
		k.saved = ""
	}
	return k, nil
}

// ensure returns the saved key, creating and saving one if there is none.
func (k *harnessKey) ensure(ctx *CommandContext, api *managementapi.Client) (string, error) {
	if k.saved != "" {
		return k.saved, nil
	}
	created, err := api.PostTeamsApiKeys(ctx, k.scope.TeamID, managementapi.CreateAPIKeyRequest{
		Type: managementapi.APIKeyCategory_ROUTES,
		Name: &k.scope.Name,
	})
	if err != nil {
		return "", fmt.Errorf("creating routes API key: %w", err)
	}
	if err := k.store.SetRoutesKey(k.scope, created.ApiKey, ctx.Log); err != nil {
		return "", err
	}
	k.saved = created.ApiKey
	ctx.Logf("Created routes API key %s\n", k.scope.Name)
	return k.saved, nil
}
