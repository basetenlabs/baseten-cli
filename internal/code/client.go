package code

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-go/client"
)

// Catalog deliberately consumes only the established OpenAI-compatible list
// envelope. Capabilities and Route-specific schemas are not yet a contract;
// callers must not infer protocol support from a model's name.
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}
type Catalog struct {
	Data          []Model   `json:"data"`
	FetchedAt     time.Time `json:"fetched_at"`
	Compatibility string    `json:"compatibility,omitempty"`
}
type Client struct {
	Token     string
	Transport http.RoundTripper
}

func (c *Client) request(ctx context.Context, method, path string, body any, stream bool) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, Endpoint+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	client.ApplyUserAgentHeader(req.Header)
	// Never follow redirects with Code credentials, including same-origin
	// redirects. The fixed gateway is the only credential destination.
	hc := &http.Client{Transport: c.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, errors.New("Code gateway request failed; check connectivity and retry")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Code gateway returned HTTP %d; check Code credential access and Route availability", resp.StatusCode)
	}
	if stream && !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return nil, errors.New("gateway did not return an event stream")
	}
	const limit = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("reading Code gateway response failed")
	}
	if len(data) > limit {
		return nil, errors.New("Code gateway response exceeded 8 MiB")
	}
	return data, nil
}
func (c *Client) Models(ctx context.Context) (*Catalog, error) {
	raw, err := c.request(ctx, "GET", "/v1/models", nil, false)
	if err != nil {
		return nil, err
	}
	var result Catalog
	if err := json.Unmarshal(raw, &result); err != nil || result.Data == nil {
		return nil, errors.New("gateway catalog must contain a data array; check the Route catalog backend contract")
	}
	seen := map[string]bool{}
	for _, m := range result.Data {
		if m.ID == "" || seen[m.ID] {
			return nil, errors.New("gateway catalog contains missing or duplicate model IDs")
		}
		seen[m.ID] = true
	}
	result.FetchedAt = time.Now().UTC()
	return &result, nil
}
func (c *Catalog) Has(id string) bool {
	for _, m := range c.Data {
		if m.ID == id {
			return true
		}
	}
	return false
}
