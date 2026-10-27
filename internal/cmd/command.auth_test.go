package cmd_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
)

// newAuthHarness returns a CommandHarness with the env credential vars cleared
// so auth commands exercise the profile store rather than ephemeral env auth.
func newAuthHarness(t *testing.T) *CommandHarness {
	h := NewCommandHarness(t)
	t.Setenv("BASETEN_API_KEY", "")
	t.Setenv("BASETEN_PROFILE", "")
	return h
}

// pointManagementAt routes management API calls (and OAuth endpoints) to urlStr
// and uses it as the login remote.
func pointManagementAt(t *testing.T, urlStr string) {
	t.Helper()
	t.Setenv("BASETEN_REMOTE_URL", urlStr)
	t.Setenv("BASETEN_MANAGEMENT_API_URL_OVERRIDE", urlStr)
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

func Test_Auth_Status_NotLoggedIn(t *testing.T) {
	h := newAuthHarness(t)
	h.Require.Error(h.Execute("auth", "status"))
	h.Require.Equal(1, h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "not logged in")
}

func Test_Auth_Status_APIKey(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.example.com", "key-xyz", true, nil))

	h.Require.NoError(h.Execute("auth", "status"))
	out := h.Stdout.String()
	h.Require.Contains(out, "alice")
	h.Require.Contains(out, "https://app.example.com")
	h.Require.Contains(out, "api_key")
}

func Test_Auth_Status_JSON(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.example.com", "key-xyz", true, nil))

	h.Require.NoError(h.Execute("auth", "status", "--output", "json"))
	var got map[string]string
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal("alice", got["profile"])
	h.Require.Equal("api_key", got["auth_type"])
	h.Require.Equal("https://app.example.com", got["remote_url"])
}

func Test_Auth_Status_EphemeralEnvAPIKey(t *testing.T) {
	h := newAuthHarness(t)
	t.Setenv("BASETEN_API_KEY", "env-key")

	h.Require.NoError(h.Execute("auth", "status"))
	h.Require.Contains(h.Stdout.String(), "BASETEN_API_KEY")
}

func Test_Auth_Status_ProfileFlagSelects(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.example.com", "key-a", true, nil))
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "bob", "https://app.other.example.com", "key-b", false, nil))

	h.Require.NoError(h.Execute("auth", "status", "--profile", "bob", "--output", "json"))
	var got map[string]string
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal("bob", got["profile"])
}

// authListLine returns the text-table line for the named profile.
func authListLine(h *CommandHarness, name string) string {
	for line := range strings.Lines(h.Stdout.String()) {
		if strings.Contains(line, " "+name+" ") {
			return line
		}
	}
	h.Require.Failf("profile not listed", "no line for %q in:\n%s", name, h.Stdout.String())
	return ""
}

// authListCurrent runs `auth list` as JSON and returns the current profile
// names.
func authListCurrent(h *CommandHarness, args ...string) []string {
	h.Require.NoError(h.Execute(append([]string{"auth", "list", "--output", "json"}, args...)...))
	var got cmd.AuthListResult
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	var current []string
	for _, p := range got.Profiles {
		if p.Current {
			current = append(current, p.Profile)
		}
	}
	return current
}

func Test_Auth_List_Empty(t *testing.T) {
	h := newAuthHarness(t)
	h.Require.NoError(h.Execute("auth", "list"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No profiles found.")
}

func Test_Auth_List_DefaultRemote(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "bob", "https://app.baseten.co", "key-b", false, nil))
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.baseten.co", "key-a", true, nil))

	h.Require.NoError(h.Execute("auth", "list"))
	out := h.Stdout.String()
	h.Require.NotContains(out, "REMOTE")
	h.Require.Less(strings.Index(out, "alice"), strings.Index(out, "bob"))
	h.Require.True(strings.HasPrefix(authListLine(h, "alice"), "*"))
	h.Require.False(strings.HasPrefix(authListLine(h, "bob"), "*"))
}

