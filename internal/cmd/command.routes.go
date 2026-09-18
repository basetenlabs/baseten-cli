package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func readRoutes(ctx context.Context, api *managementapi.Client, teamID string) ([]managementapi.Route, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var routes []managementapi.Route
	params := managementapi.GetV1RoutesParams{TeamId: &teamID}
	cursors := map[string]bool{}
	names := map[string]bool{}
	for {
		page, err := api.GetRoutes(requestCtx, params)
		if err != nil {
			return nil, fmt.Errorf("listing Routes: %w", err)
		}
		for _, route := range page.Items {
			if route.Id == "" || route.Name == "" || route.DisplayName == "" || route.InvokeUrl == "" || route.TeamId != teamID || names[route.Name] {
				return nil, cmd.NewErrServer(errors.New("Routes API returned incomplete, duplicate, or out-of-team Routes"))
			}
			names[route.Name] = true
			routes = append(routes, route)
		}
		if !page.Pagination.HasMore {
			break
		}
		cursor := page.Pagination.Cursor
		if cursor == nil || *cursor == "" || cursors[*cursor] {
			return nil, cmd.NewErrServer(errors.New("Routes API returned an invalid pagination cursor"))
		}
		cursors[*cursor] = true
		params.Cursor = cursor
	}
	if len(routes) == 0 {
		return nil, cmd.NewErrValidation(errors.New("no accessible Routes in the selected team; create a Route before running harness setup"))
	}
	return routes, nil
}

// Creation is not known to be idempotent. Only definitive API rejections allow
// the credential store to clear its pending record and permit another attempt.
func createRoutesKey(ctx context.Context, transport *auth.Transport, endpoint, name, teamID string) (string, error) {
	token, err := transport.Credential(ctx)
	if err != nil {
		return "", cmd.NewErrAuth(err)
	}
	api := &managementapi.Client{
		BaseURL:    endpoint,
		HTTPClient: transport,
		Headers:    http.Header{"Authorization": []string{"Bearer " + token}},
	}
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
		return "", cmd.NewErrServer(errors.New("Routes key creation failed; check your login and connection"))
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
