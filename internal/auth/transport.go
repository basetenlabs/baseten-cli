package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/basetenlabs/baseten-go/client"
	"golang.org/x/oauth2"
)

// Environment variables that select workload identity federation: the CLI
// exchanges the identity token in IdentityTokenFileEnv for a short-lived
// Baseten access token under the trust rule in TrustRuleIDEnv.
const (
	IdentityTokenFileEnv = "BASETEN_IDENTITY_TOKEN_FILE"
	TrustRuleIDEnv       = "BASETEN_TRUST_RULE_ID"
)

// WorkloadIdentity is the workload identity federation configuration read
// from the environment.
type WorkloadIdentity struct {
	IdentityTokenFile string
	TrustRuleID       string
}

// Session is the auth context resolved once per invocation. Exactly one of the
// credential paths applies: a stored profile (ProfileName set), an ephemeral
// API key from the environment (ephemeralAPIKey set), or workload identity
// from the environment (workloadIdentity set).
type Session struct {
	store            *Store
	profileName      string
	ephemeralAPIKey  string
	workloadIdentity *WorkloadIdentity
	remoteURL        string

	// workloadIdentityShadowed records that the workload identity variables
	// are set but BASETEN_API_KEY took precedence over them.
	workloadIdentityShadowed bool
}

// ResolveSession determines the effective auth for this invocation, most
// specific source first:
//
//  1. profileFlag (the --profile flag)
//  2. BASETEN_API_KEY (+ optional BASETEN_REMOTE_URL), ephemeral
//  3. BASETEN_IDENTITY_TOKEN_FILE and BASETEN_TRUST_RULE_ID (+ optional
//     BASETEN_REMOTE_URL), ephemeral workload identity
//  4. BASETEN_PROFILE
//  5. defaultProfile (a baked-in fallback, e.g. from the SSH config)
//  6. the current profile in auth.json
//
// defaultProfile is "" for most commands; the SSH sign/proxy commands pass
// their --default-profile so a pinned profile is used only when nothing more
// specific selects one.
//
// A named profile that does not exist is not an error here; the failure
// surfaces when a credential is actually needed (or in the auth commands that
// manage profiles directly).
func ResolveSession(profileFlag, defaultProfile string) (*Session, error) {
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
		s.workloadIdentityShadowed = os.Getenv(IdentityTokenFileEnv) != "" || os.Getenv(TrustRuleIDEnv) != ""
		return s, nil
	}

	wi, err := workloadIdentityFromEnv()
	if err != nil {
		return nil, err
	}
	if wi != nil {
		s.workloadIdentity = wi
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

	if defaultProfile != "" {
		s.profileName = defaultProfile
		if p, ok := store.GetProfile(defaultProfile); ok {
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

// workloadIdentityFromEnv returns the workload identity configuration, nil
// when neither variable is set, and an error when only one is: falling through
// to a stored profile would silently run as a different identity.
func workloadIdentityFromEnv() (*WorkloadIdentity, error) {
	file, rule := os.Getenv(IdentityTokenFileEnv), os.Getenv(TrustRuleIDEnv)
	switch {
	case file == "" && rule == "":
		return nil, nil
	case file == "":
		return nil, fmt.Errorf("%s is set but %s is not; workload identity needs both", TrustRuleIDEnv, IdentityTokenFileEnv)
	case rule == "":
		return nil, fmt.Errorf("%s is set but %s is not; workload identity needs both", IdentityTokenFileEnv, TrustRuleIDEnv)
	default:
		return &WorkloadIdentity{IdentityTokenFile: file, TrustRuleID: rule}, nil
	}
}

// RemoteURL is the remote the resolved session targets. It may be empty, in
// which case the caller applies its own default.
func (s *Session) RemoteURL() string { return s.remoteURL }

// ProfileName is the stored profile this session uses, or "" when the session
// is ephemeral or unauthenticated.
func (s *Session) ProfileName() string { return s.profileName }

// IsEphemeral reports whether the session uses credentials from the
// environment (BASETEN_API_KEY or workload identity) rather than a stored
// profile.
func (s *Session) IsEphemeral() bool { return s.ephemeralAPIKey != "" || s.workloadIdentity != nil }

// WorkloadIdentity returns the session's workload identity configuration, or
// nil when the session does not authenticate with workload identity.
func (s *Session) WorkloadIdentity() *WorkloadIdentity { return s.workloadIdentity }

// WorkloadIdentityShadowed reports whether the workload identity variables are
// set but ignored because BASETEN_API_KEY takes precedence.
func (s *Session) WorkloadIdentityShadowed() bool { return s.workloadIdentityShadowed }

// NewWorkloadIdentityCredentials builds the credentials that exchange the
// session's identity token at managementURL. Callers build them once per
// invocation and share them, so the access token is exchanged once and reused.
// A nil httpClient uses http.DefaultClient.
func (s *Session) NewWorkloadIdentityCredentials(managementURL string, httpClient *http.Client) (*client.WorkloadIdentityCredentials, error) {
	if s.workloadIdentity == nil {
		return nil, errors.New("the session does not use workload identity")
	}
	opts := client.WorkloadIdentityCredentialsOptions{
		IdentityTokenFile: s.workloadIdentity.IdentityTokenFile,
		TrustRuleID:       s.workloadIdentity.TrustRuleID,
		BaseURL:           managementURL,
	}
	// Assigned only when set: a nil *http.Client in the interface field would
	// not read as nil to the SDK's default.
	if httpClient != nil {
		opts.HTTPClient = httpClient
	}
	return client.NewWorkloadIdentityCredentials(opts)
}

// UsesAPIKey reports whether the session authenticates with an API key, from
// BASETEN_API_KEY or an API key profile, rather than OAuth.
func (s *Session) UsesAPIKey() bool {
	if s.ephemeralAPIKey != "" {
		return true
	}
	p, ok := s.store.GetProfile(s.profileName)
	return ok && p.AuthType == AuthTypeAPIKey
}

// APIKey returns the session's API key when it authenticates with one — the
// ephemeral BASETEN_API_KEY value, or the stored key of an API key profile —
// and "" for OAuth or unauthenticated sessions, which have no embeddable key.
func (s *Session) APIKey(ctx context.Context) (string, error) {
	if s.ephemeralAPIKey != "" {
		return s.ephemeralAPIKey, nil
	}
	if !s.UsesAPIKey() {
		return "", nil
	}
	return s.store.GetAPIKey(ctx, s.profileName)
}

// CachesCapabilityTokens reports whether sandbox and Grid tokens minted for
// the session may be cached across invocations. Not under workload identity:
// another workload can present a different identity through the same token
// file path, which no cache key derived from the session tells apart.
func (s *Session) CachesCapabilityTokens() bool { return s.workloadIdentity == nil }

// CacheIdentity returns a stable, filesystem-safe token for the resolved
// credential, scoping on-disk caches so an entry written under one credential is
// never read under another. Hashed to keep the profile name (an email by default)
// and the API key out of file paths.
func (s *Session) CacheIdentity() string {
	switch {
	case s.ephemeralAPIKey != "":
		sum := sha256.Sum256([]byte(s.ephemeralAPIKey))
		return "key-" + hex.EncodeToString(sum[:8])
	case s.workloadIdentity != nil:
		// The trust rule and token file together name the workload; a NUL
		// separator keeps distinct pairs from hashing alike.
		sum := sha256.Sum256([]byte(s.workloadIdentity.TrustRuleID + "\x00" + s.workloadIdentity.IdentityTokenFile))
		return "wif-" + hex.EncodeToString(sum[:8])
	case s.profileName != "":
		sum := sha256.Sum256([]byte(s.profileName))
		return "profile-" + hex.EncodeToString(sum[:8])
	default:
		return "anonymous"
	}
}

// OAuthContext returns a context that carries an oauth2 HTTP client which
// stamps the Baseten User-Agent on every request, layered over base. Requests
// made through the client are bound to ctx, so cancelling ctx aborts them.
func OAuthContext(ctx context.Context, base http.RoundTripper) context.Context {
	if base == nil {
		base = http.DefaultTransport
	}
	c := &http.Client{Transport: oauthRoundTripper{ctx: ctx, base: base}}
	return context.WithValue(ctx, oauth2.HTTPClient, c)
}

type oauthRoundTripper struct {
	// oauth2 builds its device auth and token requests without a context, so
	// the caller's context is applied here instead.
	ctx  context.Context
	base http.RoundTripper
}

func (r oauthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(r.ctx)
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

	// WorkloadIdentity exchanges and caches access tokens for a workload
	// identity session. Required when the session uses workload identity.
	WorkloadIdentity client.AccessTokenSource

	Base http.RoundTripper
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *Transport) Do(req *http.Request) (*http.Response, error) {
	// SDK clients set the User-Agent themselves, but raw requests built by
	// commands do not.
	req = req.Clone(req.Context())
	client.ApplyUserAgentHeader(req.Header)
	// A request that already carries a credential keeps it: volume transfers
	// authenticate to the volume service with a capability token over this
	// same client, and the API key would not be read there.
	if req.Header.Get("Authorization") != "" {
		return t.base().RoundTrip(req)
	}
	token, err := t.Credential(req.Context())
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return t.base().RoundTrip(req)
}

// Credential resolves the session's credential as a bearer token: an API key
// verbatim, or an access token from [Transport.AccessToken]. The backend
// accepts either kind under the Bearer scheme, which is also the only shape the
// truss CLI's forwarded credential accepts.
func (t *Transport) Credential(ctx context.Context) (string, error) {
	if t.Session.UsesAPIKey() {
		return t.Session.APIKey(ctx)
	}
	token, err := t.AccessToken(ctx)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// AccessToken returns a current access token for a workload identity or OAuth
// session: a workload identity token is exchanged, or reused while cached; an
// OAuth token is refreshed and persisted when it has expired. API key sessions
// have no access token and get an error.
func (t *Transport) AccessToken(ctx context.Context) (client.AccessToken, error) {
	if t.Session.ephemeralAPIKey != "" {
		return client.AccessToken{}, errors.New("the session authenticates with BASETEN_API_KEY, which is not an access token")
	}

	if wi := t.Session.workloadIdentity; wi != nil {
		if t.WorkloadIdentity == nil {
			return client.AccessToken{}, errors.New("workload identity session requires WorkloadIdentity to be set on Transport")
		}
		token, err := t.WorkloadIdentity.AccessToken(ctx)
		if errors.Is(err, client.ErrInvalidGrant) {
			return client.AccessToken{}, fmt.Errorf("workload identity token exchange refused; check that trust rule %s (%s) "+
				"exists and matches the identity token in %s (%s): %w",
				wi.TrustRuleID, TrustRuleIDEnv, wi.IdentityTokenFile, IdentityTokenFileEnv, err)
		}
		return token, err
	}

	if t.Session.profileName == "" {
		return client.AccessToken{}, fmt.Errorf("not logged in; run `baseten auth login` or set BASETEN_API_KEY")
	}

	profileName := t.Session.profileName
	profile, ok := t.Session.store.GetProfile(profileName)
	if !ok {
		names, err := t.Session.store.ProfileNames()
		if err != nil {
			return client.AccessToken{}, err
		}
		return client.AccessToken{}, NewErrProfileNotFound(names, profileName)
	}

	switch profile.AuthType {
	case AuthTypeAPIKey:
		return client.AccessToken{}, fmt.Errorf("profile %q authenticates with an API key, which is not an access token", profileName)

	case AuthTypeOAuth:
		if t.OAuthConfig == nil {
			return client.AccessToken{}, fmt.Errorf("OAuth credential requires OAuthConfig to be set on Transport")
		}
		cred, err := t.Session.store.GetOAuthCredential(ctx, profileName)
		if err != nil {
			return client.AccessToken{}, err
		}
		token := &oauth2.Token{
			AccessToken:  cred.AccessToken,
			RefreshToken: cred.RefreshToken,
			Expiry:       cred.Expiry,
			TokenType:    "Bearer",
		}
		src := t.OAuthConfig.TokenSource(OAuthContext(ctx, t.base()), token)
		newToken, err := src.Token()
		if err != nil {
			return client.AccessToken{}, fmt.Errorf("token expired and refresh failed: %w (run `baseten auth login` to re-authenticate)", err)
		}
		if newToken.AccessToken != cred.AccessToken {
			updated := OAuthCredential{
				AccessToken:  newToken.AccessToken,
				RefreshToken: newToken.RefreshToken,
				Expiry:       newToken.Expiry,
			}
			if err := t.Session.store.SetOAuthProfile(ctx, profileName, profile.RemoteURL, updated, false, nil); err != nil {
				return client.AccessToken{}, fmt.Errorf("storing refreshed credential: %w", err)
			}
		}
		return client.AccessToken{Token: newToken.AccessToken, ExpiresAt: newToken.Expiry}, nil

	default:
		return client.AccessToken{}, fmt.Errorf("unknown auth type %q", profile.AuthType)
	}
}
