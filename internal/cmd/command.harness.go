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
	if len(p.Conflicts) > 0 {
		ctx.Outputf("Preserved user edits: %s\n", strings.Join(p.Conflicts, ", "))
	}
}

func commandHarnessSetup(ctx *CommandContext, f *cmd.HarnessSetupFlags) error {
	interactive := ctx.IsInteractive() && !ctx.JSON
	if !interactive && (f.Harness == "" || f.Model == "") {
		return cmd.NewErrUsagef("pass --harness and --model when not interactive")
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
	credential, err := prepareHarnessAuth(ctx, &cmd.HarnessAuthSetupFlags{CommandFlags: f.CommandFlags, Team: f.Team, Name: f.KeyName, DryRun: f.DryRun})
	if err != nil {
		return err
	}
	listed, err := listHarnessRoutes(ctx, credential.scope.TeamID)
	if err != nil {
		return err
	}
	mcpServers, err := listHarnessMCPServers(ctx, credential.scope.TeamID)
	if err != nil {
		return err
	}
	mcpServers, err = resolveHarnessMCPServerTokens(ctx, mcpServers)
	if err != nil {
		return err
	}
	routes, endpoint, skipped, err := harnessCatalog(ctx, credential.transport, credential.scope.ManagementURL, listed)
	if err != nil {
		return err
	}
	if len(skipped) > 0 {
		ctx.Logf("Routes omitted because /v1/models lacks usable metadata: %s\n", strings.Join(skipped, ", "))
	}
	selections := make([]harness.Selection, len(detections))
	for i, d := range detections {
		selection := harness.Selection{Primary: f.Model, Background: f.BackgroundModel, Subagent: f.SubagentModel, Fallback: f.FallbackModel}
		if selection.Primary == "" {
			options := make([]huh.Option[string], 0, len(routes))
			for _, route := range routes {
				options = append(options, huh.NewOption(route.DisplayName+" ("+route.Name+")", route.Name))
			}
			if err := harnessPrompt(ctx, huh.NewSelect[string]().Title("Primary Route for "+d.Name).Options(options...).Value(&selection.Primary)); err != nil {
				return err
			}
		}
		selections[i] = selection
	}
	buildPlans := func(token string) ([]*harness.Plan, error) {
		var plans []*harness.Plan
		occupied := map[string]bool{}
		for i, d := range detections {
			current, err := harness.PrepareHarness(d.Name, d.Path, routes, mcpServers, selections[i], endpoint, token, f.ReplacePicker, f.ReplaceExisting)
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
	if f.DryRun {
		harnessPlansOutput(ctx, plans)
		return nil
	}
	if !ctx.JSON {
		harnessPlansOutput(ctx, plans)
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
		ctx.OutputLine("Harness configured. Restart the harness; rerun setup to refresh Routes.")
	}
	return nil
}
func harnessMCPTokenEnvVar(name string) string {
	var b strings.Builder
	b.WriteString("BASETEN_MCP_TOKEN_")
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z':
			b.WriteByte(c - ('a' - 'A'))
		case c >= 'A' && c <= 'Z' || c >= '0' && c <= '9':
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func resolveHarnessMCPServerTokens(ctx *CommandContext, servers []harness.MCPServer) ([]harness.MCPServer, error) {
	for i := range servers {
		token := os.Getenv(harnessMCPTokenEnvVar(servers[i].Name))
		if token == "" && ctx.IsInteractive() && !ctx.JSON {
			if err := harnessPrompt(ctx, huh.NewInput().Title("Token for "+servers[i].Name+" ("+servers[i].URL+") — optional, Enter to skip").EchoMode(huh.EchoModePassword).Value(&token)); err != nil {
				return nil, err
			}
		}
		servers[i].AuthorizationToken = token
	}
	return servers, nil
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
		ctx.OutputJSON(cmd.HarnessStatusResult{
			Name: r.Name, Path: r.Path, Installed: r.Installed, Version: r.Version, Supported: r.Supported,
			State: r.State, Managed: r.Managed, Drift: r.Drift, Routes: r.Routes, Note: r.Note,
		})
	} else {
		ctx.Outputf("%s: %s\nConfig: %s\nVersion: %s (supported=%t)\n", r.Name, r.State, r.Path, r.Version, r.Supported)
		ctx.OutputLine(r.Note)
		if len(r.Drift) > 0 {
			ctx.Outputf("Changed settings: %s\n", strings.Join(r.Drift, ", "))
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
	return cmd.HarnessPlanResult{Managed: p.Managed, Path: p.Path, Keys: p.Keys, Changed: p.Changed, Conflicts: p.Conflicts}
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
