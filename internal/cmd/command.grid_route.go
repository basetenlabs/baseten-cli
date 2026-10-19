package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("grid route list", commandGridRouteList)
	Register("grid route describe", commandGridRouteDescribe)
	Register("grid route create", commandGridRouteCreate)
	Register("grid route update", commandGridRouteUpdate)
	Register("grid route delete", commandGridRouteDelete)
}

func commandGridRouteList(ctx *CommandContext, flags *cmd.GridRouteListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	team, err := ResolveTeam(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}
	params := managementapi.GetV1GridRoutesParams{}
	if team != "" {
		params.TeamId = &team
	}
	items, err := listGridRoutes(ctx, cl.API(), params)
	if err != nil {
		return err
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.GridRouteList{Items: normalizeJSONSlice(items)})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No routes found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, r := range items {
		rows = append(rows, []string{r.Id, r.DisplayName, r.Name, r.CreatedAt.UTC().Format(time.RFC3339)})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "DISPLAY NAME", "NAME", "CREATED"},
		Rows:    rows,
	})
	return nil
}

// Resolve a name once, then mutate by stable ID. Never retry a mutation by name:
// deletion and recreation of the name must not redirect it to a different route.
func resolveGridRouteID(ctx *CommandContext, api *managementapi.Client, ref cmd.GridRouteRefFlags) (string, error) {
	if ref.ID != "" {
		return ref.ID, nil
	}
	if ref.Name == "" {
		return "", cmd.NewErrUsagef("pass --id or --name")
	}
	page, err := api.GetGridRoutes(ctx, managementapi.GetV1GridRoutesParams{Name: &ref.Name})
	if err != nil {
		return "", fmt.Errorf("resolve route %q: %w", ref.Name, err)
	}
	if len(page.Items) == 0 {
		return "", cmd.NewErrNotFound(fmt.Errorf("no route named %q", ref.Name))
	}
	if len(page.Items) != 1 || page.Pagination.HasMore {
		return "", fmt.Errorf("multiple routes named %q; pass --id instead", ref.Name)
	}
	if page.Items[0].Name != ref.Name || page.Items[0].Id == "" {
		return "", fmt.Errorf("route lookup returned an invalid match for %q; pass --id instead", ref.Name)
	}
	return page.Items[0].Id, nil
}

func commandGridRouteDescribe(ctx *CommandContext, flags *cmd.GridRouteDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveGridRouteID(ctx, cl.API(), flags.GridRouteRefFlags)
	if err != nil {
		return err
	}
	route, err := cl.API().GetGridRoutesRouteId(ctx, id)
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
	var t gridRouteTargetInfo
	if err == nil {
		t, err = gridRouteTargetFields(value)
	}
	if err != nil {
		ctx.Outputf("Target Type:      <unrecognized type>\n")
	} else {
		ctx.Outputf("Target Type:      %s\n", enumFromAPIValue(t.kind))
		if t.model != "" {
			ctx.Outputf("Target Model:     %s\n", t.model)
		}
		if t.connection != "" {
			ctx.Outputf("Target Connection: %s\n", t.connection)
		}
		if t.classifier != "" {
			ctx.Outputf("Classifier:       %s\n", t.classifier)
		}
		if t.defaultRoute != "" {
			ctx.Outputf("Default Route:    %s\n", t.defaultRoute)
			ctx.Outputf("Allowed Routes:   %s\n", strings.Join(t.allowedRoutes, ", "))
		}
	}
	ctx.Outputf("Invoke URL:       %s\n", hyperlink(ctx.Stdout, route.InvokeUrl))
	ctx.Outputf("Created:          %s\n", route.CreatedAt.UTC().Format(time.RFC3339))
	return nil
}

func gridRouteTarget(flags cmd.GridRouteTargetFlags) (*managementapi.CreateRouteRequest_Target, error) {
	if flags.TargetType == "" || flags.TargetModel == "" {
		return nil, cmd.NewErrUsagef("--target-type and --target-model are required")
	}
	target := &managementapi.CreateRouteRequest_Target{}
	switch flags.TargetType {
	case "baseten-model-api":
		if flags.TargetConnection != "" {
			return nil, cmd.NewErrUsagef("--target-connection is not valid with --target-type baseten-model-api")
		}
		err := target.FromUpsertRouteTargetBasetenModelAPI(managementapi.UpsertRouteTargetBasetenModelAPI{
			Type:  "BASETEN_MODEL_API",
			Model: flags.TargetModel,
		})
		return target, err
	case "connection":
		if flags.TargetConnection == "" {
			return nil, cmd.NewErrUsagef("--target-connection is required with --target-type connection")
		}
		err := target.FromUpsertRouteTargetConnection(managementapi.UpsertRouteTargetConnection{
			Type:         "CONNECTION",
			Model:        flags.TargetModel,
			ConnectionId: flags.TargetConnection,
		})
		return target, err
	default:
		return nil, cmd.NewErrUsagef("unsupported target type %q", flags.TargetType)
	}
}

