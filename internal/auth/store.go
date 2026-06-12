package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

const authFileName = "auth.json"

// keyringServiceName namespaces CLI profile secrets in the system keyring,
// keyed by profile name.
const keyringServiceName = "baseten-profile"

// AuthType identifies how a credential was obtained.
type AuthType string

const (
	AuthTypeOAuth  AuthType = "oauth"
	AuthTypeAPIKey AuthType = "api_key"
)

// OAuthCredential holds an OAuth access and refresh token pair, along with
// the access token's absolute expiry. Expiry is what golang.org/x/oauth2 uses
// to decide whether to refresh; persisting it lets refresh work across CLI
// invocations.
type OAuthCredential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry,omitempty"`
}

// Profile is a single named, self-contained credential: a remote URL plus the
// auth used against it. The profile name is the keyring account and the
// auth.json map key.
type Profile struct {
	RemoteURL string   `json:"remote_url"`
	AuthType  AuthType `json:"auth_type"`

	// InsecureOAuthCredential and InsecureAPIKey are only populated when the
	// keyring is unavailable or --insecure-storage was used.
	InsecureOAuthCredential *OAuthCredential `json:"oauth_credential,omitempty"`
	InsecureAPIKey          string           `json:"api_key,omitempty"`
}

// AuthFile is the on-disk auth.json structure. Unrecognized keys are ignored
// on read and dropped on the next write.
type AuthFile struct {
	Version int `json:"version"`
	// Current is the name of the profile used when no profile is selected via
	// flag or environment.
	Current  string             `json:"current,omitempty"`
	Profiles map[string]Profile `json:"profiles"`
}

// Store manages reading and writing auth.json and keyring secrets.
type Store struct {
	dir             string
	insecureStorage bool
	mu              sync.Mutex
}

// StoreOptions configures a Store.
type StoreOptions struct {
	// Dir is the directory where auth.json is stored.
	Dir string

	// InsecureStorage, when true, stores secrets in auth.json instead of
	// the system keyring.
	InsecureStorage bool
}

// NewStore creates a Store from the given options.
func NewStore(opts StoreOptions) *Store {
	return &Store{dir: opts.Dir, insecureStorage: opts.InsecureStorage}
}

// DefaultConfigDir returns the config directory:
// $BASETEN_CONFIG_DIR > os.UserConfigDir()/baseten
func DefaultConfigDir() (string, error) {
	if d := os.Getenv("BASETEN_CONFIG_DIR"); d != "" {
		return d, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining config directory: %w", err)
	}
	return filepath.Join(d, "baseten"), nil
}

func (s *Store) path() string {
	return filepath.Join(s.dir, authFileName)
}

