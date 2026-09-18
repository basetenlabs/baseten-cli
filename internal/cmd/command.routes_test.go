package cmd_test

import (
	"encoding/json"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"net/http"
	"net/http/httptest"
	"testing"

	public "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/stretchr/testify/require"
)

func Test_Routes_List_Pagination(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls++
		require.Equal(t, "/v1/routes", r.URL.Path)
		require.Equal(t, "team-a", r.URL.Query().Get("team_id"))
		require.Equal(t, "Bearer mock", r.Header.Get("Authorization"))
		if calls == 1 {
			json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": true, "cursor": "next"}})
			return
		}
		require.Equal(t, "next", r.URL.Query().Get("cursor"))
		json.NewEncoder(w).Encode(map[string]any{"items": []internalcmd.RouteRecordForTest{{Id: "r", Name: "acme/primary", TeamId: "team-a", DisplayName: "Primary", InvokeUrl: "https://coding.baseten.co"}}, "pagination": map[string]any{"has_more": false, "cursor": nil}})
	}))
	defer server.Close()
	routes, err := internalcmd.ReadRoutesForTest(t.Context(), &managementapi.Client{HTTPClient: server.Client(), BaseURL: server.URL, Headers: http.Header{"Authorization": []string{"Bearer mock"}}}, "team-a")
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, 2, calls)
}

func Test_Routes_List_RejectsInvalidPages(t *testing.T) {
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
		{"wrong team", 200, map[string]any{"items": []internalcmd.RouteRecordForTest{{Id: "r", Name: "acme/primary", TeamId: "other", DisplayName: "Primary", InvokeUrl: "https://coding.baseten.co"}}, "pagination": map[string]any{"has_more": false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(tc.body)
			}))
			defer server.Close()
			_, err := internalcmd.ReadRoutesForTest(t.Context(), &managementapi.Client{HTTPClient: server.Client(), BaseURL: server.URL}, "team-a")
			require.Error(t, err)
		})
	}
}

func Test_Routes_Key_Create(t *testing.T) {
	t.Setenv("BASETEN_API_KEY", "test-login")
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())
	session, err := auth.ResolveSession("", "")
	require.NoError(t, err)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls++
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/api_keys", r.URL.Path)
		require.Equal(t, "Bearer test-login", r.Header.Get("Authorization"))
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, map[string]string{"type": "ROUTES", "name": "test-key", "team_id": "team-a"}, body)
		json.NewEncoder(w).Encode(map[string]string{"api_key": "test-route-secret"})
	}))
	defer server.Close()
	key, err := internalcmd.CreateRoutesKeyForTest(t.Context(), &auth.Transport{Session: session}, server.URL, "test-key", "team-a")
	require.NoError(t, err)
	require.Equal(t, "test-route-secret", key)
	require.Equal(t, 1, calls)
}

func Test_Routes_Key_RejectionPreservesTypeAndRedacts(t *testing.T) {
	t.Setenv("BASETEN_API_KEY", "test-login")
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())
	session, err := auth.ResolveSession("", "")
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"message": "invalid test-login", "api_key": "response-secret"})
	}))
	defer server.Close()
	_, err = internalcmd.CreateRoutesKeyForTest(t.Context(), &auth.Transport{Session: session}, server.URL, "test-key", "team-a")
	require.ErrorIs(t, err, auth.ErrRoutesKeyRejected)
	var typed *public.ErrAuth
	require.ErrorAs(t, err, &typed)
	require.NotContains(t, err.Error(), "test-login")
	require.NotContains(t, err.Error(), "response-secret")
}
