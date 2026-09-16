package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-cli/internal/code"
	"github.com/charmbracelet/huh"
)

func init() {
	Register("code init", commandCodeInit)
	Register("code status", commandCodeStatus)
	Register("code models", commandCodeModels)
	Register("code spend", commandCodeSpend)
	Register("code doctor", commandCodeDoctor)
	Register("code sync", commandCodeSync)
	Register("code teardown", commandCodeTeardown)
	Register("code logout", commandCodeLogout)
	Register("code keys list", commandCodeKeysList)
	Register("code keys revoke", commandCodeKeysRevoke)
	Register("code auth token", commandCodeToken)
}

func codeState(flags cmd.CodeFlags) (*code.Store, *code.Installation, error) {
	s, err := code.NewStore()
	if err != nil {
		return nil, nil, err
	}
	i, err := s.Load()
	if err != nil {
		return nil, nil, err
	}
	if i != nil {
		if flags.Org != "" && flags.Org != i.Org {
			return nil, nil, cmd.NewErrUsagef("--org does not match this installation; organization switching and slug resolution require the Code backend")
		}
		if flags.Profile != "" && flags.Profile != i.Profile {
			return nil, nil, cmd.NewErrUsagef("--profile does not match the Code installation's human profile")
		}
	}
	return s, i, nil
}
func codeClient(ctx *CommandContext, s *code.Store, i *code.Installation) (*code.Client, error) {
	token, err := s.Token(i)
	if err != nil {
		return nil, cmd.NewErrAuth(err)
	}
	return &code.Client{Token: token, Transport: ctx.httpClient().Transport}, nil
}
func codeOutput(ctx *CommandContext, r codeReport) {
	if ctx.JSON {
		ctx.OutputJSON(r)
		return
	}
	ctx.Outputf("%s\n", r.Status)
	if r.Org != "" {
		ctx.Outputf("Organization: %s\n", r.Org)
	}
	if r.Profile != "" {
		ctx.Outputf("Human profile: %s\n", r.Profile)
	}
	if r.UserID != "" {
		ctx.Outputf("User: %s\n", r.UserID)
	}
	if r.Model != "" {
		ctx.Outputf("Route: %s\n", r.Model)
	}
	if r.CredentialStatus != "" {
		ctx.Outputf("Credential: %s\n", r.CredentialStatus)
	}
	for _, h := range r.Harnesses {
		ctx.Outputf("%s: installed=%t, version=%s, config=%s\n", h.Name, h.Installed, h.Version, h.Path)
	}
	for _, change := range r.Changes {
		ctx.Outputf("%s: %s (%s)\n", change.Harness, change.Path, strings.Join(change.Keys, ", "))
	}
	for _, check := range r.Checks {
		ctx.Outputf("%s: %s\n", check.Name, check.Status)
	}
	for _, note := range r.Notes {
		ctx.OutputLine(note)
	}
}

type codeCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}
type codeReport struct {
	Status           string         `json:"status"`
	Org              string         `json:"org,omitempty"`
	Profile          string         `json:"profile,omitempty"`
	UserID           string         `json:"user_id,omitempty"`
	Model            string         `json:"model,omitempty"`
	CredentialStatus string         `json:"credential_status,omitempty"`
	Harnesses        []code.Harness `json:"harnesses,omitempty"`
	Changes          []*code.Change `json:"changes,omitempty"`
	Checks           []codeCheck    `json:"checks,omitempty"`
	Notes            []string       `json:"notes,omitempty"`
}

