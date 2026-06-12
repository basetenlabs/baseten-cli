package auth_test

import (
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
	name, _, ok := s.CurrentProfile()
	require.False(t, ok)
	require.Empty(t, name)
}

func TestStore_SetAPIKeyProfile_Keyring(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))

	name, profile, ok := s.CurrentProfile()
	require.True(t, ok)
	require.Equal(t, profileA, name)
	require.Equal(t, remoteURL, profile.RemoteURL)
	require.Equal(t, auth.AuthTypeAPIKey, profile.AuthType)
	require.Empty(t, profile.InsecureAPIKey, "keyring-stored key must not leak to auth.json")

	got, err := s.GetAPIKey(profileA)
	require.NoError(t, err)
	require.Equal(t, apiKeyA, got)
}

func TestStore_SetAPIKeyProfile_Insecure(t *testing.T) {
	s := newInsecureStore(t)
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))

	_, profile, ok := s.CurrentProfile()
	require.True(t, ok)
	require.Equal(t, apiKeyA, profile.InsecureAPIKey)

	got, err := s.GetAPIKey(profileA)
	require.NoError(t, err)
	require.Equal(t, apiKeyA, got)
}

func TestStore_SetOAuthProfile_Keyring(t *testing.T) {
	s := newKeyringStore(t)
	cred := auth.OAuthCredential{AccessToken: accessToken, RefreshToken: refreshToken}
	require.NoError(t, s.SetOAuthProfile(profileA, remoteURL, cred, true, nil))

	_, profile, ok := s.CurrentProfile()
	require.True(t, ok)
	require.Equal(t, auth.AuthTypeOAuth, profile.AuthType)
	require.Nil(t, profile.InsecureOAuthCredential)

	got, err := s.GetOAuthCredential(profileA)
	require.NoError(t, err)
	require.Equal(t, cred, *got)
}

func TestStore_SetOAuthProfile_Insecure(t *testing.T) {
	s := newInsecureStore(t)
	cred := auth.OAuthCredential{AccessToken: accessToken, RefreshToken: refreshToken}
	require.NoError(t, s.SetOAuthProfile(profileA, remoteURL, cred, true, nil))

	_, profile, ok := s.CurrentProfile()
	require.True(t, ok)
	require.NotNil(t, profile.InsecureOAuthCredential)
	require.Equal(t, cred, *profile.InsecureOAuthCredential)
}

func TestStore_SetProfile_NoSwitchKeepsCurrent(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.SetAPIKeyProfile(profileB, remoteURL, "key-bob", false, nil))

	name, _, ok := s.CurrentProfile()
	require.True(t, ok)
	require.Equal(t, profileA, name, "switchCurrent=false must not move the current pointer")
}

func TestStore_SwitchProfile(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.SetAPIKeyProfile(profileB, remoteURL, "key-bob", true, nil))

	name, _, _ := s.CurrentProfile()
	require.Equal(t, profileB, name)

	require.NoError(t, s.SwitchProfile(profileA))
	name, _, _ = s.CurrentProfile()
	require.Equal(t, profileA, name)
}

func TestStore_SwitchProfile_UnknownFails(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))
	err := s.SwitchProfile("ghost@example.com")
	require.ErrorContains(t, err, "not found")
}

func TestStore_RemoveProfile_ClearsCurrent(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.RemoveProfile(profileA))

	_, _, ok := s.CurrentProfile()
	require.False(t, ok)

	_, err := s.GetAPIKey(profileA)
	require.Error(t, err, "keyring and file entry must both be gone")
}

func TestStore_RemoveProfile_OnlyCurrentIsCleared(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.SetAPIKeyProfile(profileB, remoteURL, "key-bob", true, nil))

	require.NoError(t, s.RemoveProfile(profileA))

	name, _, ok := s.CurrentProfile()
	require.True(t, ok)
	require.Equal(t, profileB, name)
}

func TestStore_RemoveProfile_MissingNoError(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.RemoveProfile(profileA))
}

func TestStore_ProfileIsolation(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))
	require.NoError(t, s.SetAPIKeyProfile(profileB, otherRemote, "key-other", true, nil))

	got, err := s.GetAPIKey(profileA)
	require.NoError(t, err)
	require.Equal(t, apiKeyA, got)

	got, err = s.GetAPIKey(profileB)
	require.NoError(t, err)
	require.Equal(t, "key-other", got)
}

func TestStore_ProfileNames(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyProfile(profileB, remoteURL, "key-bob", true, nil))
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))

	names, err := s.ProfileNames()
	require.NoError(t, err)
	require.Equal(t, []string{profileA, profileB}, names)
}

func TestStore_SaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	keyring.MockInit()
	s := auth.NewStore(auth.StoreOptions{Dir: dir, InsecureStorage: true})
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))

	s2 := auth.NewStore(auth.StoreOptions{Dir: dir, InsecureStorage: true})
	name, _, ok := s2.CurrentProfile()
	require.True(t, ok)
	require.Equal(t, profileA, name)
}

func TestStore_AuthFileFormat(t *testing.T) {
	dir := t.TempDir()
	keyring.MockInit()
	s := auth.NewStore(auth.StoreOptions{Dir: dir, InsecureStorage: true})
	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))

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
	_, _, ok := s.CurrentProfile()
	require.False(t, ok)

	require.NoError(t, s.SetAPIKeyProfile(profileA, remoteURL, apiKeyA, true, nil))
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "hosts")
}
