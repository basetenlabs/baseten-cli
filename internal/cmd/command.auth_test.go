package cmd_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/auth"
)

// newAuthHarness returns a CommandHarness with BASETEN_API_KEY cleared so
// auth commands exercise the credential store rather than the env override.
func newAuthHarness(t *testing.T) *CommandHarness {
	h := NewCommandHarness(t)
	t.Setenv("BASETEN_API_KEY", "")
	return h
}

// configDirStore returns a Store pointed at the harness's BASETEN_CONFIG_DIR.
func configDirStore(t *testing.T) *auth.Store {
	t.Helper()
	dir, err := auth.DefaultConfigDir()
	if err != nil {
		t.Fatalf("resolving config dir: %v", err)
	}
	return auth.NewStore(auth.StoreOptions{Dir: dir})
}

// userInfoServer returns an httptest server that answers /v1/users/me. Every
// request path and auth header is captured for assertions.
type userInfoServer struct {
	*httptest.Server
	LastAuthHeader string
	Paths          []string
}

func newUserInfoServer(t *testing.T) *userInfoServer {
	t.Helper()
	s := &userInfoServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.LastAuthHeader = r.Header.Get("Authorization")
		s.Paths = append(s.Paths, r.URL.Path)
		if r.URL.Path == "/v1/users/me" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"user_id":        "user-abc",
				"email":          "user@example.com",
				"name":           "Test User",
				"workspace_name": "my-workspace",
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestAuthStatus_NotLoggedIn(t *testing.T) {
	h := newAuthHarness(t)
	h.Require.NoError(h.Execute("auth", "status"))
	h.Require.Equal(1, h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "not logged in")
}

func TestAuthStatus_APIKey(t *testing.T) {
	h := newAuthHarness(t)
	t.Setenv("BASETEN_BASE_URL", "https://api.example.com")

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyUser("https://api.example.com", "alice", "key-xyz", nil))

	h.Require.NoError(h.Execute("auth", "status"))
	out := h.Stdout.String()
	h.Require.Contains(out, "https://api.example.com")
	h.Require.Contains(out, "alice")
	h.Require.Contains(out, "api_key")
}

func TestAuthStatus_JSON(t *testing.T) {
	h := newAuthHarness(t)
	t.Setenv("BASETEN_BASE_URL", "https://api.example.com")

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyUser("https://api.example.com", "alice", "key-xyz", nil))

	h.Require.NoError(h.Execute("auth", "status", "--output", "json"))
	var got map[string]string
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal("alice", got["user"])
	h.Require.Equal("api_key", got["auth_type"])
	h.Require.Equal("https://api.example.com", got["host"])
}

func TestAuthLogout_NoActiveUser(t *testing.T) {
	h := newAuthHarness(t)
	h.Require.NoError(h.Execute("auth", "logout"))
	h.Require.Equal(1, h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "no active user")
}

func TestAuthLogout_RemovesActiveUser(t *testing.T) {
	h := newAuthHarness(t)
	t.Setenv("BASETEN_BASE_URL", "https://api.example.com")

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyUser("https://api.example.com", "alice", "key-xyz", nil))

	h.Require.NoError(h.Execute("auth", "logout"))
	h.Require.Contains(h.Stdout.String(), "alice")

	_, _, ok := configDirStore(t).ActiveUser("https://api.example.com")
	h.Require.False(ok, "user should be removed")
}

func TestAuthSwitch_RequiresUserNonInteractive(t *testing.T) {
	h := newAuthHarness(t)
	err := h.Execute("auth", "switch")
	h.Require.ErrorContains(err, "--user")
}

func TestAuthSwitch_UpdatesActiveUser(t *testing.T) {
	h := newAuthHarness(t)
	t.Setenv("BASETEN_BASE_URL", "https://api.example.com")

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyUser("https://api.example.com", "alice", "key-alice", nil))
	h.Require.NoError(store.SetAPIKeyUser("https://api.example.com", "bob", "key-bob", nil))

	h.Require.NoError(h.Execute("auth", "switch", "--user", "alice"))

	label, _, _ := configDirStore(t).ActiveUser("https://api.example.com")
	h.Require.Equal("alice", label)
}

func TestAuthSwitch_UnknownUserFails(t *testing.T) {
	h := newAuthHarness(t)
	t.Setenv("BASETEN_BASE_URL", "https://api.example.com")

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyUser("https://api.example.com", "alice", "key-alice", nil))

	h.Require.NoError(h.Execute("auth", "switch", "--user", "ghost"))
	h.Require.Equal(1, h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "not found")
}

