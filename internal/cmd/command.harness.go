package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func init() {
	Register("harness setup", commandHarnessSetup)
	Register("harness status", commandHarnessStatus)
	Register("harness sync", commandHarnessSync)
	Register("harness teardown", commandHarnessTeardown)
}

// harnessPickerLabelWidth caps picker labels so they don't wrap.
const harnessPickerLabelWidth = 80

// harnessVersionNumber finds the version number in a harness's --version
// output, such as "2.1.272" in "2.1.272 (Claude Code)" or "0.134.0" in
// "codex-cli 0.134.0".
var harnessVersionNumber = regexp.MustCompile(`\d+(\.\d+)+\S*`)

type selectedHarness struct {
	harness.Harness
	detection harness.Detection
	selection harness.Selection
}

// selectHarnesses returns the harnesses a command applies to. Setup offers
// installed harnesses; status and teardown find configured ones, even when
// the harness is no longer installed.
func selectHarnesses(ctx *CommandContext, flags cmd.HarnessFlags, setup bool) ([]selectedHarness, error) {
	if !harness.Supported() {
		return nil, errors.New("harness commands support only macOS and Linux for now")
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
			if locations := harness.CodexDesktopLocations(); h.Name() == harness.Codex && locations != "" {
				searched += " or in " + locations
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
		selected = append(selected, selectedHarness{Harness: h, detection: d})
	}
	if !setup || explicit {
		return selected, nil
	}
	if len(selected) == 0 {
		return nil, errors.New("no supported harnesses are installed")
	}
	// The picker sizes itself for one line per option, so a label that wraps
	// pushes the options below it out of view. Labels leave out the settings
	// path, which can be arbitrarily long, and the rest of the version output.
	options := make([]huh.Option[string], 0, len(selected))
	for _, s := range selected {
		version := cmp.Or(harnessVersionNumber.FindString(s.detection.Version), "version unavailable")
		label := ansi.Truncate(s.Name()+"  "+version, harnessPickerLabelWidth, "…")
		options = append(options, huh.NewOption(label, s.Name()))
	}
	var names []string
	err := huh.NewMultiSelect[string]().
		Title("Select harnesses to configure").
		Options(options...).
		// Without a height, huh takes the title's line out of the options' rows
		// and hides the last option.
		Height(len(options) + 1).
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

// harnessTarget pairs a detected harness with the raw setup options to apply.
// Setup fills the record from flags; sync fills it from the harness.json
// record and may blank stored routes that no longer exist.
type harnessTarget struct {
	selectedHarness
	record harness.Record
}

// planHarnessSetup builds the setup plans for targets that share one team. In
// sync mode, a stored route the team no longer has is dropped with a warning
// naming its replacement, and the target's record is updated so the fallback
// persists; in setup mode the same situation is an error.
func planHarnessSetup(ctx *CommandContext, api *managementapi.Client, team *managementapi.Team, targets []harnessTarget, sync bool) ([]*harness.Plan, error) {
	listed, err := listRoutes(ctx, api, managementapi.GetV1RoutesParams{TeamId: &team.Id})
	if err != nil {
		return nil, err
	}
	if len(listed) == 0 {
		return nil, fmt.Errorf("team %s has no routes; create one with 'baseten route create'", team.Name)
	}
	var routes []harness.Route
	var skipped []string
	for _, l := range listed {
		r, err := harness.NewRoute(l)
		switch {
		case err == nil:
			routes = append(routes, r)
		case !sync && slices.ContainsFunc(targets, func(t harnessTarget) bool { return t.record.Route == l.Name }):
			return nil, cmd.NewErrValidation(err)
		default:
			skipped = append(skipped, l.Name)
		}
	}
	if len(routes) == 0 {
		return nil, cmd.NewErrValidation(errors.New("none of the team's routes have usable model metadata"))
	}
	if len(skipped) > 0 {
		ctx.Logf("warning: skipping routes without usable model metadata: %s\n", strings.Join(skipped, ", "))
	}
	// One provider configuration serves every route, so they must share an endpoint.
	endpoint := ""
	for _, r := range listed {
		if slices.Contains(skipped, r.Name) {
			continue
		}
		invokeURL := strings.TrimRight(r.InvokeUrl, "/")
		if endpoint == "" {
			endpoint = invokeURL
		} else if invokeURL != endpoint {
			return nil, fmt.Errorf("team %s has routes with different invoke URLs", team.Name)
		}
	}
	var serverDefaults harness.ServerDefaults
	for _, t := range targets {
		if t.Name() == harness.ClaudeCode && (t.record.Route == "" || t.record.BackgroundRoute == "") {
			serverDefaults, err = fetchServerDefaults(ctx, api, listed[0].TeamId)
			if err != nil {
				ctx.VerboseLogf("warning: could not load the team's harness defaults: %v\n", err)
			}
			break
		}
	}
	var plans []*harness.Plan
	for i := range targets {
		target := &targets[i]
		callable := target.Routes(routes)
		if len(callable) == 0 {
			return nil, fmt.Errorf("team %s has no routes that %s can call", team.Name, target.Name())
		}
		var defaults harness.ServerDefaults
		if target.Name() == harness.ClaudeCode {
			defaults = serverDefaults.Selectable(callable)
		}
		var dropped [][2]string // option label, vanished route name
		if sync {
			for _, option := range []struct {
				label  string
				stored *string
			}{
				{"default route", &target.record.Route},
				{"background route", &target.record.BackgroundRoute},
				{"subagent route", &target.record.SubagentRoute},
				{"fallback route", &target.record.FallbackRoute},
			} {
				if *option.stored != "" && !slices.ContainsFunc(callable, func(r harness.Route) bool { return r.Name == *option.stored }) {
					dropped = append(dropped, [2]string{option.label, *option.stored})
					*option.stored = ""
				}
			}
		}
		selection := harness.Selection{
			Primary:    cmp.Or(target.record.Route, defaults.Primary, callable[0].Name),
			Background: cmp.Or(target.record.BackgroundRoute, defaults.Background),
			Subagent:   target.record.SubagentRoute,
			Fallback:   target.record.FallbackRoute,
		}
		resolved, err := selection.Resolve(callable)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", target.Name(), err)
		}
		for _, d := range dropped {
			using := resolved.Primary
			switch d[0] {
			case "background route":
				using = target.BackgroundRoute(resolved)
			case "subagent route":
				using = resolved.Subagent
			case "fallback route":
				using = resolved.Fallback
			}
			ctx.Logf("warning: %s: %s %q no longer exists; using %q\n", target.Name(), d[0], d[1], using)
		}
		target.selection = selection
		current, err := target.Prepare(target.detection.Path, callable, selection, endpoint)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", target.Name(), err)
		}
		for _, p := range current {
			p.Harness = target.Name()
		}
		plans = append(plans, current...)
	}
	return plans, nil
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
	team, err := resolveTeamOrDefault(ctx, api, f.Team)
	if err != nil {
		return err
	}
	record := harness.Record{
		KeyName:         f.KeyName,
		Route:           f.Route,
		BackgroundRoute: f.BackgroundRoute,
		SubagentRoute:   f.SubagentRoute,
		FallbackRoute:   f.FallbackRoute,
	}
	targets := make([]harnessTarget, len(selected))
	for i := range selected {
		targets[i] = harnessTarget{selectedHarness: selected[i], record: record}
	}
	plans, err := planHarnessSetup(ctx, api, team, targets, false)
	if err != nil {
		return err
	}
	switch {
	case ctx.JSON:
	case ctx.verbose || f.DryRun:
		harnessVerboseSetupSummary(ctx, team, targets, plans)
	default:
		harnessReplacedWarnings(ctx, plans)
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
	token, err := ensureHarnessToken(ctx, api, team, f.KeyName)
	if err != nil {
		return err
	}
	for i := range targets {
		if targets[i].Name() == harness.Codex {
			harnessLogoutCodex(ctx, filepath.Dir(targets[i].detection.Path), f.Yes)
		}
	}
	if err := harness.ApplyPlans(plans, token); err != nil {
		return err
	}
	if !ctx.JSON {
		ctx.LogLine("Configuration saved. Restart the configured harnesses to load the changes.")
	}
	for i := range targets {
		t := &targets[i]
		if t.Name() == harness.Codex && !harnessRestartCodexDaemon(ctx, filepath.Dir(t.detection.Path), f.Yes) {
			// The restart was declined or failed; sync retries it later.
			t.record.CodexRestartPending = true
		}
	}
	if err := recordHarnessSetup(ctx, team, targets); err != nil {
		return err
	}
	if ctx.JSON {
		outputHarnessPlansJSON(ctx, plans, nil)
		return nil
	}
	for i := range targets {
		ctx.Logf("Undo with: %s\n", harnessFollowupCommand("teardown", targets[i].selectedHarness, f.ConfigDir))
	}
	return nil
}

// commandHarnessSync refreshes the harnesses configured on this machine with
// the team and options recorded by their last setup.
func commandHarnessSync(ctx *CommandContext, f *cmd.HarnessSyncFlags) error {
	if !harness.Supported() {
		return errors.New("harness commands support only macOS and Linux for now")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()
	dir, err := auth.DefaultConfigDir()
	if err != nil {
		return err
	}
	file, err := harness.LoadRecords(dir)
	if err != nil {
		return err
	}
	profile, managementURL, err := harnessRecordScope(ctx)
	if err != nil {
		return err
	}
	// The records say what was set up and where; a record whose settings file
	// no longer carries Baseten settings, or belongs to another profile, is
	// left alone.
	var targets []harnessTarget
	for _, h := range harness.All() {
		record, ok := file.Harnesses[h.Name()]
		if !ok || record.Profile != profile || record.ManagementURL != managementURL {
			continue
		}
		d, err := h.Detect(ctx, ctx.Execer(), filepath.Dir(record.Config))
		if err != nil {
			return err
		}
		if d.Path != record.Config {
			continue
		}
		status, err := h.Inspect(d)
		if err != nil {
			ctx.Logf("warning: skipping %s: settings at %s are misconfigured: %v\n", h.Name(), harnessDisplayPath(d.Path), err)
			continue
		}
		if status.State == harness.StateNotConfigured {
			continue
		}
		targets = append(targets, harnessTarget{selectedHarness: selectedHarness{Harness: h, detection: d}, record: record})
	}
	if len(targets) == 0 {
		if ctx.JSON {
			ctx.OutputJSON(cmd.HarnessPlanList{Items: []cmd.HarnessPlan{}})
		} else {
			ctx.LogLine("No harnesses to sync. Run 'baseten harness setup' first.")
		}
		return nil
	}
	// Plans and the routes API key are shared within a team and key name, so
	// sync groups the targets accordingly.
	slices.SortStableFunc(targets, func(a, b harnessTarget) int {
		if c := strings.Compare(a.record.TeamID, b.record.TeamID); c != 0 {
			return c
		}
		return strings.Compare(a.record.KeyName, b.record.KeyName)
	})
	type syncRun struct {
		team    *managementapi.Team
		targets []harnessTarget
		plans   []*harness.Plan
	}
	var runs []syncRun
	for start, end := 0, 0; start < len(targets); start = end {
		group := targets[start].record
		for end = start; end < len(targets) && targets[end].record.TeamID == group.TeamID && targets[end].record.KeyName == group.KeyName; end++ {
		}
		team, err := resolveTeamOrDefault(ctx, api, group.TeamID)
		if err != nil {
			return fmt.Errorf("%s: %w; rerun 'baseten harness setup --harness %s' to record a current team", targets[start].Name(), err, targets[start].Name())
		}
		run := syncRun{team: team, targets: targets[start:end]}
		if run.plans, err = planHarnessSetup(ctx, api, run.team, run.targets, true); err != nil {
			return err
		}
		runs = append(runs, run)
	}
	var plans []*harness.Plan
	for _, run := range runs {
		plans = append(plans, run.plans...)
	}
	switch {
	case ctx.JSON:
	case ctx.verbose || f.DryRun:
		for _, run := range runs {
			harnessVerboseSetupSummary(ctx, run.team, run.targets, run.plans)
		}
	default:
		harnessReplacedWarnings(ctx, plans)
	}
	if f.DryRun {
		if ctx.JSON {
			outputHarnessPlansJSON(ctx, plans, nil)
		} else {
			ctx.LogLine("Preview only. No files changed and no routes API key created.")
		}
		return nil
	}
	changed := slices.ContainsFunc(plans, func(p *harness.Plan) bool { return p.Changed })
	if !changed {
		// Retry codex daemon restarts that were declined or failed earlier.
		for i := range targets {
			syncCodexDaemon(ctx, &targets[i], false, f.Yes)
		}
		if err := saveHarnessRecords(dir, file, targets); err != nil {
			return err
		}
		if ctx.JSON {
			outputHarnessPlansJSON(ctx, plans, nil)
		} else {
			ctx.LogLine("All configured harnesses are up to date.")
		}
		return nil
	}
	if !f.Yes {
		if err := ctx.ConfirmYesNo("Apply these harness changes?"); err != nil {
			return err
		}
	}
	for _, run := range runs {
		// The routes API key is only read or created once the user has confirmed.
		token, err := ensureHarnessToken(ctx, api, run.team, run.targets[0].record.KeyName)
		if err != nil {
			return err
		}
		for i := range run.targets {
			if run.targets[i].Name() == harness.Codex {
				harnessLogoutCodex(ctx, filepath.Dir(run.targets[i].detection.Path), f.Yes)
			}
		}
		if err := harness.ApplyPlans(run.plans, token); err != nil {
			return err
		}
	}
	if !ctx.JSON {
		ctx.LogLine("Configuration saved. Restart the configured harnesses to load the changes.")
	}
	for i := range targets {
		syncCodexDaemon(ctx, &targets[i], harnessPlansChanged(plans, targets[i].Name()), f.Yes)
	}
	if err := saveHarnessRecords(dir, file, targets); err != nil {
		return err
	}
	if ctx.JSON {
		outputHarnessPlansJSON(ctx, plans, nil)
	}
	return nil
}

// syncCodexDaemon restarts the codex app-server daemon for one target, like
// setup does, when this sync changed its settings or an earlier restart is
// still pending. A declined or failed restart stays pending for the next sync.
func syncCodexDaemon(ctx *CommandContext, target *harnessTarget, changed, yes bool) {
	if target.Name() != harness.Codex || (!changed && !target.record.CodexRestartPending) {
		return
	}
	target.record.CodexRestartPending = !harnessRestartCodexDaemon(ctx, filepath.Dir(target.detection.Path), yes)
}

// harnessPlansChanged reports whether any plan of the named harness changed.
func harnessPlansChanged(plans []*harness.Plan, name string) bool {
	return slices.ContainsFunc(plans, func(p *harness.Plan) bool { return p.Harness == name && p.Changed })
}

// harnessRecordScope returns the profile and management URL that scope harness
// records, matching the scope of saved routes API keys.
func harnessRecordScope(ctx *CommandContext) (profile, managementURL string, err error) {
	remote, err := ctx.authInfo.Remote()
	if err != nil {
		return "", "", err
	}
	session, err := ctx.authInfo.Session()
	if err != nil {
		return "", "", err
	}
	return session.ProfileName(), remote.ManagementURL(), nil
}

// recordHarnessSetup saves the setup options of targets as their harness
// records, so sync can replay them.
func recordHarnessSetup(ctx *CommandContext, team *managementapi.Team, targets []harnessTarget) error {
	profile, managementURL, err := harnessRecordScope(ctx)
	if err != nil {
		return err
	}
	dir, err := auth.DefaultConfigDir()
	if err != nil {
		return err
	}
	file, err := harness.LoadRecords(dir)
	if err != nil {
		return err
	}
	for i := range targets {
		t := &targets[i]
		t.record.Config = t.detection.Path
		t.record.Profile = profile
		t.record.ManagementURL = managementURL
		t.record.TeamID = team.Id
		file.Harnesses[t.Name()] = t.record
	}
	return harness.SaveRecords(dir, file)
}

// saveHarnessRecords writes the targets' records back, persisting routes that
// fell back to defaults and deferred codex restarts.
func saveHarnessRecords(dir string, file harness.RecordFile, targets []harnessTarget) error {
	for i := range targets {
		file.Harnesses[targets[i].Name()] = targets[i].record
	}
	return harness.SaveRecords(dir, file)
}

// forgetHarnessRecords removes the setup records of the named harnesses, so
// sync no longer refreshes them.
func forgetHarnessRecords(names []string) error {
	dir, err := auth.DefaultConfigDir()
	if err != nil {
		return err
	}
	file, err := harness.LoadRecords(dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		delete(file.Harnesses, name)
	}
	return harness.SaveRecords(dir, file)
}

// ensureHarnessToken returns the routes API key for the team, creating one on
// first use and saving it for later reuse.
func ensureHarnessToken(ctx *CommandContext, api *managementapi.Client, team *managementapi.Team, name string) (string, error) {
	key, err := loadHarnessKey(ctx, api, team, name)
	if err != nil {
		return "", err
	}
	return key.ensure(ctx, api)
}

func harnessReplacedWarnings(ctx *CommandContext, plans []*harness.Plan) {
	var replaced []string
	for _, p := range plans {
		if len(p.Replaced) > 0 && !slices.Contains(replaced, p.Harness) {
			replaced = append(replaced, p.Harness)
		}
	}
	if len(replaced) > 0 {
		ctx.Logf("warning: existing integration settings in %s will be overwritten; teardown does not restore them\n", strings.Join(replaced, ", "))
	}
}

func fetchServerDefaults(ctx context.Context, api *managementapi.Client, teamID string) (harness.ServerDefaults, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	settings, err := api.GetRoutesSettingsTeams(ctx, teamID)
	if err != nil {
		return harness.ServerDefaults{}, fmt.Errorf("GET /v1/routes/settings/teams/%s: %w", teamID, err)
	}
	var defaults harness.ServerDefaults
	entry := settings.HarnessDefaults.ClaudeCode
	if entry.Primary != nil {
		defaults.Primary = entry.Primary.Route.Name
	}
	if entry.Background != nil {
		defaults.Background = entry.Background.Route.Name
	}
	return defaults, nil
}

func harnessLogoutCodex(ctx *CommandContext, dir string, yes bool) {
	if !harness.CodexLoggedIn(ctx, ctx.Execer(), dir) {
		return
	}
	hint := "Run `codex logout` and delete `" + harnessDisplayPath(harness.CodexCloudConfigPath(dir)) + "` to stop the workspace default model from overriding the Baseten route."
	ctx.LogLine("Signing codex out of OpenAI/ChatGPT and disabling ChatGPT login while the Baseten harness is configured.")
	if !yes && ctx.ConfirmYesNo("Sign codex out of OpenAI/ChatGPT now?") != nil {
		ctx.LogLine(hint)
		return
	}
	if err := harness.CodexLogout(ctx, ctx.Execer(), dir); err != nil {
		auth := filepath.Join(dir, "auth.json")
		if os.Remove(auth) != nil {
			ctx.Logf("warning: codex logout failed: %v\n", err)
			ctx.LogLine(hint)
			return
		}
		ctx.Logf("warning: codex logout failed (%v); removed %s instead\n", err, harnessDisplayPath(auth))
	}
	if err := harness.CodexClearCloudConfig(dir); err != nil {
		ctx.Logf("warning: could not remove the cached workspace policy: %v\n", err)
		ctx.LogLine(hint)
		return
	}
	ctx.LogLine("Signed codex out of OpenAI/ChatGPT and cleared the cached workspace policy.")
}

const harnessCodexDaemonRestartHint = "Run `codex app-server daemon restart` when you're done to pick up the new models."

// harnessRestartCodexDaemon restarts the codex app-server daemon after setup,
// asking first when sessions are attached. It reports false when a restart is
// still needed: the prompt was declined or the restart failed.
func harnessRestartCodexDaemon(ctx *CommandContext, dir string, yes bool) bool {
	socket := harness.CodexDaemonSocket(ctx, ctx.Execer(), dir)
	if socket == "" {
		return true
	}
	clients, err := harness.CodexDaemonClients(ctx, ctx.Execer(), socket)
	switch {
	case err != nil:
		ctx.Logf("warning: could not check for running codex sessions (%v); restarting the codex app-server daemon would interrupt their in-progress turns\n", err)
	case clients > 0:
		ctx.Logf("warning: restarting the codex app-server daemon will disconnect %d running codex session(s); in-progress turns will be interrupted (thread history is preserved and can be resumed with `codex resume`)\n", clients)
	}
	if (err != nil || clients > 0) && !yes {
		if ctx.ConfirmYesNo("Restart the codex app-server daemon now?") != nil {
			ctx.LogLine(harnessCodexDaemonRestartHint)
			return false
		}
	}
	pid, err := harness.RestartCodexDaemon(ctx, ctx.Execer(), dir)
	if err != nil {
		ctx.Logf("warning: could not restart the codex app-server daemon: %v\n", err)
		ctx.LogLine(harnessCodexDaemonRestartHint)
		return false
	}
	ctx.Logf("Restarted codex app-server daemon (pid %d) so the new model catalog takes effect.\n", pid)
	return true
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
	// Teardown forgets the recorded setup so sync no longer refreshes these.
	if err := forgetHarnessRecords(names); err != nil {
		return err
	}
	if !ctx.JSON {
		ctx.LogLine("Baseten settings removed. Restart the harnesses to load the changes.")
	}
	for _, choice := range selected {
		if choice.Name() == harness.Codex && slices.Contains(names, harness.Codex) {
			harnessRestartCodexDaemon(ctx, filepath.Dir(choice.detection.Path), f.Yes)
			ctx.LogLine("Teardown removed the ChatGPT login restriction. Run `codex login` to sign back in; your workspace defaults re-apply on the next launch.")
		}
	}
	if ctx.JSON {
		outputHarnessPlansJSON(ctx, plans, deleted)
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

func harnessVerboseSetupSummary(ctx *CommandContext, team *managementapi.Team, selected []harnessTarget, plans []*harness.Plan) {
	renderer := lipgloss.NewRenderer(ctx.Stdout)
	accent := renderer.NewStyle().Inherit(inlineCodeStyle)
	heading := renderer.NewStyle().Bold(true)
	ctx.Outputf("Team: %s\n", accent.Render(team.Name))
	for _, choice := range selected {
		s := choice.selection
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

// forgetRouteAPIKeys removes this machine's saved copies of deleted keys.
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

func loadHarnessKey(ctx *CommandContext, api *managementapi.Client, team *managementapi.Team, name string) (*harnessKey, error) {
	explicit := name != ""
	if !explicit {
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
	store, err := NewAuthStore(false)
	if err != nil {
		return nil, err
	}
	keys, err := listRouteAPIKeys(ctx, api)
	if err != nil {
		return nil, err
	}
	// Key names are unique per team, and a key made elsewhere, such as under
	// another profile, can't be reused here. The default name then moves on to
	// a numbered name saved on this machine, or else the first free one.
	var free *harnessKey
	for i := 1; i <= len(keys.Keys)+1; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", name, i)
		}
		scope, err := routesKeyScope(ctx, user.UserId, team.Id, candidate)
		if err != nil {
			return nil, err
		}
		k := &harnessKey{store: store, scope: scope}
		if k.saved, err = store.GetRoutesKey(scope); err != nil {
			return nil, err
		}
		// The saved key may have been deleted, here or on another machine; setup
		// then creates a new one.
		if !slices.ContainsFunc(keys.Keys, func(key managementapi.APIKeyInfo) bool { return strings.HasPrefix(k.saved, key.Prefix) }) {
			k.saved = ""
		}
		if k.saved != "" || explicit {
			return k, nil
		}
		taken := slices.ContainsFunc(keys.Keys, func(key managementapi.APIKeyInfo) bool {
			return deref(key.Name) == candidate && deref(key.TeamName) == team.Name
		})
		if free == nil && !taken {
			free = k
		}
	}
	return free, nil
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
