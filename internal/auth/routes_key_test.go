package auth_test

import (
	"errors"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/auth"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestRoutesKey_PendingAttemptBlocksRetry(t *testing.T) {
	keyring.MockInit()
	store := auth.NewStore(auth.StoreOptions{Dir: t.TempDir()})
	scope := auth.RoutesKeyScope{ManagementURL: "https://api.example.com", UserID: "u", TeamID: "t", Name: t.Name()}
	calls := 0
	created, err := store.EnsureRoutesKey(scope, func() (string, bool, error) {
		calls++
		return "", false, errors.New("request outcome unknown")
	})
	require.False(t, created)
	require.ErrorContains(t, err, "automatic retry is blocked")
	_, err = store.EnsureRoutesKey(scope, func() (string, bool, error) { calls++; return "secret", false, nil })
	require.ErrorContains(t, err, "unresolved")
	require.Equal(t, 1, calls)
}

func TestRoutesKey_SaveFailureDoesNotLeak(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	store := auth.NewStore(auth.StoreOptions{Dir: t.TempDir()})
	scope := auth.RoutesKeyScope{ManagementURL: "https://api.example.com", UserID: "u", TeamID: "t", Name: t.Name()}
	created, err := store.EnsureRoutesKey(scope, func() (string, bool, error) {
		keyring.MockInitWithError(errors.New("sensitive storage failure"))
		return "secret-created-key", false, nil
	})
	require.False(t, created)
	require.ErrorContains(t, err, "was created but could not be saved")
	require.NotContains(t, err.Error(), "secret-created-key")
	require.NotContains(t, err.Error(), "sensitive storage failure")
}

func TestRoutesKey_ScopeIsolation(t *testing.T) {
	keyring.MockInit()
	store := auth.NewStore(auth.StoreOptions{Dir: t.TempDir()})
	scope := auth.RoutesKeyScope{ManagementURL: "https://api.example.com", Profile: "alice", UserID: "u", TeamID: "t", Name: t.Name()}
	_, err := store.EnsureRoutesKey(scope, func() (string, bool, error) { return "saved-secret", false, nil })
	require.NoError(t, err)
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
