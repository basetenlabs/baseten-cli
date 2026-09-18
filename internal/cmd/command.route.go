package cmd

import (
	"fmt"
	"time"

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
	var items []managementapi.Route
	for {
		resp, err := cl.API().GetRoutes(ctx, params)
		if err != nil {
			return fmt.Errorf("list routes: %w", err)
		}
		items = append(items, resp.Items...)
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			break
		}
		params.Cursor = resp.Pagination.Cursor
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.RouteList{Items: items})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No routes found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, r := range items {
		target := "<unrecognized type>"
		if value, err := r.Target.ValueByDiscriminator(); err == nil {
			if kind, model, _, err := routeTargetFields(value); err == nil {
				target = model
				if kind != "BASETEN_MODEL_API" {
					target = enumFromAPIValue(kind) + ":" + model
				}
			}
		}
		rows = append(rows, []string{r.Id, r.Name, r.DisplayName, r.TeamName, target, r.CreatedAt.UTC().Format(time.RFC3339)})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "DISPLAY NAME", "TEAM", "TARGET", "CREATED"},
		Rows:    rows,
	})
	return nil
}

// Resolve a name once, then mutate by stable ID. Never retry a mutation by name:
// deletion and recreation of the name must not redirect it to a different route.
func resolveRouteID(ctx *CommandContext, api *managementapi.Client, ref cmd.RouteRefFlags) (string, error) {
	if ref.ID != "" {
		return ref.ID, nil
	}
	if ref.Name == "" {
		return "", cmd.NewErrUsagef("pass --id or --name")
	}
	page, err := api.GetRoutes(ctx, managementapi.GetV1RoutesParams{Name: &ref.Name})
	if err != nil {
		return "", fmt.Errorf("resolve route %q: %w", ref.Name, err)
	}
	if len(page.Items) == 0 {
		return "", cmd.NewErrNotFound(fmt.Errorf("no visible route named %q", ref.Name))
	}
	if len(page.Items) != 1 || page.Pagination.HasMore {
		return "", fmt.Errorf("multiple routes named %q; pass --id instead", ref.Name)
	}
	if page.Items[0].Name != ref.Name || page.Items[0].Id == "" {
		return "", fmt.Errorf("route lookup returned an invalid exact-name match for %q", ref.Name)
	}
	return page.Items[0].Id, nil
}

func commandRouteDescribe(ctx *CommandContext, flags *cmd.RouteDescribeFlags) error {
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
		return fmt.Errorf("describe route %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(route)
		return nil
	}
	ctx.Outputf("ID:               %s\n", route.Id)
	ctx.Outputf("Name:             %s\n", route.Name)
	ctx.Outputf("Display Name:     %s\n", route.DisplayName)
	ctx.Outputf("Description:      %s\n", route.Description)
	ctx.Outputf("Team ID:          %s\n", route.TeamId)
	ctx.Outputf("Team Name:        %s\n", route.TeamName)
	value, err := route.Target.ValueByDiscriminator()
	var kind, model, secret string
	if err == nil {
		kind, model, secret, err = routeTargetFields(value)
	}
	if err != nil {
		ctx.Outputf("Target Type:      <unrecognized type>\n")
	} else {
		ctx.Outputf("Target Type:      %s\n", enumFromAPIValue(kind))
		ctx.Outputf("Target Model:     %s\n", model)
		if kind != "BASETEN_MODEL_API" {
			ctx.Outputf("Target Secret:    %s\n", secret)
		}
		switch t := value.(type) {
		case managementapi.RouteTargetVertex:
			ctx.Outputf("Vertex Project:   %s\n", t.VertexConfig.ProjectId)
			ctx.Outputf("Vertex Location:  %s\n", t.VertexConfig.Location)
		case managementapi.RouteTargetOpenAICompatible:
			ctx.Outputf("Base URL:         %s\n", t.BaseUrl)
		}
	}
	ctx.Outputf("Invoke URL:       %s\n", hyperlink(ctx.Stdout, route.InvokeUrl))
	ctx.Outputf("Created:          %s\n", route.CreatedAt.UTC().Format(time.RFC3339))
	return nil
}

