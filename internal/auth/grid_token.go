package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zalando/go-keyring"
)

// gridTokenService is the keyring service for cached Grid tokens, one entry
// per [GridTokenScope] holding every team's token.
const gridTokenService = "baseten-grid-token"

// GridTokenScope keys the cache by issuing endpoint and credential
// ([Session.CacheIdentity]), so no token is read under another credential.
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

// gridTokens reads the tokens cached for scope, by team. A missing or
// unparseable entry is empty.
func gridTokens(scope GridTokenScope) map[string]GridToken {
	tokens := map[string]GridToken{}
	if secret, err := keyring.Get(gridTokenService, scope.account()); err == nil {
		if json.Unmarshal([]byte(secret), &tokens) != nil {
			clear(tokens)
		}
	}
	return tokens
}

// GetGridToken returns the token cached for scope and teamID, or nil.
func (s *Store) GetGridToken(scope GridTokenScope, teamID string) *GridToken {
	token, ok := gridTokens(scope)[teamID]
	if !ok {
		return nil
	}
	return &token
}

// SetGridToken caches token in the keyring. Without a keyring it warns via
// warnWriter (if non-nil) and skips the cache; minting again is cheap.
func (s *Store) SetGridToken(scope GridTokenScope, teamID string, token GridToken, warnWriter func(string)) {
	tokens := gridTokens(scope)
	tokens[teamID] = token
	secret, _ := json.Marshal(tokens)
	if err := keyring.Set(gridTokenService, scope.account(), string(secret)); err != nil && warnWriter != nil {
		warnWriter(fmt.Sprintf("warning: could not cache the Grid token in the system keyring: %v\n", err))
	}
}

// ClearGridTokens forgets every Grid token cached for scope.
func (s *Store) ClearGridTokens(scope GridTokenScope) {
	_ = keyring.Delete(gridTokenService, scope.account())
}
