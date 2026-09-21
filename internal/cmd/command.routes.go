package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// listRoutes follows pagination for both route commands and harness discovery.
func listRoutes(ctx context.Context, api *managementapi.Client, params managementapi.GetV1RoutesParams) ([]managementapi.Route, error) {
	var routes []managementapi.Route
	cursors := map[string]bool{}
	for {
		page, err := api.GetRoutes(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("listing routes: %w", err)
		}
		routes = append(routes, page.Items...)
		if !page.Pagination.HasMore {
			return routes, nil
		}
		cursor := page.Pagination.Cursor
		if cursor == nil || *cursor == "" || cursors[*cursor] {
			return nil, cmd.NewErrServer(errors.New("routes API returned an invalid pagination cursor"))
		}
		cursors[*cursor] = true
		params.Cursor = cursor
	}
}

// Creation is not known to be idempotent. Only definitive API rejections allow
// the credential store to clear its pending record and permit another attempt.
func createRoutesKey(ctx context.Context, api *managementapi.Client, token, name, teamID string) (string, error) {
	result, err := api.PostApiKeys(ctx, managementapi.CreateAPIKeyRequest{
		Type:   managementapi.APIKeyCategory_ROUTES,
		Name:   &name,
		TeamId: &teamID,
	})
	if err == nil {
		return result.ApiKey, nil
	}
	var response *managementapi.ResponseError
	if !errors.As(err, &response) {
		return "", cmd.NewErrServer(errors.New("routes key creation failed; check your login and connection"))
	}
	// Only retain the API's message, never arbitrary response details that may
	// include credentials. Keep the SDK error type for standard CLI classification.
	var detail struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal([]byte(response.Body), &detail)
	detail.Message = strings.ReplaceAll(detail.Message, token, "[REDACTED]")
	if len(detail.Message) > 2000 {
		detail.Message = detail.Message[:2000] + "..."
	}
	body, _ := json.Marshal(detail)
	safe := &managementapi.ResponseError{StatusCode: response.StatusCode, Body: string(body)}
	switch response.StatusCode {
	case 400, 401, 403, 404, 422:
		return "", cmd.WrapHTTPStatus(response.StatusCode, fmt.Errorf("%w: %w", auth.ErrRoutesKeyRejected, safe))
	default:
		return "", cmd.WrapHTTPStatus(response.StatusCode, safe)
	}
}
