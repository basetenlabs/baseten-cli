package auth_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/basetenlabs/baseten-go/client"
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
	t.Setenv("BASETEN_REMOTE_URL", "")
	t.Setenv(auth.IdentityTokenFileEnv, "")
	t.Setenv(auth.TrustRuleIDEnv, "")
	return auth.NewStore(auth.StoreOptions{Dir: dir})
}

func mustResolve(t *testing.T, profileFlag string) *auth.Session {
	t.Helper()
	session, err := auth.ResolveSession(profileFlag, "")
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
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, "stored-key", true, nil))
	t.Setenv("BASETEN_API_KEY", "env-key")

	es := newEchoServer(t)
	tr := &auth.Transport{Session: mustResolve(t, "")}

	resp, err := tr.Do(mustRequest(t, es.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "Bearer env-key", es.LastAuthHeader)
}

func TestTransport_APIKeyInjected(t *testing.T) {
	s := storeWithConfigDir(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))

	es := newEchoServer(t)
	tr := &auth.Transport{Session: mustResolve(t, profileA)}

	resp, err := tr.Do(mustRequest(t, es.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "Bearer "+apiKeyA, es.LastAuthHeader)
}

func TestTransport_OAuthBearerInjected(t *testing.T) {
	s := storeWithConfigDir(t)
	cred := auth.OAuthCredential{AccessToken: accessToken, RefreshToken: refreshToken}
	require.NoError(t, s.SetOAuthProfile(t.Context(), profileA, remoteURL, cred, true, nil))

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

func TestTransport_MissingProfileSuggestsAddingProfile(t *testing.T) {
	s := storeWithConfigDir(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileB, remoteURL, "key-bob", false, nil))
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))

	tr := &auth.Transport{Session: mustResolve(t, "ghost")}
	_, err := tr.Credential(t.Context())
	require.ErrorContains(t, err, "profile \"ghost\" not found; add it with")
	require.ErrorContains(t, err, "baseten auth login --profile ghost --no-switch")
	require.ErrorContains(t, err, "(existing profiles: alice@example.com, bob@example.com)")
}