func Test_Auth_List_MixedRemotes(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.baseten.co", "key-a", true, nil))
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "bob", "https://app.example.com", "key-b", false, nil))

	h.Require.NoError(h.Execute("auth", "list"))
	h.Require.Contains(h.Stdout.String(), "REMOTE")
	h.Require.Contains(authListLine(h, "bob"), "https://app.example.com")
}

func Test_Auth_List_JSON(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.example.com", "key-a", true, nil))

	h.Require.NoError(h.Execute("auth", "list", "--output", "json"))
	var got cmd.AuthListResult
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal([]cmd.AuthListProfile{{
		Profile:   "alice",
		RemoteURL: "https://app.example.com",
		AuthType:  "api_key",
		Current:   true,
	}}, got.Profiles)
}

func Test_Auth_List_EnvProfileMarksCurrent(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.baseten.co", "key-a", true, nil))
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "bob", "https://app.baseten.co", "key-b", false, nil))
	t.Setenv("BASETEN_PROFILE", "bob")

	h.Require.Equal([]string{"bob"}, authListCurrent(h))
}

func Test_Auth_List_ProfileFlagMarksCurrent(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.baseten.co", "key-a", true, nil))
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "bob", "https://app.baseten.co", "key-b", false, nil))

	h.Require.Equal([]string{"bob"}, authListCurrent(h, "--profile", "bob"))
}

func Test_Auth_List_EphemeralEnvAPIKey(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.baseten.co", "key-a", true, nil))
	t.Setenv("BASETEN_API_KEY", "env-key")

	h.Require.Empty(authListCurrent(h))
}

func Test_Auth_Logout_NoCurrentProfile(t *testing.T) {
	h := newAuthHarness(t)
	h.Require.Error(h.Execute("auth", "logout"))
	h.Require.Equal(1, h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "no current profile")
}

func Test_Auth_Logout_RemovesCurrentProfile(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.example.com", "key-xyz", true, nil))

	h.Require.NoError(h.Execute("auth", "logout"))
	h.Require.Contains(h.Stdout.String(), "alice")

	_, _, ok := configDirStore(t).CurrentProfile()
	h.Require.False(ok, "profile should be removed")
}

func Test_Auth_Logout_ProfileFlagSelects(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.example.com", "key-a", true, nil))
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "bob", "https://app.example.com", "key-b", false, nil))

	h.Require.NoError(h.Execute("auth", "logout", "--profile", "bob"))

	_, ok := configDirStore(t).GetProfile("bob")
	h.Require.False(ok, "named profile should be removed")
	name, _, ok := configDirStore(t).CurrentProfile()
	h.Require.True(ok, "current profile must be untouched")
	h.Require.Equal("alice", name)
}

func Test_Auth_Logout_APIKey_SkipsServerCall(t *testing.T) {
	h := newAuthHarness(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	pointManagementAt(t, srv.URL)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", srv.URL, "key-xyz", true, nil))

	h.Require.NoError(h.Execute("auth", "logout"))
	h.Require.Equal(0, calls, "API key logout should not call server")
}

func Test_Auth_Logout_OAuth_CallsRevokeEndpoint(t *testing.T) {
	h := newAuthHarness(t)
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	pointManagementAt(t, srv.URL)

	store := configDirStore(t)
	cred := auth.OAuthCredential{AccessToken: "at_abc", RefreshToken: "rt_abc"}
	h.Require.NoError(store.SetOAuthProfile(t.Context(), "alice", srv.URL, cred, true, nil))

	h.Require.NoError(h.Execute("auth", "logout"))
	h.Require.Equal("/v1/users/auth/logout", gotPath)
	h.Require.Equal("Bearer at_abc", gotAuth)

	_, _, ok := configDirStore(t).CurrentProfile()
	h.Require.False(ok, "profile should be removed")
}

func Test_Auth_Logout_OAuth_WarnsOnServerFailureButStillRemoves(t *testing.T) {
	h := newAuthHarness(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":"VALIDATION_ERROR","message":"nope"}`, http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	pointManagementAt(t, srv.URL)

	store := configDirStore(t)
	cred := auth.OAuthCredential{AccessToken: "at_abc", RefreshToken: "rt_abc"}
	h.Require.NoError(store.SetOAuthProfile(t.Context(), "alice", srv.URL, cred, true, nil))

	h.Require.NoError(h.Execute("auth", "logout"))
	h.Require.Contains(h.Stderr.String(), "warning")

	_, _, ok := configDirStore(t).CurrentProfile()
	h.Require.False(ok, "profile should still be removed after server failure")
}

func Test_Auth_Switch_RequiresProfileNonInteractive(t *testing.T) {
	h := newAuthHarness(t)
	err := h.Execute("auth", "switch")
	h.Require.ErrorContains(err, "--profile")
}

func Test_Auth_Switch_UpdatesCurrentProfile(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.example.com", "key-alice", true, nil))
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "bob", "https://app.example.com", "key-bob", true, nil))

	h.Require.NoError(h.Execute("auth", "switch", "--profile", "alice"))

	name, _, _ := configDirStore(t).CurrentProfile()
	h.Require.Equal("alice", name)
}

