package auth_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestRoutesKey_ScopeIsolation(t *testing.T) {
	keyring.MockInit()
	store := auth.NewStore(auth.StoreOptions{Dir: t.TempDir()})
	scope := auth.RoutesKeyScope{ManagementURL: "https://api.example.com", Profile: "alice", UserID: "u", TeamID: "t", Name: t.Name()}
	require.NoError(t, store.SetRoutesKey(scope, "saved-secret", nil))
	key, err := store.GetRoutesKey(scope)
	require.NoError(t, err)
	require.Equal(t, "saved-secret", key)
	for _, mutate := range []func(*auth.RoutesKeyScope){
		func(s *auth.RoutesKeyScope) { s.ManagementURL = "https://api.other.example.com" },
		func(s *auth.RoutesKeyScope) { s.Profile = "bob" },
		func(s *auth.RoutesKeyScope) { s.UserID = "v" },
		func(s *auth.RoutesKeyScope) { s.TeamID = "other" },
		func(s *auth.RoutesKeyScope) { s.Name = "another-installation" },
	} {
		other := scope
		mutate(&other)
		key, err := store.GetRoutesKey(other)
		require.NoError(t, err)
		require.Empty(t, key)
	}
}

func TestRoutesKey_KeyringUnavailableFallsBackToPlaintext(t *testing.T) {
	keyring.MockInitWithError(errors.New("no keyring"))
	t.Cleanup(keyring.MockInit)
	dir := t.TempDir()
	store := auth.NewStore(auth.StoreOptions{Dir: dir})
	scope := auth.RoutesKeyScope{ManagementURL: "https://api.example.com", UserID: "u", TeamID: "t", Name: t.Name()}
	var warning string
	require.NoError(t, store.SetRoutesKey(scope, "plaintext-secret", func(s string) { warning += s }))
	require.Contains(t, warning, "storing in plain text")
	key, err := store.GetRoutesKey(scope)
	require.NoError(t, err)
	require.Equal(t, "plaintext-secret", key)
	require.FileExists(t, filepath.Join(dir, "auth.json"))
}

func TestRoutesKey_InsecureStorageSkipsKeyring(t *testing.T) {
	keyring.MockInit()
	store := auth.NewStore(auth.StoreOptions{Dir: t.TempDir(), InsecureStorage: true})
	scope := auth.RoutesKeyScope{ManagementURL: "https://api.example.com", UserID: "u", TeamID: "t", Name: t.Name()}
	require.NoError(t, store.SetRoutesKey(scope, "file-secret", nil))
	af, err := store.Load()
	require.NoError(t, err)
	require.Len(t, af.InsecureRoutesKeys, 1)
	key, err := store.GetRoutesKey(scope)
	require.NoError(t, err)
	require.Equal(t, "file-secret", key)
}
