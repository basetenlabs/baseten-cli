// Package sandbox is a high-level client for Baseten sandboxes: creating,
// finding, updating, and deleting sandboxes on the control plane, the images
// they are created from, and running commands in one sandbox through its
// execution API.
//
// It exchanges an API key for the short-lived bearer token the sandbox APIs
// require, caches it until shortly before it expires, and re-authenticates
// when the server revokes it. Both generated clients stay reachable through
// RawAPI for operations this package does not wrap.
//
// The generated API surfaces are not covered by Go compatibility guarantees
// and may change between versions.
package sandboxclient

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/basetenlabs/baseten-go/client"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// SandboxesClientOptions configures a SandboxesClient.
type SandboxesClientOptions struct {
	// APIKey is the Baseten API key exchanged for the sandbox token. Empty
	// with TokenProvider unset is the advanced opt-out of authentication.
	APIKey string

	// TokenProvider returns the bearer token for each request, instead of
	// one minted from the API key, which must then be empty.
	TokenProvider TokenProvider

	// TeamID is the team to act in. Empty uses the caller's only accessible
	// team, and fails on the server for callers in more than one team.
	TeamID string

	// ManagementBaseURL overrides the management API base URL. Empty uses
	// "https://api.baseten.co".
	ManagementBaseURL string

	// SandboxesBaseURL overrides the base URL for the /v1/sandboxes control
	// plane. Empty uses ManagementBaseURL. The token exchange always stays on
	// the management API; this exists for routing sandbox traffic to a
	// different domain during development.
	SandboxesBaseURL string

	// HTTPClient overrides the default client for both planes. When nil,
	// http.DefaultClient is used.
	HTTPClient HTTPDoer

	// Headers are sent on every request. The map is cloned at construction,
	// so later mutations by the caller do not affect the live client.
	Headers http.Header
}

// CreateSandboxRequest creates a sandbox. Zero values are omitted from the
// request, so the server applies its defaults: baseten/base-image:latest,
// 4096 MB, and the closest region.
type CreateSandboxRequest struct {
	// Name is the unique sandbox name. The server generates one when empty.
	Name string

	// Image is the image reference including its tag.
	Image string

	// Memory is the allocation in megabytes, which also sets the CPU
	// allocation. Zero uses the server default.
	Memory int

	// Region is where the sandbox runs. Empty picks the closest region.
	Region string

	// Envs are the environment variables injected into the sandbox.
	Envs map[string]SandboxEnvValue

	// Labels are the key-value pairs for organizing and filtering.
	Labels map[string]string

	// ExternalID is the caller-owned identifier for external lookups.
	ExternalID string

	// CreateIfNotExist returns the existing live sandbox with this name
	// instead of conflicting, or recreates one that is failed, terminated,
	// or being deleted. Requires Name.
	CreateIfNotExist bool
}

// UpdateSandboxRequest updates a sandbox. The API is a partial update: unset
// fields stay unchanged, and set slices and maps replace their previous
// values. Name, memory, and network are immutable and rejected by the server.
type UpdateSandboxRequest struct {
	// Enabled false cuts the sandbox off from every connection until it is
	// set back. This is an access cutoff, not lifecycle management:
	// sandboxes autosleep on their own, and this field is not their
	// wake control. Nil leaves it unchanged.
	Enabled *bool

	// Envs replace the sandbox's environment variables. Nil leaves them
	// unchanged.
	Envs map[string]SandboxEnvValue

	// Labels replace the sandbox's labels. Nil leaves them unchanged.
	Labels map[string]string

	// Image is the image reference including its tag. Empty leaves it
	// unchanged.
	Image string

	// ExternalID is the caller-owned identifier for external lookups. Empty
	// leaves it unchanged.
	ExternalID string
}

// ListSandboxesRequest filters a sandbox listing.
type ListSandboxesRequest struct {
	// Query searches indexed sandbox names and labels.
	Query string

	// Statuses keeps only sandboxes with one of these statuses. Cannot be
	// combined with ExternalID.
	Statuses []SandboxStatus

	// ExternalID keeps only the sandbox with this external identifier.
	// Cannot be combined with Statuses.
	ExternalID string

	// PageSize is how many sandboxes to fetch per underlying request. Zero
	// uses the server default.
	PageSize int
}

