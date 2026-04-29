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
	testHost     = "https://api.example.com"
	otherHost    = "https://api.other.example.com"
	userLabelA   = "alice@example.com"
	userLabelB   = "bob@example.com"
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

func TestStore_EmptyNoActiveUser(t *testing.T) {
	s := newKeyringStore(t)
	label, _, ok := s.ActiveUser(testHost)
	require.False(t, ok)
	require.Empty(t, label)
}

func TestStore_SetAPIKeyUser_Keyring(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, apiKeyA, nil))

	label, entry, ok := s.ActiveUser(testHost)
	require.True(t, ok)
	require.Equal(t, userLabelA, label)
	require.Equal(t, auth.AuthTypeAPIKey, entry.AuthType)
	require.Empty(t, entry.InsecureAPIKey, "keyring-stored key must not leak to auth.json")

	got, err := s.GetAPIKey(testHost, userLabelA)
	require.NoError(t, err)
	require.Equal(t, apiKeyA, got)
}

func TestStore_SetAPIKeyUser_Insecure(t *testing.T) {
	s := newInsecureStore(t)
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, apiKeyA, nil))

	_, entry, ok := s.ActiveUser(testHost)
	require.True(t, ok)
	require.Equal(t, apiKeyA, entry.InsecureAPIKey)

	got, err := s.GetAPIKey(testHost, userLabelA)
	require.NoError(t, err)
	require.Equal(t, apiKeyA, got)
}

func TestStore_SetOAuthUser_Keyring(t *testing.T) {
	s := newKeyringStore(t)
	cred := auth.OAuthCredential{AccessToken: accessToken, RefreshToken: refreshToken}
	require.NoError(t, s.SetOAuthUser(testHost, userLabelA, cred, nil))

	_, entry, ok := s.ActiveUser(testHost)
	require.True(t, ok)
	require.Equal(t, auth.AuthTypeOAuth, entry.AuthType)
	require.Nil(t, entry.InsecureOAuthCredential)

	got, err := s.GetOAuthCredential(testHost, userLabelA)
	require.NoError(t, err)
	require.Equal(t, cred, *got)
}

func TestStore_SetOAuthUser_Insecure(t *testing.T) {
	s := newInsecureStore(t)
	cred := auth.OAuthCredential{AccessToken: accessToken, RefreshToken: refreshToken}
	require.NoError(t, s.SetOAuthUser(testHost, userLabelA, cred, nil))

	_, entry, ok := s.ActiveUser(testHost)
	require.True(t, ok)
	require.NotNil(t, entry.InsecureOAuthCredential)
	require.Equal(t, cred, *entry.InsecureOAuthCredential)
}

func TestStore_SwitchUser(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, apiKeyA, nil))
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelB, "key-bob", nil))

	label, _, _ := s.ActiveUser(testHost)
	require.Equal(t, userLabelB, label)

	require.NoError(t, s.SwitchUser(testHost, userLabelA))
	label, _, _ = s.ActiveUser(testHost)
	require.Equal(t, userLabelA, label)
}

func TestStore_SwitchUser_UnknownFails(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, apiKeyA, nil))
	err := s.SwitchUser(testHost, "ghost@example.com")
	require.ErrorContains(t, err, "not found")
}

func TestStore_SwitchUser_UnknownHostFails(t *testing.T) {
	s := newKeyringStore(t)
	err := s.SwitchUser(testHost, userLabelA)
	require.ErrorContains(t, err, "no credentials stored")
}

func TestStore_RemoveUser_ClearsActive(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, apiKeyA, nil))
	require.NoError(t, s.RemoveUser(testHost, userLabelA))

	_, _, ok := s.ActiveUser(testHost)
	require.False(t, ok)

	_, err := s.GetAPIKey(testHost, userLabelA)
	require.Error(t, err, "keyring and file entry must both be gone")
}

func TestStore_RemoveUser_OnlyActiveIsCleared(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, apiKeyA, nil))
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelB, "key-bob", nil))

	require.NoError(t, s.RemoveUser(testHost, userLabelA))

	label, _, ok := s.ActiveUser(testHost)
	require.True(t, ok)
	require.Equal(t, userLabelB, label)
}

func TestStore_RemoveUser_MissingHostNoError(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.RemoveUser(testHost, userLabelA))
}

func TestStore_HostIsolation(t *testing.T) {
	s := newKeyringStore(t)
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, apiKeyA, nil))
	require.NoError(t, s.SetAPIKeyUser(otherHost, userLabelA, "key-other", nil))

	got, err := s.GetAPIKey(testHost, userLabelA)
	require.NoError(t, err)
	require.Equal(t, apiKeyA, got)

	got, err = s.GetAPIKey(otherHost, userLabelA)
	require.NoError(t, err)
	require.Equal(t, "key-other", got)
}

func TestStore_SaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	keyring.MockInit()
	s := auth.NewStore(auth.StoreOptions{Dir: dir, InsecureStorage: true})
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, apiKeyA, nil))

	s2 := auth.NewStore(auth.StoreOptions{Dir: dir, InsecureStorage: true})
	label, _, ok := s2.ActiveUser(testHost)
	require.True(t, ok)
	require.Equal(t, userLabelA, label)
}

func TestStore_AuthFileFormat(t *testing.T) {
	dir := t.TempDir()
	keyring.MockInit()
	s := auth.NewStore(auth.StoreOptions{Dir: dir, InsecureStorage: true})
	require.NoError(t, s.SetAPIKeyUser(testHost, userLabelA, apiKeyA, nil))

	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	require.NoError(t, err)
	var af auth.AuthFile
	require.NoError(t, json.Unmarshal(data, &af))
	require.Equal(t, 1, af.Version)
	require.Contains(t, af.Hosts, testHost)
	require.Equal(t, userLabelA, af.Hosts[testHost].ActiveUser)
}
