package cmd

import (
	"fmt"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("route list", commandRouteList)
	Register("route describe", commandRouteDescribe)
	Register("route create", commandRouteCreate)
	Register("route update", commandRouteUpdate)
	Register("route delete", commandRouteDelete)
}

func commandRouteList(ctx *CommandContext, flags *cmd.RouteListFlags) error {
	if err := routeNonemptyFlags(ctx, "team", "name"); err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	team, err := ResolveTeam(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}
	params := managementapi.GetV1RoutesParams{}
	if team != "" {
		params.TeamId = &team
	}
	if flags.Name != "" {
		params.Name = &flags.Name
	}
	result, err := fetchRoutePages(ctx, cl.API(), params)
	if err != nil {
		return err
	}
	if ctx.JSON {
		ctx.OutputJSON(result)
		return nil
	}
	if len(result.Items) == 0 {
		ctx.LogLine("No Routes found.")
		return nil
	}
	rows := make([][]string, 0, len(result.Items))
	for _, r := range result.Items {
		value, err := r.Target.ValueByDiscriminator()
		if err != nil {
			// Match autoscaling-schedule list: a newer target must not hide
			// the Routes this CLI can still render. JSON retains every item.
			ctx.Logf("Skipped Route %s with a target this CLI cannot read: %v\n", r.Name, err)
			continue
		}
		kind, model, _, err := routeTargetFields(value)
		if err != nil {
			return err
		}
		target := model
		if kind != "BASETEN_MODEL_API" {
			target = kind + ":" + model
		}
		rows = append(rows, []string{r.Id, r.Name, r.DisplayName, r.TeamId, target})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "DISPLAY NAME", "TEAM ID", "TARGET"},
		Rows:    rows,
	})
	return nil
}

func fetchRoutePages(ctx *CommandContext, api *managementapi.Client, params managementapi.GetV1RoutesParams) (managementapi.RoutesResponse, error) {
	result := managementapi.RoutesResponse{Items: []managementapi.Route{}}
	limit := 100
	params.Limit = &limit
	seen := map[string]bool{"": true}
	for {
		page, err := api.GetRoutes(ctx, params)
		if err != nil {
			return result, fmt.Errorf("list Routes: %w", err)
		}
		result.Items = append(result.Items, page.Items...)
		result.Pagination = page.Pagination
		if !page.Pagination.HasMore {
			return result, nil
		}
		cursor := page.Pagination.Cursor
		if cursor == nil || *cursor == "" || seen[*cursor] {
			return result, fmt.Errorf("list Routes: invalid or repeated continuation cursor")
		}
		seen[*cursor] = true
		params.Cursor = cursor
	}
}

func validateRouteRef(ref cmd.RouteRefFlags) error {
	if (ref.ID == "") == (ref.Name == "") {
		return cmd.NewErrUsagef("pass exactly one nonempty --id or --name")
	}
	return nil
}

// Resolve a name once, then mutate by stable ID. Never retry a mutation by name:
// deletion and recreation of the name must not redirect it to a different Route.
func resolveRouteID(ctx *CommandContext, api *managementapi.Client, ref cmd.RouteRefFlags) (string, error) {
	if ref.ID != "" {
		return ref.ID, nil
	}
	limit := 2
	page, err := api.GetRoutes(ctx, managementapi.GetV1RoutesParams{Name: &ref.Name, Limit: &limit})
	if err != nil {
		return "", fmt.Errorf("resolve Route %q: %w", ref.Name, err)
	}
	if len(page.Items) == 0 {
		return "", cmd.NewErrNotFound(fmt.Errorf("no visible Route named %q", ref.Name))
	}
	if len(page.Items) != 1 || page.Pagination.HasMore {
		return "", fmt.Errorf("multiple Routes named %q; pass --id instead", ref.Name)
	}
	if page.Items[0].Name != ref.Name || page.Items[0].Id == "" {
		return "", fmt.Errorf("Route lookup returned an invalid exact-name match for %q", ref.Name)
	}
	return page.Items[0].Id, nil
}