// SandboxesClient works with sandboxes on the control plane.
type SandboxesClient struct {
	options    SandboxesClientOptions
	tokens     *tokenSource
	headers    http.Header
	httpClient HTTPDoer
	api        *managementapi.Client
}

// NewSandboxesClient creates a SandboxesClient without making a network
// request.
func NewSandboxesClient(opts SandboxesClientOptions) (*SandboxesClient, error) {
	managementBaseURL := strings.TrimRight(opts.ManagementBaseURL, "/")
	if managementBaseURL == "" {
		managementBaseURL = "https://api.baseten.co"
	}
	sandboxesBaseURL := strings.TrimRight(opts.SandboxesBaseURL, "/")
	if sandboxesBaseURL == "" {
		sandboxesBaseURL = managementBaseURL
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = newDefaultHTTPClient()
	}
	tokens, err := newTokenSource(opts.APIKey, opts.TokenProvider, managementBaseURL, httpClient, opts.Headers)
	if err != nil {
		return nil, err
	}
	headers := opts.Headers.Clone()
	if headers == nil {
		headers = http.Header{}
	}
	client.ApplyUserAgentHeader(headers)
	// Resolved once: each sandbox built from this client shares the same
	// connection pool instead of cloning its own transport.
	return &SandboxesClient{
		options:    opts,
		tokens:     tokens,
		headers:    headers,
		httpClient: httpClient,
		api: &managementapi.Client{
			BaseURL:    sandboxesBaseURL,
			HTTPClient: &tokenAuthClient{inner: httpClient, tokens: tokens},
			Headers:    headers,
		},
	}, nil
}

// Options returns the options this client was constructed with.
func (c *SandboxesClient) Options() SandboxesClientOptions {
	return c.options
}

// RawAPI returns the generated control-plane client, authenticated for
// sandboxes, for operations this package does not wrap. Its generated surface
// may change between versions.
func (c *SandboxesClient) RawAPI() *managementapi.Client {
	return c.api
}

// Create creates a sandbox. The Info of the returned Sandbox is the record as of
// creation; the sandbox reaches it URL and DEPLOYED status asynchronously.
func (c *SandboxesClient) Create(ctx context.Context, request *CreateSandboxRequest) (*Sandbox, error) {
	body := managementapi.CreateSandboxRequest{
		Name:              optionalString(request.Name),
		CreateIfNotExists: optionalBool(request.CreateIfNotExist),
		Image:             optionalString(request.Image),
		Memory:            optionalInt(request.Memory),
		Region:            optionalString(request.Region),
		Envs:              envsToAPI(request.Envs),
		Labels:            labelsToAPI(request.Labels),
		ExternalId:        optionalString(request.ExternalID),
	}
	created, err := c.api.CreateSandbox(ctx, managementapi.CreateSandboxParams{TeamId: c.teamID()}, body)
	if err != nil {
		return nil, toSandboxAPIError(err, "control")
	}
	// The record as of creation is usually DEPLOYING and without its URL,
	// so the Sandbox is built without the URL requirement that working in
	// one needs; exec on it fails loudly until the record has one.
	info, err := sandboxInfoFromAPI(created)
	if err != nil {
		return nil, err
	}
	return newSandbox(sandboxOptions{
		info:       info,
		tokens:     c.tokens,
		httpClient: c.httpClient,
		headers:    c.headers,
	}), nil
}

// GetInfoOptions tunes GetInfo. Nil applies every default.
type GetInfoOptions struct {
	// ShowSecrets reveals environment variable values. Requires the
	// workspace administrator role; other callers receive masked values
	// even when true.
	ShowSecrets bool
}