func TestTransport_CorruptAuthFileSurfacesLoadError(t *testing.T) {
	storeWithConfigDir(t)
	dir, err := auth.DefaultConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{"), 0o600))

	tr := &auth.Transport{Session: mustResolve(t, "ghost")}
	_, err = tr.Credential(t.Context())
	require.ErrorContains(t, err, "parsing")
	require.ErrorContains(t, err, "auth.json")
	require.NotContains(t, err.Error(), "not found")
}

func TestTransport_OAuthRefreshRotatesStoredToken(t *testing.T) {
	s := storeWithConfigDir(t)
	// Blank access token with a refresh token forces oauth2 to refresh
	// immediately: the library treats a missing access token as invalid.
	require.NoError(t, s.SetOAuthProfile(t.Context(), profileA, remoteURL,
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

	stored, err := s.GetOAuthCredential(t.Context(), profileA)
	require.NoError(t, err)
	require.Equal(t, "new-access", stored.AccessToken)
	require.Equal(t, "new-refresh", stored.RefreshToken)
}

func TestTransport_OAuthRefreshFailureSurfacesError(t *testing.T) {
	s := storeWithConfigDir(t)
	require.NoError(t, s.SetOAuthProfile(t.Context(), profileA, remoteURL,
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
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))

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

func TestSession_CacheIdentity(t *testing.T) {
	s := storeWithConfigDir(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileB, remoteURL, "api-key-bob", false, nil))

	alice := mustResolve(t, profileA).CacheIdentity()
	bob := mustResolve(t, profileB).CacheIdentity()

	// Distinct credentials never share an identity, and the raw profile name (an
	// email) does not appear in it, since it is used as a path segment.
	require.NotEqual(t, alice, bob)
	require.NotContains(t, alice, profileA)
	require.Regexp(t, `^profile-[0-9a-f]{16}$`, alice)

	// Stable across resolutions, so sign and proxy agree.
	require.Equal(t, alice, mustResolve(t, profileA).CacheIdentity())
}

func TestSession_CacheIdentityEphemeralKey(t *testing.T) {
	storeWithConfigDir(t)
	t.Setenv("BASETEN_API_KEY", "env-key")
	first := mustResolve(t, "").CacheIdentity()
	require.Regexp(t, `^key-[0-9a-f]{16}$`, first)
	require.NotContains(t, first, "env-key")

	// A different key is a different identity: ephemeral sessions carry no
	// profile name to distinguish them.
	t.Setenv("BASETEN_API_KEY", "other-key")
	require.NotEqual(t, first, mustResolve(t, "").CacheIdentity())
}

func TestSession_CacheIdentityUnauthenticated(t *testing.T) {
	storeWithConfigDir(t)
	require.Equal(t, "anonymous", mustResolve(t, "").CacheIdentity())
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSession_UsesAPIKey(t *testing.T) {
	s := storeWithConfigDir(t)
	require.False(t, mustResolve(t, "").UsesAPIKey(), "unauthenticated")

	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.SetOAuthProfile(t.Context(), profileB, remoteURL, auth.OAuthCredential{AccessToken: "token"}, false, nil))
	require.True(t, mustResolve(t, profileA).UsesAPIKey())
	require.False(t, mustResolve(t, profileB).UsesAPIKey())

	t.Setenv("BASETEN_API_KEY", "env-key")
	require.True(t, mustResolve(t, "").UsesAPIKey())
}

// setWorkloadIdentityEnv writes an identity token file and sets both workload
// identity variables, returning the token file path.
func setWorkloadIdentityEnv(t *testing.T, trustRuleID string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(file, []byte("identity-jwt\n"), 0o600))
	t.Setenv(auth.IdentityTokenFileEnv, file)
	t.Setenv(auth.TrustRuleIDEnv, trustRuleID)
	return file
}

// tokenExchangeServer answers the workload identity token exchange with
// access-<n> tokens, or with invalid_grant when refuse is set.
type tokenExchangeServer struct {
	*httptest.Server
	Exchanges  int
	Assertions []string
	refuse     bool
}

func newTokenExchangeServer(t *testing.T, refuse bool) *tokenExchangeServer {
	t.Helper()
	s := &tokenExchangeServer{refuse: refuse}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/auth/token", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		s.Assertions = append(s.Assertions, body["assertion"])
		w.Header().Set("Content-Type", "application/json")
		if s.refuse {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		s.Exchanges++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-" + strconv.Itoa(s.Exchanges),
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	t.Cleanup(s.Close)
	return s
}

func TestResolveSession_WorkloadIdentityPrecedence(t *testing.T) {
	s := storeWithConfigDir(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileB, remoteURL, "key-bob", false, nil))
	file := setWorkloadIdentityEnv(t, "rule-1")
	t.Setenv("BASETEN_REMOTE_URL", "https://app.staging.baseten.co")

	// Workload identity wins over BASETEN_PROFILE and the current profile.
	t.Setenv("BASETEN_PROFILE", profileB)
	session := mustResolve(t, "")
	require.Equal(t, &auth.WorkloadIdentity{IdentityTokenFile: file, TrustRuleID: "rule-1"}, session.WorkloadIdentity())
	require.True(t, session.IsEphemeral())
	require.False(t, session.UsesAPIKey())
	require.Empty(t, session.ProfileName())
	require.Equal(t, "https://app.staging.baseten.co", session.RemoteURL())

	// --profile wins over workload identity.
	session = mustResolve(t, profileA)
	require.Nil(t, session.WorkloadIdentity())
	require.Equal(t, profileA, session.ProfileName())

	// BASETEN_API_KEY wins over workload identity, which it shadows.
	t.Setenv("BASETEN_API_KEY", "env-key")
	session = mustResolve(t, "")
	require.Nil(t, session.WorkloadIdentity())
	require.True(t, session.UsesAPIKey())
	require.True(t, session.WorkloadIdentityShadowed())
}

func TestResolveSession_WorkloadIdentityUnsetKeepsProfileOrder(t *testing.T) {
	s := storeWithConfigDir(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	session := mustResolve(t, "")
	require.Nil(t, session.WorkloadIdentity())
	require.False(t, session.WorkloadIdentityShadowed())
	require.Equal(t, profileA, session.ProfileName())

	t.Setenv("BASETEN_API_KEY", "env-key")
	require.False(t, mustResolve(t, "").WorkloadIdentityShadowed())
}

func TestResolveSession_WorkloadIdentityHalfConfigured(t *testing.T) {
	for _, tc := range []struct {
		set, missing string
	}{
		{auth.IdentityTokenFileEnv, auth.TrustRuleIDEnv},
		{auth.TrustRuleIDEnv, auth.IdentityTokenFileEnv},
	} {
		t.Run(tc.set, func(t *testing.T) {
			s := storeWithConfigDir(t)
			require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
			t.Setenv(tc.set, "value")

			// Falling through to the current profile would silently run as a
			// different identity.
			_, err := auth.ResolveSession("", "")
			require.ErrorContains(t, err, tc.set+" is set but "+tc.missing+" is not")

			// More specific sources still resolve.
			require.Equal(t, profileA, mustResolve(t, profileA).ProfileName())
			t.Setenv("BASETEN_API_KEY", "env-key")
			require.True(t, mustResolve(t, "").UsesAPIKey())
		})
	}
}

func TestTransport_WorkloadIdentityBearerInjected(t *testing.T) {
	storeWithConfigDir(t)
	setWorkloadIdentityEnv(t, "rule-1")
	exchange := newTokenExchangeServer(t, false)
	session := mustResolve(t, "")
	creds, err := session.NewWorkloadIdentityCredentials(exchange.URL, nil)
	require.NoError(t, err)

	es := newEchoServer(t)
	tr := &auth.Transport{Session: session, WorkloadIdentity: creds}
	for range 2 {
		resp, err := tr.Do(mustRequest(t, es.URL))
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, "Bearer access-1", es.LastAuthHeader)
	}
	// The cached access token is reused across requests.
	require.Equal(t, 1, exchange.Exchanges)
	require.Equal(t, []string{"identity-jwt"}, exchange.Assertions)
}

func TestTransport_WorkloadIdentityInvalidGrant(t *testing.T) {
	storeWithConfigDir(t)
	file := setWorkloadIdentityEnv(t, "rule-1")
	exchange := newTokenExchangeServer(t, true)
	session := mustResolve(t, "")
	creds, err := session.NewWorkloadIdentityCredentials(exchange.URL, nil)
	require.NoError(t, err)

	_, err = (&auth.Transport{Session: session, WorkloadIdentity: creds}).Credential(t.Context())
	require.ErrorIs(t, err, client.ErrInvalidGrant)
	require.ErrorContains(t, err, "workload identity token exchange refused; check that trust rule rule-1")
	require.ErrorContains(t, err, file)
}

func TestTransport_AccessTokenRefusesAPIKey(t *testing.T) {
	s := storeWithConfigDir(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	_, err := (&auth.Transport{Session: mustResolve(t, profileA)}).AccessToken(t.Context())
	require.ErrorContains(t, err, "not an access token")
}

func TestSession_CacheIdentityWorkloadIdentity(t *testing.T) {
	storeWithConfigDir(t)
	setWorkloadIdentityEnv(t, "rule-1")
	first := mustResolve(t, "").CacheIdentity()
	require.Regexp(t, `^wif-[0-9a-f]{16}$`, first)

	t.Setenv(auth.TrustRuleIDEnv, "rule-2")
	require.NotEqual(t, first, mustResolve(t, "").CacheIdentity())
}
