package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// routesKeyService namespaces routes API keys in the system keyring, keyed by
// [RoutesKeyScope].
const routesKeyService = "baseten-harness-routes"

// RoutesKeyScope identifies a saved routes API key by issuing endpoint,
// profile, user, team, and key name, so a key is reused only where it was
// created.
type RoutesKeyScope struct {
	ManagementURL string `json:"management_url"`
	Profile       string `json:"profile"`
	UserID        string `json:"user_id"`
	TeamID        string `json:"team_id"`
	Name          string `json:"name"`
}

func (s RoutesKeyScope) account() string {
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// GetRoutesKey returns the routes API key saved for scope, or "" if there is
// none. Tries the keyring first, then falls back to the auth.json plaintext
// field.
func (s *Store) GetRoutesKey(scope RoutesKeyScope) (string, error) {
	if key, err := keyring.Get(routesKeyService, scope.account()); err == nil {
		return key, nil
	}
	af, err := s.Load()
	if err != nil {
		return "", err
	}
	return af.InsecureRoutesKeys[scope.account()], nil
}

// SetRoutesKey saves a routes API key for scope. Tries the keyring first;
// falls back to plaintext in auth.json with a warning written to warnWriter
// (if non-nil).
func (s *Store) SetRoutesKey(scope RoutesKeyScope, key string, warnWriter func(string)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.insecureStorage {
		err := keyring.Set(routesKeyService, scope.account(), key)
		if err == nil {
			return nil
		}
		if warnWriter != nil {
			warnWriter(fmt.Sprintf("warning: could not store routes API key in system keyring, storing in plain text: %v\n", err))
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

// DeleteRoutesKey forgets the routes API key saved for scope, from both the
// keyring and auth.json.
func (s *Store) DeleteRoutesKey(scope RoutesKeyScope) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keyringErr := keyring.Delete(routesKeyService, scope.account())
	af, err := s.loadLocked()
	if err != nil {
		return err
	}
	if _, ok := af.InsecureRoutesKeys[scope.account()]; ok {
		delete(af.InsecureRoutesKeys, scope.account())
		return s.saveLocked(af)
	}
	if keyringErr != nil && !errors.Is(keyringErr, keyring.ErrNotFound) && !s.insecureStorage {
		return fmt.Errorf("deleting routes API key from system keyring: %w", keyringErr)
	}
	return nil
}