// GetInfo gets a sandbox's current record.
func (c *SandboxesClient) GetInfo(ctx context.Context, name string, opts *GetInfoOptions) (*SandboxInfo, error) {
	params := managementapi.GetSandboxParams{TeamId: c.teamID()}
	if opts != nil && opts.ShowSecrets {
		params.ShowSecrets = &opts.ShowSecrets
	}
	record, err := c.api.GetSandbox(ctx, name, params)
	if err != nil {
		return nil, toSandboxAPIError(err, "control")
	}
	info, err := sandboxInfoFromAPI(record)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// Get gets a Sandbox to work in, fetching the sandbox's record first.
func (c *SandboxesClient) Get(ctx context.Context, name string) (*Sandbox, error) {
	info, err := c.GetInfo(ctx, name, nil)
	if err != nil {
		return nil, err
	}
	return c.SandboxFromInfo(*info)
}

// SandboxFromInfo builds a Sandbox from a record already in hand, making no
// call. The record must carry the sandbox's URL.
func (c *SandboxesClient) SandboxFromInfo(info SandboxInfo) (*Sandbox, error) {
	if info.URL == "" {
		return nil, fmt.Errorf("sandbox %s has no URL yet", info.Name)
	}
	return newSandbox(sandboxOptions{
		info:       info,
		tokens:     c.tokens,
		httpClient: c.httpClient,
		headers:    c.headers,
	}), nil
}

// Update updates a sandbox and returns its new record.
func (c *SandboxesClient) Update(ctx context.Context, name string, request *UpdateSandboxRequest) (*SandboxInfo, error) {
	body := managementapi.UpdateSandboxRequest{
		Enabled:     request.Enabled,
		Envs:        envsToAPI(request.Envs),
		Labels:      labelsToAPI(request.Labels),
		Image:       optionalString(request.Image),
		ExternalId:  optionalString(request.ExternalID),
	}
	updated, err := c.api.UpdateSandbox(ctx, name, managementapi.UpdateSandboxParams{TeamId: c.teamID()}, body)
	if err != nil {
		return nil, toSandboxAPIError(err, "control")
	}
	info, err := sandboxInfoFromAPI(updated)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// Delete deletes a sandbox. Deletion continues after this returns, so the
// returned record usually shows the sandbox as still deleting.
func (c *SandboxesClient) Delete(ctx context.Context, name string) (*SandboxInfo, error) {
	record, err := c.api.DeleteSandbox(ctx, name, managementapi.DeleteSandboxParams{TeamId: c.teamID()})
	if err != nil {
		return nil, toSandboxAPIError(err, "control")
	}
	info, err := sandboxInfoFromAPI(record)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// List lists sandboxes, fetching further pages as iteration reaches them. An
// error yields as the pair's second value and ends the iteration.
func (c *SandboxesClient) List(ctx context.Context, request *ListSandboxesRequest) iter.Seq2[*SandboxInfo, error] {
	return func(yield func(*SandboxInfo, error) bool) {
		params := managementapi.ListSandboxesParams{TeamId: c.teamID()}
		if request.Query != "" {
			params.Q = &request.Query
		}
		if len(request.Statuses) > 0 {
			statuses := request.Statuses
			params.Status = &statuses
		}
		if request.ExternalID != "" {
			params.ExternalId = &request.ExternalID
		}
		if request.PageSize > 0 {
			params.Limit = &request.PageSize
		}
		// A server repeating a cursor would otherwise page forever.
		seenCursors := make(map[string]bool)
		var cursor *string
		for {
			params.Cursor = cursor
			page, err := c.api.ListSandboxes(ctx, params)
			if err != nil {
				yield(nil, toSandboxAPIError(err, "control"))
				return
			}
			for i := range page.Items {
				info, err := sandboxInfoFromAPI(&page.Items[i])
				if err != nil {
					yield(nil, err)
					return
				}
				if !yield(&info, nil) {
					return
				}
			}
			if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
				return
			}
			cursor = page.Pagination.Cursor
			if seenCursors[*cursor] {
				yield(nil, fmt.Errorf("sandbox list returned a repeated cursor"))
				return
			}
			seenCursors[*cursor] = true
		}
	}
}

// teamID returns the options' team selector, nil to use the caller's only
// accessible team.
func (c *SandboxesClient) teamID() *string {
	if c.options.TeamID == "" {
		return nil
	}
	teamID := c.options.TeamID
	return &teamID
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalInt(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}

func optionalBool(value bool) *bool {
	if !value {
		return nil
	}
	return &value
}
