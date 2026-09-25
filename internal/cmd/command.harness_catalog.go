package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// harnessRoute is a route from GET /v1/routes, with the model metadata the
// generated management client does not expose yet.
// TODO: Use listRoutes once the generated Route has metadata.
type harnessRoute struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	InvokeURL   string `json:"invoke_url"`
	Target      struct {
		Type string `json:"type"`
	} `json:"target"`
	Metadata *struct {
		ContextWindow       int      `json:"context_window"`
		MaxOutputTokens     int      `json:"max_output_tokens"`
		InputModalities     []string `json:"input_modalities"`
		Tools               bool     `json:"tools"`
		ReasoningLevels     []string `json:"reasoning_effort_levels"`
		ParallelToolCalls   bool     `json:"parallel_tool_calls"`
		SupportedAPIFormats struct {
			Responses bool `json:"responses"`
		} `json:"supported_api_formats"`
	} `json:"metadata"`
}

func listHarnessRoutes(ctx context.Context, api *managementapi.Client, teamID string) ([]harnessRoute, error) {
	var routes []harnessRoute
	query := url.Values{"team_id": {teamID}}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(api.BaseURL, "/")+"/v1/routes?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header = api.Headers.Clone()
		resp, err := api.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list routes: %w", err)
		}
		var page struct {
			Items      []harnessRoute `json:"items"`
			Pagination struct {
				HasMore bool    `json:"has_more"`
				Cursor  *string `json:"cursor"`
			} `json:"pagination"`
		}
		err = func() error {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
				return cmd.WrapHTTPStatus(resp.StatusCode, fmt.Errorf("list routes: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
			}
			return json.NewDecoder(resp.Body).Decode(&page)
		}()
		if err != nil {
			return nil, err
		}
		routes = append(routes, page.Items...)
		if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
			return routes, nil
		}
		query.Set("cursor", *page.Pagination.Cursor)
	}
}

// harnessCatalog converts routes to harness routes, skipping routes whose
// metadata is missing or unusable. The selected primary route must be usable.
func harnessCatalog(listed []harnessRoute, primary string) (routes []harness.Route, skipped []string, err error) {
	for _, l := range listed {
		r := harness.Route{Name: l.Name, DisplayName: l.DisplayName, Target: l.Target.Type}
		err := fmt.Errorf("route %q has no model metadata", l.Name)
		if m := l.Metadata; m != nil {
			r.ContextWindow, r.OutputLimit = m.ContextWindow, m.MaxOutputTokens
			r.InputModalities, r.Tools = m.InputModalities, m.Tools
			r.ReasoningLevels = harness.NormalizeReasoningLevels(m.ReasoningLevels)
			r.ParallelTools, r.Responses = m.ParallelToolCalls, m.SupportedAPIFormats.Responses
			err = harness.ValidateRoute(r)
		}
		switch {
		case err == nil:
			routes = append(routes, r)
		case l.Name == primary:
			return nil, nil, cmd.NewErrValidation(err)
		default:
			skipped = append(skipped, l.Name)
		}
	}
	if len(routes) == 0 {
		return nil, skipped, cmd.NewErrValidation(errors.New("none of the team's routes have usable model metadata"))
	}
	return routes, skipped, nil
}
