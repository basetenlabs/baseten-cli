package auth

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/basetenlabs/baseten-go/client"
	"golang.org/x/oauth2"
)

// Session is the auth context resolved once per invocation. Exactly one of the
// credential paths applies: a stored profile (ProfileName set) or ephemeral env
// credentials (ephemeralAPIKey set).
type Session struct {
	store           *Store
	profileName     string
	ephemeralAPIKey string
	remoteURL       string
}

// ResolveSession determines the effective auth for this invocation, most
// specific source first:
//
//  1. profileFlag (the --profile flag)
//  2. BASETEN_API_KEY (+ optional BASETEN_REMOTE_URL), ephemeral
//  3. BASETEN_PROFILE
//  4. the current profile in auth.json
//
// A named profile that does not exist is not an error here; the failure
// surfaces when a credential is actually needed (or in the auth commands that
// manage profiles directly).
func ResolveSession(profileFlag string) (*Session, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return nil, err
	}
	store := NewStore(StoreOptions{Dir: dir})
	s := &Session{store: store}

	if profileFlag != "" {
		s.profileName = profileFlag
		if p, ok := store.GetProfile(profileFlag); ok {
			s.remoteURL = p.RemoteURL
		}
		return s, nil
	}

	if key := os.Getenv("BASETEN_API_KEY"); key != "" {
		s.ephemeralAPIKey = key
		s.remoteURL = os.Getenv("BASETEN_REMOTE_URL")
		return s, nil
	}

	if envProfile := os.Getenv("BASETEN_PROFILE"); envProfile != "" {
		s.profileName = envProfile
		if p, ok := store.GetProfile(envProfile); ok {
			s.remoteURL = p.RemoteURL
		}
		return s, nil
	}

	if name, p, ok := store.CurrentProfile(); ok {
		s.profileName = name
		s.remoteURL = p.RemoteURL
	}
	return s, nil
}

// RemoteURL is the remote the resolved session targets. It may be empty, in
// which case the caller applies its own default.
func (s *Session) RemoteURL() string { return s.remoteURL }

// ProfileName is the stored profile this session uses, or "" when the session
// is ephemeral or unauthenticated.
func (s *Session) ProfileName() string { return s.profileName }

// IsEphemeral reports whether the session uses credentials from BASETEN_API_KEY
// rather than a stored profile.
func (s *Session) IsEphemeral() bool { return s.ephemeralAPIKey != "" }

// OAuthContext returns a context that carries an oauth2 HTTP client which
// stamps the Baseten User-Agent on every request, layered over base.
func OAuthContext(ctx context.Context, base http.RoundTripper) context.Context {
	if base == nil {
		base = http.DefaultTransport
	}
	c := &http.Client{Transport: userAgentRoundTripper{base: base}}
	return context.WithValue(ctx, oauth2.HTTPClient, c)
}

type userAgentRoundTripper struct{ base http.RoundTripper }

func (r userAgentRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	client.ApplyUserAgentHeader(req.Header)
	return r.base.RoundTrip(req)
}

// Transport is an HTTP client that injects the appropriate Authorization
// header for the resolved session. For OAuth credentials, it uses
// oauth2.TokenSource for transparent token refresh.
//
// It implements the Do(*http.Request) (*http.Response, error) interface
// expected by the baseten-go SDK clients.
type Transport struct {
	Session *Session

	// OAuthConfig is the OAuth2 configuration used for token refresh.
	OAuthConfig *oauth2.Config

	Base http.RoundTripper
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *Transport) Do(req *http.Request) (*http.Response, error) {
	if t.Session.ephemeralAPIKey != "" {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Api-Key "+t.Session.ephemeralAPIKey)
		return t.base().RoundTrip(req)
	}

	if t.Session.profileName == "" {
		return nil, fmt.Errorf("not logged in; run `baseten auth login` or set BASETEN_API_KEY")
	}

	profileName := t.Session.profileName
	profile, ok := t.Session.store.GetProfile(profileName)
	if !ok {
		return nil, fmt.Errorf("profile %q not found; run `baseten auth login`", profileName)
	}

	switch profile.AuthType {
	case AuthTypeAPIKey:
		apiKey, err := t.Session.store.GetAPIKey(profileName)
		if err != nil {
			return nil, err
		}
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Api-Key "+apiKey)
		return t.base().RoundTrip(req)

	case AuthTypeOAuth:
		if t.OAuthConfig == nil {
			return nil, fmt.Errorf("OAuth credential requires OAuthConfig to be set on Transport")
		}
		cred, err := t.Session.store.GetOAuthCredential(profileName)
		if err != nil {
			return nil, err
		}
		token := &oauth2.Token{
			AccessToken:  cred.AccessToken,
			RefreshToken: cred.RefreshToken,
			Expiry:       cred.Expiry,
			TokenType:    "Bearer",
		}
		src := t.OAuthConfig.TokenSource(OAuthContext(req.Context(), t.base()), token)
		newToken, err := src.Token()
		if err != nil {
			return nil, fmt.Errorf("token expired and refresh failed: %w (run `baseten auth login` to re-authenticate)", err)
		}
		if newToken.AccessToken != cred.AccessToken {
			updated := OAuthCredential{
				AccessToken:  newToken.AccessToken,
				RefreshToken: newToken.RefreshToken,
				Expiry:       newToken.Expiry,
			}
			if err := t.Session.store.SetOAuthProfile(profileName, profile.RemoteURL, updated, false, nil); err != nil {
				return nil, fmt.Errorf("storing refreshed credential: %w", err)
			}
		}
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer "+newToken.AccessToken)
		return t.base().RoundTrip(req)

	default:
		return nil, fmt.Errorf("unknown auth type %q", profile.AuthType)
	}
}
