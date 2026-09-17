package cmd_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/stretchr/testify/require"
)

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
	routes, endpoint, skipped, err := internalcmd.HarnessCatalogForTest(t.Context(), server.Client(), server.URL, []internalcmd.RouteRecordForTest{
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
	_, _, _, err := internalcmd.HarnessCatalogForTest(t.Context(), http.DefaultClient, "https://api.baseten.co", []internalcmd.RouteRecordForTest{{InvokeURL: "http://127.0.0.1:1234"}})
	require.ErrorContains(t, err, "unsupported invoke URL")
	_, _, _, err = internalcmd.HarnessCatalogForTest(t.Context(), http.DefaultClient, "https://api.baseten.co", []internalcmd.RouteRecordForTest{{InvokeURL: "https://example.com"}})
	require.ErrorContains(t, err, "unsupported invoke URL")
}
