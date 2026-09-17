package cmd

import (
	"bytes"
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
	"github.com/basetenlabs/baseten-cli/internal/auth"
)

// routeRecord follows RouteV1 from the Routes management API.
// Target details are intentionally not treated as model capability metadata.
type routeRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TeamID      string `json:"team_id"`
	DisplayName string `json:"display_name"`
	InvokeURL   string `json:"invoke_url"`
}

func readRoutes(ctx context.Context, client interface {
	Do(*http.Request) (*http.Response, error)
}, baseURL string, headers http.Header, teamID string) ([]routeRecord, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var routes []routeRecord
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
			Items      []routeRecord `json:"items"`
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

// The pinned management SDK's CreateAPIKeyRequest does not yet include team_id.
// Keep this compatibility request narrow until the SDK exposes that field.
// Only the API error message is surfaced, never raw bodies or details. Never retry
// this POST automatically: creation is not known to be idempotent.
func createRoutesKey(ctx context.Context, transport *auth.Transport, endpoint, name, teamID string) (string, error) {
	body := struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		TeamID string `json:"team_id"`
	}{Type: "ROUTES", Name: name, TeamID: teamID}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/api_keys", bytes.NewReader(encoded))
	if err != nil {
		return "", errors.New("invalid Routes key API request")
	}
	// Resolve once so error redaction uses the exact credential sent, even if
	// an OAuth token expires while the request is in flight.
	token, err := transport.Credential(ctx)
	if err != nil {
		return "", cmd.NewErrAuth(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := transport.Do(req)
	if err != nil {
		return "", cmd.NewErrServer(errors.New("Routes key API request failed; check your login and connection"))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Match the REST ApiError contract. Do not dump arbitrary JSON fields,
		// HTML errors, or the response to a successful credential request.
		var apiError struct {
			Message string `json:"message"`
		}
		detail := ""
		if json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&apiError) == nil && apiError.Message != "" {
			message := apiError.Message
			// A faulty server can echo the authentication header in its error.
			if token != "" {
				message = strings.ReplaceAll(message, token, "[REDACTED]")
			}
			if len(message) > 2000 {
				message = message[:2000] + "..."
			}
			detail = fmt.Sprintf(": %q", message)
		}
		switch resp.StatusCode {
		case 400, 401, 403, 404, 422:
			return "", cmd.WrapHTTPStatus(resp.StatusCode, fmt.Errorf("%w: POST /v1/api_keys returned HTTP %d%s", auth.ErrRoutesKeyRejected, resp.StatusCode, detail))
		}
		return "", cmd.WrapHTTPStatus(resp.StatusCode, fmt.Errorf("Routes key API: POST /v1/api_keys returned HTTP %d%s", resp.StatusCode, detail))
	}
	var result struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", cmd.NewErrServer(errors.New("invalid Routes key API response"))
	}
	return result.APIKey, nil
}
