package cmd_test

import (
	"encoding/json"
	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Harness_Setup_ReadHarnessRoutesPagination(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/v1/routes", r.URL.Path)
		require.Equal(t, "team-a", r.URL.Query().Get("team_id"))
		require.Equal(t, "Bearer mock", r.Header.Get("Authorization"))
		if calls == 1 {
			json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": true, "cursor": "next"}})
			return
		}
		require.Equal(t, "next", r.URL.Query().Get("cursor"))
		json.NewEncoder(w).Encode(map[string]any{"items": []internalcmd.HarnessRouteForTest{{ID: "r", Name: "acme/primary", TeamID: "team-a", DisplayName: "Primary", InvokeURL: "https://coding.baseten.co"}}, "pagination": map[string]any{"has_more": false, "cursor": nil}})
	}))
	defer server.Close()
	routes, err := internalcmd.ReadHarnessRoutesForTest(t.Context(), server.Client(), server.URL, http.Header{"Authorization": []string{"Bearer mock"}}, "team-a")
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, 2, calls)
}

func Test_Harness_Setup_ReadHarnessRoutesRejectsInvalidPages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   any
	}{
		{"not deployed", 404, map[string]any{}},
		{"denied", 403, map[string]any{}},
		{"empty", 200, map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": false}}},
		{"missing cursor", 200, map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": true}}},
		{"repeated cursor", 200, map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": true, "cursor": "repeat"}}},
		{"wrong team", 200, map[string]any{"items": []internalcmd.HarnessRouteForTest{{ID: "r", Name: "acme/primary", TeamID: "other", DisplayName: "Primary", InvokeURL: "https://coding.baseten.co"}}, "pagination": map[string]any{"has_more": false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(tc.body)
			}))
			defer server.Close()
			_, err := internalcmd.ReadHarnessRoutesForTest(t.Context(), server.Client(), server.URL, nil, "team-a")
			require.Error(t, err)
		})
	}
}

func Test_Harness_Setup_HarnessCatalogJoinsRouteNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		require.Empty(t, r.Header.Get("X-Baseten-Client"))
		json.NewEncoder(w).Encode(map[string]any{"data": []any{
			map[string]any{"id": "acme/primary", "context_length": 128000, "max_completion_tokens": 4096, "supported_features": []string{"tools", "reasoning"}, "input_modalities": []string{"text", "image"}},
			map[string]any{"id": "other-team/route", "context_length": 128000, "max_completion_tokens": 4096, "supported_features": []string{"tools"}, "input_modalities": []string{"text"}},
		}})
	}))
	defer server.Close()
	routes, endpoint, skipped, err := internalcmd.HarnessCatalogForTest(t.Context(), server.Client(), server.URL, []internalcmd.HarnessRouteForTest{
		{Name: "acme/primary", DisplayName: "Engineering", InvokeURL: server.URL},
		{Name: "acme/provider", DisplayName: "Provider", InvokeURL: server.URL},
	})
	require.NoError(t, err)
	require.Equal(t, server.URL, endpoint)
	require.Equal(t, []string{"acme/provider"}, skipped)
	require.Len(t, routes, 1)
	require.Equal(t, "acme/primary", routes[0].Name)
	require.Equal(t, "Engineering", routes[0].DisplayName)
	require.Equal(t, 128000, routes[0].ContextWindow)
	require.Equal(t, 4096, routes[0].OutputLimit)
	require.Empty(t, routes[0].ReasoningLevels)
	require.False(t, routes[0].ParallelTools)
}

func Test_Harness_Setup_HarnessCatalogRejectsUntrustedInvokeURL(t *testing.T) {
	_, _, _, err := internalcmd.HarnessCatalogForTest(t.Context(), http.DefaultClient, "https://api.baseten.co", []internalcmd.HarnessRouteForTest{{InvokeURL: "http://127.0.0.1:1234"}})
	require.ErrorContains(t, err, "unsupported invoke URL")
	_, _, _, err = internalcmd.HarnessCatalogForTest(t.Context(), http.DefaultClient, "https://api.baseten.co", []internalcmd.HarnessRouteForTest{{InvokeURL: "https://example.com"}})
	require.ErrorContains(t, err, "unsupported invoke URL")
}
