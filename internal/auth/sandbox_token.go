package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/zalando/go-keyring"
)

// sandboxTokenService is the keyring service for cached sandbox tokens, one
// entry per [SandboxTokenScope]. Without it, every sandbox command would mint
// a new token before its first call.
const sandboxTokenService = "baseten-sandbox-token"

// SandboxTokenScope keys the cache by issuing endpoint and
// [Session.CacheIdentity].
type SandboxTokenScope struct {
	ManagementURL string `json:"management_url"`
	Identity      string `json:"identity"`
}

func (s SandboxTokenScope) account() string {
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type SandboxToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GetSandboxToken returns the token cached for scope, or nil. Tries the
// keyring first, then falls back to the auth.json plaintext field. Only ctx's
// error is returned: a cache that cannot be read is a miss.
func (s *Store) GetSandboxToken(ctx context.Context, scope SandboxTokenScope) (*SandboxToken, error) {
	var secret string
	keyringErr := keyringDo(ctx, func() (err error) {
		secret, err = keyring.Get(sandboxTokenService, scope.account())
		return err
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if keyringErr == nil {
		var token SandboxToken
		if json.Unmarshal([]byte(secret), &token) != nil {
			return nil, nil
		}
		return &token, nil
	}
	af, err := s.Load()
	if err != nil {
		// Unreadable cache is a miss.
		return nil, nil
	}
	token, ok := af.InsecureSandboxTokens[scope.account()]
	if !ok {
		return nil, nil
	}
	return &token, nil
}

// SetSandboxToken caches token. Tries the keyring first; falls back to
// plaintext in auth.json. Only ctx's error is returned: failing to cache never
// fails the caller, which still has the token.
func (s *Store) SetSandboxToken(ctx context.Context, scope SandboxTokenScope, token SandboxToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// The fallback is intentionally silent, unlike other secrets': the cache
	// is temporary, until the sandbox APIs accept other credentials, and a
	// token is written on every mint, so a warning would repeat on every few
	// sandbox commands on a machine without a keyring.
	if !s.insecureStorage {
		secret, _ := json.Marshal(token)
		keyringErr := keyringDo(ctx, func() error { return keyring.Set(sandboxTokenService, scope.account(), string(secret)) })
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if keyringErr == nil {
			return nil
		}
	}
	af, err := s.loadLocked()
	if err != nil {
		return nil
	}
	if af.InsecureSandboxTokens == nil {
		af.InsecureSandboxTokens = map[string]SandboxToken{}
	}
	af.InsecureSandboxTokens[scope.account()] = token
	_ = s.saveLocked(af)
	return nil
}

// ClearSandboxToken forgets the token cached for scope, in the keyring and in
// auth.json.
func (s *Store) ClearSandboxToken(ctx context.Context, scope SandboxTokenScope) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = keyringDo(ctx, func() error { return keyring.Delete(sandboxTokenService, scope.account()) })
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	af, err := s.loadLocked()
	if err != nil {
		return err
	}
	if _, ok := af.InsecureSandboxTokens[scope.account()]; !ok {
		return nil
	}
	delete(af.InsecureSandboxTokens, scope.account())
	return s.saveLocked(af)
}
