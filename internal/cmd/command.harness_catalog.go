package cmd

import (
	"errors"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func listHarnessRoutes(ctx *CommandContext, teamID string) ([]managementapi.Route, error) {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return nil, err
	}
	api := cl.API()
	routes, err := listRoutes(ctx, api, managementapi.GetV1RoutesParams{TeamId: &teamID})
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, route := range routes {
		if route.Id == "" || route.Name == "" || route.DisplayName == "" || route.InvokeUrl == "" || route.TeamId != teamID || names[route.Name] {
			return nil, cmd.NewErrServer(errors.New("routes API returned incomplete, duplicate, or out-of-team routes"))
		}
		names[route.Name] = true
	}
	if len(routes) == 0 {
		return nil, cmd.NewErrValidation(errors.New("no accessible routes in the selected team; create a route before running harness setup"))
	}
	return routes, nil
}

// Use Route names and display labels only; model metadata is a follow-up.
func harnessCatalog(managementURL string, listed []managementapi.Route) ([]harness.Route, string, error) {
	if len(listed) == 0 {
		return nil, "", cmd.NewErrValidation(errors.New("no accessible routes"))
	}
	endpoint := strings.TrimRight(listed[0].InvokeUrl, "/")
	for _, route := range listed {
		if strings.TrimRight(route.InvokeUrl, "/") != endpoint {
			return nil, "", cmd.NewErrValidation(errors.New("selected team has routes with different invoke URLs"))
		}
	}
	// RouteV1 currently returns this fixed production origin. Permit loopback only
	// when both management and inference use an explicit local development server.
	if endpoint != "https://coding.baseten.co" && !(harness.FixtureEndpoint(managementURL) == nil && harness.FixtureEndpoint(endpoint) == nil) {
		return nil, "", cmd.NewErrValidation(errors.New("routes API returned an unsupported invoke URL"))
	}
	routes := make([]harness.Route, 0, len(listed))
	for _, route := range listed {
		routes = append(routes, harness.Route{Name: route.Name, DisplayName: route.DisplayName})
	}
	routes, err := harness.ValidateCatalog(routes)
	return routes, endpoint, err
}
