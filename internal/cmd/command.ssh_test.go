package cmd_test

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/ssh"
)

// sshEnv points the SSH key dir and config path at fresh temp locations.
func sshEnv(t *testing.T) (dir, configPath string) {
	t.Helper()
	dir = t.TempDir()
	configPath = filepath.Join(t.TempDir(), "config")
	t.Setenv("BASETEN_SSH_DIR", dir)
	t.Setenv("BASETEN_SSH_CONFIG_PATH", configPath)
	return dir, configPath
}

// signResponse is a stub body for the ssh/sign endpoints.
func signResponse(jwt, proxyAddr string) map[string]any {
	return map[string]any{
		"jwt":                 jwt,
		"proxy_address":       proxyAddr,
		"ssh_certificate":     "the-cert",
		"ssh_cert_expires_at": "2026-01-02T03:04:05Z",
	}
}

func Test_SSH_Setup_GeneratesKeyAndConfig(t *testing.T) {
	h := NewCommandHarness(t)
	_, configPath := sshEnv(t)

	h.Require.NoError(h.Execute("ssh", "setup", "--output", "json"))
	out := h.Stdout.String()
	h.Require.Contains(out, `"key_reused": false`)
	h.Require.Contains(out, `"key_path"`)

	cfg, err := os.ReadFile(configPath)
	h.Require.NoError(err)
	h.Require.Contains(string(cfg), "# --- baseten-cli-ssh ---")
	h.Require.Contains(string(cfg), "Match host model-*.ssh.baseten.co")
	h.Require.Contains(string(cfg), "Match host training-job-*.ssh.baseten.co")
	// The ephemeral BASETEN_API_KEY session has no profile name, so nothing is pinned.
	h.Require.NotContains(string(cfg), "--default-profile")
}