func commandGridRouteCreate(ctx *CommandContext, flags *cmd.GridRouteCreateFlags) error {
	target, err := gridRouteTarget(flags.GridRouteTargetFlags)
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
		DisplayName: flags.DisplayName.Pointer(),
		Description: flags.Description.Pointer(),
		Target:      *target,
	}
	if team != "" {
		body.TeamId = &team
	}
	route, err := cl.API().PostGridRoutes(ctx, body)
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

func commandGridRouteUpdate(ctx *CommandContext, flags *cmd.GridRouteUpdateFlags) error {
	if !flags.DisplayName.IsSet() && !flags.Description.IsSet() {
		return cmd.NewErrUsagef("pass --display-name or --description")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveGridRouteID(ctx, cl.API(), flags.GridRouteRefFlags)
	if err != nil {
		return err
	}
	body := managementapi.UpdateRouteRequest{
		DisplayName: flags.DisplayName.Pointer(),
		Description: flags.Description.Pointer(),
	}
	route, err := cl.API().PatchGridRoutes(ctx, id, body)
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

func commandGridRouteDelete(ctx *CommandContext, flags *cmd.GridRouteDeleteFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveGridRouteID(ctx, cl.API(), flags.GridRouteRefFlags)
	if err != nil {
		return err
	}
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete route %s?", id)); err != nil {
			return err
		}
	}
	tombstone, err := cl.API().DeleteGridRoutes(ctx, id)
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

// gridRouteTargetInfo is the displayable shape of any route target variant.
type gridRouteTargetInfo struct {
	kind, model, connection string
	// Classifier routes pick one of their allowed routes per request.
	classifier, defaultRoute string
	allowedRoutes            []string
}

// gridRouteTargetFields extracts the shared fields from generated target variants,
// like autoscalingScheduleFields does for schedule variants.
func gridRouteTargetFields(value any) (gridRouteTargetInfo, error) {
	switch t := value.(type) {
	case managementapi.RouteTargetBasetenModelAPI:
		return gridRouteTargetInfo{kind: t.Type, model: t.Model}, nil
	case managementapi.RouteTargetConnection:
		return gridRouteTargetInfo{kind: t.Type, model: t.Model, connection: t.ConnectionId}, nil
	case managementapi.RouteTargetClassifier:
		info := gridRouteTargetInfo{kind: t.Type, defaultRoute: t.DefaultRoute.Slug}
		// Null for routers created before classifiers could be selected.
		if t.Classifier != nil {
			info.classifier = t.Classifier.Name
		}
		for _, r := range t.AllowedRoutes {
			info.allowedRoutes = append(info.allowedRoutes, r.Slug)
		}
		return info, nil
	default:
		return gridRouteTargetInfo{}, fmt.Errorf("unsupported route target %T", value)
	}
}

// listGridConnections returns a team's route connections across all pages.
func listGridConnections(ctx context.Context, api *managementapi.Client, params managementapi.GetV1GridConnectionsParams) ([]managementapi.RouteConnection, error) {
	var connections []managementapi.RouteConnection
	for {
		resp, err := api.GetGridConnections(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("list route connections: %w", err)
		}
		connections = append(connections, resp.Items...)
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			return connections, nil
		}
		params.Cursor = resp.Pagination.Cursor
	}
}

// listGridRoutes returns the routes across all pages.
func listGridRoutes(ctx context.Context, api *managementapi.Client, params managementapi.GetV1GridRoutesParams) ([]managementapi.Route, error) {
	var routes []managementapi.Route
	for {
		resp, err := api.GetGridRoutes(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("list routes: %w", err)
		}
		routes = append(routes, resp.Items...)
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			return routes, nil
		}
		params.Cursor = resp.Pagination.Cursor
	}
}