// Load reads auth.json from disk. Returns an empty AuthFile if it does not
// exist.
func (s *Store) Load() (*AuthFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (*AuthFile, error) {
	data, err := os.ReadFile(s.path())
	if os.IsNotExist(err) {
		return &AuthFile{Version: 1, Profiles: map[string]Profile{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.path(), err)
	}
	var af AuthFile
	if err := json.Unmarshal(data, &af); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", s.path(), err)
	}
	if af.Profiles == nil {
		af.Profiles = map[string]Profile{}
	}
	return &af, nil
}

func (s *Store) saveLocked(af *AuthFile) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	af.Version = 1
	data, err := json.MarshalIndent(af, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling auth file: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(s.path(), data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", s.path(), err)
	}
	return nil
}

// SetOAuthProfile stores an OAuth credential under name. When switchCurrent is
// true, the profile also becomes the current profile. Tries the keyring first;
// falls back to plaintext in auth.json with a warning written to warnWriter
// (if non-nil).
func (s *Store) SetOAuthProfile(profileName, remoteURL string, cred OAuthCredential, switchCurrent bool, warnWriter func(string)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	af, err := s.loadLocked()
	if err != nil {
		return err
	}

	profile := Profile{RemoteURL: remoteURL, AuthType: AuthTypeOAuth}
	if !s.insecureStorage {
		secretJSON, err := json.Marshal(cred)
		if err != nil {
			return fmt.Errorf("marshaling credential: %w", err)
		}
		if err := keyring.Set(keyringServiceName, profileName, string(secretJSON)); err != nil {
			if warnWriter != nil {
				warnWriter("warning: could not store credentials in system keyring, storing in plain text\n")
			}
			profile.InsecureOAuthCredential = &cred
		}
	} else {
		profile.InsecureOAuthCredential = &cred
	}

	af.Profiles[profileName] = profile
	if switchCurrent {
		af.Current = profileName
	}
	return s.saveLocked(af)
}

// SetAPIKeyProfile stores an API key credential under name. When switchCurrent
// is true, the profile also becomes the current profile.
func (s *Store) SetAPIKeyProfile(profileName, remoteURL, apiKey string, switchCurrent bool, warnWriter func(string)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	af, err := s.loadLocked()
	if err != nil {
		return err
	}

	profile := Profile{RemoteURL: remoteURL, AuthType: AuthTypeAPIKey}
	if !s.insecureStorage {
		if err := keyring.Set(keyringServiceName, profileName, apiKey); err != nil {
			if warnWriter != nil {
				warnWriter("warning: could not store credentials in system keyring, storing in plain text\n")
			}
			profile.InsecureAPIKey = apiKey
		}
	} else {
		profile.InsecureAPIKey = apiKey
	}

	af.Profiles[profileName] = profile
	if switchCurrent {
		af.Current = profileName
	}
	return s.saveLocked(af)
}

// GetOAuthCredential retrieves the OAuth credential for a profile, trying the
// keyring first, then falling back to the auth.json plaintext field.
func (s *Store) GetOAuthCredential(profileName string) (*OAuthCredential, error) {
	secret, err := keyring.Get(keyringServiceName, profileName)
	if err == nil {
		var cred OAuthCredential
		if err := json.Unmarshal([]byte(secret), &cred); err != nil {
			return nil, fmt.Errorf("parsing keyring credential: %w", err)
		}
		return &cred, nil
	}

	af, err := s.Load()
	if err != nil {
		return nil, err
	}
	profile, ok := af.Profiles[profileName]
	if !ok {
		return nil, fmt.Errorf("no profile named %q", profileName)
	}
	if profile.InsecureOAuthCredential == nil {
		return nil, fmt.Errorf("no OAuth credential found for profile %q", profileName)
	}
	return profile.InsecureOAuthCredential, nil
}

// GetAPIKey retrieves the API key for a profile, trying the keyring first, then
// falling back to the auth.json plaintext field.
func (s *Store) GetAPIKey(profileName string) (string, error) {
	secret, err := keyring.Get(keyringServiceName, profileName)
	if err == nil {
		return secret, nil
	}

	af, err := s.Load()
	if err != nil {
		return "", err
	}
	profile, ok := af.Profiles[profileName]
	if !ok {
		return "", fmt.Errorf("no profile named %q", profileName)
	}
	if profile.InsecureAPIKey == "" {
		return "", fmt.Errorf("no API key found for profile %q", profileName)
	}
	return profile.InsecureAPIKey, nil
}

// RemoveProfile removes a profile and deletes its keyring entry. If the removed
// profile was current, the current pointer is cleared.
func (s *Store) RemoveProfile(profileName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = keyring.Delete(keyringServiceName, profileName)

	af, err := s.loadLocked()
	if err != nil {
		return err
	}
	delete(af.Profiles, profileName)
	if af.Current == profileName {
		af.Current = ""
	}
	return s.saveLocked(af)
}

// SwitchProfile sets the current profile. Returns an error if it does not
// exist.
func (s *Store) SwitchProfile(profileName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	af, err := s.loadLocked()
	if err != nil {
		return err
	}
	if _, ok := af.Profiles[profileName]; !ok {
		return fmt.Errorf("profile %q not found", profileName)
	}
	af.Current = profileName
	return s.saveLocked(af)
}

// GetProfile returns the profile with the given name.
func (s *Store) GetProfile(profileName string) (Profile, bool) {
	af, err := s.Load()
	if err != nil {
		return Profile{}, false
	}
	profile, ok := af.Profiles[profileName]
	return profile, ok
}

// CurrentProfile returns the current profile name and entry.
func (s *Store) CurrentProfile() (name string, profile Profile, ok bool) {
	af, err := s.Load()
	if err != nil || af.Current == "" {
		return "", Profile{}, false
	}
	profile, ok = af.Profiles[af.Current]
	if !ok {
		return "", Profile{}, false
	}
	return af.Current, profile, true
}

// ProfileNames returns the stored profile names in sorted order.
func (s *Store) ProfileNames() ([]string, error) {
	af, err := s.Load()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(af.Profiles))
	for name := range af.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
