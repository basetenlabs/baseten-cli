package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/charmbracelet/huh"
)

// RoutePromptOption associates a human-readable label with a stable choice.
type RoutePromptOption struct{ Label, Value string }

// RoutePrompter is the terminal UI for Route commands. Tests inject scripted
// answers to exercise command execution without depending on terminal timing.
type RoutePrompter interface {
	Input(title string, value *string) error
	OptionalInput(title string, value *string, maxLength int) error
	Password(title string, value *string) error
	Select(title string, options []RoutePromptOption, value *string) error
	Confirm(title string) error
}

type routePrompterKey struct{}

// WithRoutePrompter enables Route prompts using p, intended for scripted tests.
// Ordinary invocations only prompt when stdin is a terminal.
func WithRoutePrompter(ctx context.Context, p RoutePrompter) context.Context {
	return context.WithValue(ctx, routePrompterKey{}, p)
}

func routeInteractive(ctx *CommandContext) bool {
	return ctx.Value(routePrompterKey{}) != nil || ctx.IsInteractive()
}

func routePrompt(ctx *CommandContext) RoutePrompter {
	if p, ok := ctx.Value(routePrompterKey{}).(RoutePrompter); ok {
		return p
	}
	return terminalRoutePrompter{ctx}
}

type terminalRoutePrompter struct{ ctx *CommandContext }

func (p terminalRoutePrompter) run(field huh.Field) error {
	return huh.NewForm(huh.NewGroup(field)).WithInput(p.ctx.Stdin).WithOutput(p.ctx.Stderr).RunWithContext(p.ctx)
}

func (p terminalRoutePrompter) Input(title string, value *string) error {
	return p.run(huh.NewInput().Title(title).Value(value).Validate(func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("a value is required")
		}
		return nil
	}))
}

func (p terminalRoutePrompter) Password(title string, value *string) error {
	return p.run(huh.NewInput().Title(title).EchoMode(huh.EchoModePassword).Value(value).Validate(func(s string) error {
		if s == "" {
			return fmt.Errorf("a value is required")
		}
		return nil
	}))
}

func (p terminalRoutePrompter) OptionalInput(title string, value *string, maxLength int) error {
	return p.run(huh.NewInput().Title(title).Value(value).Validate(func(s string) error {
		if utf8.RuneCountInString(s) > maxLength {
			return fmt.Errorf("use at most %d characters", maxLength)
		}
		return nil
	}))
}

func (p terminalRoutePrompter) Select(title string, options []RoutePromptOption, value *string) error {
	choices := make([]huh.Option[string], 0, len(options))
	for _, option := range options {
		choices = append(choices, huh.NewOption(option.Label, option.Value))
	}
	return p.run(huh.NewSelect[string]().Title(title).Options(choices...).Value(value))
}

func (p terminalRoutePrompter) Confirm(title string) error {
	var yes bool
	if err := p.run(huh.NewConfirm().Title(title).Value(&yes)); err != nil {
		return err
	}
	if !yes {
		return fmt.Errorf("aborted")
	}
	return nil
}

// Explicit empty or conflicting flags are mistakes, not missing answers.
func routeNonemptyFlags(ctx *CommandContext, names ...string) error {
	for _, name := range names {
		f := ctx.Command.Flags().Lookup(name)
		if f != nil && f.Changed && f.Value.String() == "" {
			return cmd.NewErrUsagef("--%s must not be empty", name)
		}
	}
	return nil
}

func checkRouteRef(ctx *CommandContext, ref cmd.RouteRefFlags) error {
	if err := routeNonemptyFlags(ctx, "id", "name"); err != nil {
		return err
	}
	if ref.ID != "" && ref.Name != "" {
		return cmd.NewErrUsagef("pass exactly one of --id or --name")
	}
	return nil
}

