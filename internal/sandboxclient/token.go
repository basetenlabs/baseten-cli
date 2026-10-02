package sandboxclient

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/basetenlabs/baseten-go/client"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// TokenProvider returns the bearer token for a request. Called for every
// request, so it must be fast or cache on its own.
type TokenProvider func(ctx context.Context) (string, error)

// HTTPDoer is the part of http.Client the SDK uses. Matching http.Client, Do
// closes the request body.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Refresh this long before the token's stated expiry, to allow for clock skew
// between this machine and the server, and for time spent in flight.
const tokenExpiryLeeway = 60 * time.Second

// A few retries cover a new token being invalidated in the same event as the
// old one. Past that, the rejection is returned, since tokens that keep being
// rejected point at something a new token cannot fix.
const tokenInvalidationMaxRetries = 2

// Both planes send this header when they reject a revoked token, which can
// happen before the token's stated expiry, for example after a role change.
// The request was rejected before any work was done, so sending it again with
// a new token is safe even when it has side effects.
const tokenRevokedCode = "TOKEN_REVOKED"

// tokenSource is where each request's bearer token comes from: a caller's
// TokenProvider, or a token minted from the API key, cached until shortly
// before it expires.
//
// The API key is exchanged for a token because the sandbox APIs do not accept
// API keys directly. Should that change, only this type changes.
type tokenSource struct {
	provider     TokenProvider
	mint         func(ctx context.Context) (string, time.Time, error)
	mu           sync.Mutex
	cachedToken  string
	cachedExpiry time.Time
}

func newTokenSource(apiKey string, provider TokenProvider, managementBaseURL string, httpClient HTTPDoer, headers http.Header) (*tokenSource, error) {
	if provider != nil && apiKey != "" {
		return nil, errAPIKeyWithProvider
	}
	if provider != nil {
		return &tokenSource{provider: provider}, nil
	}
	if apiKey == "" {
		// Empty API key with no provider is the advanced opt-out of
		// Authorization entirely.
		return &tokenSource{}, nil
	}
	mintClient, err := client.NewManagementClient(client.ManagementClientOptions{
		APIKey:     apiKey,
		BaseURL:    managementBaseURL,
		HTTPClient: httpClient,
		Headers:    headers,
	})
	if err != nil {
		return nil, err
	}
	mint := func(ctx context.Context) (string, time.Time, error) {
		token, err := mintClient.API().PostToken(ctx, managementapi.CreateTokenRequest{
			Scopes: []managementapi.TokenScope{managementapi.TokenScope_sandboxes},
		})
		if err != nil {
			return "", time.Time{}, err
		}
		return token.Token, token.ExpiresAt, nil
	}
	return &tokenSource{mint: mint}, nil
}

// token returns the token for a request. The empty string means no
// Authorization should be sent.
func (s *tokenSource) token(ctx context.Context) (string, error) {
	if s.provider != nil {
		return s.provider(ctx)
	}
	if s.mint == nil {
		return "", nil
	}
	// The lock spans the expiry check and the mint, so a burst of requests on
	// a cold cache sends a single exchange.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cachedToken != "" && time.Now().Before(s.cachedExpiry.Add(-tokenExpiryLeeway)) {
		return s.cachedToken, nil
	}
	token, expiresAt, err := s.mint(ctx)
	if err != nil {
		return "", err
	}
	s.cachedToken = token
	s.cachedExpiry = expiresAt
	return token, nil
}

// invalidate drops a token the server rejected, so the next request mints a
// new one. Compared by value, so a rejection of an older token never discards
// a newer one another request already minted.
func (s *tokenSource) invalidate(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cachedToken == token {
		s.cachedToken = ""
		s.cachedExpiry = time.Time{}
	}
}

// tokenAuthClient sends every request with a token from the source, and sends
// it again with a fresh one when the token was revoked.
type tokenAuthClient struct {
	inner  HTTPDoer
	tokens *tokenSource
}

func (c *tokenAuthClient) Do(req *http.Request) (*http.Response, error) {
	// A bodyless request is trivially re-sent; a body re-sends only through
	// GetBody, so a streaming raw body, which can be read once, is returned
	// with its revoked response rather than retried.
	bodyless := req.Body == nil
	var sentToken string
	for attempt := 0; ; attempt++ {
		token, err := c.tokens.token(req.Context())
		if err != nil {
			return nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
			sentToken = token
		} else if sentToken != "" {
			// A provider that stops returning a token must not leave the
			// previous attempt's header on the re-send.
			req.Header.Del("Authorization")
			sentToken = ""
		}
		resp, err := c.inner.Do(req)
		if err != nil {
			return nil, err
		}
		revoked := token != "" &&
			resp.StatusCode == http.StatusUnauthorized &&
			resp.Header.Get("x-blaxel-error-code") == tokenRevokedCode
		// Every revoked token leaves the cache, including the last attempt's,
		// so the next request never reuses a token the server just rejected.
		if revoked {
			c.tokens.invalidate(token)
		}
		replayable := bodyless || req.GetBody != nil
		// The last attempt's response is returned even when still revoked,
		// so the caller gets a meaningful error.
		if !revoked || attempt == tokenInvalidationMaxRetries || !replayable {
			return resp, nil
		}
		if !bodyless {
			replayed, err := req.GetBody()
			if err != nil {
				resp.Body.Close()
				return nil, err
			}
			req.Body = replayed
		}
		resp.Body.Close()
	}
}
