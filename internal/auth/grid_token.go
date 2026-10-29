package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zalando/go-keyring"
)

// gridTokenService is the keyring service for cached Grid tokens, one entry
// per [GridTokenScope] holding every team's token. Tokens are never written to
// auth.json: without a keyring, minting again is cheap.
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
func gridTokens(ctx context.Context, scope GridTokenScope) (map[string]GridToken, error) {
	var secret string
	keyringErr := keyringDo(ctx, func() (err error) {
		secret, err = keyring.Get(gridTokenService, scope.account())
		return err
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	tokens := map[string]GridToken{}
	if keyringErr == nil && json.Unmarshal([]byte(secret), &tokens) != nil {
		clear(tokens)
	}
	return tokens, nil
}

// GetGridToken returns the token cached for scope and teamID, or nil.
func (s *Store) GetGridToken(ctx context.Context, scope GridTokenScope, teamID string) (*GridToken, error) {
	if s.insecureStorage {
		return nil, nil
	}
	tokens, err := gridTokens(ctx, scope)
	if err != nil {
		return nil, err
	}
	token, ok := tokens[teamID]
	if !ok {
		return nil, nil
	}
	return &token, nil
}

// SetGridToken caches token in the keyring. Without a keyring it warns via
// warnWriter (if non-nil) and skips the cache.
func (s *Store) SetGridToken(ctx context.Context, scope GridTokenScope, teamID string, token GridToken, warnWriter func(string)) error {
	if s.insecureStorage {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tokens, err := gridTokens(ctx, scope)
	if err != nil {
		return err
	}
	tokens[teamID] = token
	secret, _ := json.Marshal(tokens)
	keyringErr := keyringDo(ctx, func() error { return keyring.Set(gridTokenService, scope.account(), string(secret)) })
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if keyringErr != nil && warnWriter != nil {
		warnWriter(fmt.Sprintf("warning: could not cache the Grid token in the system keyring: %v\n", keyringErr))
	}
	return nil
}

// ClearGridTokens forgets every Grid token cached for scope.
func (s *Store) ClearGridTokens(ctx context.Context, scope GridTokenScope) error {
	_ = keyringDo(ctx, func() error { return keyring.Delete(gridTokenService, scope.account()) })
	return ctx.Err()
}
