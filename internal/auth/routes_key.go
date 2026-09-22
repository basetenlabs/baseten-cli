package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const routesKeyService = "baseten-harness-routes"

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
// never print it. A pending record means a previous request may have created a
// key but lost its response. Repeating creation could leave an orphaned key.
// Review that named key in Baseten before clearing the record and retrying.
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
	if record.Key == "" {
		return "", errors.New("invalid routes key in system keyring")
	}
	return record.Key, nil
}

// EnsureRoutesKey saves the secret only in the system keyring. There is no
// plaintext fallback. A pending marker is stored before invoking create so a
// crash, ambiguous API failure or failed save blocks a later retry. The create
// callback sets rejected only when it knows the server did not create a key.
// Concurrent setup invocations are not supported; this marker is not a lock.
func (s *Store) EnsureRoutesKey(scope RoutesKeyScope, create func() (key string, rejected bool, err error)) (created bool, err error) {
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
	key, rejected, err := create()
	if err != nil {
		if rejected {
			if deleteErr := keyring.Delete(routesKeyService, scope.account()); deleteErr != nil {
				return false, fmt.Errorf("%w; cannot clear the pending keyring record", err)
			}
			return false, err
		}
		return false, fmt.Errorf("%w; automatic retry is blocked until this attempt is reviewed", err)
	}
	if key == "" {
		return false, errors.New("API returned an invalid routes key; creation may have succeeded, so review the attempt before retrying")
	}
	data, _ := json.Marshal(routesKeyRecord{Key: key})
	if err := keyring.Set(routesKeyService, scope.account(), string(data)); err != nil {
		return false, fmt.Errorf("routes key %q was created but could not be saved; revoke it in Baseten before resolving the pending keyring entry", scope.Name)
	}
	return true, nil
}