func TestAuthLogin_APIKey_Stdin(t *testing.T) {
	h := newAuthHarness(t)
	srv := newUserInfoServer(t)
	t.Setenv("BASETEN_BASE_URL", srv.URL)

	h.Stdin.WriteString("secret-api-key\n")
	h.Require.NoError(h.Execute("auth", "login", "--with-api-key", "--label", "my-laptop"))

	h.Require.Equal("Api-Key secret-api-key", srv.LastAuthHeader)
	h.Require.Contains(h.Stdout.String(), "user@example.com")

	store := configDirStore(t)
	label, entry, ok := store.ActiveUser(srv.URL)
	h.Require.True(ok)
	h.Require.Equal("my-laptop", label)
	h.Require.Equal(auth.AuthTypeAPIKey, entry.AuthType)

	got, err := store.GetAPIKey(srv.URL, "my-laptop")
	h.Require.NoError(err)
	h.Require.Equal("secret-api-key", got)
}

func TestAuthLogin_APIKey_EmptyFails(t *testing.T) {
	h := newAuthHarness(t)
	t.Setenv("BASETEN_BASE_URL", "http://127.0.0.1:1")

	h.Stdin.WriteString("\n")
	err := h.Execute("auth", "login", "--with-api-key", "--label", "my-laptop")
	h.Require.ErrorContains(err, "empty")
}

func TestAuthLogin_APIKey_RequiresLabelNonInteractive(t *testing.T) {
	h := newAuthHarness(t)
	srv := newUserInfoServer(t)
	t.Setenv("BASETEN_BASE_URL", srv.URL)

	h.Stdin.WriteString("secret-api-key\n")
	err := h.Execute("auth", "login", "--with-api-key")
	h.Require.ErrorContains(err, "--label")
}

func TestAuthLogin_APIKey_RejectsWebAndKeyFlags(t *testing.T) {
	h := newAuthHarness(t)
	err := h.Execute("auth", "login", "--web", "--with-api-key", "--label", "x")
	h.Require.ErrorContains(err, "mutually exclusive")
}

// deviceAuthServer mocks the device flow endpoints plus /v1/users/me.
type deviceAuthServer struct {
	*httptest.Server
	DeviceAuthCalls  int
	DeviceTokenCalls int
}

func newDeviceAuthServer(t *testing.T) *deviceAuthServer {
	t.Helper()
	s := &deviceAuthServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/auth/device/authorize":
			s.DeviceAuthCalls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "dev-code-xyz",
				"user_code":        "ABCD-EFGH",
				"verification_uri": "https://auth.example.com/device",
				"expires_in":       300,
				"interval":         1,
			})
		case "/v1/users/auth/device/token":
			s.DeviceTokenCalls++
			if err := r.ParseForm(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if r.Form.Get("device_code") != "dev-code-xyz" {
				http.Error(w, "bad device code", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-xyz",
				"refresh_token": "refresh-xyz",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		case "/v1/users/me":
			if r.Header.Get("Authorization") != "Bearer access-xyz" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"user_id":        "user-abc",
				"email":          "user@example.com",
				"name":           "Test User",
				"workspace_name": "my-workspace",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestAuthLogin_Web_DeviceFlow(t *testing.T) {
	h := newAuthHarness(t)
	srv := newDeviceAuthServer(t)
	t.Setenv("BASETEN_BASE_URL", srv.URL)

	h.Require.NoError(h.Execute("auth", "login", "--web"))

	h.Require.Equal(1, srv.DeviceAuthCalls)
	h.Require.GreaterOrEqual(srv.DeviceTokenCalls, 1)
	h.Require.Contains(h.Stderr.String(), "ABCD-EFGH")
	h.Require.Contains(h.Stdout.String(), "user@example.com")

	store := configDirStore(t)
	label, entry, ok := store.ActiveUser(srv.URL)
	h.Require.True(ok)
	h.Require.Equal("user@example.com", label)
	h.Require.Equal(auth.AuthTypeOAuth, entry.AuthType)

	cred, err := store.GetOAuthCredential(srv.URL, label)
	h.Require.NoError(err)
	h.Require.Equal("access-xyz", cred.AccessToken)
	h.Require.Equal("refresh-xyz", cred.RefreshToken)
}
