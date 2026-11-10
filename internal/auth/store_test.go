package auth_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const (
	remoteURL    = "https://app.example.com"
	otherRemote  = "https://app.other.example.com"
	profileA     = "alice@example.com"
	profileB     = "bob@example.com"
	apiKeyA      = "api-key-alice"
	accessToken  = "access-token-xyz"
	refreshToken = "refresh-token-xyz"
)

func init() {
	keyring.MockInit()
}

func newKeyringStore(t *testing.T) *auth.Store {
	t.Helper()
	return auth.NewStore(auth.StoreOptions{Dir: t.TempDir()})
}

func newInsecureStore(t *testing.T) *auth.Store {
	t.Helper()
	return auth.NewStore(auth.StoreOptions{Dir: t.TempDir(), InsecureStorage: true})
}

func TestStore_EmptyNoCurrentProfile(t *testing.T) {
	s := newKeyringStore(t)
	name, _, ok, err := s.CurrentProfile()
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, name)
}

func TestStore_SetAPIKeyProfile_Keyring(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))

	name, profile, ok, err := s.CurrentProfile()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, profileA, name)
	require.Equal(t, remoteURL, profile.RemoteURL)
	require.Equal(t, auth.AuthTypeAPIKey, profile.AuthType)
	require.Empty(t, profile.InsecureAPIKey, "keyring-stored key must not leak to auth.json")

	got, err := s.GetAPIKey(t.Context(), profileA)
	require.NoError(t, err)
	require.Equal(t, apiKeyA, got)
}

func TestStore_SetAPIKeyProfile_Insecure(t *testing.T) {
	s := newInsecureStore(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))

	_, profile, ok, err := s.CurrentProfile()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, apiKeyA, profile.InsecureAPIKey)

	got, err := s.GetAPIKey(t.Context(), profileA)
	require.NoError(t, err)
	require.Equal(t, apiKeyA, got)
}

func TestStore_SetOAuthProfile_Keyring(t *testing.T) {
	s := newKeyringStore(t)
	cred := auth.OAuthCredential{AccessToken: accessToken, RefreshToken: refreshToken}
	require.NoError(t, s.SetOAuthProfile(t.Context(), profileA, remoteURL, cred, true, nil))

	_, profile, ok, err := s.CurrentProfile()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, auth.AuthTypeOAuth, profile.AuthType)
	require.Nil(t, profile.InsecureOAuthCredential)

	got, err := s.GetOAuthCredential(t.Context(), profileA)
	require.NoError(t, err)
	require.Equal(t, cred, *got)
}

func TestStore_SetOAuthProfile_Insecure(t *testing.T) {
	s := newInsecureStore(t)
	cred := auth.OAuthCredential{AccessToken: accessToken, RefreshToken: refreshToken}
	require.NoError(t, s.SetOAuthProfile(t.Context(), profileA, remoteURL, cred, true, nil))

	_, profile, ok, err := s.CurrentProfile()
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, profile.InsecureOAuthCredential)
	require.Equal(t, cred, *profile.InsecureOAuthCredential)
}

func TestStore_SetProfile_NoSwitchKeepsCurrent(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileB, remoteURL, "key-bob", false, nil))

	name, _, ok, err := s.CurrentProfile()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, profileA, name, "switchCurrent=false must not move the current pointer")
}

func TestStore_SwitchProfile(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileB, remoteURL, "key-bob", true, nil))

	name, _, _, err := s.CurrentProfile()
	require.NoError(t, err)
	require.Equal(t, profileB, name)

	require.NoError(t, s.SwitchProfile(profileA))
	name, _, _, err = s.CurrentProfile()
	require.NoError(t, err)
	require.Equal(t, profileA, name)
}

func TestStore_SwitchProfile_UnknownFails(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	err := s.SwitchProfile("ghost@example.com")
	require.ErrorContains(t, err, "not found")
}