func commandRouteDescribe(ctx *CommandContext, flags *cmd.RouteDescribeFlags) error {
	if err := validateRouteRefFlags(ctx, flags.RouteRefFlags); err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveRouteID(ctx, cl.API(), flags.RouteRefFlags)
	if err != nil {
		return err
	}
	route, err := cl.API().GetRoutesRouteId(ctx, id)
	if err != nil {
		return fmt.Errorf("describe Route %s: %w", id, err)
	}
	teamName := ""
	if !ctx.JSON && flags.Output != "none" && route.TeamId != "" {
		team, err := cl.API().GetTeamsTeamId(ctx, route.TeamId)
		if err != nil {
			ctx.VerboseLogf("Could not resolve team name for %s: %v\n", route.TeamId, err)
		} else {
			teamName = team.Name
		}
	}
	return outputRoute(ctx, *route, teamName)
}

func routeTarget(ctx *CommandContext, flags cmd.RouteTargetFlags, required bool) (*managementapi.CreateRouteRequest_Target, error) {
	values := map[string]string{
		"target-model-api":                flags.TargetModelAPI,
		"target-provider":                 flags.TargetProvider,
		"target-provider-model":           flags.TargetProviderModel,
		"target-provider-secret":          flags.TargetProviderSecret,
		"target-provider-base-url":        flags.TargetProviderBaseURL,
		"target-provider-vertex-project":  flags.TargetProviderVertexProject,
		"target-provider-vertex-location": flags.TargetProviderVertexLocation,
	}
	anySet := false
	for name, value := range values {
		if value != "" {
			anySet = true
		}
		if ctx.Command.Flags().Changed(name) {
			anySet = true
			if value == "" {
				return nil, cmd.NewErrUsagef("--%s must not be empty", name)
			}
		}
	}
	if !anySet && !required {
		return nil, nil
	}
	if (flags.TargetModelAPI == "") == (flags.TargetProvider == "") {
		return nil, cmd.NewErrUsagef("pass exactly one target: --target-model-api or --target-provider")
	}
	if flags.TargetModelAPI != "" {
		for name, value := range values {
			if name != "target-model-api" && value != "" {
				return nil, cmd.NewErrUsagef("--target-model-api is mutually exclusive with --%s", name)
			}
		}
		target := &managementapi.CreateRouteRequest_Target{}
		err := target.FromRouteTargetBasetenModelAPI(managementapi.RouteTargetBasetenModelAPI{
			Type:     "BASETEN_MODEL_API",
			ModelApi: flags.TargetModelAPI,
		})
		return target, err
	}
	if flags.TargetProviderModel == "" || flags.TargetProviderSecret == "" {
		return nil, cmd.NewErrUsagef("--target-provider requires --target-provider-model and --target-provider-secret; restate the complete target")
	}
	target := &managementapi.CreateRouteRequest_Target{}
	if flags.TargetProvider == "openai-compatible" {
		if flags.TargetProviderBaseURL == "" {
			return nil, cmd.NewErrUsagef("openai-compatible requires --target-provider-base-url")
		}
		u, err := url.Parse(flags.TargetProviderBaseURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" {
			return nil, cmd.NewErrUsagef("--target-provider-base-url must be an HTTPS URL with a host")
		}
		if u.User != nil || u.Port() != "" {
			return nil, cmd.NewErrUsagef("--target-provider-base-url must not include credentials or a port")
		}
	} else if flags.TargetProviderBaseURL != "" {
		return nil, cmd.NewErrUsagef("--target-provider-base-url is only valid with openai-compatible")
	}
	if flags.TargetProvider == "vertex" {
		if flags.TargetProviderVertexProject == "" || flags.TargetProviderVertexLocation == "" {
			return nil, cmd.NewErrUsagef("vertex requires --target-provider-vertex-project and --target-provider-vertex-location")
		}
	} else if flags.TargetProviderVertexProject != "" || flags.TargetProviderVertexLocation != "" {
		return nil, cmd.NewErrUsagef("Vertex project and location flags are only valid with vertex")
	}
	var err error
	switch flags.TargetProvider {
	case "anthropic":
		err = target.FromRouteTargetAnthropic(managementapi.RouteTargetAnthropic{
			Type:       "ANTHROPIC",
			Model:      flags.TargetProviderModel,
			SecretName: flags.TargetProviderSecret,
		})
	case "openai":
		err = target.FromRouteTargetOpenAI(managementapi.RouteTargetOpenAI{
			Type:       "OPENAI",
			Model:      flags.TargetProviderModel,
			SecretName: flags.TargetProviderSecret,
		})
	case "xai":
		err = target.FromRouteTargetXAI(managementapi.RouteTargetXAI{
			Type:       "XAI",
			Model:      flags.TargetProviderModel,
			SecretName: flags.TargetProviderSecret,
		})
	case "vertex":
		err = target.FromRouteTargetVertex(managementapi.RouteTargetVertex{
			Type:       "VERTEX",
			Model:      flags.TargetProviderModel,
			SecretName: flags.TargetProviderSecret,
			VertexConfig: managementapi.VertexTargetConfig{
				ProjectId: flags.TargetProviderVertexProject,
				Location:  flags.TargetProviderVertexLocation,
			},
		})
	case "openai-compatible":
		err = target.FromRouteTargetOpenAICompatible(managementapi.RouteTargetOpenAICompatible{
			Type:       "OPENAI_COMPATIBLE",
			Model:      flags.TargetProviderModel,
			SecretName: flags.TargetProviderSecret,
			BaseUrl:    flags.TargetProviderBaseURL,
		})
	default:
		return nil, cmd.NewErrUsagef("unsupported provider %q", flags.TargetProvider)
	}
	return target, err
}

