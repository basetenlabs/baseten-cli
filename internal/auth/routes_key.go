package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/zalando/go-keyring"
)

// routesKeyService namespaces Grid API keys in the system keyring, keyed by
// [RoutesKeyScope].
const routesKeyService = "baseten-harness-routes"

// RoutesKeyScope identifies a saved Grid API key by issuing endpoint,
// profile, user, and team: each profile keeps one key per user and team on
// this machine.
type RoutesKeyScope struct {
	ManagementURL string `json:"management_url"`
	Profile       string `json:"profile"`
	UserID        string `json:"user_id"`
	TeamID        string `json:"team_id"`
}

func (s RoutesKeyScope) account() string {
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// GetRoutesKey returns the Grid API key saved for scope, or "" if there is
// none. Tries the keyring first, then falls back to the auth.json plaintext
// field.
func (s *Store) GetRoutesKey(ctx context.Context, scope RoutesKeyScope) (string, error) {
	var key string
	keyringErr := keyringDo(ctx, func() (err error) {
		key, err = keyring.Get(routesKeyService, scope.account())
		return err
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if keyringErr == nil {
		return key, nil
	}
	af, err := s.Load()
	if err != nil {
		return "", err
	}
	return af.InsecureRoutesKeys[scope.account()], nil
}

// SetRoutesKey saves a Grid API key for scope. Tries the keyring first;
// falls back to plaintext in auth.json with a warning written to warnWriter
// (if non-nil).
func (s *Store) SetRoutesKey(ctx context.Context, scope RoutesKeyScope, key string, warnWriter func(string)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.insecureStorage {
		keyringErr := keyringDo(ctx, func() error { return keyring.Set(routesKeyService, scope.account(), key) })
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if keyringErr == nil {
			return nil
		}
		if warnWriter != nil {
			warnWriter(fmt.Sprintf("warning: could not store Grid API key in system keyring, storing in plain text: %v\n", keyringErr))
		}
	}
	af, err := s.loadLocked()
	if err != nil {
		return err
	}
	if af.InsecureRoutesKeys == nil {
		af.InsecureRoutesKeys = map[string]string{}
	}
	af.InsecureRoutesKeys[scope.account()] = key
	return s.saveLocked(af)
}
