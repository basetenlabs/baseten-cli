package code

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body, kind string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{kind}}, Body: io.NopCloser(strings.NewReader(body))}
}
func event(v any) string { b, _ := json.Marshal(v); return "data: " + string(b) + "\n\n" }
func responsesStream(output []any) string {
	return event(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": output}})
}
func messagesStream(block map[string]any) string {
	return event(map[string]any{"type": "content_block_start", "index": 0, "content_block": block}) + event(map[string]any{"type": "message_stop"})
}
func TestCatalogContractAndErrors(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		ok     bool
	}{{`{"data":[]}`, 200, true}, {`{"data":[{"id":"plain-route"}]}`, 200, true}, {`{"models":[]}`, 200, false}, {`{"data":[{"id":"x"},{"id":"x"}]}`, 200, false}, {`{"data":[{}]}`, 200, false}, {`{"data":null}`, 200, false}, {`secret`, 401, false}, {`secret`, 403, false}, {`secret`, 500, false}} {
		t.Run(fmt.Sprint(tc.status, tc.body), func(t *testing.T) {
			cl := Client{Token: "secret", Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, Endpoint+"/v1/models", r.URL.String())
				require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
				return response(tc.status, tc.body, "application/json"), nil
			})}
			catalog, err := cl.Models(t.Context())
			if tc.ok {
				require.NoError(t, err)
				require.NotNil(t, catalog.Data)
				require.False(t, catalog.FetchedAt.IsZero())
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret")
			}
		})
	}
}
func TestCatalogNeverFollowsRedirect(t *testing.T) {
	calls := 0
	cl := Client{Token: "secret", Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := response(302, "", "application/json")
		resp.Header.Set("Location", "https://other.example/models")
		return resp, nil
	})}
	_, err := cl.Models(t.Context())
	require.ErrorContains(t, err, "302")
	require.Equal(t, 1, calls)
}
func TestProbeStreamingAndToolReplay(t *testing.T) {
	for _, h := range []string{"codex", "claude-code"} {
		t.Run(h, func(t *testing.T) {
			count := 0
			cl := Client{Token: "secret", Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				count++
				require.Equal(t, "POST", r.Method)
				require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
				require.Empty(t, r.Header.Get("x-baseten-web-search-provider-priority"))
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, "engineering", body["model"])
				require.Equal(t, true, body["stream"])
				if h == "codex" {
					require.Equal(t, "/v1/responses", r.URL.Path)
				} else {
					require.Equal(t, "/v1/messages", r.URL.Path)
					require.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
				}
				if count == 1 {
					if h == "codex" {
						return response(200, responsesStream([]any{map[string]any{"type": "function_call", "name": probeTool, "call_id": "call-1", "arguments": "{}"}}), "text/event-stream"), nil
					}
					return response(200, messagesStream(map[string]any{"type": "tool_use", "name": probeTool, "id": "call-1", "input": map[string]any{}}), "text/event-stream"), nil
				}
				var marker string
				if h == "codex" {
					input := body["input"].([]any)
					result := input[len(input)-1].(map[string]any)
					require.Equal(t, "call-1", result["call_id"])
					marker = result["output"].(string)
					return response(200, responsesStream([]any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": marker}}}}), "text/event-stream"), nil
				}
				messages := body["messages"].([]any)
				result := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
				require.Equal(t, "call-1", result["tool_use_id"])
				marker = result["content"].(string)
				return response(200, event(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})+event(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": marker}})+event(map[string]any{"type": "message_stop"}), "text/event-stream"), nil
			})}
			require.NoError(t, cl.Probe(t.Context(), h, "engineering"))
			require.Equal(t, 2, count)
		})
	}
}
func TestProbeRejectsIncompleteAndFailedStreams(t *testing.T) {
	for _, h := range []string{"codex", "claude-code"} {
		for _, stream := range []string{"", `data: {}`, "data: {}\n\n", event(map[string]any{"type": "error", "error": "secret"}), event(map[string]any{"type": "response.incomplete"})} {
			_, _, err := parseProbeStream(h, []byte(stream))
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
		}
	}
	cl := Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return response(200, `{"ok":true}`, "application/json"), nil
	})}
	require.ErrorContains(t, cl.Probe(t.Context(), "codex", "route"), "event stream")
}
func TestProbeRejectsMissingToolAndWrongReplay(t *testing.T) {
	cl := Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return response(200, responsesStream([]any{}), "text/event-stream"), nil
	})}
	require.ErrorContains(t, cl.Probe(t.Context(), "codex", "route"), "tool call")
	count := 0
	cl.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		count++
		output := []any{map[string]any{"type": "function_call", "name": probeTool, "call_id": "id", "arguments": "{}"}}
		if count > 1 {
			output = []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "unrelated"}}}}
		}
		return response(200, responsesStream(output), "text/event-stream"), nil
	})
	require.ErrorContains(t, cl.Probe(t.Context(), "codex", "route"), "did not replay")
}
func TestSSEMultilineDataAndComments(t *testing.T) {
	events, err := streamEvents([]byte(":keepalive\n\ndata: {\ndata: \"type\":\"message_stop\"}\n\n"))
	require.NoError(t, err)
	require.Len(t, events, 1)
}
