package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/basetenlabs/baseten-cli/internal/safefile"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

func init() {
	Register("harness setup", commandHarnessSetup)
	Register("harness status", commandHarnessStatus)
	Register("harness teardown", commandHarnessTeardown)
}
func harnessConfirm(ctx *CommandContext, yes bool, path string) error {
	if yes {
		return nil
	}
	if !ctx.IsInteractive() || ctx.JSON {
		return cmd.NewErrUsagef("pass --yes to apply the configuration plan, or --dry-run to preview it")
	}
	return ctx.ConfirmYesNo("Apply harness changes to " + path + "?")
}
func harnessPlanOutput(ctx *CommandContext, p *harness.Plan) {
	if ctx.JSON {
		ctx.OutputJSON(harnessPlanResult(p))
		return
	}
	ctx.Outputf("Config: %s\nSettings: %s\nChanged: %t\n", p.Path, strings.Join(p.Keys, ", "), p.Changed)
	if len(p.Replaced) > 0 {
		ctx.Outputf("Settings to replace: %s\n", strings.Join(p.Replaced, ", "))
		ctx.OutputLine("Original settings are backed up when applied and can be restored with harness teardown.")
	}
	if len(p.Conflicts) > 0 {
		ctx.Outputf("Preserved user edits: %s\n", strings.Join(p.Conflicts, ", "))
	}
}

