package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
)

// sandboxTokenService is the keyring service for cached sandbox tokens, one
// entry per [SandboxTokenScope]. Without it, every sandbox command would mint
// a new token before its first call.
const sandboxTokenService = "baseten-sandbox-token"

// sandboxTokenFileName holds cached sandbox tokens by scope hash when the
// keyring is unavailable. Kept out of auth.json, which sandbox commands would
// otherwise rewrite on every mint while other invocations read it.
const sandboxTokenFileName = "auth.sandbox.json"

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
// keyring first, then falls back to the plaintext file. Only ctx's error is
// returned: a cache that cannot be read is a miss.
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
	token, ok := s.loadSandboxTokens()[scope.account()]
	if !ok {
		return nil, nil
	}
	return &token, nil
}

// SetSandboxToken caches token. Tries the keyring first; falls back to the
// plaintext file. Only ctx's error is returned: failing to cache never fails
// the caller, which still has the token.
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
	tokens := s.loadSandboxTokens()
	tokens[scope.account()] = token
	_ = s.saveSandboxTokens(tokens)
	return nil
}

// ClearSandboxToken forgets the token cached for scope, in the keyring and in
// the plaintext file.
func (s *Store) ClearSandboxToken(ctx context.Context, scope SandboxTokenScope) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = keyringDo(ctx, func() error { return keyring.Delete(sandboxTokenService, scope.account()) })
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	tokens := s.loadSandboxTokens()
	if _, ok := tokens[scope.account()]; !ok {
		return nil
	}
	delete(tokens, scope.account())
	return s.saveSandboxTokens(tokens)
}

// loadSandboxTokens reads the plaintext file. One that is missing or cannot
// be read is empty.
func (s *Store) loadSandboxTokens() map[string]SandboxToken {
	data, err := os.ReadFile(filepath.Join(s.dir, sandboxTokenFileName))
	if err != nil {
		return map[string]SandboxToken{}
	}
	tokens := map[string]SandboxToken{}
	if json.Unmarshal(data, &tokens) != nil {
		return map[string]SandboxToken{}
	}
	return tokens
}

// saveSandboxTokens replaces the plaintext file atomically, so a concurrent
// invocation never reads it half-written.
func (s *Store) saveSandboxTokens(tokens map[string]SandboxToken) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(tokens)
	// Created 0600, in the same directory so the rename stays on one
	// filesystem.
	file, err := os.CreateTemp(s.dir, sandboxTokenFileName+".*")
	if err != nil {
		return err
	}
	// A no-op once renamed.
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(s.dir, sandboxTokenFileName))
}