func Test_SSH_Setup_PinsProfile(t *testing.T) {
	h := NewCommandHarness(t)
	_, configPath := sshEnv(t)
	t.Setenv("BASETEN_API_KEY", "")
	t.Setenv("BASETEN_PROFILE", "prod")

	h.Require.NoError(h.Execute("ssh", "setup", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"profile": "prod"`)

	cfg, err := os.ReadFile(configPath)
	h.Require.NoError(err)
	h.Require.Contains(string(cfg), "baseten ssh sign --default-profile=prod")
	h.Require.Contains(string(cfg), "baseten ssh proxy --default-profile=prod")
}

func Test_SSH_Setup_ReusesKeypair(t *testing.T) {
	h := NewCommandHarness(t)
	sshEnv(t)

	h.Require.NoError(h.Execute("ssh", "setup", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"key_reused": false`)

	h.Require.NoError(h.Execute("ssh", "setup", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"key_reused": true`)
}

func Test_SSH_Setup_RefusesForeignConfig(t *testing.T) {
	h := NewCommandHarness(t)
	_, configPath := sshEnv(t)
	foreign := "Match host model-foo.ssh.baseten.co\n    ProxyCommand truss ssh proxy\n"
	h.Require.NoError(os.WriteFile(configPath, []byte(foreign), 0o644))

	err := h.Execute("ssh", "setup")
	h.Require.ErrorContains(err, "outside the managed block")
}

func Test_SSH_Sign_Model(t *testing.T) {
	h := NewCommandHarness(t)
	dir, _ := sshEnv(t)
	keyPath, _, err := ssh.EnsureKeypair()
	h.Require.NoError(err)

	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/models/m1/deployments/d1/ssh/sign", 200,
		signResponse("tok", "127.0.0.1:9"))

	h.Require.NoError(h.Execute("ssh", "sign", "model-m1-d1.ssh.baseten.co", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), "{}")

	call := m.FindCall("POST", "/v1/models/m1/deployments/d1/ssh/sign")
	h.Require.NotNil(call)
	h.Require.Contains(call.Body, `"public_key"`)
	h.Require.NotContains(call.Body, `"replica_id"`)

	cert, err := os.ReadFile(keyPath + "-cert.pub")
	h.Require.NoError(err)
	h.Require.Equal("the-cert", string(cert))
	_ = dir
}

func Test_SSH_Sign_ModelWithReplica(t *testing.T) {
	h := NewCommandHarness(t)
	sshEnv(t)
	_, _, err := ssh.EnsureKeypair()
	h.Require.NoError(err)

	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/models/m1/deployments/d1/ssh/sign", 200,
		signResponse("tok", "127.0.0.1:9"))

	h.Require.NoError(h.Execute("ssh", "sign", "model-m1-d1-r1.ssh.baseten.co"))
	call := m.FindCall("POST", "/v1/models/m1/deployments/d1/ssh/sign")
	h.Require.NotNil(call)
	h.Require.Contains(call.Body, `"replica_id":"r1"`)
}

func Test_SSH_Sign_Training(t *testing.T) {
	h := NewCommandHarness(t)
	sshEnv(t)
	_, _, err := ssh.EnsureKeypair()
	h.Require.NoError(err)

	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/training_jobs/search", 200,
		map[string]any{"training_jobs": []any{map[string]any{
			"id": "job-1", "training_project_id": "p-1",
		}}})
	m.SetRoute("POST", "/v1/training_projects/p-1/jobs/job-1/ssh/sign", 200,
		signResponse("tok", "127.0.0.1:9"))

	h.Require.NoError(h.Execute("ssh", "sign", "training-job-job-1-0.ssh.baseten.co"))
	search := m.FindCall("POST", "/v1/training_jobs/search")
	h.Require.NotNil(search)
	h.Require.Contains(search.Body, `"job_id":"job-1"`)
	sign := m.FindCall("POST", "/v1/training_projects/p-1/jobs/job-1/ssh/sign")
	h.Require.NotNil(sign)
	// Training jobs always pin a node as the replica.
	h.Require.Contains(sign.Body, `"replica_id":"0"`)
}

func Test_SSH_Sign_InvalidHostname(t *testing.T) {
	h := NewCommandHarness(t)
	sshEnv(t)
	_, _, err := ssh.EnsureKeypair()
	h.Require.NoError(err)

	err = h.Execute("ssh", "sign", "not-a-workload.example.com")
	h.Require.ErrorContains(err, "invalid ssh hostname")
}

func Test_SSH_Sign_NoKeypair(t *testing.T) {
	h := NewCommandHarness(t)
	sshEnv(t)

	err := h.Execute("ssh", "sign", "model-m1-d1.ssh.baseten.co")
	h.Require.ErrorContains(err, "no SSH keypair found")
}

func Test_SSH_Proxy_NoCachedCredential(t *testing.T) {
	h := NewCommandHarness(t)
	sshEnv(t)

	err := h.Execute("ssh", "proxy", "model-m1-d1.ssh.baseten.co")
	h.Require.ErrorContains(err, "no cached SSH credential")
}

func Test_SSH_Proxy_RelaysUsingCachedCredential(t *testing.T) {
	h := NewCommandHarness(t)
	sshEnv(t)
	_, _, err := ssh.EnsureKeypair()
	h.Require.NoError(err)
	t.Setenv("BASETEN_SSH_PROXY_INSECURE", "1")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	h.Require.NoError(err)
	defer ln.Close()
	gotJWT := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			gotJWT <- ""
			return
		}
		defer conn.Close()
		var lp [4]byte
		if _, err := io.ReadFull(conn, lp[:]); err != nil {
			gotJWT <- ""
			return
		}
		buf := make([]byte, binary.BigEndian.Uint32(lp[:]))
		if _, err := io.ReadFull(conn, buf); err != nil {
			gotJWT <- ""
			return
		}
		// Accept the connection.
		_, _ = conn.Write([]byte{0x00})
		gotJWT <- string(buf)
	}()

	// Sign first so the JWT and proxy address (our listener) get cached.
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/models/m1/deployments/d1/ssh/sign", 200,
		signResponse("relay-tok", ln.Addr().String()))
	h.Require.NoError(h.Execute("ssh", "sign", "model-m1-d1.ssh.baseten.co"))

	// Proxy reads from the (empty) stdin buffer, so the relay returns once the
	// local side closes.
	h.Require.NoError(h.Execute("ssh", "proxy", "model-m1-d1.ssh.baseten.co"))
	h.Require.Equal("relay-tok", <-gotJWT)
}
