package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// The types and call below stand in for baseten-go's generated
// GetRoutesHarnessConfigs, which doesn't exist until baseten-go is regenerated
// from a spec that includes GET /v1/routes/harness-configs. They mirror the
// generated names and shapes, so switching over means deleting this file and
// calling api.GetRoutesHarnessConfigs(ctx, managementapi.GetV1RoutesHarnessConfigsParams{TeamId: &teamID}).

type routeHarnessConfigsResponse struct {
	// HarnessConfigs is keyed by harness name, such as claude-code.
	HarnessConfigs map[string]routeHarnessConfig `json:"harness_configs"`
}

type routeHarnessConfig struct {
	// Models is keyed by role: primary or background.
	Models map[string]routeHarnessModel `json:"models"`
}

type routeHarnessModel struct {
	Route managementapi.Route `json:"route"`
	// Source is team when the team set the role, or baseten when it is
	// Baseten's default from the team's Model API routes.
	Source string `json:"source"`
}

// Harness default sources and roles, as the harness configs API reports them.
const (
	harnessSourceTeam    = "team"
	harnessSourceBaseten = "baseten"

	harnessRolePrimary    = "primary"
	harnessRoleBackground = "background"
)

// getRouteHarnessConfigs lists the default models for each harness that has at
// least one, using Baseten's default for any role the team hasn't set.
func getRouteHarnessConfigs(ctx context.Context, api *managementapi.Client, teamID string) (*routeHarnessConfigsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api.BaseURL+"/v1/routes/harness-configs?"+url.Values{"team_id": {teamID}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	for key, vals := range api.Headers {
		for _, val := range vals {
			req.Header.Add(key, val)
		}
	}
	resp, err := api.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &managementapi.ResponseError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	var result routeHarnessConfigsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decoding harness configs: %w", err)
	}
	return &result, nil
}
