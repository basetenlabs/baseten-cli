package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const routesKeyService = "baseten-harness-routes"

// ErrRoutesKeyRejected marks a definitive API rejection before key creation.
var ErrRoutesKeyRejected = errors.New("routes key creation rejected")

// RoutesKeyScope separates saved keys by issuing endpoint, authenticated user,
// profile, team and installation name. It never stores the login credential.
type RoutesKeyScope struct {
	ManagementURL string `json:"management_url"`
	Profile       string `json:"profile"`
	UserID        string `json:"user_id"`
	TeamID        string `json:"team_id"`
	Name          string `json:"name"`
}

func (s RoutesKeyScope) account() string {
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type routesKeyRecord struct {
	Pending bool   `json:"pending"`
	Key     string `json:"key,omitempty"`
}

// GetRoutesKey returns only a key saved for this exact scope. Callers must
// never print it. A pending attempt blocks retries because POST is not known
// to be idempotent and the server may already have created a key.
func (s *Store) GetRoutesKey(scope RoutesKeyScope) (string, error) {
	raw, err := keyring.Get(routesKeyService, scope.account())
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("cannot read routes key from system keyring")
	}
	var record routesKeyRecord
	if json.Unmarshal([]byte(raw), &record) != nil {
		return "", errors.New("invalid routes key record in system keyring")
	}
	if record.Pending {
		return "", fmt.Errorf("a previous routes key creation attempt is unresolved; review keys named %q in Baseten before removing keyring entry %s/%s and retrying", scope.Name, routesKeyService, scope.account())
	}
	if !validRoutesKey(record.Key) {
		return "", errors.New("invalid routes key in system keyring")
	}
	return record.Key, nil
}

func validRoutesKey(key string) bool { return key != "" && !strings.ContainsAny(key, " \r\n\t") }

// EnsureRoutesKey saves the secret only in the system keyring. There is no
// plaintext fallback. A pending marker is stored before invoking create so a
// crash, ambiguous API failure or failed save cannot cause automatic reminting.
func (s *Store) EnsureRoutesKey(scope RoutesKeyScope, create func() (string, error)) (created bool, err error) {
	if scope.ManagementURL == "" || scope.UserID == "" || scope.TeamID == "" || scope.Name == "" {
		return false, errors.New("incomplete routes key scope")
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return false, err
	}
	lock := filepath.Join(s.dir, "routes-key-"+scope.account()+".lock")
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false, fmt.Errorf("routes key setup is locked; check for another setup before removing %s", lock)
	}
	_ = f.Close()
	defer os.Remove(lock)
	key, err := s.GetRoutesKey(scope)
	if err != nil {
		return false, err
	}
	if key != "" {
		return false, nil
	}
	if err := keyring.Set(routesKeyService, scope.account(), `{"pending":true}`); err != nil {
		return false, errors.New("cannot write to system keyring; no routes key was created")
	}
	key, err = create()
	if err != nil {
		if errors.Is(err, ErrRoutesKeyRejected) {
			if deleteErr := keyring.Delete(routesKeyService, scope.account()); deleteErr != nil {
				return false, fmt.Errorf("%w; cannot clear the pending keyring record", err)
			}
			return false, err
		}
		return false, fmt.Errorf("%w; automatic retry is blocked until this attempt is reviewed", err)
	}
	if !validRoutesKey(key) {
		return false, errors.New("API returned an invalid routes key; creation may have succeeded, so review the attempt before retrying")
	}
	data, _ := json.Marshal(routesKeyRecord{Key: key})
	if err := keyring.Set(routesKeyService, scope.account(), string(data)); err != nil {
		return false, fmt.Errorf("routes key %q was created but could not be saved; revoke it in Baseten before resolving the pending keyring entry", scope.Name)
	}
	return true, nil
}
