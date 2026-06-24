package ssh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// configPathEnv points BASETEN_SSH_CONFIG_PATH at a fresh temp file and returns
// it.
func configPathEnv(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	t.Setenv("BASETEN_SSH_CONFIG_PATH", path)
	return path
}

func TestWriteConfig_FreshAppendBakesProfile(t *testing.T) {
	path := configPathEnv(t)
	require.NoError(t, WriteConfig("/keys/id_ed25519", "prod"))

	content := readFile(t, path)
	require.Contains(t, content, markerStart)
	require.Contains(t, content, markerEnd)
	require.Contains(t, content, "Match host training-job-*.ssh.baseten.co")
	require.Contains(t, content, "Match host model-*.ssh.baseten.co")
	require.Contains(t, content, "baseten ssh sign --default-profile=prod")
	require.Contains(t, content, "baseten ssh proxy --default-profile=prod")
	require.Contains(t, content, "IdentityFile /keys/id_ed25519")
	require.Contains(t, content, "CertificateFile /keys/id_ed25519-cert.pub")
}

func TestWriteConfig_NoProfileOmitsDefaultProfile(t *testing.T) {
	path := configPathEnv(t)
	require.NoError(t, WriteConfig("/keys/id_ed25519", ""))

	content := readFile(t, path)
	require.NotContains(t, content, "--default-profile")
	require.Contains(t, content, "baseten ssh sign %n")
}

func TestWriteConfig_IdempotentReplaceInPlace(t *testing.T) {
	path := configPathEnv(t)
	require.NoError(t, WriteConfig("/keys/id_ed25519", "prod"))
	first := readFile(t, path)
	require.NoError(t, WriteConfig("/keys/id_ed25519", "prod"))
	second := readFile(t, path)

	require.Equal(t, first, second)
	require.Equal(t, 1, strings.Count(second, markerStart))
	require.Equal(t, 1, strings.Count(second, markerEnd))
}

func TestWriteConfig_PreservesSurroundingContent(t *testing.T) {
	path := configPathEnv(t)
	require.NoError(t, os.WriteFile(path, []byte("Host example\n  HostName example.com\n"), 0o644))
	require.NoError(t, WriteConfig("/keys/id_ed25519", ""))

	content := readFile(t, path)
	require.Contains(t, content, "Host example")
	require.Contains(t, content, "HostName example.com")
	require.Contains(t, content, markerStart)
}

func TestWriteConfig_RefusesForeignProxyHosts(t *testing.T) {
	path := configPathEnv(t)
	foreign := "Match host model-foo.ssh.baseten.co\n    ProxyCommand truss ssh proxy\n"
	require.NoError(t, os.WriteFile(path, []byte(foreign), 0o644))

	err := WriteConfig("/keys/id_ed25519", "")
	require.ErrorContains(t, err, "outside the managed block")
	// The file must be left untouched.
	require.Equal(t, foreign, readFile(t, path))
}

func TestReferencesProxyHosts(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"host directive", "Host bastion.ssh.baseten.co\n", true},
		{"match directive", "Match host model-x.ssh.baseten.co\n", true},
		{"case insensitive", "HOST model-x.SSH.BASETEN.CO\n", true},
		{"unrelated host", "Host example.com\n", false},
		{"comment mentioning host", "# ssh.baseten.co note\n", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, referencesProxyHosts(tc.text))
		})
	}
}

func TestSafeProfileName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"alnum", true},
		{"user@example.com", true},
		{"a.b_c:d+e-f", true},
		{"ABC123", true},
		{"", false},
		{"has space", false},
		{"semi;colon", false},
		{"$(inject)", false},
		{"back`tick`", false},
		{"new\nline", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, SafeProfileName(tc.name))
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}