func TestStore_RemoveProfile_ClearsCurrent(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.RemoveProfile(t.Context(), profileA))

	_, _, ok, err := s.CurrentProfile()
	require.NoError(t, err)
	require.False(t, ok)

	_, err = s.GetAPIKey(t.Context(), profileA)
	require.Error(t, err, "keyring and file entry must both be gone")
}

func TestStore_RemoveProfile_OnlyCurrentIsCleared(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileB, remoteURL, "key-bob", true, nil))

	require.NoError(t, s.RemoveProfile(t.Context(), profileA))

	name, _, ok, err := s.CurrentProfile()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, profileB, name)
}

func TestStore_RemoveProfile_MissingNoError(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.RemoveProfile(t.Context(), profileA))
}

func TestStore_CancelledContext(t *testing.T) {
	// Fresh keyring, so entries other tests left for the same profiles cannot
	// pass for something written here.
	keyring.MockInit()
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// A cancelled read fails rather than falling back to auth.json and
	// reporting the credential missing.
	_, err := s.GetAPIKey(ctx, profileA)
	require.ErrorIs(t, err, context.Canceled)

	// A cancelled write fails without storing anything, in the keyring or as a
	// plaintext fallback.
	require.ErrorIs(t, s.SetAPIKeyProfile(ctx, profileB, remoteURL, "key-bob", true, nil), context.Canceled)
	_, ok := s.GetProfile(profileB)
	require.False(t, ok)
	_, err = s.GetAPIKey(t.Context(), profileB)
	require.Error(t, err)

	// A cancelled removal leaves the keyring entry in place.
	require.ErrorIs(t, s.RemoveProfile(ctx, profileA), context.Canceled)
	got, err := s.GetAPIKey(t.Context(), profileA)
	require.NoError(t, err)
	require.Equal(t, apiKeyA, got)
}

func TestStore_ProfileIsolation(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileB, otherRemote, "key-other", true, nil))

	got, err := s.GetAPIKey(t.Context(), profileA)
	require.NoError(t, err)
	require.Equal(t, apiKeyA, got)

	got, err = s.GetAPIKey(t.Context(), profileB)
	require.NoError(t, err)
	require.Equal(t, "key-other", got)
}

func TestStore_ProfileNames(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileB, remoteURL, "key-bob", true, nil))
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))

	names, err := s.ProfileNames()
	require.NoError(t, err)
	require.Equal(t, []string{profileA, profileB}, names)
}

func TestNewErrProfileNotFound_ListsExistingProfiles(t *testing.T) {
	err := auth.NewErrProfileNotFound([]string{"a", "b"}, "the-name")
	require.ErrorContains(t, err, "profile \"the-name\" not found; add it with")
	require.ErrorContains(t, err, "baseten auth login --profile the-name --no-switch")
	require.ErrorContains(t, err, "(existing profiles: a, b)")
}

func TestNewErrProfileNotFound_NoExistingProfilesOmitsList(t *testing.T) {
	err := auth.NewErrProfileNotFound(nil, "the-name")
	require.ErrorContains(t, err, "no profiles stored")
	require.ErrorContains(t, err, "baseten auth login --profile the-name --no-switch")
	require.NotContains(t, err.Error(), "existing profiles")
	require.NotContains(t, err.Error(), "not found")
}

func TestNewErrProfileNotFound_ShellQuotesCommand(t *testing.T) {
	for name, want := range map[string]string{
		"prod-internal-coding": "`baseten auth login --profile prod-internal-coding --no-switch`",
		"team-$HOME":           "`baseten auth login --profile 'team-$HOME' --no-switch`",
		"a`id`":                "`baseten auth login --profile 'a`id`' --no-switch`",
		"bob's team":           "`baseten auth login --profile 'bob'\\''s team' --no-switch`",
		"-dash":                "`baseten auth login --profile '-dash' --no-switch`",
		"has space":            "`baseten auth login --profile 'has space' --no-switch`",
	} {
		require.ErrorContains(t, auth.NewErrProfileNotFound(nil, name), want)
	}
}

