package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zalando/go-keyring"
)

// gridTokenService namespaces cached Grid tokens in the system keyring, keyed
// by [GridTokenScope]. One entry holds a credential's tokens for every team.
const gridTokenService = "baseten-grid-token"

// GridTokenScope identifies the Grid tokens cached for one credential: the
// issuing endpoint and [Session.CacheIdentity], which stands for the profile
// or the ephemeral API key, so a token is never read under another credential.
type GridTokenScope struct {
	ManagementURL string `json:"management_url"`
	Identity      string `json:"identity"`
}

func (s GridTokenScope) account() string {
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// GridToken is a short-lived Grid token and when it expires.
type GridToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// gridTokens reads the tokens cached for scope, by team, from the keyring,
// else from the auth.json plaintext field.
func (s *Store) gridTokens(scope GridTokenScope) (map[string]GridToken, error) {
	tokens := map[string]GridToken{}
	if secret, err := keyring.Get(gridTokenService, scope.account()); err == nil {
		// An entry that does not parse is a miss; the next mint overwrites it.
		if json.Unmarshal([]byte(secret), &tokens) != nil {
			clear(tokens)
		}
		return tokens, nil
	}
	af, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	for team, token := range af.InsecureGridTokens[scope.account()] {
		tokens[team] = token
	}
	return tokens, nil
}

// GetGridToken returns the Grid token cached for scope and teamID, or nil if
// there is none.
func (s *Store) GetGridToken(scope GridTokenScope, teamID string) (*GridToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tokens, err := s.gridTokens(scope)
	if err != nil {
		return nil, err
	}
	token, ok := tokens[teamID]
	if !ok {
		return nil, nil
	}
	return &token, nil
}

// SetGridToken caches token for scope and teamID. Tries the keyring first;
// falls back to plaintext in auth.json with a warning written to warnWriter
// (if non-nil).
func (s *Store) SetGridToken(scope GridTokenScope, teamID string, token GridToken, warnWriter func(string)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tokens, err := s.gridTokens(scope)
	if err != nil {
		return err
	}
	tokens[teamID] = token
	if !s.insecureStorage {
		secret, err := json.Marshal(tokens)
		if err != nil {
			return fmt.Errorf("marshaling Grid tokens: %w", err)
		}
		if err := keyring.Set(gridTokenService, scope.account(), string(secret)); err == nil {
			return nil
		} else if warnWriter != nil {
			warnWriter(fmt.Sprintf("warning: could not store the Grid token in system keyring, storing in plain text: %v\n", err))
		}
	}
	af, err := s.loadLocked()
	if err != nil {
		return err
	}
	if af.InsecureGridTokens == nil {
		af.InsecureGridTokens = map[string]map[string]GridToken{}
	}
	af.InsecureGridTokens[scope.account()] = tokens
	return s.saveLocked(af)
}

// ClearGridTokens forgets every Grid token cached for scope.
func (s *Store) ClearGridTokens(scope GridTokenScope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = keyring.Delete(gridTokenService, scope.account())
	af, err := s.loadLocked()
	if err != nil {
		return err
	}
	if _, ok := af.InsecureGridTokens[scope.account()]; !ok {
		return nil
	}
	delete(af.InsecureGridTokens, scope.account())
	return s.saveLocked(af)
}
