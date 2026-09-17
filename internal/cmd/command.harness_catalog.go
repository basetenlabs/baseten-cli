package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/harness"
)

// The pinned SDK does not yet expose GET /v1/routes. Use its configured client
// and headers until the generated operation is available.
func listHarnessRoutes(ctx *CommandContext, teamID string) ([]routeRecord, error) {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return nil, err
	}
	api := cl.API()
	return readRoutes(ctx, api.HTTPClient, api.BaseURL, api.Headers, teamID)
}

// Read the standard inference catalog, not X-Baseten-Client's custom catalogs.
// The standard response preserves Route IDs and exposes Model API capabilities.
func harnessCatalog(ctx context.Context, client interface {
	Do(*http.Request) (*http.Response, error)
}, managementURL string, listed []routeRecord) ([]harness.Route, string, []string, error) {
	if len(listed) == 0 {
		return nil, "", nil, cmd.NewErrValidation(errors.New("no accessible Routes"))
	}
	endpoint := strings.TrimRight(listed[0].InvokeURL, "/")
	for _, route := range listed {
		if strings.TrimRight(route.InvokeURL, "/") != endpoint {
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
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint+"/v1/models", nil)
	if err != nil {
		return nil, "", nil, errors.New("invalid model catalog endpoint")
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, "", nil, cmd.NewErrServer(errors.New("cannot fetch Route metadata from /v1/models"))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", nil, cmd.WrapHTTPStatus(response.StatusCode, fmt.Errorf("GET /v1/models returned HTTP %d", response.StatusCode))
	}
	var catalog struct {
		Data []struct {
			ID       string   `json:"id"`
			Context  int      `json:"context_length"`
			Output   int      `json:"max_completion_tokens"`
			Features []string `json:"supported_features"`
			Input    []string `json:"input_modalities"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if decoder.Decode(&catalog) != nil {
		return nil, "", nil, cmd.NewErrServer(errors.New("invalid /v1/models response"))
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || catalog.Data == nil {
		return nil, "", nil, cmd.NewErrServer(errors.New("invalid /v1/models response"))
	}
	metadata := map[string]harness.Route{}
	for _, model := range catalog.Data {
		if _, exists := metadata[model.ID]; exists {
			return nil, "", nil, cmd.NewErrServer(errors.New("duplicate model in /v1/models"))
		}
		metadata[model.ID] = harness.Route{Name: model.ID, ContextWindow: model.Context, OutputLimit: model.Output, InputModalities: model.Input, Tools: slices.Contains(model.Features, "tools"), Messages: true, Responses: true, ChatCompletions: true}
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