func Test_Auth_Switch_UnknownProfileFails(t *testing.T) {
	h := newAuthHarness(t)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", "https://app.example.com", "key-alice", true, nil))

	h.Require.Error(h.Execute("auth", "switch", "--profile", "ghost"))
	h.Require.Equal(1, h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "not found")
}

func Test_Auth_Login_APIKey_Stdin(t *testing.T) {
	h := newAuthHarness(t)
	srv := newUserInfoServer(t)
	pointManagementAt(t, srv.URL)

	h.Stdin.WriteString("secret-api-key\n")
	h.Require.NoError(h.Execute("auth", "login", "--with-api-key", "--profile", "my-laptop"))

	h.Require.Equal("Api-Key secret-api-key", srv.LastAuthHeader)
	h.Require.Contains(h.Stdout.String(), "user@example.com")
	h.Require.Contains(h.Stdout.String(), "my-laptop")

	store := configDirStore(t)
	name, profile, ok := store.CurrentProfile()
	h.Require.True(ok)
	h.Require.Equal("my-laptop", name)
	h.Require.Equal(auth.AuthTypeAPIKey, profile.AuthType)

	got, err := store.GetAPIKey(t.Context(), "my-laptop")
	h.Require.NoError(err)
	h.Require.Equal("secret-api-key", got)
}

func Test_Auth_Login_APIKey_NoSwitchKeepsCurrent(t *testing.T) {
	h := newAuthHarness(t)
	srv := newUserInfoServer(t)
	pointManagementAt(t, srv.URL)

	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "existing", srv.URL, "key-existing", true, nil))

	h.Stdin.WriteString("secret-api-key\n")
	h.Require.NoError(h.Execute("auth", "login", "--with-api-key", "--profile", "my-laptop", "--no-switch"))

	name, _, ok := configDirStore(t).CurrentProfile()
	h.Require.True(ok)
	h.Require.Equal("existing", name, "--no-switch must not move the current pointer")
}

// devNullStdin points the harness at /dev/null, as cron and CI do. It is a
// character device without being a terminal, so it must take the
// non-interactive branch rather than trying to prompt.
func devNullStdin(t *testing.T, h *CommandHarness) {
	f, err := os.Open(os.DevNull)
	h.Require.NoError(err)
	t.Cleanup(func() { _ = f.Close() })
	h.StdinReader = f
}

func Test_Auth_Login_DevNullStdinRequiresMethodFlag(t *testing.T) {
	h := newAuthHarness(t)
	pointManagementAt(t, "http://127.0.0.1:1")
	devNullStdin(t, h)

	err := h.Execute("auth", "login")
	h.Require.ErrorContains(err, "must specify --web or --with-api-key when not interactive")
}

func Test_Auth_Login_APIKey_DevNullStdinEmptyFails(t *testing.T) {
	h := newAuthHarness(t)
	pointManagementAt(t, "http://127.0.0.1:1")
	devNullStdin(t, h)

	// Reads stdin, hits EOF immediately, and reports the empty key rather
	// than prompting.
	err := h.Execute("auth", "login", "--with-api-key", "--profile", "my-laptop")
	h.Require.ErrorContains(err, "empty")
}

