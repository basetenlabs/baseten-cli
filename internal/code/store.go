// Package code owns local Baseten Code installation state. It deliberately has
// no credential-issuance API: the dedicated server contract is not published.
package code

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/basetenlabs/baseten-cli/internal/auth"
)

const Endpoint = "https://coding.baseten.co"
const CredentialDependency = "Code credential creation, listing, and revocation require dedicated backend endpoints that are not yet integrated; ordinary API keys and OAuth access tokens cannot substitute for a Code key"
const CatalogRefreshGap = "Active-session catalog refresh is not implemented; sync is a manual recovery operation. Restart the harness after configuration changes."

type Installation struct {
	Version int                       `json:"version"`
	Org     string                    `json:"org"`
	Profile string                    `json:"profile"`
	UserID  string                    `json:"user_id"`
	KeyID   string                    `json:"key_id"`
	Label   string                    `json:"label,omitempty"`
	Model   string                    `json:"model"`
	Configs map[string]*ManagedConfig `json:"configs"`
}

// Secrets use the CLI's existing keyring/fallback machinery in a separate
// store and namespace. Neither ambient credentials nor a switched profile can
// change the key emitted by the helper.
type Store struct{ Dir string }

func NewStore() (*Store, error) {
	dir, err := auth.DefaultConfigDir()
	if err != nil {
		return nil, err
	}
	dir, err = filepath.Abs(filepath.Join(dir, "code"))
	if err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}
func (s *Store) Path() string { return filepath.Join(s.Dir, "installation.json") }
func (s *Store) Credentials() *auth.Store {
	return auth.NewStore(auth.StoreOptions{Dir: filepath.Join(s.Dir, "credentials")})
}
func credentialName(keyID string) string { return "baseten-code:" + keyID }
func (s *Store) Token(i *Installation) (string, error) {
	if i == nil || i.KeyID == "" {
		return "", errors.New("no Code credential is installed; " + CredentialDependency)
	}
	token, err := s.Credentials().GetAPIKey(credentialName(i.KeyID))
	if err != nil {
		return "", errors.New("stored Code credential is unavailable; sign in again after Code issuance is available")
	}
	if strings.TrimSpace(token) != token || token == "" || strings.ContainsAny(token, "\r\n\t ") {
		return "", errors.New("stored Code credential is invalid")
	}
	return token, nil
}
func (s *Store) Load() (*Installation, error) {
	data, err := os.ReadFile(s.Path())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var i Installation
	if err = json.Unmarshal(data, &i); err != nil {
		return nil, errors.New("invalid Code installation file; restore its backup before continuing")
	}
	if i.Version != 1 || i.KeyID == "" || i.Org == "" {
		return nil, errors.New("unsupported or incomplete Code installation state")
	}
	if i.Configs == nil {
		i.Configs = map[string]*ManagedConfig{}
	}
	for name, record := range i.Configs {
		canonical, err := Canonical(name)
		if err != nil || canonical != name || record == nil || record.Harness != name || !filepath.IsAbs(record.Path) || (record.Format != "toml" && record.Format != "json") {
			return nil, errors.New("invalid Code configuration backup; restore the installation journal before continuing")
		}
		for _, setting := range record.Settings {
			if len(setting.Path) == 0 {
				return nil, errors.New("invalid Code setting backup")
			}
			for _, key := range setting.Path {
				if key == "" {
					return nil, errors.New("invalid Code setting backup")
				}
			}
		}
	}
	return &i, nil
}
func (s *Store) Save(i *Installation) error {
	data, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return err
	}
	snap, err := ReadSnapshot(s.Path())
	if err != nil {
		return err
	}
	if snap.Info != nil && snap.Info.Mode().Perm()&0077 != 0 && os.PathSeparator != '\\' {
		return errors.New("Code installation journal must be private; set its permissions to 0600 before continuing")
	}
	return snap.Write(append(data, '\n'))
}

// Lock serializes cooperating init/sync/teardown processes. Read-only commands
// never create directories or lock files. An interrupted mutation leaves a
// clear recovery instruction, rather than silently stealing another lock.
func (s *Store) Lock() (func(), error) {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(s.Dir, "config.lock")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot lock Code setup; check for another init/sync/teardown process before removing %s: %w", path, err)
	}
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
}

// Snapshot retains identity, bytes, permissions and the resolved symlink
// target. Writes reject concurrent edits and leave an existing symlink intact.
// As with normal editor saves, a non-cooperating writer can still race the
// final check and rename. The installation lock covers other CLI processes.
type Snapshot struct {
	Path   string
	Target string
	Data   []byte
	Info   os.FileInfo
}

func ReadSnapshot(path string) (*Snapshot, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{Path: path, Target: path}
	target, err := filepath.EvalSymlinks(path)
	if err == nil {
		s.Target = target
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		if _, e := os.Lstat(path); e == nil {
			return nil, fmt.Errorf("refusing dangling symlink %s", path)
		}
		// Resolve an existing parent symlink, including when the final file is new.
		parent := filepath.Dir(path)
		suffix := filepath.Base(path)
		for {
			resolved, e := filepath.EvalSymlinks(parent)
			if e == nil {
				s.Target = filepath.Join(resolved, suffix)
				break
			}
			if !os.IsNotExist(e) {
				return nil, e
			}
			next := filepath.Dir(parent)
			if next == parent {
				return nil, e
			}
			suffix = filepath.Join(filepath.Base(parent), suffix)
			parent = next
		}
	}
	s.Info, err = os.Stat(s.Target)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !s.Info.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing non-regular configuration file %s", path)
	}
	s.Data, err = os.ReadFile(s.Target)
	return s, err
}
func (s *Snapshot) Check() error {
	now, err := ReadSnapshot(s.Path)
	if err != nil {
		return err
	}
	same := s.Target == now.Target && bytes.Equal(s.Data, now.Data) && (s.Info == nil) == (now.Info == nil)
	if same && s.Info != nil {
		same = os.SameFile(s.Info, now.Info) && s.Info.ModTime() == now.Info.ModTime() && s.Info.Mode() == now.Info.Mode()
	}
	if !same {
		return fmt.Errorf("configuration changed concurrently: %s; retry after reviewing it", s.Path)
	}
	return nil
}
func (s *Snapshot) Write(data []byte) error {
	if err := s.Check(); err != nil {
		return err
	}
	if s.Info != nil && bytes.Equal(s.Data, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.Target), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.Target), ".baseten-code-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	mode := os.FileMode(0600)
	if s.Info != nil {
		mode = s.Info.Mode().Perm()
	}
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = s.Check(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.Target)
}
