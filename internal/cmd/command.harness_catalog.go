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
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/harness"
)

// harnessRouteRecord follows RouteV1 from the Routes management API, plus the
// per-route model metadata the generated management client does not expose yet.
// Target details are intentionally not treated as model capability metadata.
type harnessRouteRecord struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	TeamID      string                `json:"team_id"`
	DisplayName string                `json:"display_name"`
	InvokeURL   string                `json:"invoke_url"`
	Metadata    *harnessRouteMetadata `json:"metadata"`
}

type harnessRouteMetadata struct {
	ContextWindow       int      `json:"context_window"`
	MaxOutputTokens     int      `json:"max_output_tokens"`
	InputModalities     []string `json:"input_modalities"`
	Tools               bool     `json:"tools"`
	ReasoningLevels     []string `json:"reasoning_effort_levels"`
	ParallelToolCalls   bool     `json:"parallel_tool_calls"`
	SupportedAPIFormats struct {
		Messages        bool `json:"messages"`
		Responses       bool `json:"responses"`
		ChatCompletions bool `json:"chat_completions"`
	} `json:"supported_api_formats"`
}

func readHarnessRoutes(ctx context.Context, client interface {
	Do(*http.Request) (*http.Response, error)
}, baseURL string, headers http.Header, teamID string) ([]harnessRouteRecord, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var routes []harnessRouteRecord
	cursor := ""
	cursors := map[string]bool{}
	names := map[string]bool{}
	for {
		query := url.Values{"team_id": {teamID}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/routes?"+query.Encode(), nil)
		if err != nil {
			return nil, errors.New("invalid Routes API endpoint")
		}
		req.Header = headers.Clone()
		response, err := client.Do(req)
		if err != nil {
			return nil, cmd.NewErrServer(errors.New("listing Routes failed; check your login and connection"))
		}
		var page struct {
			Items      []harnessRouteRecord `json:"items"`
			Pagination *struct {
				HasMore bool   `json:"has_more"`
				Cursor  string `json:"cursor"`
			} `json:"pagination"`
		}
		err = func() error {
			defer response.Body.Close()
			if response.StatusCode == http.StatusNotFound {
				return cmd.NewErrNotFound(errors.New("Routes API is unavailable on this backend; deploy the Routes API before running harness setup"))
			}
			if response.StatusCode != http.StatusOK {
				return cmd.WrapHTTPStatus(response.StatusCode, fmt.Errorf("GET /v1/routes returned HTTP %d", response.StatusCode))
			}
			decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
			if err := decoder.Decode(&page); err != nil {
				return cmd.NewErrServer(errors.New("invalid Routes API response"))
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF || page.Pagination == nil || page.Items == nil {
				return cmd.NewErrServer(errors.New("invalid Routes API response"))
			}
			return nil
		}()
		if err != nil {
			return nil, err
		}
		for _, route := range page.Items {
			if route.ID == "" || route.Name == "" || route.DisplayName == "" || route.InvokeURL == "" || route.TeamID != teamID || names[route.Name] {
				return nil, cmd.NewErrServer(errors.New("Routes API returned incomplete, duplicate, or out-of-team Routes"))
			}
			names[route.Name] = true
			routes = append(routes, route)
		}
		if !page.Pagination.HasMore {
			break
		}
		cursor = page.Pagination.Cursor
		if cursor == "" || cursors[cursor] {
			return nil, cmd.NewErrServer(errors.New("Routes API returned an invalid pagination cursor"))
		}
		cursors[cursor] = true
	}
	if len(routes) == 0 {
		return nil, cmd.NewErrValidation(errors.New("no accessible Routes in the selected team; create a Route before running harness setup"))
	}
	return routes, nil
}

// harnessCatalog converts each Route's own metadata into the internal catalog.
// Routes with null or unusable metadata are skipped; a selected primary Route
// with null metadata is a hard error.
func harnessCatalog(listed []harnessRouteRecord, primary string) ([]harness.Route, []string, error) {
	var routes []harness.Route
	var skipped []string
	for _, route := range listed {
		if route.Metadata == nil {
			if route.Name == primary {
				return nil, nil, cmd.NewErrValidation(fmt.Errorf("Route %q has no model metadata; set it with: baseten route update --name %s --metadata <slug>", route.Name, route.Name))
			}
			skipped = append(skipped, route.Name)
			continue
		}
		m := route.Metadata
		r := harness.Route{
			Name: route.Name, DisplayName: route.DisplayName,
			ContextWindow: m.ContextWindow, OutputLimit: m.MaxOutputTokens,
			InputModalities: m.InputModalities, Tools: m.Tools,
			ReasoningLevels: m.ReasoningLevels, ParallelTools: m.ParallelToolCalls,
			Messages:        m.SupportedAPIFormats.Messages,
			Responses:       m.SupportedAPIFormats.Responses,
			ChatCompletions: m.SupportedAPIFormats.ChatCompletions,
		}
		if _, err := harness.ValidateCatalog([]harness.Route{r}); err != nil {
			skipped = append(skipped, route.Name)
			continue
		}
		routes = append(routes, r)
	}
	if len(routes) == 0 {
		return nil, skipped, cmd.NewErrValidation(errors.New("no accessible Routes have model metadata; set one with: baseten route update --name <route> --metadata <slug>"))
	}
	return routes, skipped, nil
}