func Test_Auth_Login_APIKey_EmptyFails(t *testing.T) {
	h := newAuthHarness(t)
	pointManagementAt(t, "http://127.0.0.1:1")

	h.Stdin.WriteString("\n")
	err := h.Execute("auth", "login", "--with-api-key", "--profile", "my-laptop")
	h.Require.ErrorContains(err, "empty")
}

func Test_Auth_Login_APIKey_RequiresProfileNonInteractive(t *testing.T) {
	h := newAuthHarness(t)
	srv := newUserInfoServer(t)
	pointManagementAt(t, srv.URL)

	h.Stdin.WriteString("secret-api-key\n")
	err := h.Execute("auth", "login", "--with-api-key")
	h.Require.ErrorContains(err, "--profile")
}

func Test_Auth_Login_RejectsWebAndKeyFlags(t *testing.T) {
	h := newAuthHarness(t)
	err := h.Execute("auth", "login", "--web", "--with-api-key", "--profile", "x")
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
				"device_code":               "dev-code-xyz",
				"user_code":                 "ABCD-EFGH",
				"verification_uri":          "https://auth.example.com/device",
				"verification_uri_complete": "https://auth.example.com/device?user_code=ABCD-EFGH",
				"expires_in":                300,
				"interval":                  1,
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

func Test_Auth_Login_Web_DeviceFlow(t *testing.T) {
	h := newAuthHarness(t)
	srv := newDeviceAuthServer(t)
	pointManagementAt(t, srv.URL)

	h.Require.NoError(h.Execute("auth", "login", "--web"))
	h.Require.Equal([]string{"https://auth.example.com/device?user_code=ABCD-EFGH"}, h.OpenedURLs)

	h.Require.Equal(1, srv.DeviceAuthCalls)
	h.Require.GreaterOrEqual(srv.DeviceTokenCalls, 1)
	h.Require.Contains(h.Stderr.String(), "ABCD-EFGH")
	h.Require.Contains(h.Stdout.String(), "user@example.com")

	store := configDirStore(t)
	name, profile, ok := store.CurrentProfile()
	h.Require.True(ok)
	h.Require.Contains(name, "user@example.com", "profile is named after the email")
	h.Require.Equal(auth.AuthTypeOAuth, profile.AuthType)

	cred, err := store.GetOAuthCredential(t.Context(), name)
	h.Require.NoError(err)
	h.Require.Equal("access-xyz", cred.AccessToken)
	h.Require.Equal("refresh-xyz", cred.RefreshToken)
}

func Test_Auth_Login_Web_CancelDuringDeviceAuth(t *testing.T) {
	h := newAuthHarness(t)
	ctx, cancel := context.WithCancel(h.Context)
	h.Context = ctx
	// The device authorization request never gets a response. Once it arrives,
	// the command is cancelled as Ctrl-C would, and the request must be aborted
	// rather than left waiting.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		cancel()
		<-release
	}))
	t.Cleanup(srv.Close)
	// Registered after srv.Close so it runs first, letting Close finish.
	t.Cleanup(func() { close(release) })
	pointManagementAt(t, srv.URL)

	h.Require.Error(h.Execute("auth", "login", "--web"))
	h.Require.Equal(int(cmd.ExitInterrupted), h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "Canceled.")
	h.Require.Empty(h.OpenedURLs)
}

func Test_Auth_Login_Web_ProfileFlagOverridesName(t *testing.T) {
	h := newAuthHarness(t)
	srv := newDeviceAuthServer(t)
	pointManagementAt(t, srv.URL)

	h.Require.NoError(h.Execute("auth", "login", "--web", "--profile", "work"))
	h.Require.Equal([]string{"https://auth.example.com/device?user_code=ABCD-EFGH"}, h.OpenedURLs)

	name, _, ok := configDirStore(t).CurrentProfile()
	h.Require.True(ok)
	h.Require.Equal("work", name)
}
