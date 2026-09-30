package ssh

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestEnsureKeypair_GeneratesEd25519(t *testing.T) {
	d := t.TempDir()
	t.Setenv("BASETEN_SSH_DIR", d)

	keyPath, reused, err := EnsureKeypair()
	require.NoError(t, err)
	require.False(t, reused)
	require.Equal(t, filepath.Join(d, "id_ed25519"), keyPath)

	// The private key parses as ed25519 and both files exist with the expected
	// permissions.
	privBytes, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	key, err := ssh.ParseRawPrivateKey(privBytes)
	require.NoError(t, err)
	require.IsType(t, &ed25519.PrivateKey{}, key)

	requirePerm(t, keyPath, 0o600)
	requirePerm(t, keyPath+".pub", 0o644)
	requirePerm(t, d, 0o700)
}

func TestEnsureKeypair_ReusesExisting(t *testing.T) {
	d := t.TempDir()
	t.Setenv("BASETEN_SSH_DIR", d)

	first, reused, err := EnsureKeypair()
	require.NoError(t, err)
	require.False(t, reused)

	second, reused, err := EnsureKeypair()
	require.NoError(t, err)
	require.True(t, reused)
	require.Equal(t, first, second)
}

func TestEnsureKeypair_ReusesExistingRSA(t *testing.T) {
	d := t.TempDir()
	t.Setenv("BASETEN_SSH_DIR", d)
	rsaPath := filepath.Join(d, "id_rsa")
	require.NoError(t, os.MkdirAll(d, 0o700))
	require.NoError(t, os.WriteFile(rsaPath, []byte("dummy"), 0o600))

	keyPath, reused, err := EnsureKeypair()
	require.NoError(t, err)
	require.True(t, reused)
	require.Equal(t, rsaPath, keyPath)
}

func TestJWTCacheRoundTrip(t *testing.T) {
	d := t.TempDir()
	h := hostname{kind: workloadModel, id: "abc", deploymentID: "def", replica: "7"}

	require.NoError(t, saveJWT(d, "profile-abc123", h, "jwt-value", "proxy:443"))
	requirePerm(t, jwtCachePath(d, "profile-abc123", h), 0o600)
	requirePerm(t, filepath.Join(d, ".jwt-cache", "profile-abc123"), 0o700)

	c, ok := loadJWT(d, "profile-abc123", h)
	require.True(t, ok)
	require.Equal(t, "jwt-value", c.JWT)
	require.Equal(t, "proxy:443", c.ProxyAddress)
}

func TestJWTCachePath_EnvFormDistinctFromDeployment(t *testing.T) {
	d := t.TempDir()
	env := hostname{kind: workloadModel, id: "abc", env: "staging"}
	dep := hostname{kind: workloadModel, id: "abc", deploymentID: "staging"}

	// The env tag keeps an environment named like a deployment id from
	// colliding with the deployment cache entry.
	require.Contains(t, jwtCachePath(d, "profile-abc123", env), "model-abc-env-staging")
	require.NotEqual(t,
		jwtCachePath(d, "profile-abc123", dep),
		jwtCachePath(d, "profile-abc123", env))
}

func TestJWTCache_ScopedByCacheIdentity(t *testing.T) {
	d := t.TempDir()
	// Same workload id under two credentials: ids repeat across workspaces and
	// remotes, so each credential must get its own entry rather than overwriting
	// the token and proxy address cached for the other.
	h := hostname{kind: workloadModel, id: "abc", deploymentID: "def"}

	require.NoError(t, saveJWT(d, "profile-one", h, "one-jwt", "one-proxy:443"))
	require.NoError(t, saveJWT(d, "key-two", h, "two-jwt", "two-proxy:443"))

	one, ok := loadJWT(d, "profile-one", h)
	require.True(t, ok)
	require.Equal(t, "one-jwt", one.JWT)
	require.Equal(t, "one-proxy:443", one.ProxyAddress)

	two, ok := loadJWT(d, "key-two", h)
	require.True(t, ok)
	require.Equal(t, "two-jwt", two.JWT)
	require.Equal(t, "two-proxy:443", two.ProxyAddress)
}

func TestLoadJWT_MissingReturnsFalse(t *testing.T) {
	d := t.TempDir()
	h := hostname{kind: workloadModel, id: "abc", deploymentID: "def"}
	_, ok := loadJWT(d, "profile-abc123", h)
	require.False(t, ok)
}

func TestLoadJWT_OtherCacheIdentityNotRead(t *testing.T) {
	d := t.TempDir()
	h := hostname{kind: workloadModel, id: "abc", deploymentID: "def"}
	require.NoError(t, saveJWT(d, "profile-one", h, "one-jwt", "one-proxy:443"))

	_, ok := loadJWT(d, "profile-two", h)
	require.False(t, ok)
}

func requirePerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS == "windows" {
		// Windows does not honor Unix permission bits, so Perm() does not
		// reflect the mode we wrote; only assert the file exists there.
		return
	}
	require.Equal(t, want, info.Mode().Perm())
}