func completeRouteRef(ctx *CommandContext, ref *cmd.RouteRefFlags) error {
	if err := checkRouteRef(ctx, *ref); err != nil {
		return err
	}
	if ref.ID != "" || ref.Name != "" || !routeInteractive(ctx) {
		return validateRouteRef(*ref)
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	page, err := fetchRoutePages(ctx, cl.API(), url.Values{"limit": {"100"}}, true)
	if err != nil {
		return err
	}
	if len(page.Items) == 0 {
		return cmd.NewErrNotFound(fmt.Errorf("no visible Routes to choose from; create one first or pass --id if you have management access"))
	}
	options := make([]RoutePromptOption, 0, len(page.Items))
	for _, route := range page.Items {
		options = append(options, RoutePromptOption{Label: fmt.Sprintf("%s (%s, team %s)", route.Name, route.ID, route.TeamID), Value: route.ID})
	}
	return routePrompt(ctx).Select("Choose a Route", options, &ref.ID)
}

func completeRouteCreate(ctx *CommandContext, flags *cmd.RouteCreateFlags) error {
	if err := routeNonemptyFlags(ctx, "name", "team"); err != nil {
		return err
	}
	if err := validateRouteDescription(flags.Description); err != nil {
		return err
	}
	if err := validateRouteDisplayName(flags.DisplayName); err != nil {
		return err
	}
	if err := checkRouteTargetInputs(ctx, flags.RouteTargetFlags); err != nil {
		return err
	}
	if !routeInteractive(ctx) {
		return nil
	}
	if flags.Team == "" {
		cl, err := ctx.NewManagementClient()
		if err != nil {
			return err
		}
		teams, err := cl.API().GetTeams(ctx, managementapi.GetV1TeamsParams{})
		if err != nil {
			return fmt.Errorf("list teams: %w", err)
		}
		if len(teams.Teams) == 0 {
			return cmd.NewErrNotFound(fmt.Errorf("no teams available to choose from"))
		}
		options := make([]RoutePromptOption, 0, len(teams.Teams))
		for _, team := range teams.Teams {
			options = append(options, RoutePromptOption{Label: fmt.Sprintf("%s (%s)", team.Name, team.Id), Value: team.Id})
		}
		if err := routePrompt(ctx).Select("Choose the owning team", options, &flags.Team); err != nil {
			return err
		}
	}
	if flags.Name == "" {
		prefixes, err := routeSlugPrefixes(ctx, flags.Team)
		if err != nil {
			ctx.VerboseLogf("Could not look up Route prefixes: %v", err)
		}
		var prefix string
		if len(prefixes) == 1 {
			prefix = prefixes[0]
		} else if len(prefixes) > 1 {
			options := make([]RoutePromptOption, 0, len(prefixes))
			for _, p := range prefixes {
				options = append(options, RoutePromptOption{Label: p, Value: p})
			}
			if err := routePrompt(ctx).Select("Choose an organization prefix", options, &prefix); err != nil {
				return err
			}
		}
		if prefix != "" {
			flags.Name = strings.TrimSuffix(prefix, "/") + "/"
		}
		if err := routePrompt(ctx).Input("Route slug (organization-prefix/slug)", &flags.Name); err != nil {
			return err
		}
		if strings.HasSuffix(flags.Name, "/") {
			return cmd.NewErrUsagef("enter a Route slug after the organization prefix")
		}
	}
	if !flags.DisplayName.IsSet() {
		var label string
		if err := routePrompt(ctx).OptionalInput("Display name (optional, Enter to use the Route slug)", &label, 255); err != nil {
			return err
		}
		if label != "" {
			flags.DisplayName.SetValue(label)
		}
	}
	if !flags.Description.IsSet() {
		var description string
		if err := routePrompt(ctx).OptionalInput("Description (optional, Enter to skip)", &description, 1000); err != nil {
			return err
		}
		if description != "" {
			flags.Description.SetValue(description)
		}
	}
	if err := validateRouteDisplayName(flags.DisplayName); err != nil {
		return err
	}
	if err := validateRouteDescription(flags.Description); err != nil {
		return err
	}
	return completeRouteTarget(ctx, &flags.RouteTargetFlags, true, func() (string, error) {
		cl, err := ctx.NewManagementClient()
		if err != nil {
			return "", err
		}
		return ResolveTeam(ctx, cl.API(), flags.Team)
	})
}

// Prefix discovery currently exists only in the app's GraphQL API. Failure is
// nonfatal because users can still enter the full Route slug themselves.
func routeSlugPrefixes(ctx *CommandContext, team string) ([]string, error) {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return nil, err
	}
	teamID, err := ResolveTeam(ctx, cl.API(), team)
	if err != nil {
		return nil, err
	}
	transport, remote, err := ctx.AuthTransport()
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
		"query":     "query RouteSlugPrefixes($teamId: ID!) { route_slug_prefixes(team_id: $teamId) }",
		"variables": map[string]string{"teamId": teamID},
	})
	if err != nil {
		return nil, err
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(lookupCtx, http.MethodPost, remote.RemoteURL()+"/graphql/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := transport.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prefix lookup returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Data struct {
			Prefixes []string `json:"route_slug_prefixes"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("prefix lookup returned GraphQL errors")
	}
	return result.Data.Prefixes, nil
}

func completeRouteUpdate(ctx *CommandContext, flags *cmd.RouteUpdateFlags) error {
	if err := checkRouteRef(ctx, flags.RouteRefFlags); err != nil {
		return err
	}
	if err := validateRouteDescription(flags.Description); err != nil {
		return err
	}
	if err := validateRouteDisplayName(flags.DisplayName); err != nil {
		return err
	}
	if err := checkRouteTargetInputs(ctx, flags.RouteTargetFlags); err != nil {
		return err
	}
	if !routeInteractive(ctx) {
		return nil
	}
	if err := completeRouteRef(ctx, &flags.RouteRefFlags); err != nil {
		return err
	}
	requireTarget := false
	if !flags.DisplayName.IsSet() && !flags.Description.IsSet() && !routeTargetSupplied(flags.RouteTargetFlags) {
		var change string
		if err := routePrompt(ctx).Select("What would you like to update?", []RoutePromptOption{
			{Label: "Display name", Value: "label"}, {Label: "Description", Value: "description"}, {Label: "Clear description", Value: "clear-description"}, {Label: "Replace target", Value: "target"}, {Label: "Display name and target", Value: "both"},
		}, &change); err != nil {
			return err
		}
		if change == "label" || change == "both" {
			var label string
			if err := routePrompt(ctx).Input("New display name", &label); err != nil {
				return err
			}
			flags.DisplayName.SetValue(label)
		}
		if change == "description" {
			var description string
			if err := routePrompt(ctx).Input("New Route description", &description); err != nil {
				return err
			}
			flags.Description.SetValue(description)
		}
		if change == "clear-description" {
			flags.Description.SetValue("")
		}
		requireTarget = change == "target" || change == "both"
	}
	if err := validateRouteDescription(flags.Description); err != nil {
		return err
	}
	return completeRouteTarget(ctx, &flags.RouteTargetFlags, requireTarget, func() (string, error) {
		cl, err := ctx.NewManagementClient()
		if err != nil {
			return "", err
		}
		id, err := resolveRouteID(ctx, cl.API(), flags.RouteRefFlags)
		if err != nil {
			return "", err
		}
		var route cmd.Route
		if err := routeRequest(ctx, cl.API(), http.MethodGet, id, nil, nil, &route); err != nil {
			return "", err
		}
		return route.TeamID, nil
	})
}

func routeTargetSupplied(f cmd.RouteTargetFlags) bool {
	return f.TargetModelAPI != "" || f.TargetProvider != "" || f.TargetProviderModel != "" || f.TargetProviderSecret != "" || f.TargetProviderBaseURL != "" || f.TargetProviderVertexProject != "" || f.TargetProviderVertexLocation != ""
}

func checkRouteTargetInputs(ctx *CommandContext, f cmd.RouteTargetFlags) error {
	if err := routeNonemptyFlags(ctx, "target-model-api", "target-provider", "target-provider-model", "target-provider-secret", "target-provider-base-url", "target-provider-vertex-project", "target-provider-vertex-location"); err != nil {
		return err
	}
	providerFields := f
	providerFields.TargetModelAPI = ""
	if f.TargetModelAPI != "" && routeTargetSupplied(providerFields) {
		return cmd.NewErrUsagef("--target-model-api is mutually exclusive with provider target flags")
	}
	if f.TargetProvider != "" && f.TargetProvider != "openai-compatible" && f.TargetProviderBaseURL != "" {
		return cmd.NewErrUsagef("--target-provider-base-url is only valid with openai-compatible")
	}
	vertex := f.TargetProviderVertexProject != "" || f.TargetProviderVertexLocation != ""
	if vertex && ((f.TargetProvider != "" && f.TargetProvider != "vertex") || f.TargetProviderBaseURL != "") {
		return cmd.NewErrUsagef("Vertex project and location flags are only valid with vertex")
	}
	return nil
}

func completeRouteTarget(ctx *CommandContext, flags *cmd.RouteTargetFlags, required bool, resolveTeam func() (string, error)) error {
	if !routeInteractive(ctx) || (!required && !routeTargetSupplied(*flags)) {
		return nil
	}
	p := routePrompt(ctx)
	if flags.TargetModelAPI == "" && flags.TargetProvider == "" {
		// Provider companions already identify an external target. Do not offer
		// a Model API choice that would conflict with supplied flags.
		kind := "provider"
		if !routeTargetSupplied(*flags) {
			if err := p.Select("Target type", []RoutePromptOption{{Label: "Model API", Value: "model-api"}, {Label: "External provider", Value: "provider"}}, &kind); err != nil {
				return err
			}
		}
		if kind == "model-api" {
			return promptRouteModelAPI(ctx, &flags.TargetModelAPI)
		}
		providers := []RoutePromptOption{}
		for _, provider := range []string{"anthropic", "openai", "xai", "vertex", "openai-compatible"} {
			if flags.TargetProviderBaseURL != "" && provider != "openai-compatible" {
				continue
			}
			if (flags.TargetProviderVertexProject != "" || flags.TargetProviderVertexLocation != "") && provider != "vertex" {
				continue
			}
			providers = append(providers, RoutePromptOption{Label: provider, Value: provider})
		}
		if err := p.Select("External provider", providers, &flags.TargetProvider); err != nil {
			return err
		}
	}
	if flags.TargetModelAPI != "" {
		return nil
	}
	type targetInput struct {
		title string
		value *string
	}
	inputs := []targetInput{
		{"Provider model name", &flags.TargetProviderModel},
	}
	if flags.TargetProvider == "openai-compatible" {
		inputs = append(inputs, targetInput{"Provider HTTPS base URL", &flags.TargetProviderBaseURL})
	}
	if flags.TargetProvider == "vertex" {
		inputs = append(inputs,
			targetInput{"Google Cloud project ID or number", &flags.TargetProviderVertexProject},
			targetInput{"Google Cloud location (for example, global)", &flags.TargetProviderVertexLocation})
	}
	for _, input := range inputs {
		if *input.value == "" {
			if err := p.Input(input.title, input.value); err != nil {
				return err
			}
		}
	}
	if flags.TargetProviderSecret == "" {
		teamID, err := resolveTeam()
		if err != nil {
			return err
		}
		return promptRouteSecret(ctx, teamID, flags.TargetProvider, &flags.TargetProviderSecret)
	}
	return nil
}

func promptRouteModelAPI(ctx *CommandContext, value *string) error {
	items, err := fetchModelAPIs(ctx, false)
	if err != nil {
		ctx.VerboseLogf("Could not list Model APIs: %v", err)
		return routePrompt(ctx).Input("Target Model API name", value)
	}
	if len(items) == 0 {
		return routePrompt(ctx).Input("Target Model API name", value)
	}
	const manual = "__manual__"
	options := make([]RoutePromptOption, 0, len(items)+1)
	for _, item := range items {
		options = append(options, RoutePromptOption{Label: item.Name, Value: item.Name})
	}
	options = append(options, RoutePromptOption{Label: "Enter a Model API name manually", Value: manual})
	var selected string
	if err := routePrompt(ctx).Select("Choose a Model API (/ to search)", options, &selected); err != nil {
		return err
	}
	if selected == manual {
		return routePrompt(ctx).Input("Target Model API name", value)
	}
	*value = selected
	return nil
}

func promptRouteSecret(ctx *CommandContext, teamID, provider string, value *string) error {
	if teamID == "" {
		return fmt.Errorf("cannot select a secret without the Route's owning team")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	secrets, err := cl.API().GetTeamsSecrets(ctx, teamID)
	if err != nil {
		ctx.VerboseLogf("Could not list team secrets: %v", err)
		return routePrompt(ctx).Input("Existing credential secret name in the Route's team", value)
	}
	const create = "__create_secret__"
	options := make([]RoutePromptOption, 0, len(secrets.Secrets)+1)
	for _, secret := range secrets.Secrets {
		options = append(options, RoutePromptOption{Label: secret.Name, Value: secret.Name})
	}
	options = append(options, RoutePromptOption{Label: "Create new secret", Value: create})
	var selected string
	if err := routePrompt(ctx).Select("Choose a credential secret (/ to search)", options, &selected); err != nil {
		return err
	}
	if selected != create {
		*value = selected
		return nil
	}
	name := provider + "-api-key"
	if err := routePrompt(ctx).Input("New secret name", &name); err != nil {
		return err
	}
	// The endpoint upserts, so reject known collisions before asking for a value.
	for _, secret := range secrets.Secrets {
		if secret.Name == name {
			return cmd.NewErrUsagef("secret %q already exists in this team; select it or choose a different name", name)
		}
	}
	var secretValue string
	if err := routePrompt(ctx).Password("Secret value", &secretValue); err != nil {
		return err
	}
	if secretValue == "" {
		return cmd.NewErrUsagef("secret value must not be empty")
	}
	if _, err := cl.API().PostTeamsSecrets(ctx, teamID, managementapi.UpsertSecretRequest{Name: name, Value: secretValue}); err != nil {
		return fmt.Errorf("creating team secret: %w", err)
	}
	ctx.Logf("Created secret %s in team %s\n", name, teamID)
	*value = name
	return nil
}
