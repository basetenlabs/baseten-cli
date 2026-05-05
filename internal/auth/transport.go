package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/basetenlabs/baseten-go/client"
	"golang.org/x/oauth2"
)

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
// header based on the active credential. For OAuth credentials, it uses
// oauth2.TokenSource for transparent token refresh.
//
// It implements the Do(*http.Request) (*http.Response, error) interface
// expected by the baseten-go SDK clients.
type Transport struct {
	Store *Store
	Host  string

	// OAuthConfig is the OAuth2 configuration used for token refresh.
	OAuthConfig *oauth2.Config

	// EnvAPIKey, if set, takes priority over all stored credentials.
	EnvAPIKey string

	Base http.RoundTripper
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *Transport) Do(req *http.Request) (*http.Response, error) {
	if t.EnvAPIKey != "" {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Api-Key "+t.EnvAPIKey)
		return t.base().RoundTrip(req)
	}

	label, entry, ok := t.Store.ActiveUser(t.Host)
	if !ok {
		return nil, fmt.Errorf("not logged in; run `baseten auth login` or set BASETEN_API_KEY")
	}

	switch entry.AuthType {
	case AuthTypeAPIKey:
		apiKey, err := t.Store.GetAPIKey(t.Host, label)
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
		cred, err := t.Store.GetOAuthCredential(t.Host, label)
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
			if err := t.Store.SetOAuthUser(t.Host, label, updated, nil); err != nil {
				return nil, fmt.Errorf("storing refreshed credential: %w", err)
			}
		}
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer "+newToken.AccessToken)
		return t.base().RoundTrip(req)

	default:
		return nil, fmt.Errorf("unknown auth type %q", entry.AuthType)
	}
}