func commandHarnessSetup(ctx *CommandContext, f *cmd.HarnessSetupFlags) error {
	interactive := ctx.IsInteractive() && !ctx.JSON
	if !interactive && f.Harness == "" {
		return cmd.NewErrUsagef("pass --harness when not interactive")
	}
	names := []string{f.Harness}
	if f.Harness == "" {
		names = []string{"claude-code", "codex", "opencode"}
	}
	detected := make(map[string]harness.Detection, len(names))
	var available []harness.Detection
	for _, name := range names {
		d, err := harness.Detect(ctx, ctx.Execer(), name, f.Config)
		if err != nil {
			return cmd.NewErrUsage(err)
		}
		detected[name] = d
		available = append(available, d)
	}
	options, summary := harnessSetupOptions(available)
	if f.Harness == "" {
		if len(options) == 0 {
			return cmd.NewErrUsagef("no supported harnesses are installed:\n%s", summary)
		}
		names = nil
		if err := harnessPrompt(ctx, huh.NewMultiSelect[string]().Title("Detected harnesses: select which to configure").Description(summary).Options(options...).Value(&names).Validate(func(names []string) error {
			if len(names) == 0 {
				return fmt.Errorf("choose at least one harness")
			}
			return nil
		})); err != nil {
			return err
		}
	}
	if len(names) > 1 && f.Config != "" {
		return cmd.NewErrUsagef("--config names one file; select one harness or omit --config")
	}
	var detections []harness.Detection
	for _, name := range names {
		d := detected[name]
		if !d.Installed {
			return cmd.NewErrUsagef("%s is not installed or not on PATH", name)
		}
		if !d.Supported {
			return cmd.NewErrUsagef("%s; tested version is %s on macOS", harnessDetectionLabel(d), harness.TestedVersions[name])
		}
		detections = append(detections, d)
	}
	credential, err := prepareHarnessAuth(ctx, f)
	if err != nil {
		return err
	}
	listed, err := listHarnessRoutes(ctx, credential.scope.TeamID)
	if err != nil {
		return err
	}
	routes, endpoint, err := harnessCatalog(credential.scope.ManagementURL, listed)
	if err != nil {
		return err
	}
	selections := make([]harness.Selection, len(detections))
	for i := range detections {
		selection := harness.Selection{Primary: f.Model, Background: f.BackgroundModel, Subagent: f.SubagentModel, Fallback: f.FallbackModel}
		if selection.Primary == "" {
			selection.Primary = routes[0].Name
		}
		selections[i] = selection
	}
	buildPlans := func(token string) ([]*harness.Plan, error) {
		var plans []*harness.Plan
		occupied := map[string]bool{}
		for i, d := range detections {
			current, err := harness.PrepareHarness(d.Name, d.Path, routes, selections[i], endpoint, token, d.Name == "claude-code", true)
			if err != nil {
				return nil, cmd.NewErrUsage(fmt.Errorf("%s: %w", d.Name, err))
			}
			for _, plan := range current {
				snapshot, err := safefile.ReadSnapshot(plan.Path)
				if err != nil {
					return nil, err
				}
				for _, target := range []string{snapshot.Target, harness.JournalPath(snapshot.Target)} {
					if occupied[target] {
						return nil, cmd.NewErrUsagef("selected harness configurations overlap at %s", target)
					}
					occupied[target] = true
				}
			}
			plans = append(plans, current...)
		}
		return plans, nil
	}
	token := credential.saved
	if token == "" {
		token = "baseten-harness-pending-credential"
	}
	plans, err := buildPlans(token)
	if err != nil {
		return err
	}
	if !ctx.JSON {
		harnessSetupSummary(ctx, credential, routes, detections, selections, plans)
	}
	if f.DryRun {
		if ctx.JSON {
			harnessPlansOutput(ctx, plans)
		} else {
			ctx.OutputLine("Preview only. No files changed or routes API key created.")
		}
		return nil
	}
	paths := make([]string, len(detections))
	for i, d := range detections {
		paths[i] = d.Path
	}
	if err := harnessConfirm(ctx, f.Yes, strings.Join(paths, ", ")); err != nil {
		return err
	}
	for _, path := range paths {
		unlock, err := harness.Lock(path)
		if err != nil {
			return err
		}
		defer unlock()
	}
	if err := harness.CheckPlans(plans); err != nil {
		return err
	}
	// Mint only after every configuration has passed validation and confirmation.
	token, _, err = credential.ensure(ctx)
	if err != nil {
		return err
	}
	if err := harness.CheckPlans(plans); err != nil {
		return err
	}
	plans, err = buildPlans(token)
	if err != nil {
		return err
	}
	if err := harness.ApplyPlans(plans); err != nil {
		return err
	}
	if ctx.JSON {
		harnessPlansOutput(ctx, plans)
	} else {
		ctx.OutputLine("Configuration saved. Restart the configured harnesses to load the changes.")
		for _, d := range detections {
			ctx.Outputf("Restore settings: baseten harness teardown --harness %s\n", d.Name)
		}
	}
	return nil
}
func commandHarnessStatus(ctx *CommandContext, f *cmd.HarnessFlags) error {
	d, err := harness.Detect(ctx, ctx.Execer(), f.Harness, f.Config)
	if err != nil {
		return cmd.NewErrUsage(err)
	}
	r, err := harness.Inspect(d)
	if err != nil {
		return err
	}
	if ctx.JSON {
		details := make([]cmd.HarnessRouteSummary, 0, len(r.RouteDetails))
		for _, route := range r.RouteDetails {
			details = append(details, cmd.HarnessRouteSummary{Name: route.Name, DisplayName: route.DisplayName})
		}
		ctx.OutputJSON(cmd.HarnessStatusResult{
			DefaultRoute: r.DefaultRoute, SmallTaskModel: r.SmallTaskModel, RouteDetails: details,
			Name: r.Name, Path: r.Path, Installed: r.Installed, Version: r.Version, Supported: r.Supported,
			State: r.State, Managed: r.Managed, Drift: r.Drift, Routes: r.Routes, Note: r.Note,
		})
	} else {
		ctx.Outputf("%s: %s\n", r.Name, r.State)
		ctx.Outputf("  Config         %s\n  Version        %s\n", harnessDisplayPath(r.Path), r.Version)
		if !r.Installed {
			ctx.OutputLine("  Installation   Not found on PATH")
		} else if !r.Supported {
			ctx.OutputLine("  Compatibility  Version or platform not supported for setup")
		}
		if r.DefaultRoute != "" {
			ctx.Outputf("  Default route  %s\n", r.DefaultRoute)
		}
		if r.SmallTaskModel != "" {
			ctx.Outputf("  %s  %s\n", harnessSmallTaskLabel(r.Name), r.SmallTaskModel)
		}
		if len(r.Drift) > 0 {
			ctx.Outputf("  Changed settings  %s\n", strings.Join(r.Drift, ", "))
		}
		ctx.OutputLine("")
		harnessRouteTable(ctx, "Configured routes", r.RouteDetails)
		ctx.Outputf("\nLocal configuration only; key validity and live routes were not checked.\nRefresh: baseten harness setup --harness %s (add --team <team> if needed), then restart the harness.\n", r.Name)
		if len(r.Managed) > 0 {
			ctx.VerboseLogf("Managed settings: %s\n", strings.Join(r.Managed, ", "))
		}
	}
	return nil
}
func commandHarnessTeardown(ctx *CommandContext, f *cmd.HarnessTeardownFlags) error {
	d, err := harness.Detect(ctx, ctx.Execer(), f.Harness, f.Config)
	if err != nil {
		return cmd.NewErrUsage(err)
	}
	plans, err := harness.PrepareHarnessTeardown(f.Harness, d.Path)
	if err != nil {
		return err
	}
	if f.DryRun {
		harnessPlansOutput(ctx, plans)
		return nil
	}
	if !ctx.JSON {
		harnessPlansOutput(ctx, plans)
	}
	managed := false
	for _, p := range plans {
		managed = managed || p.Managed
	}
	if !managed {
		if ctx.JSON {
			harnessPlansOutput(ctx, plans)
		}
		return nil
	}
	if err := harnessConfirm(ctx, f.Yes, d.Path); err != nil {
		return err
	}
	unlock, err := harness.Lock(d.Path)
	if err != nil {
		return err
	}
	defer unlock()
	if err := harness.ApplyPlans(plans); err != nil {
		return err
	}
	if ctx.JSON {
		harnessPlansOutput(ctx, plans)
	}
	conflicts := []string{}
	for _, p := range plans {
		conflicts = append(conflicts, p.Conflicts...)
	}
	if len(conflicts) > 0 {
		if ctx.JSON {
			ctx.SuppressJSONError()
		}
		return fmt.Errorf("user edits preserved; ownership journal retained for: %s", strings.Join(conflicts, ", "))
	}
	return nil
}

