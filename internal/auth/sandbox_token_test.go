package auth_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestSandboxToken_ScopeIsolation(t *testing.T) {
	keyring.MockInit()
	store := auth.NewStore(auth.StoreOptions{Dir: t.TempDir()})
	scope := auth.SandboxTokenScope{ManagementURL: "https://api.example.com", Identity: "profile-a"}
	saved := auth.SandboxToken{Token: "tok", ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Second)}
	require.NoError(t, store.SetSandboxToken(t.Context(), scope, saved))
	token, err := store.GetSandboxToken(t.Context(), scope)
	require.NoError(t, err)
	require.Equal(t, &saved, token)
	for _, other := range []auth.SandboxTokenScope{
		{ManagementURL: "https://api.other.example.com", Identity: "profile-a"},
		{ManagementURL: "https://api.example.com", Identity: "profile-b"},
	} {
		token, err := store.GetSandboxToken(t.Context(), other)
		require.NoError(t, err)
		require.Nil(t, token)
	}
}

func TestSandboxToken_KeyringUnavailableFallsBackToPlaintext(t *testing.T) {
	keyring.MockInitWithError(errors.New("no keyring"))
	t.Cleanup(keyring.MockInit)
	dir := t.TempDir()
	store := auth.NewStore(auth.StoreOptions{Dir: dir})
	scope := auth.SandboxTokenScope{ManagementURL: "https://api.example.com", Identity: "profile-a"}
	saved := auth.SandboxToken{Token: "tok", ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Second)}
	require.NoError(t, store.SetSandboxToken(t.Context(), scope, saved))
	// Its own file, never auth.json.
	require.FileExists(t, filepath.Join(dir, "auth.sandbox.json"))
	require.NoFileExists(t, filepath.Join(dir, "auth.json"))
	token, err := store.GetSandboxToken(t.Context(), scope)
	require.NoError(t, err)
	require.Equal(t, &saved, token)

	require.NoError(t, store.ClearSandboxToken(t.Context(), scope))
	token, err = store.GetSandboxToken(t.Context(), scope)
	require.NoError(t, err)
	require.Nil(t, token)
}

func TestSandboxToken_ClearRemovesKeyringEntry(t *testing.T) {
	keyring.MockInit()
	store := auth.NewStore(auth.StoreOptions{Dir: t.TempDir()})
	scope := auth.SandboxTokenScope{ManagementURL: "https://api.example.com", Identity: "profile-a"}
	require.NoError(t, store.SetSandboxToken(t.Context(), scope, auth.SandboxToken{Token: "tok"}))
	require.NoError(t, store.ClearSandboxToken(t.Context(), scope))
	token, err := store.GetSandboxToken(t.Context(), scope)
	require.NoError(t, err)
	require.Nil(t, token)
}

func TestSandboxToken_UnreadableCacheIsAMiss(t *testing.T) {
	keyring.MockInitWithError(errors.New("no keyring"))
	t.Cleanup(keyring.MockInit)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "auth.sandbox.json"), []byte("not json"), 0o600))
	store := auth.NewStore(auth.StoreOptions{Dir: dir})
	scope := auth.SandboxTokenScope{ManagementURL: "https://api.example.com", Identity: "profile-a"}
	token, err := store.GetSandboxToken(t.Context(), scope)
	require.NoError(t, err)
	require.Nil(t, token)
	// Nor does failing to cache fail the caller.
	require.NoError(t, store.SetSandboxToken(t.Context(), scope, auth.SandboxToken{Token: "tok"}))
}