func TestStore_SwitchProfile_UnknownSuggestsAddingProfile(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileB, remoteURL, "key-bob", true, nil))
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))

	err := s.SwitchProfile("ghost@example.com")
	require.ErrorContains(t, err, "baseten auth login --profile ghost@example.com --no-switch")
	require.ErrorContains(t, err, "(existing profiles: alice@example.com, bob@example.com)")
}

func TestStore_SaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	keyring.MockInit()
	s := auth.NewStore(auth.StoreOptions{Dir: dir, InsecureStorage: true})
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))

	s2 := auth.NewStore(auth.StoreOptions{Dir: dir, InsecureStorage: true})
	name, _, ok, err := s2.CurrentProfile()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, profileA, name)
}

func TestStore_AuthFileFormat(t *testing.T) {
	dir := t.TempDir()
	keyring.MockInit()
	s := auth.NewStore(auth.StoreOptions{Dir: dir, InsecureStorage: true})
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))

	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	require.NoError(t, err)
	var af auth.AuthFile
	require.NoError(t, json.Unmarshal(data, &af))
	require.Equal(t, 1, af.Version)
	require.Equal(t, profileA, af.Current)
	require.Contains(t, af.Profiles, profileA)
	require.Equal(t, remoteURL, af.Profiles[profileA].RemoteURL)
}

// TestStore_IgnoresUnknownKeys confirms an auth.json carrying keys from an
// earlier layout loads without error and those keys are dropped on save.
func TestStore_IgnoresUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	keyring.MockInit()
	legacy := `{"version":1,"hosts":{"https://app.example.com":{"active_user":"x","users":{}}}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "auth.json"), []byte(legacy), 0o600))

	s := auth.NewStore(auth.StoreOptions{Dir: dir, InsecureStorage: true})
	_, _, ok, err := s.CurrentProfile()
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "hosts")
}

func TestStore_RequireExplicitProfile(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileA, remoteURL, apiKeyA, true, nil))

	// Enabling clears the current profile and reports the stored profiles.
	require.NoError(t, s.SetRequireExplicitProfile(true))
	_, _, ok, err := s.CurrentProfile()
	require.False(t, ok)
	var required *auth.ErrExplicitProfileRequired
	require.ErrorAs(t, err, &required)
	require.Equal(t, []string{profileA}, required.Profiles)

	// Storing a profile never makes it current.
	require.NoError(t, s.SetAPIKeyProfile(t.Context(), profileB, remoteURL, "key-bob", true, nil))
	_, _, _, err = s.CurrentProfile()
	require.ErrorAs(t, err, &required)
	require.Equal(t, []string{profileA, profileB}, required.Profiles)

	// Disabling does not revive the old current profile.
	require.NoError(t, s.SetRequireExplicitProfile(false))
	_, _, ok, err = s.CurrentProfile()
	require.NoError(t, err)
	require.False(t, ok)
}

func TestErrExplicitProfileRequired_Error(t *testing.T) {
	err := &auth.ErrExplicitProfileRequired{Profiles: []string{"a", "b", "c", "d", "e", "f", "g"}}
	require.Equal(t, "no profile selected and explicit profile selection is required (7 profiles exist)\n"+
		"  Pass one of:\n"+
		"    --profile a\n"+
		"    --profile b\n"+
		"    --profile c\n"+
		"    --profile d\n"+
		"    --profile e\n"+
		"    ...and 2 more (see `baseten auth list`)\n"+
		"  Or set BASETEN_PROFILE, or disable with `baseten auth config update --require-explicit-profile=false` "+
		"and then select a current profile with `baseten auth switch --profile <profile>`",
		err.Error())

	err = &auth.ErrExplicitProfileRequired{Profiles: []string{"bob's"}}
	require.Contains(t, err.Error(), "(1 profile exists)")
	require.Contains(t, err.Error(), `--profile 'bob'\''s'`)
	require.NotContains(t, err.Error(), "more")

	err = &auth.ErrExplicitProfileRequired{}
	require.Contains(t, err.Error(), "no profiles are stored")
	require.Contains(t, err.Error(), "baseten auth login --profile <profile>")
}