func routeTarget(ctx *CommandContext, flags cmd.RouteTargetFlags, required bool) (*managementapi.CreateRouteRequest_Target, error) {
	supplied := false
	for _, name := range []string{"target-type", "target-model", "target-secret", "target-base-url", "target-vertex-project", "target-vertex-location"} {
		supplied = supplied || ctx.Command.Flags().Changed(name)
	}
	if !supplied && !required {
		return nil, nil
	}
	if flags.TargetType == "" || flags.TargetModel == "" {
		return nil, cmd.NewErrUsagef("--target-type and --target-model are required; restate the complete target")
	}
	if flags.TargetType == "baseten-model-api" {
		if flags.TargetSecret != "" || flags.TargetBaseURL != "" || flags.TargetVertexProject != "" || flags.TargetVertexLocation != "" {
			return nil, cmd.NewErrUsagef("baseten-model-api does not accept external target configuration")
		}
		target := &managementapi.CreateRouteRequest_Target{}
		err := target.FromRouteTargetBasetenModelAPI(managementapi.RouteTargetBasetenModelAPI{
			Type:  "BASETEN_MODEL_API",
			Model: flags.TargetModel,
		})
		return target, err
	}
	if flags.TargetSecret == "" {
		return nil, cmd.NewErrUsagef("external targets require --target-secret; restate the complete target")
	}
	target := &managementapi.CreateRouteRequest_Target{}
	if flags.TargetType == "openai-compatible" {
		if flags.TargetBaseURL == "" {
			return nil, cmd.NewErrUsagef("openai-compatible requires --target-base-url")
		}
	} else if flags.TargetBaseURL != "" {
		return nil, cmd.NewErrUsagef("--target-base-url is only valid with openai-compatible")
	}
	if flags.TargetType == "vertex" {
		if flags.TargetVertexProject == "" || flags.TargetVertexLocation == "" {
			return nil, cmd.NewErrUsagef("vertex requires --target-vertex-project and --target-vertex-location")
		}
	} else if flags.TargetVertexProject != "" || flags.TargetVertexLocation != "" {
		return nil, cmd.NewErrUsagef("Vertex project and location flags are only valid with vertex")
	}
	var err error
	switch flags.TargetType {
	case "anthropic":
		err = target.FromRouteTargetAnthropic(managementapi.RouteTargetAnthropic{
			Type:       "ANTHROPIC",
			Model:      flags.TargetModel,
			SecretName: flags.TargetSecret,
		})
	case "openai":
		err = target.FromRouteTargetOpenAI(managementapi.RouteTargetOpenAI{
			Type:       "OPENAI",
			Model:      flags.TargetModel,
			SecretName: flags.TargetSecret,
		})
	case "xai":
		err = target.FromRouteTargetXAI(managementapi.RouteTargetXAI{
			Type:       "XAI",
			Model:      flags.TargetModel,
			SecretName: flags.TargetSecret,
		})
	case "vertex":
		err = target.FromRouteTargetVertex(managementapi.RouteTargetVertex{
			Type:       "VERTEX",
			Model:      flags.TargetModel,
			SecretName: flags.TargetSecret,
			VertexConfig: managementapi.VertexTargetConfig{
				ProjectId: flags.TargetVertexProject,
				Location:  flags.TargetVertexLocation,
			},
		})
	case "openai-compatible":
		err = target.FromRouteTargetOpenAICompatible(managementapi.RouteTargetOpenAICompatible{
			Type:       "OPENAI_COMPATIBLE",
			Model:      flags.TargetModel,
			SecretName: flags.TargetSecret,
			BaseUrl:    flags.TargetBaseURL,
		})
	default:
		return nil, cmd.NewErrUsagef("unsupported target type %q", flags.TargetType)
	}
	return target, err
}

func commandRouteCreate(ctx *CommandContext, flags *cmd.RouteCreateFlags) error {
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
		DisplayName: flags.DisplayName.Pointer(),
		Description: flags.Description.Pointer(),
		Target:      *target,
	}
	if team != "" {
		body.TeamId = &team
	}
	route, err := cl.API().PostRoutes(ctx, body)
	if err != nil {
		return fmt.Errorf("create route: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(route)
	} else {
		ctx.Logf("Created route %s (%s)\n", route.Name, route.Id)
	}
	return nil
}

func commandRouteUpdate(ctx *CommandContext, flags *cmd.RouteUpdateFlags) error {
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
		return fmt.Errorf("update route %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(route)
	} else {
		ctx.Logf("Updated route %s (%s)\n", route.Name, route.Id)
	}
	return nil
}

func commandRouteDelete(ctx *CommandContext, flags *cmd.RouteDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveRouteID(ctx, cl.API(), flags.RouteRefFlags)
	if err != nil {
		return err
	}
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete route %s?", id)); err != nil {
			return err
		}
	}
	tombstone, err := cl.API().DeleteRoutes(ctx, id)
	if err != nil {
		return fmt.Errorf("delete route %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(tombstone)
	} else {
		ctx.Logf("Deleted route %s (%s)\n", tombstone.Name, tombstone.Id)
	}
	return nil
}

// routeTargetFields extracts the shared fields from generated target variants,
// like autoscalingScheduleFields does for schedule variants.
func routeTargetFields(value any) (kind, model, secret string, err error) {
	switch t := value.(type) {
	case managementapi.RouteTargetBasetenModelAPI:
		return t.Type, t.Model, "", nil
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
		return "", "", "", fmt.Errorf("unsupported route target %T", value)
	}
}
