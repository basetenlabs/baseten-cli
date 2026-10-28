//go:build e2e

package e2etests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
)

// sandboxE2ERegion pins every e2e sandbox, as the SDK e2e suites do.
// TODO: Temporary, while other regions' exec planes reject our tokens.
const sandboxE2ERegion = "us-was-1"

// setupSandboxE2E points the CLI at the e2e environment, skipping when it is
// not configured.
func setupSandboxE2E(t *testing.T) {
	t.Helper()
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")
	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())
}

// createE2ESandbox creates a sandbox and registers its deletion, which runs
// whether or not the test fails. Returns the created record.
func createE2ESandbox(t *testing.T, name string, args ...string) map[string]any {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		// Already deleted when the test got that far.
		if _, errOut, err := cliCtx(t, ctx, "sandbox", "delete", "--name", name, "--yes"); err != nil && !strings.Contains(errOut, "not found") {
			t.Logf("deleting sandbox %s: %v\nstderr: %s", name, err, errOut)
		}
	})
	out := mustCLI(t, append([]string{"sandbox", "create", "--name", name, "--region", sandboxE2ERegion,
		"--label", "created_by=cli-e2e", "--output", "json"}, args...)...)
	var record map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &record))
	return record
}

// cliJSON runs the CLI, failing the test on error, and decodes its stdout. The
// output flag goes before any "--", after which everything is the sandbox
// command's.
func cliJSON[T any](t *testing.T, args ...string) T {
	t.Helper()
	at := len(args)
	if i := slices.Index(args, "--"); i >= 0 {
		at = i
	}
	args = slices.Insert(slices.Clone(args), at, "--output", "json")
	var result T
	require.NoError(t, json.Unmarshal([]byte(mustCLI(t, args...)), &result))
	return result
}