func harnessPlansOutput(ctx *CommandContext, plans []*harness.Plan) {
	if len(plans) == 1 {
		harnessPlanOutput(ctx, plans[0])
		return
	}
	if ctx.JSON {
		results := make([]cmd.HarnessPlanResult, 0, len(plans))
		for _, plan := range plans {
			results = append(results, harnessPlanResult(plan))
		}
		ctx.OutputJSON(cmd.HarnessPlansResult{Changes: results})
		return
	}
	for _, p := range plans {
		harnessPlanOutput(ctx, p)
	}
}

func harnessPlanResult(p *harness.Plan) cmd.HarnessPlanResult {
	return cmd.HarnessPlanResult{Replaced: p.Replaced, Managed: p.Managed, Path: p.Path, Keys: p.Keys, Changed: p.Changed, Conflicts: p.Conflicts}
}

// Keep unavailable installations visible without offering choices that setup
// would reject. Use the shared multiselect's native toggling and select-all keys.
func harnessSetupOptions(detections []harness.Detection) ([]huh.Option[string], string) {
	var options []huh.Option[string]
	var unavailable []string
	for _, d := range detections {
		label := harnessDetectionLabel(d)
		if d.Installed && d.Supported {
			options = append(options, huh.NewOption(label, d.Name))
		} else {
			unavailable = append(unavailable, "[-] "+label)
		}
	}
	return options, strings.Join(unavailable, "\n")
}

func harnessDetectionLabel(d harness.Detection) string {
	if !d.Installed {
		return fmt.Sprintf("%-12s  not installed or not on PATH", d.Name)
	}
	version := d.Version
	if version == "" {
		version = "unknown"
	}
	path := d.Path
	if home, err := os.UserHomeDir(); err == nil {
		if relative, err := filepath.Rel(home, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			path = filepath.Join("~", relative)
		}
	}
	label := fmt.Sprintf("%-12s  %-10s  %s", d.Name, version, path)
	if d.Version == "" {
		return label + " (version unavailable)"
	}
	if !d.Supported {
		return label + " (unsupported version/platform)"
	}
	return label
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
	if name == "claude-code" {
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
	ctx.OutputTable(TableOutput{Headers: []string{"NAME", "DISPLAY NAME"}, Rows: rows})
}

func harnessSetupSummary(ctx *CommandContext, credential *harnessAuth, routes []harness.Route, detections []harness.Detection, selections []harness.Selection, plans []*harness.Plan) {
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
	for i, d := range detections {
		ctx.Outputf("\n%s\n  Config         %s\n  Default route  %s\n", heading.Render(d.Name), harnessDisplayPath(d.Path), accent.Render(selections[i].Primary))
		if small := harness.SmallTaskModel(d.Name, selections[i]); small != "" {
			ctx.Outputf("  %s  %s\n", harnessSmallTaskLabel(d.Name), accent.Render(small))
		}
		if selections[i].Subagent != "" {
			ctx.Outputf("  Subagents      %s\n", accent.Render(selections[i].Subagent))
		}
		if d.Name == "claude-code" {
			fallback := selections[i].Fallback
			if fallback == "" {
				fallback = selections[i].Primary
			}
			ctx.Outputf("  Fallback route %s\n", accent.Render(fallback))
		}
		ctx.Outputf("  Routes API key %s\n", keyAction)
		changed, replaced := false, false
		for _, p := range plans {
			if p.Path != d.Path && (d.Name != "codex" || p.Path != harness.CatalogPath(d.Path)) {
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
			ctx.OutputLine("  Existing integration settings will be replaced. Teardown restores settings from the first setup.")
		}
	}
	ctx.VerboseLogf("Routes API key name: %s\n", credential.scope.Name)
	ctx.OutputLine("")
}
