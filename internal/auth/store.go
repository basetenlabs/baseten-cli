package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

const authFileName = "auth.json"

// AuthType identifies how a credential was obtained.
type AuthType string

const (
	AuthTypeOAuth  AuthType = "oauth"
	AuthTypeAPIKey AuthType = "api_key"
)

// OAuthCredential holds an OAuth access and refresh token pair.
type OAuthCredential struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// UserEntry is a single stored credential in auth.json.
type UserEntry struct {
	AuthType AuthType `json:"auth_type"`

	// InsecureOAuthCredential and InsecureAPIKey are only populated when
	// the keyring is unavailable or --insecure-storage was used.
	InsecureOAuthCredential *OAuthCredential `json:"oauth_credential,omitempty"`
	InsecureAPIKey          string           `json:"api_key,omitempty"`
}

// HostEntry holds all users for a single host.
type HostEntry struct {
	ActiveUser string               `json:"active_user"`
	Users      map[string]UserEntry `json:"users"`
}

// AuthFile is the on-disk auth.json structure.
type AuthFile struct {
	Version int                  `json:"version"`
	Hosts   map[string]HostEntry `json:"hosts"`
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

func keyringService(host string) string {
	return "baseten:" + host
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
		return &AuthFile{Version: 1, Hosts: map[string]HostEntry{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.path(), err)
	}
	var af AuthFile
	if err := json.Unmarshal(data, &af); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", s.path(), err)
	}
	if af.Hosts == nil {
		af.Hosts = map[string]HostEntry{}
	}
	return &af, nil
}

// Save writes auth.json to disk, creating the directory if needed.
func (s *Store) Save(af *AuthFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(af)
}

func (s *Store) saveLocked(af *AuthFile) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
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

// SetOAuthUser stores an OAuth credential for a user and sets them as active.
// Tries the keyring first; falls back to plaintext in auth.json with a
// warning written to warnWriter (if non-nil).
func (s *Store) SetOAuthUser(host, label string, cred OAuthCredential, warnWriter func(string)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	af, err := s.loadLocked()
	if err != nil {
		return err
	}

	he := s.getOrCreateHost(af, host)
	entry := UserEntry{AuthType: AuthTypeOAuth}

	if !s.insecureStorage {
		secretJSON, err := json.Marshal(cred)
		if err != nil {
			return fmt.Errorf("marshaling credential: %w", err)
		}
		if err := keyring.Set(keyringService(host), label, string(secretJSON)); err != nil {
			if warnWriter != nil {
				warnWriter("warning: could not store credentials in system keyring, storing in plain text\n")
			}
			entry.InsecureOAuthCredential = &cred
		}
	} else {
		entry.InsecureOAuthCredential = &cred
	}

	he.Users[label] = entry
	he.ActiveUser = label
	af.Hosts[host] = he
	return s.saveLocked(af)
}

// SetAPIKeyUser stores an API key credential for a user and sets them as
// active.
func (s *Store) SetAPIKeyUser(host, label, apiKey string, warnWriter func(string)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	af, err := s.loadLocked()
	if err != nil {
		return err
	}

	he := s.getOrCreateHost(af, host)
	entry := UserEntry{AuthType: AuthTypeAPIKey}

	if !s.insecureStorage {
		if err := keyring.Set(keyringService(host), label, apiKey); err != nil {
			if warnWriter != nil {
				warnWriter("warning: could not store credentials in system keyring, storing in plain text\n")
			}
			entry.InsecureAPIKey = apiKey
		}
	} else {
		entry.InsecureAPIKey = apiKey
	}

	he.Users[label] = entry
	he.ActiveUser = label
	af.Hosts[host] = he
	return s.saveLocked(af)
}

// GetOAuthCredential retrieves the OAuth credential for a user, trying the
// keyring first, then falling back to the auth.json plaintext field.
func (s *Store) GetOAuthCredential(host, label string) (*OAuthCredential, error) {
	secret, err := keyring.Get(keyringService(host), label)
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
	he, ok := af.Hosts[host]
	if !ok {
		return nil, fmt.Errorf("no credentials for host %s", host)
	}
	ue, ok := he.Users[label]
	if !ok {
		return nil, fmt.Errorf("no credential for user %q on host %s", label, host)
	}
	if ue.InsecureOAuthCredential == nil {
		return nil, fmt.Errorf("no OAuth credential found for user %q on host %s", label, host)
	}
	return ue.InsecureOAuthCredential, nil
}

// GetAPIKey retrieves the API key for a user, trying the keyring first, then
// falling back to the auth.json plaintext field.
func (s *Store) GetAPIKey(host, label string) (string, error) {
	secret, err := keyring.Get(keyringService(host), label)
	if err == nil {
		return secret, nil
	}

	af, err := s.Load()
	if err != nil {
		return "", err
	}
	he, ok := af.Hosts[host]
	if !ok {
		return "", fmt.Errorf("no credentials for host %s", host)
	}
	ue, ok := he.Users[label]
	if !ok {
		return "", fmt.Errorf("no credential for user %q on host %s", label, host)
	}
	if ue.InsecureAPIKey == "" {
		return "", fmt.Errorf("no API key found for user %q on host %s", label, host)
	}
	return ue.InsecureAPIKey, nil
}

// RemoveUser removes a user from a host and deletes their keyring entry. If
// the removed user was active, active_user is cleared.
func (s *Store) RemoveUser(host, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = keyring.Delete(keyringService(host), label)

	af, err := s.loadLocked()
	if err != nil {
		return err
	}

	he, ok := af.Hosts[host]
	if !ok {
		return nil
	}
	delete(he.Users, label)
	if he.ActiveUser == label {
		he.ActiveUser = ""
	}
	af.Hosts[host] = he
	return s.saveLocked(af)
}

// SwitchUser sets the active user for a host. Returns an error if the user
// does not exist.
func (s *Store) SwitchUser(host, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	af, err := s.loadLocked()
	if err != nil {
		return err
	}

	he, ok := af.Hosts[host]
	if !ok {
		return fmt.Errorf("no credentials stored for host %s", host)
	}
	if _, ok := he.Users[label]; !ok {
		return fmt.Errorf("user %q not found for host %s", label, host)
	}
	he.ActiveUser = label
	af.Hosts[host] = he
	return s.saveLocked(af)
}

// ActiveUser returns the active user label and entry for a host.
func (s *Store) ActiveUser(host string) (label string, entry UserEntry, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	af, err := s.loadLocked()
	if err != nil {
		return "", UserEntry{}, false
	}
	he, exists := af.Hosts[host]
	if !exists || he.ActiveUser == "" {
		return "", UserEntry{}, false
	}
	ue, exists := he.Users[he.ActiveUser]
	if !exists {
		return "", UserEntry{}, false
	}
	return he.ActiveUser, ue, true
}

func (s *Store) getOrCreateHost(af *AuthFile, host string) HostEntry {
	he, ok := af.Hosts[host]
	if !ok {
		he = HostEntry{Users: map[string]UserEntry{}}
	}
	return he
}