// TestE2ESandbox creates one sandbox, works in it with every sandbox and
// process command, and deletes it.
func TestE2ESandbox(t *testing.T) {
	setupSandboxE2E(t)
	name := "cli-e2e-" + randomSuffix(t)[:8]

	step(t, "create %s", name)
	created := createE2ESandbox(t, name, "--plain-env", "E2E_PLAIN=plain-value", "--env", "E2E_SECRET=secret-value")
	// Create returns once the sandbox is up, which is why it has no --wait.
	require.Equal(t, "DEPLOYED", created["status"])

	t.Run("Describe", func(t *testing.T) {
		out := mustCLI(t, "sandbox", "describe", "--name", name)
		require.Contains(t, out, "Status:       DEPLOYED")
		require.Contains(t, out, "Region:       "+sandboxE2ERegion)

		envValues := func(record map[string]any) map[string]any {
			values := map[string]any{}
			for _, env := range record["envs"].([]any) {
				env := env.(map[string]any)
				values[env["name"].(string)] = env["value"]
			}
			return values
		}
		masked := envValues(cliJSON[map[string]any](t, "sandbox", "describe", "--name", name))
		require.Equal(t, "plain-value", masked["E2E_PLAIN"])
		require.NotEqual(t, "secret-value", masked["E2E_SECRET"])
		// The e2e key is a workspace administrator's.
		revealed := envValues(cliJSON[map[string]any](t, "sandbox", "describe", "--name", name, "--show-secrets"))
		require.Equal(t, "secret-value", revealed["E2E_SECRET"])
	})

	t.Run("List", func(t *testing.T) {
		listed := cliJSON[struct {
			Items []map[string]any `json:"items"`
		}](t, "sandbox", "list", "--query", name)
		require.Len(t, listed.Items, 1)
		require.Equal(t, name, listed.Items[0]["name"])
		require.Contains(t, mustCLI(t, "sandbox", "list", "--query", name, "--status", "deployed"), name)
	})

	t.Run("Update", func(t *testing.T) {
		updated := cliJSON[map[string]any](t, "sandbox", "update", "--name", name, "--label", "created_by=cli-e2e", "--label", "stage=two")
		require.Equal(t, map[string]any{"created_by": "cli-e2e", "stage": "two"}, updated["labels"])
	})

	t.Run("Exec", func(t *testing.T) {
		require.Equal(t, "hello world\n", mustCLI(t, "sandbox", "exec", "--name", name, "--", "echo", "hello", "world"))
		// One argument is the whole command line, for the sandbox's shell.
		require.Equal(t, "3\n", mustCLI(t, "sandbox", "exec", "--name", name, "--", "echo $((1+2))"))
		require.Equal(t, "/tmp\n", mustCLI(t, "sandbox", "exec", "--name", name, "--working-dir", "/tmp", "--", "pwd"))
		require.Equal(t, "on\n", mustCLI(t, "sandbox", "exec", "--name", name, "--env", "E2E_FLAG=on", "--", "echo $E2E_FLAG"))

		// The command's exit code becomes the CLI's.
		out, errOut, err := cli(t, "sandbox", "exec", "--name", name, "--", "echo out; echo err >&2; exit 7")
		require.ErrorContains(t, err, "exit 7")
		require.Equal(t, "out\n", out)
		require.Contains(t, errOut, "err\n")

		record := cliJSON[map[string]any](t, "sandbox", "exec", "--name", name, "--", "printf json")
		require.Equal(t, "json", record["stdout"])
		require.Equal(t, float64(0), record["exitCode"])

		stdinOut, errOut, err := cliWithStdin(t, t.Context(), "a\nb\nc\n", "sandbox", "exec", "--name", name, "--stdin", "--", "wc -l")
		require.NoError(t, err, errOut)
		require.Equal(t, "3", strings.TrimSpace(stdinOut))
	})

	t.Run("Process", func(t *testing.T) {
		started := cliJSON[map[string]any](t, "sandbox", "process", "start", "--name", name, "--process-name", "e2e-echo",
			"--", "sh -c 'echo first; sleep 1; echo second >&2; echo third'")
		pid := started["pid"].(string)
		require.NotEmpty(t, pid)
		require.Contains(t, mustCLI(t, "sandbox", "process", "list", "--name", name), "e2e-echo")

		finished := cliJSON[map[string]any](t, "sandbox", "process", "wait", "--name", name, "--process-name", "e2e-echo")
		require.Equal(t, "completed", finished["status"])
		require.Equal(t, "first\nthird\n", finished["stdout"])
		require.Equal(t, "second\n", finished["stderr"])
		require.Equal(t, pid, cliJSON[map[string]any](t, "sandbox", "process", "describe", "--name", name, "--pid", pid)["pid"])

		// TODO: Assert the order once the spec says how the output so far
		// orders standard output and standard error.
		logs := mustCLI(t, "sandbox", "process", "logs", "--name", name, "--pid", pid)
		require.ElementsMatch(t, []string{"first", "second", "third"}, strings.Split(strings.TrimSuffix(logs, "\n"), "\n"))
		// Following a finished process replays its output, each line marked
		// with its stream.
		tailed := mustCLI(t, "sandbox", "process", "logs", "--name", name, "--pid", pid, "--tail", "--output", "jsonl")
		require.Contains(t, tailed, `{"stream":"stdout","text":"first"}`)
		require.Contains(t, tailed, `{"stream":"stderr","text":"second"}`)

		// A failed process's exit code becomes wait's.
		mustCLI(t, "sandbox", "process", "start", "--name", name, "--process-name", "e2e-fail", "--", "sh -c 'exit 5'")
		_, _, err := cli(t, "sandbox", "process", "wait", "--name", name, "--process-name", "e2e-fail")
		require.ErrorContains(t, err, "exit 5")

		for _, action := range []string{"stop", "kill"} {
			processName := "e2e-" + action
			mustCLI(t, "sandbox", "process", "start", "--name", name, "--process-name", processName, "--", "sleep 300")
			mustCLI(t, "sandbox", "process", action, "--name", name, "--process-name", processName)
			// Waiting fails with the ended process's exit code, but still
			// prints its record.
			out, _, _ := cli(t, "sandbox", "process", "wait", "--name", name, "--process-name", processName, "--output", "json")
			var ended map[string]any
			require.NoError(t, json.Unmarshal([]byte(out), &ended))
			require.NotEqual(t, "running", ended["status"], action)
		}
	})

	t.Run("API", func(t *testing.T) {
		url := created["url"].(string)
		record := cliJSON[map[string]any](t, "api", "sandbox", "process", "--sandbox-url", url,
			"-f", "command=echo raw", "-F", "waitForCompletion=true")
		require.Equal(t, "raw\n", record["stdout"])
	})

	t.Run("Connect", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("connect is tested through a Unix pseudo-terminal")
		}
		session := startE2EConnect(t, name, 30, 100)
		// Commands are typed with quotes the shell drops, so the terminal's
		// echo of what was typed never matches the output looked for.
		session.run("tt''y", "/dev/pts/")
		session.run("echo si''ze-$(stty size)", "size-30 100")
		// The shell redraws its prompt on a resize only while at the prompt.
		session.waitFor("# ")
		require.NoError(t, pty.Setsize(session.terminal, &pty.Winsize{Rows: 40, Cols: 120}))
		// SIGWINCH, which tells connect the window changed, as a terminal
		// would. Signal 28 on Linux and macOS.
		process, err := os.FindProcess(os.Getpid())
		require.NoError(t, err)
		require.NoError(t, process.Signal(syscall.Signal(28)))
		// The redraw, which starts by clearing the line, shows the resize
		// reached the shell.
		session.waitFor("\x1b[K")
		session.run("echo si''ze-$(stty size)", "size-40 120")
		session.run("export E2E_VALUE=ke''pt; cd /tmp", "")
		session.run("echo st''ate-$E2E_VALUE-$(pwd)", "state-kept-/tmp")
		session.run("false; echo ex''it-$?", "exit-1")
		session.run("echo std''err >&2", "stderr")
		session.run("echo h''éllo-✓", "héllo-✓")
		// An interactive prompt, answered at the terminal.
		session.run("read -p 'na''me? ' answer; echo go''t-$answer", "name? ")
		session.keys("zed\r")
		session.waitFor("got-zed")
		// Ctrl+C reaches the remote command, not connect, which stays up: the
		// next prompt comes well before the sleep would have ended.
		session.run("echo sl''eeping; sleep 30", "sleeping")
		session.keys("\x03")
		session.run("echo af''ter-interrupt", "after-interrupt")
		t.Logf("connect shell: %s", session.run("echo sh''ell-$0", "shell-"))
		// TODO: Assert that exit and Ctrl+D at the prompt end connect cleanly,
		// once the server closes the connection when the shell exits. Today it
		// stays open.

		// A second connect is a new shell, without the first one's state.
		session = startE2EConnect(t, name, 24, 80)
		session.run("echo ne''w-[$E2E_VALUE]-$(pwd)", "new-[]-")
	})

	t.Run("Preview", func(t *testing.T) {
		mustCLI(t, "sandbox", "process", "start", "--name", name, "--process-name", "e2e-web", "--wait-for-port", "8123",
			"--", "python3 -m http.server 8123")
		previewName := "e2e-" + randomSuffix(t)[:8]
		previewFlags := []string{"--name", name, "--preview-name", previewName}

		created := cliJSON[map[string]any](t, append([]string{"sandbox", "preview", "create", "--port", "8123",
			"--response-header", "X-E2E=added", "--ttl", "1h"}, previewFlags...)...)
		require.Equal(t, previewName, created["name"])
		require.Equal(t, float64(8123), created["port"])
		require.Equal(t, false, created["public"])
		url := created["url"].(string)
		require.True(t, strings.HasPrefix(url, "https://"), url)

		described := mustCLI(t, append([]string{"sandbox", "preview", "describe"}, previewFlags...)...)
		require.Contains(t, described, "URL:               "+url+"\n")
		require.Contains(t, described, "Response headers:  X-E2E=added\n")
		require.Contains(t, mustCLI(t, "sandbox", "preview", "list", "--name", name), previewName)

		// A token cannot outlive its preview, which was created with a 1h TTL.
		expiresAt := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)
		tokenText := mustCLI(t, append([]string{"sandbox", "preview", "token", "create", "--expires-at", expiresAt}, previewFlags...)...)
		// The printed URL is the one fetched, so it must work as printed.
		tokenURL := regexp.MustCompile(`URL:\s+(` + regexp.QuoteMeta(url) + `\?preview_token=\S+)\n`).FindStringSubmatch(tokenText)
		require.NotNil(t, tokenURL, tokenText)
		header := fetchE2EPreview(t, tokenURL[1])
		require.Equal(t, "added", header.Get("X-E2E"))

		tokenNameMatch := regexp.MustCompile(`Name:\s+(\S+)\n`).FindStringSubmatch(tokenText)
		require.NotNil(t, tokenNameMatch, tokenText)
		tokenName := tokenNameMatch[1]
		require.Contains(t, mustCLI(t, append([]string{"sandbox", "preview", "token", "list"}, previewFlags...)...), tokenName)
		_, errOut, err := cli(t, append([]string{"sandbox", "preview", "token", "delete", "--token-name", tokenName, "--yes"}, previewFlags...)...)
		require.NoError(t, err, errOut)
		require.Contains(t, errOut, "Deleted token "+tokenName+".")

		updated := cliJSON[map[string]any](t, append([]string{"sandbox", "preview", "update", "--public"}, previewFlags...)...)
		require.Equal(t, true, updated["public"])
		fetchE2EPreview(t, url)

		_, errOut, err = cli(t, append([]string{"sandbox", "preview", "delete", "--yes"}, previewFlags...)...)
		require.NoError(t, err, errOut)
		require.Contains(t, errOut, "Deleted preview "+previewName+".")
	})

	t.Run("ListLibrary", func(t *testing.T) {
		require.Contains(t, mustCLI(t, "sandbox", "image", "list-library"), "baseten/base-image:latest")
	})

	step(t, "delete %s", name)
	_, errOut, err := cli(t, "sandbox", "delete", "--name", name, "--yes")
	require.NoError(t, err, errOut)
	require.Contains(t, errOut, "Deleting sandbox "+name+".")
}

