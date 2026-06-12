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

// storeWithConfigDir points BASETEN_CONFIG_DIR at a temp dir and clears the
// env credential vars so ResolveSession reads the returned store.
func storeWithConfigDir(t *testing.T) *auth.Store {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("BASETEN_CONFIG_DIR", dir)
	t.Setenv("BASETEN_API_KEY", "")
	t.Setenv("BASETEN_PROFILE", "")
	return auth.NewStore(auth.StoreOptions{Dir: dir})
}

func mustResolve(t *testing.T, profileFlag string) *auth.Session {
	t.Helper()
	session, err := auth.ResolveSession(profileFlag)
	require.NoError(t, err)
	return session
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

func TestTransport_EphemeralAPIKeyWinsOverCurrentProfile(t *testing.T) {
	s := storeWithConfigDir(t)
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, "stored-key", true, nil))
	t.Setenv("BASETEN_API_KEY", "env-key")

	es := newEchoServer(t)
	tr := &auth.Transport{Session: mustResolve(t, "")}

	resp, err := tr.Do(mustRequest(t, es.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "Api-Key env-key", es.LastAuthHeader)
}

func TestTransport_APIKeyInjected(t *testing.T) {
	s := storeWithConfigDir(t)
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))

	es := newEchoServer(t)
	tr := &auth.Transport{Session: mustResolve(t, profileA)}

	resp, err := tr.Do(mustRequest(t, es.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "Api-Key "+apiKeyA, es.LastAuthHeader)
}

func TestTransport_OAuthBearerInjected(t *testing.T) {
	s := storeWithConfigDir(t)
	cred := auth.OAuthCredential{AccessToken: accessToken, RefreshToken: refreshToken}
	require.NoError(t, s.SetOAuthProfile(profileA, remoteURL, cred, true, nil))

	es := newEchoServer(t)
	tr := &auth.Transport{
		Session:     mustResolve(t, profileA),
		OAuthConfig: newOAuthConfig("http://unused/token"),
	}

	resp, err := tr.Do(mustRequest(t, es.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "Bearer "+accessToken, es.LastAuthHeader)
}

func TestTransport_NotLoggedInErrors(t *testing.T) {
	storeWithConfigDir(t)
	tr := &auth.Transport{Session: mustResolve(t, "")}
	_, err := tr.Do(mustRequest(t, "http://127.0.0.1:1"))
	require.ErrorContains(t, err, "not logged in")
}

func TestTransport_OAuthRefreshRotatesStoredToken(t *testing.T) {
	s := storeWithConfigDir(t)
	// Blank access token with a refresh token forces oauth2 to refresh
	// immediately: the library treats a missing access token as invalid.
	require.NoError(t, s.SetOAuthProfile(profileA, remoteURL,
		auth.OAuthCredential{AccessToken: "", RefreshToken: "old-refresh"}, true, nil))

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
	tr := &auth.Transport{Session: mustResolve(t, profileA), OAuthConfig: newOAuthConfig(tokenSrv.URL)}

	resp, err := tr.Do(mustRequest(t, es.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "Bearer new-access", es.LastAuthHeader)

	stored, err := s.GetOAuthCredential(profileA)
	require.NoError(t, err)
	require.Equal(t, "new-access", stored.AccessToken)
	require.Equal(t, "new-refresh", stored.RefreshToken)
}

func TestTransport_OAuthRefreshFailureSurfacesError(t *testing.T) {
	s := storeWithConfigDir(t)
	require.NoError(t, s.SetOAuthProfile(profileA, remoteURL,
		auth.OAuthCredential{AccessToken: "", RefreshToken: "bad-refresh"}, true, nil))

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	}))
	defer tokenSrv.Close()

	tr := &auth.Transport{
		Session:     mustResolve(t, profileA),
		OAuthConfig: newOAuthConfig(tokenSrv.URL),
	}
	_, err := tr.Do(mustRequest(t, "http://127.0.0.1:1"))
	require.ErrorContains(t, err, "refresh failed")
}

func TestTransport_CustomBaseUsed(t *testing.T) {
	s := storeWithConfigDir(t)
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))

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
	tr := &auth.Transport{Session: mustResolve(t, profileA), Base: base}
	req := mustRequest(t, "http://example.invalid/")
	resp, err := tr.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.True(t, called, "custom Base RoundTripper must be used")
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
