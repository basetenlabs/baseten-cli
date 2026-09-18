package cmd

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/basetenlabs/baseten-go/client/inferenceapi"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func listHarnessRoutes(ctx *CommandContext, teamID string) ([]managementapi.Route, error) {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return nil, err
	}
	api := cl.API()
	return readRoutes(ctx, api, teamID)
}

// Read the standard inference catalog, not X-Baseten-Client's custom catalogs.
// The standard response preserves Route IDs and exposes Model API capabilities.
func harnessCatalog(ctx context.Context, client interface {
	Do(*http.Request) (*http.Response, error)
}, managementURL string, listed []managementapi.Route) ([]harness.Route, string, []string, error) {
	if len(listed) == 0 {
		return nil, "", nil, cmd.NewErrValidation(errors.New("no accessible Routes"))
	}
	endpoint := strings.TrimRight(listed[0].InvokeUrl, "/")
	for _, route := range listed {
		if strings.TrimRight(route.InvokeUrl, "/") != endpoint {
			return nil, "", nil, cmd.NewErrValidation(errors.New("selected team has Routes with different invoke URLs"))
		}
	}
	// RouteV1 currently returns this fixed production origin. Permit loopback only
	// when both management and inference use an explicit local development server.
	if endpoint != "https://coding.baseten.co" && !(harness.FixtureEndpoint(managementURL) == nil && harness.FixtureEndpoint(endpoint) == nil) {
		return nil, "", nil, cmd.NewErrValidation(errors.New("Routes API returned an unsupported invoke URL"))
	}
	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	api := &inferenceapi.Client{BaseURL: endpoint, HTTPClient: client}
	catalog, err := api.GetModels(requestCtx)
	if err != nil {
		return nil, "", nil, err
	}
	if catalog.Data == nil {
		return nil, "", nil, cmd.NewErrServer(errors.New("invalid model catalog response"))
	}
	metadata := map[string]harness.Route{}
	for _, model := range catalog.Data {
		if _, exists := metadata[model.Id]; exists {
			return nil, "", nil, cmd.NewErrServer(errors.New("duplicate model in /v1/models"))
		}
		metadata[model.Id] = harness.Route{
			Name:            model.Id,
			ContextWindow:   model.ContextLength,
			OutputLimit:     model.MaxCompletionTokens,
			InputModalities: model.InputModalities,
			Tools:           slices.Contains(model.SupportedFeatures, "tools"),
			Messages:        true,
			Responses:       true,
			ChatCompletions: true,
		}
	}
	var routes []harness.Route
	var skipped []string
	for _, route := range listed {
		model, exists := metadata[route.Name]
		model.DisplayName = route.DisplayName
		if !exists || model.ContextWindow <= 0 || model.OutputLimit <= 0 || model.OutputLimit >= model.ContextWindow || len(model.InputModalities) == 0 {
			skipped = append(skipped, route.Name)
			continue
		}
		if _, err := harness.ValidateCatalog([]harness.Route{model}); err != nil {
			skipped = append(skipped, route.Name)
			continue
		}
		routes = append(routes, model)
	}
	if len(routes) == 0 {
		return nil, "", skipped, cmd.NewErrValidation(errors.New("no accessible Routes have usable tool and model metadata in /v1/models; provider-backed Routes may not be listed yet"))
	}
	return routes, endpoint, skipped, nil
}
