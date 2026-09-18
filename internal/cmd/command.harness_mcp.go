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

// listHarnessMCPServers returns the team's registered MCP servers. The backend
// endpoint is not deployed everywhere yet, so a missing route means "no
// servers", not an error.
func listHarnessMCPServers(ctx *CommandContext, teamID string) ([]harness.MCPServer, error) {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return nil, err
	}
	api := cl.API()
	return readMCPServers(ctx, api.HTTPClient, api.BaseURL, api.Headers, teamID)
}

func readMCPServers(ctx context.Context, client interface {
	Do(*http.Request) (*http.Response, error)
}, baseURL string, headers http.Header, teamID string) ([]harness.MCPServer, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var servers []harness.MCPServer
	cursor := ""
	cursors := map[string]bool{}
	names := map[string]bool{}
	for {
		query := url.Values{"team_id": {teamID}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/mcp_servers?"+query.Encode(), nil)
		if err != nil {
			return nil, errors.New("invalid MCP servers API endpoint")
		}
		req.Header = headers.Clone()
		response, err := client.Do(req)
		if err != nil {
			return nil, cmd.NewErrServer(errors.New("listing MCP servers failed; check your login and connection"))
		}
		if response.StatusCode == http.StatusNotFound {
			response.Body.Close()
			return nil, nil
		}
		var page struct {
			Servers    []harness.MCPServer `json:"mcp_servers"`
			NextCursor string              `json:"next_cursor"`
		}
		err = func() error {
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				return cmd.WrapHTTPStatus(response.StatusCode, fmt.Errorf("GET /v1/mcp_servers returned HTTP %d", response.StatusCode))
			}
			decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
			if err := decoder.Decode(&page); err != nil {
				return cmd.NewErrServer(errors.New("invalid MCP servers API response"))
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF || page.Servers == nil {
				return cmd.NewErrServer(errors.New("invalid MCP servers API response"))
			}
			return nil
		}()
		if err != nil {
			return nil, err
		}
		for _, server := range page.Servers {
			if server.Name == "" || server.URL == "" || names[server.Name] {
				return nil, cmd.NewErrServer(errors.New("MCP servers API returned incomplete or duplicate MCP servers"))
			}
			names[server.Name] = true
			servers = append(servers, server)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if cursors[cursor] {
			return nil, cmd.NewErrServer(errors.New("MCP servers API returned an invalid pagination cursor"))
		}
		cursors[cursor] = true
	}
	return servers, nil
}