// fetchE2EPreview GETs a preview URL until it answers 200, and returns the
// response's header. The edge applies preview changes shortly after the API
// returns, so other responses and connection errors are retried, but only for
// a few seconds.
func fetchE2EPreview(t *testing.T, url string) http.Header {
	t.Helper()
	// No cookie jar, so the cookie a token request sets never stands in for
	// the token.
	client := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var last string
		resp, err := client.Get(url)
		if err != nil {
			last = err.Error()
		} else {
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			require.NoError(t, err)
			if resp.StatusCode == http.StatusOK {
				return resp.Header
			}
			last = fmt.Sprintf("status %d: %s", resp.StatusCode, body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("preview %s: want 200, got %s", url, last)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// e2eConnectSession is a 'sandbox connect' running in a pseudo-terminal, as a
// user's would, driven from the terminal's other side.
type e2eConnectSession struct {
	t *testing.T
	// terminal is the command's stdin and stdout; control is the other side,
	// where keys are typed and output read.
	terminal *os.File
	control  *os.File
	chunks   <-chan []byte
	output   string
	seen     int
	stderr   *bytes.Buffer
	done     <-chan error
}

// startE2EConnect runs 'sandbox connect' in a new pseudo-terminal of the
// given size.
func startE2EConnect(t *testing.T, name string, rows, cols uint16) *e2eConnectSession {
	t.Helper()
	control, terminal, err := pty.Open()
	require.NoError(t, err)
	t.Cleanup(func() { control.Close(); terminal.Close() })
	require.NoError(t, pty.Setsize(terminal, &pty.Winsize{Rows: rows, Cols: cols}))
	chunks := make(chan []byte, 64)
	go func() {
		defer close(chunks)
		buf := make([]byte, 4096)
		for {
			n, err := control.Read(buf)
			if n > 0 {
				chunks <- bytes.Clone(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), defaultCLITimeout)
	t.Cleanup(cancel)
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- cliRun(ctx, cliRunOptions{
			Args:   []string{"sandbox", "connect", "--name", name},
			Stdin:  terminal,
			Stdout: terminal,
			Stderr: &stderr,
		}).Err
	}()
	return &e2eConnectSession{t: t, terminal: terminal, control: control, chunks: chunks, stderr: &stderr, done: done}
}

// run waits for the shell's prompt, types a command and Enter, then waits
// until the output after anything already matched contains want, returning
// the rest of that line. An empty want only types the command. Typing only at
// the prompt matters: the shell can drop keys typed while it switches the
// terminal between commands.
func (s *e2eConnectSession) run(command, want string) string {
	s.t.Helper()
	s.waitFor("# ")
	s.keys(command + "\r")
	if want == "" {
		return ""
	}
	return s.waitFor(want)
}

func (s *e2eConnectSession) keys(keys string) {
	s.t.Helper()
	_, err := s.control.Write([]byte(keys))
	require.NoError(s.t, err)
}

// waitFor reads output until it contains want past what was already matched.
// The 20s limit is shorter than the sleep the Ctrl+C check interrupts.
func (s *e2eConnectSession) waitFor(want string) string {
	s.t.Helper()
	timeout := time.After(20 * time.Second)
	for {
		if i := strings.Index(s.output[s.seen:], want); i >= 0 {
			start := s.seen + i
			end := strings.IndexAny(s.output[start:], "\r\n")
			if end < 0 {
				end = len(s.output) - start
			}
			s.seen = start + len(want)
			return s.output[start : start+end]
		}
		select {
		case chunk, ok := <-s.chunks:
			if !ok {
				s.t.Fatalf("terminal closed waiting for %q; output so far:\n%s", want, s.output)
			}
			s.output += string(chunk)
		case err := <-s.done:
			// stderr is safe to read once connect has returned.
			s.t.Fatalf("connect ended waiting for %q: %v\nstderr: %s\noutput so far:\n%s", want, err, s.stderr, s.output)
		case <-timeout:
			s.t.Fatalf("timed out waiting for %q; output so far:\n%s", want, s.output)
		}
	}
}

// Bound on each sandbox volume state change TestE2ESandboxVolume waits for:
// deploying, resizing, becoming deletable once unmounted. Each has taken about
// 3s in the SDK e2e suites.
const (
	sandboxVolumeE2EWait = 30 * time.Second
	sandboxVolumeE2EPoll = 200 * time.Millisecond
)

// TestE2ESandboxVolume walks one sandbox volume through its life: CRUD, then
// mounted in a sandbox and grown there. Cleanups run last-registered first, so
// the sandbox is deleted before the volume, which cannot be deleted while
// mounted.
func TestE2ESandboxVolume(t *testing.T) {
	setupSandboxE2E(t)
	volumeName := "cli-e2e-" + randomSuffix(t)[:8]
	volumeDeleted := false
	// Fails only with the 409 of a volume still mounted in a sandbox being
	// deleted; any other failure fails the test.
	deleteVolume := func(ctx context.Context) (map[string]any, bool) {
		result := cliRun(ctx, cliRunOptions{Args: []string{"sandbox", "volume", "delete", "--name", volumeName, "--yes", "--output", "json"}})
		if result.Err == nil {
			var deleted map[string]any
			require.NoError(t, json.Unmarshal([]byte(result.Stdout), &deleted))
			return deleted, true
		}
		require.Equal(t, http.StatusConflict, result.APIStatus(t), "stdout: %s\nstderr: %s", result.Stdout, result.Stderr)
		return nil, false
	}
	t.Cleanup(func() {
		if volumeDeleted {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*sandboxVolumeE2EWait)
		defer cancel()
		for ctx.Err() == nil {
			if _, done := deleteVolume(ctx); done {
				return
			}
			time.Sleep(sandboxVolumeE2EPoll)
		}
		t.Logf("deleting sandbox volume %s: still mounted", volumeName)
	})

	step(t, "create volume %s", volumeName)
	created := cliJSON[map[string]any](t, "sandbox", "volume", "create", "--name", volumeName, "--size", "512",
		"--region", sandboxE2ERegion, "--label", "created_by=cli-e2e")
	// The server normalizes either size form to a size string.
	require.Equal(t, "512Mi", created["size"])
	elapsed := waitE2ESandboxVolume(t, "volume deployed", func() bool {
		status := cliJSON[map[string]any](t, "sandbox", "volume", "describe", "--name", volumeName)["status"]
		require.NotEqual(t, "FAILED", status)
		return status == "DEPLOYED"
	})
	step(t, "volume deployed %v after create returned", elapsed)

	described := mustCLI(t, "sandbox", "volume", "describe", "--name", volumeName)
	require.Contains(t, described, "Labels:       created_by=cli-e2e")
	require.Contains(t, mustCLI(t, "sandbox", "volume", "list", "--query", volumeName), volumeName)
	updated := cliJSON[map[string]any](t, "sandbox", "volume", "update", "--name", volumeName,
		"--label", "created_by=cli-e2e", "--label", "stage=two")
	require.Equal(t, map[string]any{"created_by": "cli-e2e", "stage": "two"}, updated["labels"])
	shrunk := cliRun(t.Context(), cliRunOptions{Args: []string{"sandbox", "volume", "update", "--name", volumeName, "--size", "256", "--output", "json"}})
	require.Error(t, shrunk.Err)
	require.Equal(t, http.StatusBadRequest, shrunk.APIStatus(t), "stdout: %s\nstderr: %s", shrunk.Stdout, shrunk.Stderr)

	sandboxName := "cli-e2e-" + randomSuffix(t)[:8]
	step(t, "create sandbox %s with volume %s", sandboxName, volumeName)
	createE2ESandbox(t, sandboxName, "--volume", volumeName+":/data")
	require.Contains(t, mustCLI(t, "sandbox", "describe", "--name", sandboxName), "Volume:       "+volumeName+" at /data\n")
	require.Equal(t, sandboxName, cliJSON[map[string]any](t, "sandbox", "volume", "describe", "--name", volumeName)["attached_to"])
	mustCLI(t, "sandbox", "exec", "--name", sandboxName, "--", "echo written > /data/written.txt")
	require.Equal(t, "written\n", mustCLI(t, "sandbox", "exec", "--name", sandboxName, "--", "cat /data/written.txt"))
	megabytes := func() int {
		fields := strings.Fields(mustCLI(t, "sandbox", "exec", "--name", sandboxName, "--", "df -m /data | tail -n 1"))
		require.GreaterOrEqual(t, len(fields), 2, "unexpected df output %q", fields)
		value, err := strconv.Atoi(fields[1])
		require.NoError(t, err)
		return value
	}
	initial := megabytes()
	step(t, "df reports %d MB for a 512 MB volume", initial)

	_, mounted := deleteVolume(t.Context())
	require.False(t, mounted, "volume deleted while mounted")

	grown := cliJSON[map[string]any](t, "sandbox", "volume", "update", "--name", volumeName, "--size", "1Gi")
	require.Equal(t, "1Gi", grown["size"])
	elapsed = waitE2ESandboxVolume(t, "sandbox sees the grown volume", func() bool { return megabytes() > initial })
	step(t, "sandbox saw the grown volume %v after update returned", elapsed)

	mustCLI(t, "sandbox", "delete", "--name", sandboxName, "--yes")
	var deleted map[string]any
	elapsed = waitE2ESandboxVolume(t, "volume deletable", func() bool {
		var done bool
		deleted, done = deleteVolume(t.Context())
		return done
	})
	volumeDeleted = true
	step(t, "volume deletable %v after sandbox delete returned", elapsed)
	require.Equal(t, "DELETING", deleted["status"])
}

// waitE2ESandboxVolume polls check until it reports true, failing once
// sandboxVolumeE2EWait passes. A hard failure inside check fails the test at
// once. Returns how long it took.
func waitE2ESandboxVolume(t *testing.T, what string, check func() bool) time.Duration {
	t.Helper()
	started := time.Now()
	for !check() {
		if time.Since(started) > sandboxVolumeE2EWait {
			t.Fatalf("%s: not within %v", what, sandboxVolumeE2EWait)
		}
		time.Sleep(sandboxVolumeE2EPoll)
	}
	return time.Since(started)
}

// TestE2ESandboxImage pushes an image from a directory with a .dockerignore,
// creates a sandbox from it, and checks what the build context held. It is the
// run's only image push, since builds are costly.
func TestE2ESandboxImage(t *testing.T) {
	// TODO: Remove once sandbox image builds work again platform-side.
	t.Skip("sandbox image builds are failing platform-side")
	setupSandboxE2E(t)
	suffix := randomSuffix(t)[:8]
	imageName := "cli-e2e-" + suffix
	dir := t.TempDir()
	for path, content := range map[string]string{
		// The sandbox API is what a sandbox's commands run through.
		"Dockerfile": "FROM debian:bookworm-slim\n" +
			"COPY --from=ghcr.io/blaxel-ai/sandbox:latest /sandbox-api /usr/local/bin/sandbox-api\n" +
			"COPY . /context\n" +
			"ENTRYPOINT [\"/usr/local/bin/sandbox-api\"]\n",
		".dockerignore": "excluded.txt\n",
		// Unique per run, so the build is never shared with another image's,
		// which would block deleting either while the other exists.
		"included.txt": "included " + suffix,
		"excluded.txt": "excluded",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644))
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, errOut, err := cliCtx(t, ctx, "sandbox", "image", "delete", "--name", imageName, "--yes"); err != nil {
			t.Logf("deleting sandbox image %s: %v\nstderr: %s", imageName, err, errOut)
		}
	})

	step(t, "push %s", imageName)
	ctx, cancel := context.WithTimeout(t.Context(), pushCLITimeout)
	defer cancel()
	out := mustCLICtx(t, ctx, "sandbox", "image", "push", "--name", imageName, "--dir", dir, "--wait", "--output", "json")
	var pushed map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &pushed))
	require.Equal(t, "BUILT", pushed["status"])

	require.Contains(t, mustCLI(t, "sandbox", "image", "describe", "--name", imageName), "Status:         BUILT")
	require.Contains(t, mustCLI(t, "sandbox", "image", "list"), imageName)
	tags := cliJSON[struct {
		Items []map[string]any `json:"items"`
	}](t, "sandbox", "image", "list-tags", "--name", imageName)
	require.NotEmpty(t, tags.Items)
	// Build logs can be empty, so only the command is checked.
	mustCLI(t, "sandbox", "image", "logs", "--name", imageName)

	sandboxName := "cli-e2e-" + randomSuffix(t)[:8]
	step(t, "create %s from %s", sandboxName, imageName)
	createE2ESandbox(t, sandboxName, "--image", imageName+":latest")
	// TODO: Drop --output json once ghcr.io/blaxel-ai/sandbox:latest, copied in
	// by the Dockerfile, streams for Accept: application/x-ndjson. Until then
	// only the non-streaming exec works in this sandbox.
	type execResult struct {
		Stdout   string `json:"stdout"`
		ExitCode int    `json:"exitCode"`
	}
	included := cliJSON[execResult](t, "sandbox", "exec", "--name", sandboxName, "--", "cat /context/included.txt")
	require.Equal(t, "included "+suffix, included.Stdout)
	excluded := cliJSON[execResult](t, "sandbox", "exec", "--name", sandboxName, "--", "test ! -e /context/excluded.txt")
	require.Equal(t, 0, excluded.ExitCode)
	// The sandbox goes first, since an image in use cannot be deleted.
	mustCLI(t, "sandbox", "delete", "--name", sandboxName, "--yes")
}