func reportFor(i *code.Installation, status string) codeReport {
	r := codeReport{Status: status, Notes: []string{code.CatalogRefreshGap}}
	if i != nil {
		r.Org = i.Org
		r.Profile = i.Profile
		r.UserID = i.UserID
		r.Model = i.Model
	}
	return r
}
func configured(i *code.Installation) []string {
	names := []string{}
	if i != nil {
		for h := range i.Configs {
			names = append(names, h)
		}
	}
	sort.Strings(names)
	return names
}
func codeTargets(ctx *CommandContext, i *code.Installation) ([]string, error) {
	if len(ctx.Args) > 0 {
		names, err := code.Normalize(ctx.Args)
		if err != nil {
			return nil, cmd.NewErrUsage(err)
		}
		return names, nil
	}
	names := configured(i)
	if len(names) == 0 {
		return nil, cmd.NewErrUsagef("no harnesses are configured; run baseten code init --dry-run --harness <name> to inspect setup requirements")
	}
	return names, nil
}
func commandCodeStatus(ctx *CommandContext, flags *cmd.CodeHarnessFlags) error {
	s, i, err := codeState(flags.CodeFlags)
	if err != nil {
		return err
	}
	r := reportFor(i, "not_initialized")
	r.CredentialStatus = "not_installed"
	names := configured(i)
	if flags.Harness != "" {
		names, err = code.Normalize([]string{flags.Harness})
		if err != nil {
			return cmd.NewErrUsage(err)
		}
	}
	if i != nil {
		r.Status = "installed"
		r.CredentialStatus = "stored_unverified"
		if _, err := s.Token(i); err != nil {
			r.CredentialStatus = "unavailable"
		}
	} else {
		// Read only profile metadata. Do not refresh OAuth or expose its token.
		session, err := auth.ResolveSession(flags.Profile, "")
		if err != nil {
			return err
		}
		r.Profile = session.ProfileName()
		r.Notes = append(r.Notes, code.CredentialDependency)
	}
	for _, name := range names {
		h, err := code.Describe(ctx, name)
		if err != nil {
			return err
		}
		r.Harnesses = append(r.Harnesses, h)
	}
	r.Notes = append(r.Notes, "Status is local only; it does not establish credential validity or current organization permissions.")
	codeOutput(ctx, r)
	return nil
}
func commandCodeModels(ctx *CommandContext, flags *cmd.CodeHarnessFlags) error {
	s, i, err := codeState(flags.CodeFlags)
	if err != nil {
		return err
	}
	cl, err := codeClient(ctx, s, i)
	if err != nil {
		return err
	}
	catalog, err := cl.Models(ctx)
	if err != nil {
		return err
	}
	if flags.Harness != "" {
		catalog.Compatibility = flags.Harness + ": protocol and capability compatibility is unverified; all server-returned IDs are shown"
	}
	if ctx.JSON {
		ctx.OutputJSON(catalog)
		return nil
	}
	rows := [][]string{}
	for _, m := range catalog.Data {
		rows = append(rows, []string{m.ID, m.Name})
	}
	ctx.OutputTable(TableOutput{Headers: []string{"ROUTE ID", "NAME"}, Rows: rows})
	ctx.Outputf("Fetched at %s\n", catalog.FetchedAt.Format(time.RFC3339))
	if catalog.Compatibility != "" {
		ctx.OutputLine(catalog.Compatibility)
	}
	return nil
}
func commandCodeInit(ctx *CommandContext, flags *cmd.CodeInitFlags) error {
	s, i, err := codeState(flags.CodeFlags)
	if err != nil {
		return err
	}
	names, err := code.Normalize(flags.Harness)
	if err != nil {
		return cmd.NewErrUsage(err)
	}
	if len(names) == 0 {
		for _, name := range []string{"codex", "claude-code", "opencode"} {
			h, e := code.Describe(ctx, name)
			if e != nil {
				return e
			}
			if h.Installed {
				names = append(names, name)
			}
		}
		if !flags.DryRun && (!ctx.IsInteractive() || flags.NoInteractive) {
			return cmd.NewErrUsagef("--harness is required with --no-interactive or when stdin is not a terminal")
		}
		if !flags.DryRun && len(names) > 0 {
			var selected []string
			options := []huh.Option[string]{}
			for _, name := range names {
				options = append(options, huh.NewOption(name, name))
			}
			if err := huh.NewMultiSelect[string]().Title("Configure which installed harnesses?").Options(options...).Value(&selected).Run(); err != nil {
				return err
			}
			names = selected
		}
	}
	if len(names) == 0 {
		return cmd.NewErrUsagef("no harness selected or detected; install a harness and pass --harness <name>")
	}
	r := reportFor(i, "preview")
	r.Org = flags.Org
	if i != nil {
		r.Org = i.Org
	}
	model := flags.Model
	if model == "" && i != nil {
		model = i.Model
	}
	r.Model = model
	for _, name := range names {
		h, e := code.Describe(ctx, name)
		if e != nil {
			return e
		}
		r.Harnesses = append(r.Harnesses, h)
		if h.Limitation != "" {
			r.Notes = append(r.Notes, h.Limitation)
		}
	}
	if i == nil {
		r.Notes = append(r.Notes, "Sign-in, Code credential creation, authenticated catalog retrieval and Route selection are required before configuration can be installed.", code.CredentialDependency)
		if flags.DryRun {
			helper, e := os.Executable()
			if e != nil {
				return e
			}
			previewModel := model
			if previewModel == "" {
				previewModel = "<select-route-after-sign-in>"
			}
			for _, h := range r.Harnesses {
				change, e := code.Prepare(h, previewModel, helper, s.Dir, flags.Org, nil)
				if e != nil {
					r.Checks = append(r.Checks, codeCheck{h.Name + " setup", e.Error()})
				} else {
					r.Changes = append(r.Changes, change)
				}
			}
			r.Notes = append(r.Notes, "Preview contains paths and setting names only. Changed TOML files are normalized; the private rollback journal retains the original bytes.")
			codeOutput(ctx, r)
			return nil
		}
		return cmd.NewErrAuth(errors.New(code.CredentialDependency + "; use init --dry-run to inspect setup, or baseten auth login --web to establish the human profile separately"))
	}
	if flags.Label != "" && flags.Label != i.Label {
		return cmd.NewErrUsagef("changing an installation label requires the dedicated Code credential backend")
	}
	// Serialize mutations and reload after locking, before preparing any edits.
	if !flags.DryRun {
		unlock, e := s.Lock()
		if e != nil {
			return e
		}
		defer unlock()
		s, i, err = codeState(flags.CodeFlags)
		if err != nil {
			return err
		}
		if i == nil {
			return errors.New("installation changed; retry init")
		}
		if flags.Model == "" {
			model = i.Model
		}
	}
	cl, err := codeClient(ctx, s, i)
	if err != nil {
		return err
	}
	catalog, err := cl.Models(ctx)
	if err != nil {
		return err
	}
	if model == "" {
		if !ctx.IsInteractive() || flags.NoInteractive || flags.DryRun {
			return cmd.NewErrUsagef("--model must select a Route from baseten code models")
		}
		options := []huh.Option[string]{}
		for _, m := range catalog.Data {
			options = append(options, huh.NewOption(m.ID, m.ID))
		}
		if len(options) == 0 {
			return cmd.NewErrUsagef("no Routes are available")
		}
		if err := huh.NewSelect[string]().Title("Initial Route").Options(options...).Value(&model).Run(); err != nil {
			return err
		}
	}
	if !catalog.Has(model) {
		return cmd.NewErrUsagef("selected Route is absent from the authenticated catalog; choose --model from baseten code models")
	}
	helper, err := os.Executable()
	if err != nil {
		return err
	}
	for _, h := range r.Harnesses {
		change, e := code.Prepare(h, model, helper, s.Dir, i.Org, i.Configs[h.Name])
		if e != nil {
			return e
		}
		r.Changes = append(r.Changes, change)
	}
	r.Model = model
	if flags.DryRun {
		codeOutput(ctx, r)
		return nil
	}
	ctx.LogLine("Validating streaming and tool replay before configuration; these inference requests incur usage.")
	for _, h := range r.Harnesses {
		if err := cl.Probe(ctx, h.Name, model); err != nil {
			return fmt.Errorf("%s validation: %w", h.Name, err)
		}
	}
	i.Model = model
	if err := s.Apply(i, r.Changes); err != nil {
		return err
	}
	r.Status = "configured"
	codeOutput(ctx, r)
	return nil
}
func commandCodeSync(ctx *CommandContext, flags *cmd.CodeSyncFlags) error {
	s, i, err := codeState(flags.CodeFlags)
	if err != nil {
		return err
	}
	names, err := codeTargets(ctx, i)
	if err != nil {
		return err
	}
	cl, err := codeClient(ctx, s, i)
	if err != nil {
		return err
	}
	catalog, err := cl.Models(ctx)
	if err != nil {
		return err
	}
	if !catalog.Has(i.Model) {
		return cmd.NewErrUsagef("configured Route is no longer in the catalog; run code init with --model to select an available Route")
	}
	r := reportFor(i, "preview")
	helper, err := os.Executable()
	if err != nil {
		return err
	}
	for _, name := range names {
		if i.Configs[name] == nil {
			return cmd.NewErrUsagef("%s is not configured", name)
		}
		h, e := code.Describe(ctx, name)
		if e != nil {
			return e
		}
		change, e := code.Prepare(h, i.Model, helper, s.Dir, i.Org, i.Configs[name])
		if e != nil {
			return e
		}
		r.Changes = append(r.Changes, change)
	}
	r.Notes = append(r.Notes, "Catalog retrieved and selected Route validated; generated picker catalogs are not implemented.")
	if !flags.DryRun {
		return errors.New("catalog retrieved, but picker catalog synchronization is not implemented for the configured harnesses; no configuration changed")
	}
	codeOutput(ctx, r)
	return nil
}
func commandCodeDoctor(ctx *CommandContext, flags *cmd.CodeDoctorFlags) error {
	if flags.Model != "" && !flags.Test {
		return cmd.NewErrUsagef("--model requires --test")
	}
	s, i, err := codeState(flags.CodeFlags)
	if err != nil {
		return err
	}
	names, err := codeTargets(ctx, i)
	if err != nil {
		return err
	}
	r := reportFor(i, "diagnostics")
	failed := false
	if i == nil {
		r.Checks = append(r.Checks, codeCheck{"credential", "not installed"})
		failed = true
	} else {
		r.Checks = append(r.Checks, codeCheck{"credential", "stored; checking catalog authorization"})
	}
	for _, name := range names {
		h, e := code.Describe(ctx, name)
		if e != nil {
			return e
		}
		r.Harnesses = append(r.Harnesses, h)
		if !h.Installed {
			r.Checks = append(r.Checks, codeCheck{name, "not installed or not on PATH"})
			failed = true
		}
		if i == nil || i.Configs[name] == nil {
			r.Checks = append(r.Checks, codeCheck{name + " configuration", "not configured"})
			failed = true
			continue
		}
		helper, e := os.Executable()
		if e != nil {
			return e
		}
		_, e = code.Prepare(h, i.Model, helper, s.Dir, i.Org, i.Configs[name])
		if e != nil {
			r.Checks = append(r.Checks, codeCheck{name + " configuration", e.Error()})
			failed = true
		} else {
			r.Checks = append(r.Checks, codeCheck{name + " configuration", "managed settings match"})
		}
	}
	cl, e := codeClient(ctx, s, i)
	if e != nil {
		r.Checks = append(r.Checks, codeCheck{"catalog", e.Error()})
		failed = true
	} else {
		catalog, e := cl.Models(ctx)
		if e != nil {
			r.Checks = append(r.Checks, codeCheck{"catalog", e.Error()})
			failed = true
		} else {
			r.Checks = append(r.Checks, codeCheck{"catalog", "retrieved at " + catalog.FetchedAt.Format(time.RFC3339)})
			model := flags.Model
			if model == "" && i != nil {
				model = i.Model
			}
			if !catalog.Has(model) {
				r.Checks = append(r.Checks, codeCheck{"Route", "not in current catalog"})
				failed = true
			} else if flags.Test {
				ctx.LogLine("Running streaming and tool replay probes; these inference requests incur usage.")
				for _, name := range names {
					if e := cl.Probe(ctx, name, model); e != nil {
						r.Checks = append(r.Checks, codeCheck{name + " protocol", e.Error()})
						failed = true
					} else {
						r.Checks = append(r.Checks, codeCheck{name + " protocol", "streaming and tool-result replay passed"})
					}
				}
			}
		}
	}
	r.Notes = append(r.Notes, "Managed policies and project/launch overrides can change effective harness behavior. Protocol probes do not certify the complete harness.")
	if failed {
		r.Status = "failed"
	} else {
		r.Status = "passed"
	}
	codeOutput(ctx, r)
	if failed {
		ctx.SuppressJSONError()
		return errors.New("Code diagnostics failed; review the reported checks")
	}
	return nil
}
func codeConfirm(ctx *CommandContext, flags cmd.CodeConfirmFlags, title string) error {
	if flags.Yes {
		return nil
	}
	if flags.NoInteractive || !ctx.IsInteractive() {
		return cmd.NewErrUsagef("pass --yes to confirm the explicitly selected scope")
	}
	var yes bool
	if err := huh.NewConfirm().Title(title).Value(&yes).Run(); err != nil {
		return err
	}
	if !yes {
		return errors.New("cancelled")
	}
	return nil
}
func commandCodeTeardown(ctx *CommandContext, flags *cmd.CodeTeardownFlags) error {
	if flags.All && len(ctx.Args) > 0 {
		return cmd.NewErrUsagef("a harness and --all are mutually exclusive")
	}
	s, i, err := codeState(flags.CodeFlags)
	if err != nil {
		return err
	}
	if !flags.All && len(ctx.Args) == 0 {
		if flags.NoInteractive || !ctx.IsInteractive() {
			return cmd.NewErrUsagef("select a harness or --all; --yes does not imply --all")
		}
		names := configured(i)
		if len(names) == 0 {
			return cmd.NewErrUsagef("no harnesses are configured")
		}
		options := []huh.Option[string]{}
		for _, name := range names {
			options = append(options, huh.NewOption(name, name))
		}
		var name string
		if err := huh.NewSelect[string]().Title("Remove which harness setup?").Options(options...).Value(&name).Run(); err != nil {
			return err
		}
		ctx.Args = []string{name}
	}
	if !flags.DryRun {
		unlock, e := s.Lock()
		if e != nil {
			return e
		}
		defer unlock()
		s, i, err = codeState(flags.CodeFlags)
		if err != nil {
			return err
		}
	}
	names, err := codeTargets(ctx, i)
	if err != nil {
		return err
	}
	r := reportFor(i, "preview")
	conflicts := false
	for _, name := range names {
		if i == nil || i.Configs[name] == nil {
			return cmd.NewErrUsagef("%s is not configured", name)
		}
		change, kept, e := code.Restore(i.Configs[name])
		if e != nil {
			return e
		}
		r.Changes = append(r.Changes, change)
		if len(kept) > 0 {
			conflicts = true
			r.Notes = append(r.Notes, name+": preserving edited settings: "+strings.Join(kept, ", "))
		}
	}
	if !flags.DryRun {
		if err := codeConfirm(ctx, flags.CodeConfirmFlags, "Remove Baseten setup for "+strings.Join(names, ", ")+"?"); err != nil {
			return err
		}
		if err := s.Teardown(i, r.Changes); err != nil {
			return err
		}
		r.Status = "restored"
	}
	r.Notes = append(r.Notes, "Code credential retained; logout is a separate server revocation operation.")
	codeOutput(ctx, r)
	if conflicts && !flags.DryRun {
		ctx.SuppressJSONError()
		return errors.New("user-edited settings were preserved; their backup records remain for review")
	}
	return nil
}
func commandCodeSpend(ctx *CommandContext, flags *cmd.CodeSpendFlags) error {
	if (flags.From == "") != (flags.To == "") {
		return cmd.NewErrUsagef("--from and --to must be supplied together")
	}
	if flags.From != "" {
		from, e := time.Parse("2006-01-02", flags.From)
		if e != nil {
			return cmd.NewErrUsagef("--from must be YYYY-MM-DD")
		}
		to, e := time.Parse("2006-01-02", flags.To)
		if e != nil {
			return cmd.NewErrUsagef("--to must be YYYY-MM-DD")
		}
		if !from.Before(to) {
			return cmd.NewErrUsagef("--from must precede exclusive --to")
		}
	}
	if _, _, err := codeState(flags.CodeFlags); err != nil {
		return err
	}
	return errors.New("Code spend is unavailable: the Cost API has daily user/model/API-key-prefix filters, but Code-key inventory and creator attribution are not integrated; cannot safely report current-user Code spend or Route grouping. Use baseten org billing for its documented organization billing scope")
}
func commandCodeLogout(ctx *CommandContext, flags *cmd.CodeConfirmFlags) error {
	if _, _, err := codeState(flags.CodeFlags); err != nil {
		return err
	}
	return errors.New(code.CredentialDependency + "; logout cannot revoke this installation, so local credentials and configuration were retained")
}
func commandCodeKeysList(ctx *CommandContext, flags *cmd.CodeKeysListFlags) error {
	if _, _, err := codeState(flags.CodeFlags); err != nil {
		return err
	}
	return errors.New(code.CredentialDependency)
}
func commandCodeKeysRevoke(ctx *CommandContext, flags *cmd.CodeKeysRevokeFlags) error {
	if (len(ctx.Args) > 0) == flags.All {
		return cmd.NewErrUsagef("select exactly one key ID or --all; --yes does not imply --all")
	}
	if _, _, err := codeState(flags.CodeFlags); err != nil {
		return err
	}
	return errors.New(code.CredentialDependency + "; no key was revoked")
}
func commandCodeToken(ctx *CommandContext, flags *cmd.CodeTokenFlags) error {
	ctx.SuppressJSONError()
	if flags.Output != "text" || flags.JQ != "" {
		return cmd.NewErrUsagef("auth token emits only the credential; output formatting is not supported")
	}
	s, err := code.NewStore()
	if err != nil {
		return err
	}
	if flags.ConfigDir != "" {
		if !filepath.IsAbs(flags.ConfigDir) {
			return cmd.NewErrUsagef("--config-dir must be absolute")
		}
		s = &code.Store{Dir: flags.ConfigDir}
	}
	i, err := s.Load()
	if err != nil {
		return err
	}
	if i != nil && ((flags.Org != "" && flags.Org != i.Org) || (flags.Profile != "" && flags.Profile != i.Profile)) {
		return cmd.NewErrAuth(errors.New("credential helper context does not match the installed Code credential"))
	}
	token, err := s.Token(i)
	if err != nil {
		return cmd.NewErrAuth(err)
	}
	ctx.OutputLine(token)
	return nil
}
