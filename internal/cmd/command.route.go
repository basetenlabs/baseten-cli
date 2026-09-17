package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

// The SDK does not yet expose Routes. Keep this shim Route-specific and use the
// existing client's transport, headers and error type until it does.
func routeRequest(ctx *CommandContext, api *managementapi.Client, method, id string, query url.Values, body, result any) error {
	path := "/v1/routes"
	if id != "" {
		path += "/" + url.PathEscape(id)
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(api.BaseURL, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header = api.Headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := api.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s Route: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return fmt.Errorf("reading Route error response: %w", err)
		}
		return &managementapi.ResponseError{StatusCode: resp.StatusCode, Body: string(data)}
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		return fmt.Errorf("unexpected Route response content type %q", resp.Header.Get("Content-Type"))
	}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decoding Route response: %w", err)
	}
	return nil
}

func commandRouteList(ctx *CommandContext, flags *cmd.RouteListFlags) error {
	if flags.Limit < 1 || flags.Limit > 1000 {
		return cmd.NewErrUsagef("--limit must be between 1 and 1000")
	}
	for name, value := range map[string]string{"team": flags.Team, "name": flags.Name, "cursor": flags.Cursor} {
		if ctx.Command.Flags().Changed(name) && value == "" {
			return cmd.NewErrUsagef("--%s must not be empty", name)
		}
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	team, err := ResolveTeam(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}
	query := url.Values{"limit": {strconv.Itoa(flags.Limit)}}
	if team != "" {
		query.Set("team_id", team)
	}
	if flags.Name != "" {
		query.Set("name", flags.Name)
	}
	if flags.Cursor != "" {
		query.Set("cursor", flags.Cursor)
	}
	result, err := fetchRoutePages(ctx, cl.API(), query, flags.All)
	if err != nil {
		return err
	}
	if ctx.JSON {
		ctx.OutputJSON(result)
		return nil
	}
	if len(result.Items) == 0 {
		ctx.LogLine("No Routes found.")
	} else {
		rows := make([][]string, 0, len(result.Items))
		for _, r := range result.Items {
			target := r.Target.ModelAPI
			if r.Target.Type != "BASETEN_MODEL_API" {
				target = r.Target.Type + ":" + r.Target.Model
			}
			rows = append(rows, []string{r.ID, r.Name, r.DisplayName, r.TeamID, target})
		}
		ctx.OutputTable(TableOutput{Headers: []string{"ID", "SLUG", "DISPLAY NAME", "TEAM ID", "TARGET"}, Rows: rows})
	}
	if result.Pagination.HasMore && result.Pagination.Cursor != nil {
		ctx.Logf("More Routes available. Continue with --cursor %q or use --all.\n", *result.Pagination.Cursor)
	}
	return nil
}

func fetchRoutePages(ctx *CommandContext, api *managementapi.Client, query url.Values, all bool) (cmd.RouteList, error) {
	result := cmd.RouteList{Items: []cmd.Route{}}
	seen := map[string]bool{query.Get("cursor"): true}
	for {
		var page cmd.RouteList
		if err := routeRequest(ctx, api, http.MethodGet, "", query, nil, &page); err != nil {
			return result, fmt.Errorf("list Routes: %w", err)
		}
		result.Items = append(result.Items, page.Items...)
		result.Pagination = page.Pagination
		if !all || !page.Pagination.HasMore {
			break
		}
		cursor := page.Pagination.Cursor
		if cursor == nil || *cursor == "" || seen[*cursor] {
			return result, fmt.Errorf("list Routes: invalid or repeated continuation cursor")
		}
		seen[*cursor] = true
		query.Set("cursor", *cursor)
	}
	return result, nil
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
	var page cmd.RouteList
	if err := routeRequest(ctx, api, http.MethodGet, "", url.Values{"name": {ref.Name}, "limit": {"2"}}, nil, &page); err != nil {
		return "", fmt.Errorf("resolve Route %q: %w", ref.Name, err)
	}
	if len(page.Items) == 0 {
		return "", cmd.NewErrNotFound(fmt.Errorf("no visible Route named %q", ref.Name))
	}
	if len(page.Items) != 1 || page.Pagination.HasMore {
		return "", fmt.Errorf("multiple Routes named %q; pass --id instead", ref.Name)
	}
	if page.Items[0].Name != ref.Name || page.Items[0].ID == "" {
		return "", fmt.Errorf("Route lookup returned an invalid exact-name match for %q", ref.Name)
	}
	return page.Items[0].ID, nil
}

func commandRouteDescribe(ctx *CommandContext, flags *cmd.RouteDescribeFlags) error {
	if err := completeRouteRef(ctx, &flags.RouteRefFlags); err != nil {
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
	var route cmd.Route
	if err := routeRequest(ctx, cl.API(), http.MethodGet, id, nil, nil, &route); err != nil {
		return fmt.Errorf("describe Route %s: %w", id, err)
	}
	teamName := ""
	if !ctx.JSON && flags.Output != "none" && route.TeamID != "" {
		team, err := cl.API().GetTeamsTeamId(ctx, route.TeamID)
		if err != nil {
			ctx.VerboseLogf("Could not resolve team name for %s: %v\n", route.TeamID, err)
		} else {
			teamName = team.Name
		}
	}
	outputRoute(ctx, route, teamName)
	return nil
}

func routeTarget(ctx *CommandContext, flags cmd.RouteTargetFlags, required bool) (*cmd.RouteTarget, error) {
	values := map[string]string{
		"target-model-api": flags.TargetModelAPI, "target-provider": flags.TargetProvider,
		"target-provider-model": flags.TargetProviderModel, "target-provider-secret": flags.TargetProviderSecret,
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
		return nil, cmd.NewErrUsagef("pass exactly one target head: --target-model-api or --target-provider")
	}
	if flags.TargetModelAPI != "" {
		for name, value := range values {
			if name != "target-model-api" && value != "" {
				return nil, cmd.NewErrUsagef("--target-model-api is mutually exclusive with --%s", name)
			}
		}
		return &cmd.RouteTarget{Type: "BASETEN_MODEL_API", ModelAPI: flags.TargetModelAPI}, nil
	}
	if flags.TargetProviderModel == "" || flags.TargetProviderSecret == "" {
		return nil, cmd.NewErrUsagef("--target-provider requires --target-provider-model and --target-provider-secret; restate the complete target")
	}
	target := &cmd.RouteTarget{Type: strings.ToUpper(strings.ReplaceAll(flags.TargetProvider, "-", "_")), Model: flags.TargetProviderModel, SecretName: flags.TargetProviderSecret}
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
		target.BaseURL = &flags.TargetProviderBaseURL
	} else if flags.TargetProviderBaseURL != "" {
		return nil, cmd.NewErrUsagef("--target-provider-base-url is only valid with openai-compatible")
	}
	if flags.TargetProvider == "vertex" {
		if flags.TargetProviderVertexProject == "" || flags.TargetProviderVertexLocation == "" {
			return nil, cmd.NewErrUsagef("vertex requires --target-provider-vertex-project and --target-provider-vertex-location")
		}
		target.VertexConfig = &cmd.RouteVertexConfig{ProjectID: flags.TargetProviderVertexProject, Location: flags.TargetProviderVertexLocation}
	} else if flags.TargetProviderVertexProject != "" || flags.TargetProviderVertexLocation != "" {
		return nil, cmd.NewErrUsagef("Vertex project and location flags are only valid with vertex")
	}
	return target, nil
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

type createRouteRequest struct {
	Name        string           `json:"name"`
	TeamID      string           `json:"team_id"`
	DisplayName *string          `json:"display_name,omitempty"`
	Description *string          `json:"description,omitempty"`
	Target      *cmd.RouteTarget `json:"target"`
}

type updateRouteRequest struct {
	DisplayName *string          `json:"display_name,omitempty"`
	Description *string          `json:"description,omitempty"`
	Target      *cmd.RouteTarget `json:"target,omitempty"`
}

func commandRouteCreate(ctx *CommandContext, flags *cmd.RouteCreateFlags) error {
	if err := completeRouteCreate(ctx, flags); err != nil {
		return err
	}
	if flags.Name == "" || flags.Team == "" {
		return cmd.NewErrUsagef("--name and --team must not be empty")
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
	body := createRouteRequest{Name: flags.Name, TeamID: team, DisplayName: flags.DisplayName.Pointer(), Description: flags.Description.Pointer(), Target: target}
	var route cmd.Route
	if err := routeRequest(ctx, cl.API(), http.MethodPost, "", nil, body, &route); err != nil {
		return fmt.Errorf("create Route: %w", err)
	}
	outputRoute(ctx, route, "")
	return nil
}

func commandRouteUpdate(ctx *CommandContext, flags *cmd.RouteUpdateFlags) error {
	if err := completeRouteUpdate(ctx, flags); err != nil {
		return err
	}
	if err := completeRouteRef(ctx, &flags.RouteRefFlags); err != nil {
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
	body := updateRouteRequest{DisplayName: flags.DisplayName.Pointer(), Description: flags.Description.Pointer(), Target: target}
	var route cmd.Route
	if err := routeRequest(ctx, cl.API(), http.MethodPatch, id, nil, body, &route); err != nil {
		return fmt.Errorf("update Route %s: %w", id, err)
	}
	outputRoute(ctx, route, "")
	return nil
}

func commandRouteDelete(ctx *CommandContext, flags *cmd.RouteDeleteFlags) error {
	if err := completeRouteRef(ctx, &flags.RouteRefFlags); err != nil {
		return err
	}
	if !flags.Yes && !routeInteractive(ctx) {
		return cmd.NewErrUsagef("cannot confirm deletion: stdin is not a terminal; pass --yes to skip the prompt")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	id, err := resolveRouteID(ctx, cl.API(), flags.RouteRefFlags)
	if err != nil {
		return err
	}
	if !flags.Yes {
		if err := routePrompt(ctx).Confirm(fmt.Sprintf("Delete Route %s?", id)); err != nil {
			return err
		}
	}
	var tombstone cmd.RouteTombstone
	if err := routeRequest(ctx, cl.API(), http.MethodDelete, id, nil, nil, &tombstone); err != nil {
		return fmt.Errorf("delete Route %s: %w", id, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(tombstone)
	} else {
		ctx.Logf("Deleted Route %s (%s)\n", tombstone.Name, tombstone.ID)
	}
	return nil
}

func outputRoute(ctx *CommandContext, route cmd.Route, teamName string) {
	if ctx.JSON {
		ctx.OutputJSON(route)
		return
	}
	ctx.Outputf("ID:               %s\n", route.ID)
	ctx.Outputf("Slug:             %s\n", route.Name)
	ctx.Outputf("Display Name:     %s\n", route.DisplayName)
	ctx.Outputf("Description:      %s\n", route.Description)
	ctx.Outputf("Team ID:          %s\n", route.TeamID)
	if teamName != "" {
		ctx.Outputf("Team Name:        %s\n", teamName)
	}
	ctx.Outputf("Target Type:      %s\n", route.Target.Type)
	if route.Target.Type == "BASETEN_MODEL_API" {
		ctx.Outputf("Model API:        %s\n", route.Target.ModelAPI)
	} else {
		ctx.Outputf("Provider:         %s\n", route.Target.Type)
		ctx.Outputf("Provider Model:   %s\n", route.Target.Model)
		ctx.Outputf("Provider Secret:  %s\n", route.Target.SecretName)
		if route.Target.BaseURL != nil {
			ctx.Outputf("Base URL:         %s\n", *route.Target.BaseURL)
		}
		if route.Target.VertexConfig != nil {
			ctx.Outputf("Vertex Project:   %s\n", route.Target.VertexConfig.ProjectID)
			ctx.Outputf("Vertex Location:  %s\n", route.Target.VertexConfig.Location)
		}
	}
	ctx.Outputf("Invoke URL:       %s\n", hyperlink(ctx.Stdout, route.InvokeURL))
	ctx.Outputf("Created:          %s\n", route.CreatedAt.UTC().Format(time.RFC3339))
}
