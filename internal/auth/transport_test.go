package auth_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func newOAuthConfig(tokenURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: "baseten-cli",
		Endpoint: oauth2.Endpoint{TokenURL: tokenURL},
	}
}

// echoServer captures the last request and replies with an empty 200.
type echoServer struct {
	*httptest.Server
	LastAuthHeader string
}

func newEchoServer(t *testing.T) *echoServer {
	t.Helper()
	es := &echoServer{}
	es.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		es.LastAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(es.Close)
	return es
}

func mustRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	return req
}

func TestTransport_EnvAPIKeyOverridesStoredCreds(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, "stored-key", nil))

	es := newEchoServer(t)
	tr := &auth.Transport{Store: s, Host: testHost, EnvAPIKey: "env-key"}

	resp, err := tr.Do(mustRequest(t, es.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "Api-Key env-key", es.LastAuthHeader)
}

func TestTransport_APIKeyInjected(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, apiKeyA, nil))

	es := newEchoServer(t)
	tr := &auth.Transport{Store: s, Host: testHost}

	resp, err := tr.Do(mustRequest(t, es.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "Api-Key "+apiKeyA, es.LastAuthHeader)
}

func TestTransport_OAuthBearerInjected(t *testing.T) {
	s := newKeyringStore(t)
	cred := auth.OAuthCredential{AccessToken: accessToken, RefreshToken: refreshToken}
	require.NoError(t, s.SetOAuthUser(testHost, userLabelA, cred, nil))

	es := newEchoServer(t)
	tr := &auth.Transport{
		Store:       s,
		Host:        testHost,
		OAuthConfig: newOAuthConfig("http://unused/token"),
	}

	resp, err := tr.Do(mustRequest(t, es.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "Bearer "+accessToken, es.LastAuthHeader)
}

func TestTransport_NotLoggedInErrors(t *testing.T) {
	s := newKeyringStore(t)
	tr := &auth.Transport{Store: s, Host: testHost}
	_, err := tr.Do(mustRequest(t, "http://127.0.0.1:1"))
	require.ErrorContains(t, err, "not logged in")
}

func TestTransport_APIKeyOnlyRejectsOAuth(t *testing.T) {
	s := newKeyringStore(t)
	cred := auth.OAuthCredential{AccessToken: accessToken, RefreshToken: refreshToken}
	require.NoError(t, s.SetOAuthUser(testHost, userLabelA, cred, nil))

	tr := &auth.Transport{
		Store:      s,
		Host:       testHost,
		APIKeyOnly: true,
	}
	_, err := tr.Do(mustRequest(t, "http://127.0.0.1:1"))
	require.ErrorContains(t, err, "requires an API key")
}

func TestTransport_APIKeyOnlyAllowsAPIKey(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, apiKeyA, nil))

	es := newEchoServer(t)
	tr := &auth.Transport{Store: s, Host: testHost, APIKeyOnly: true}
	resp, err := tr.Do(mustRequest(t, es.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "Api-Key "+apiKeyA, es.LastAuthHeader)
}

func TestTransport_OAuthRefreshRotatesStoredToken(t *testing.T) {
	s := newKeyringStore(t)
	// Blank access token with a refresh token forces oauth2 to refresh
	// immediately: the library treats a missing access token as invalid.
	require.NoError(t, s.SetOAuthUser(testHost, userLabelA,
		auth.OAuthCredential{AccessToken: "", RefreshToken: "old-refresh"}, nil))

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		require.Equal(t, "refresh_token", r.Form.Get("grant_type"))
		require.Equal(t, "old-refresh", r.Form.Get("refresh_token"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenSrv.Close()

	es := newEchoServer(t)
	cfg := newOAuthConfig(tokenSrv.URL)
	tr := &auth.Transport{Store: s, Host: testHost, OAuthConfig: cfg}

	resp, err := tr.Do(mustRequest(t, es.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "Bearer new-access", es.LastAuthHeader)

	stored, err := s.GetOAuthCredential(testHost, userLabelA)
	require.NoError(t, err)
	require.Equal(t, "new-access", stored.AccessToken)
	require.Equal(t, "new-refresh", stored.RefreshToken)
}

func TestTransport_OAuthRefreshFailureSurfacesError(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetOAuthUser(testHost, userLabelA,
		auth.OAuthCredential{AccessToken: "", RefreshToken: "bad-refresh"}, nil))

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	}))
	defer tokenSrv.Close()

	tr := &auth.Transport{
		Store:       s,
		Host:        testHost,
		OAuthConfig: newOAuthConfig(tokenSrv.URL),
	}
	_, err := tr.Do(mustRequest(t, "http://127.0.0.1:1"))
	require.ErrorContains(t, err, "refresh failed")
}

func TestTransport_CustomBaseUsed(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, apiKeyA, nil))

	called := false
	base := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     http.Header{},
			Request:    r,
		}, nil
	})
	tr := &auth.Transport{Store: s, Host: testHost, Base: base}
	req := mustRequest(t, "http://example.invalid/")
	resp, err := tr.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.True(t, called, "custom Base RoundTripper must be used")
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
