// Package ssh implements the local mechanics behind the SSH access commands:
// generating a keypair, writing the managed ~/.ssh/config block, signing a
// certificate against the REST API, and relaying a connection through the SSH
// proxy. It handles both inference model and training job workloads, mirroring
// truss's proxy_command.py, and holds no knowledge of the command harness:
// callers pass in plain values and an API client.
package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// keyNames are the private key filenames recognized when reusing an existing
// key, preferring ed25519. New keys are always ed25519.
var keyNames = []string{"id_ed25519", "id_rsa"}

// dir returns the directory holding the keypair, cert, and JWT cache.
func dir() (string, error) {
	if d := os.Getenv("BASETEN_SSH_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".ssh", "baseten"), nil
}

// configPath returns the SSH client config file to manage.
func configPath() (string, error) {
	if p := os.Getenv("BASETEN_SSH_CONFIG_PATH"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// EnsureKeypair generates an ed25519 keypair in the baseten SSH directory if
// none exists. It returns the private key path and whether an existing key was
// reused.
func EnsureKeypair() (keyPath string, reused bool, err error) {
	d, err := dir()
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", false, fmt.Errorf("creating ssh directory: %w", err)
	}
	if err := os.Chmod(d, 0o700); err != nil {
		return "", false, fmt.Errorf("securing ssh directory: %w", err)
	}
	if existing := findKey(d); existing != "" {
		return existing, true, nil
	}

	// Generate the key and marshal it to the OpenSSH on-disk formats: a PEM
	// private key and an authorized_keys public line.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", false, fmt.Errorf("generating ed25519 key: %w", err)
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "baseten")
	if err != nil {
		return "", false, fmt.Errorf("marshaling private key: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", false, fmt.Errorf("marshaling public key: %w", err)
	}

	keyPath = filepath.Join(d, "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(pemBlock), 0o600); err != nil {
		return "", false, fmt.Errorf("writing private key: %w", err)
	}
	if err := os.WriteFile(keyPath+".pub", ssh.MarshalAuthorizedKey(sshPub), 0o644); err != nil {
		return "", false, fmt.Errorf("writing public key: %w", err)
	}
	return keyPath, false, nil
}

// findKey returns the path to an existing private key in d, or "".
func findKey(d string) string {
	for _, name := range keyNames {
		p := filepath.Join(d, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// jwtCache is the on-disk handoff written by Sign and read by Proxy.
type jwtCache struct {
	JWT          string `json:"jwt"`
	ProxyAddress string `json:"proxy_address"`
}

func jwtCachePath(d string, h hostname) string {
	parts := []string{"model", h.id}
	if h.kind == workloadTraining {
		parts[0] = "training"
	}
	// The env form carries no deployment id, so key on the environment instead
	// (with an "env" tag so an env never collides with a deployment id). sign
	// and proxy both parse the same hostname, so the keys stay consistent.
	if h.env != "" {
		parts = append(parts, "env", h.env)
	}
	if h.deploymentID != "" {
		parts = append(parts, h.deploymentID)
	}
	if h.replica != "" {
		parts = append(parts, h.replica)
	}
	return filepath.Join(d, ".jwt-cache", strings.Join(parts, "-")+".json")
}

func saveJWT(d string, h hostname, jwt, proxyAddr string) error {
	if err := os.MkdirAll(filepath.Join(d, ".jwt-cache"), 0o700); err != nil {
		return fmt.Errorf("creating jwt cache directory: %w", err)
	}
	b, err := json.Marshal(jwtCache{JWT: jwt, ProxyAddress: proxyAddr})
	if err != nil {
		return fmt.Errorf("encoding jwt cache: %w", err)
	}
	if err := os.WriteFile(jwtCachePath(d, h), b, 0o600); err != nil {
		return fmt.Errorf("writing jwt cache: %w", err)
	}
	return nil
}

func loadJWT(d string, h hostname) (jwtCache, bool) {
	b, err := os.ReadFile(jwtCachePath(d, h))
	if err != nil {
		return jwtCache{}, false
	}
	var c jwtCache
	if err := json.Unmarshal(b, &c); err != nil || c.JWT == "" || c.ProxyAddress == "" {
		return jwtCache{}, false
	}
	return c, true
}