func validateRouteDisplayName(name cmd.OptionalFlag[string]) error {
	if value := name.Pointer(); value != nil && (utf8.RuneCountInString(*value) < 1 || utf8.RuneCountInString(*value) > 255) {
		return cmd.NewErrUsagef("--display-name must contain 1 to 255 characters")
	}
	return nil
}

func validateRouteDescription(description cmd.OptionalFlag[string]) error {
	if value := description.Pointer(); value != nil && utf8.RuneCountInString(*value) > 1000 {
		return cmd.NewErrUsagef("--description must contain at most 1000 characters")
	}
	return nil
}

func commandRouteCreate(ctx *CommandContext, flags *cmd.RouteCreateFlags) error {
	if err := routeNonemptyFlags(ctx, "name", "team"); err != nil {
		return err
	}
	if flags.Name == "" || flags.Team == "" {
		return cmd.NewErrUsagef("--name and --team must not be empty")
	}
	if err := validateRouteDescription(flags.Description); err != nil {
		return err
	}
	if err := validateRouteDisplayName(flags.DisplayName); err != nil {
		return err
	}
	target, err := routeTarget(ctx, flags.RouteTargetFlags, true)
	if err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	team, err := ResolveTeam(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}
	body := managementapi.CreateRouteRequest{
		Name:        flags.Name,
		TeamId:      team,
		DisplayName: flags.DisplayName.Pointer(),
		Description: flags.Description.Pointer(),
		Target:      *target,
	}
	route, err := cl.API().PostRoutes(ctx, body)
	if err != nil {
		return fmt.Errorf("create Route: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(route)
	} else {
		ctx.Logf("Created Route %s (%s)\n", route.Name, route.Id)
	}
	return nil
}

func commandRouteUpdate(ctx *CommandContext, flags *cmd.RouteUpdateFlags) error {
	if err := validateRouteRefFlags(ctx, flags.RouteRefFlags); err != nil {
		return err
	}
	if err := validateRouteDescription(flags.Description); err != nil {
		return err
	}
	if err := validateRouteDisplayName(flags.DisplayName); err != nil {
		return err
	}
	target, err := routeTarget(ctx, flags.RouteTargetFlags, false)
	if err != nil {
		return err
	}
	if target == nil && !flags.DisplayName.IsSet() && !flags.Description.IsSet() {
		return cmd.NewErrUsagef("set --display-name, --description, a complete target, or a combination")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveRouteID(ctx, cl.API(), flags.RouteRefFlags)
	if err != nil {
		return err
	}
	body := managementapi.UpdateRouteRequest{
		DisplayName: flags.DisplayName.Pointer(),
		Description: flags.Description.Pointer(),
	}
	if target != nil {
		// The generated create and update unions are distinct Go types.
		data, err := target.MarshalJSON()
		if err != nil {
			return err
		}
		body.Target = &managementapi.UpdateRouteRequest_Target{}
		if err := body.Target.UnmarshalJSON(data); err != nil {
			return err
		}
	}
	route, err := cl.API().PatchRoutes(ctx, id, body)
	if err != nil {
		return fmt.Errorf("update Route %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(route)
	} else {
		ctx.Logf("Updated Route %s (%s)\n", route.Name, route.Id)
	}
	return nil
}

func commandRouteDelete(ctx *CommandContext, flags *cmd.RouteDeleteFlags) error {
	if err := validateRouteRefFlags(ctx, flags.RouteRefFlags); err != nil {
		return err
	}
	if !flags.Yes {
		return cmd.NewErrUsagef("pass --yes to confirm deletion")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveRouteID(ctx, cl.API(), flags.RouteRefFlags)
	if err != nil {
		return err
	}
	tombstone, err := cl.API().DeleteRoutes(ctx, id)
	if err != nil {
		return fmt.Errorf("delete Route %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(tombstone)
	} else {
		ctx.Logf("Deleted Route %s (%s)\n", tombstone.Name, tombstone.Id)
	}
	return nil
}

func outputRoute(ctx *CommandContext, route managementapi.Route, teamName string) error {
	if ctx.JSON {
		ctx.OutputJSON(route)
		return nil
	}
	value, err := route.Target.ValueByDiscriminator()
	if err != nil {
		return fmt.Errorf("decode Route target: %w", err)
	}
	kind, model, secret, err := routeTargetFields(value)
	if err != nil {
		return err
	}
	ctx.Outputf("ID:               %s\n", route.Id)
	ctx.Outputf("Name:             %s\n", route.Name)
	ctx.Outputf("Display Name:     %s\n", route.DisplayName)
	ctx.Outputf("Description:      %s\n", route.Description)
	ctx.Outputf("Team ID:          %s\n", route.TeamId)
	if teamName != "" {
		ctx.Outputf("Team Name:        %s\n", teamName)
	}
	ctx.Outputf("Target Type:      %s\n", kind)
	if kind == "BASETEN_MODEL_API" {
		ctx.Outputf("Model API:        %s\n", model)
	} else {
		ctx.Outputf("Provider:         %s\n", kind)
		ctx.Outputf("Provider Model:   %s\n", model)
		ctx.Outputf("Provider Secret:  %s\n", secret)
	}
	switch t := value.(type) {
	case managementapi.RouteTargetVertex:
		ctx.Outputf("Vertex Project:   %s\n", t.VertexConfig.ProjectId)
		ctx.Outputf("Vertex Location:  %s\n", t.VertexConfig.Location)
	case managementapi.RouteTargetOpenAICompatible:
		ctx.Outputf("Base URL:         %s\n", t.BaseUrl)
	}
	ctx.Outputf("Invoke URL:       %s\n", hyperlink(ctx.Stdout, route.InvokeUrl))
	ctx.Outputf("Created:          %s\n", route.CreatedAt.UTC().Format(time.RFC3339))
	return nil
}

// routeTargetFields extracts the shared fields from generated target variants,
// like autoscalingScheduleFields does for schedule variants.
func routeTargetFields(value any) (kind, model, secret string, err error) {
	switch t := value.(type) {
	case managementapi.RouteTargetBasetenModelAPI:
		return t.Type, t.ModelApi, "", nil
	case managementapi.RouteTargetAnthropic:
		return t.Type, t.Model, t.SecretName, nil
	case managementapi.RouteTargetOpenAI:
		return t.Type, t.Model, t.SecretName, nil
	case managementapi.RouteTargetXAI:
		return t.Type, t.Model, t.SecretName, nil
	case managementapi.RouteTargetVertex:
		return t.Type, t.Model, t.SecretName, nil
	case managementapi.RouteTargetOpenAICompatible:
		return t.Type, t.Model, t.SecretName, nil
	default:
		return "", "", "", fmt.Errorf("unsupported Route target %T", value)
	}
}

func routeNonemptyFlags(ctx *CommandContext, names ...string) error {
	for _, name := range names {
		f := ctx.Command.Flags().Lookup(name)
		if f != nil && f.Changed && f.Value.String() == "" {
			return cmd.NewErrUsagef("--%s must not be empty", name)
		}
	}
	return nil
}

func validateRouteRefFlags(ctx *CommandContext, ref cmd.RouteRefFlags) error {
	if err := routeNonemptyFlags(ctx, "id", "name"); err != nil {
		return err
	}
	return validateRouteRef(ref)
}
