//go:build e2e

package e2etests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/stretchr/testify/require"
)

const watchConfigTmpl = `model_name: %s
python_version: py313
resources:
  cpu: 50m
  memory: 50Mi
  use_gpu: false
`

// watchModelPyTmpl is a model whose predict response carries a version token
// and the serving process PID. The watch test rewrites the token on disk and
// asserts the running development deployment serves the new value, proving the
// live-patch loop; the PID distinguishes a cold patch (process restart, new
// PID) from a hot reload (in-place module reload, same PID).
const watchModelPyTmpl = `import os


class Model:
    def load(self):
        pass

    def predict(self, request):
        return {"watch_version": %q, "pid": os.getpid()}
`

// TestE2EModelWatch exercises the live-patch loop end to end against a real
// backend: it pushes a development deployment with --watch, confirms a code
// change propagates to the running container, drives the same round-trip
// through the standalone `model watch` command, then confirms `--hot-reload`
// patches in place without restarting the process (same PID). Skips when the
// env vars the lifecycle test uses are absent. Self-contained: its own model
// and teardown.
func TestE2EModelWatch(t *testing.T) {
	w := newWatchTest(t)

	// Phase 1: push --watch (implies --develop) creates the development
	// deployment and enters the watch loop. The first push builds and deploys a
	// fresh deployment, so allow it plenty of time to reach the watch loop.
	push := w.startWatch("model", "push", "--watch", "--dir", w.dir)
	push.waitForMarker(t, 5*time.Minute)

	// The push created the model; resolve its ID for predicting and cleanup, and
	// confirm the development deployment serves the original code. The first
	// predict may race the initial-sync model reload, so allow it generous time.
	w.resolveModelID(push)
	pidV1 := w.requireServesVersion(push, "v1", 5*time.Minute)

	// Mutate the predict response and confirm the push --watch loop patches the
	// running container. The model is already serving, so propagation is quick.
	// Without --watch-hot-reload the patch is cold: the process restarts, so the
	// serving PID must change.
	w.writeModelPy("v2")
	pidV2 := w.requireServesVersion(push, "v2", 20*time.Second)
	require.NotEqual(t, pidV1, pidV2, "a cold patch should restart the model process")

	push.stop(t)

	// Phase 2: standalone `model watch` attaches to the existing development
	// deployment and patches a further change. It is already built and ACTIVE,
	// so it reaches the watch loop in seconds (no build or deploy). Still cold,
	// so the PID changes again.
	watch := w.startWatch("model", "watch", "--dir", w.dir)
	watch.waitForMarker(t, 30*time.Second)

	w.writeModelPy("v3")
	pidV3 := w.requireServesVersion(watch, "v3", 20*time.Second)
	require.NotEqual(t, pidV2, pidV3, "a cold patch should restart the model process")

	watch.stop(t)

	// Phase 3: standalone `model watch --hot-reload`. A model-code-only change
	// hot-reloads in place rather than restarting the process, so the new code
	// is served under the SAME PID.
	hot := w.startWatch("model", "watch", "--hot-reload", "--dir", w.dir)
	hot.waitForMarker(t, 30*time.Second)

	w.writeModelPy("v4")
	pidV4 := w.requireServesVersion(hot, "v4", 20*time.Second)
	require.Equal(t, pidV3, pidV4, "a hot reload should not restart the model process")

	hot.stop(t)

	// Phase 4: a full development re-push (not a watch patch) to the now-existing
	// model. Unlike a watch patch, this goes through the create-deployment path,
	// which for an existing model must resolve the development deployment's
	// instance type. A regression there leaves the instance type unset and the
	// build hangs in BUILDING indefinitely rather than failing, so --wait would
	// poll forever: bound it with a deadline. On the bug the push errors at the
	// deadline; when correct it builds and reaches ACTIVE well within it.
	w.writeModelPy("v5")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, errOut, err := cliCtx(t, ctx, "model", "push", "--develop", "--wait", "--dir", w.dir, "--output", "json")
	require.NoError(t, err,
		"development re-push to an existing model should build and reach ACTIVE; "+
			"a hang here means the existing-model development push did not resolve an instance type\nstderr:\n%s",
		errOut)

	// Confirm the re-pushed code is actually live: the development deployment now
	// serves v5. The push reached ACTIVE above, so this settles quickly; a fresh
	// deployment may still briefly return the not-ready 400 while it loads.
	require.Eventually(t, func() bool {
		out, errOut, err := cli(t, "model", "predict",
			"--model-name", w.modelName, "--data", "{}", "--output", "json")
		if err != nil {
			require.Contains(t, errOut+out, notReadyPredictMarker,
				"predict after re-push failed: %s", errOut)
			return false
		}
		var r struct {
			WatchVersion string `json:"watch_version"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &r))
		return r.WatchVersion == "v5"
	}, 60*time.Second, 2*time.Second, "re-pushed development deployment should serve watch_version v5")
}

// watchTest holds the state shared across a single watch run: the model under
// test, its source directory, and (once pushed) its ID. Created by
// [newWatchTest], which performs the env gate, materializes the source, and
// registers teardown.
type watchTest struct {
	t         *testing.T
	modelName string
	dir       string
	modelID   string
}

// newWatchTest runs the env gate, writes the version-tagged source, and
// registers cleanup. Skips when the e2e env vars are absent.
func newWatchTest(t *testing.T) *watchTest {
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")

	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())

	w := &watchTest{
		t:         t,
		modelName: fmt.Sprintf("cli-e2e-watch-%s", randomSuffix(t)),
		dir:       t.TempDir(),
	}
	cfg := fmt.Sprintf(watchConfigTmpl, w.modelName)
	require.NoError(t, os.WriteFile(filepath.Join(w.dir, "config.yaml"), []byte(cfg), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(w.dir, "model"), 0o755))
	w.writeModelPy("v1")

	// Register cleanup before the push so even a partial create gets removed.
	t.Cleanup(func() {
		if os.Getenv("BASETEN_E2E_KEEP_MODEL") != "" {
			t.Logf("BASETEN_E2E_KEEP_MODEL set; leaving model %q in place", w.modelName)
			return
		}
		if w.modelID == "" {
			w.modelID = lookupModelIDByName(t, w.modelName)
		}
		if w.modelID == "" {
			return
		}
		t.Logf("deleting model %s (%s)", w.modelName, w.modelID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, errOut, err := cliCtx(t, ctx, "model", "delete", "--model-id", w.modelID, "--yes"); err != nil {
			t.Logf("cleanup delete failed: %v\nstderr: %s", err, errOut)
		}
	})
	return w
}

// writeModelPy (re)writes model/model.py with the given version token.
func (w *watchTest) writeModelPy(version string) {
	w.t.Helper()
	py := fmt.Sprintf(watchModelPyTmpl, version)
	require.NoError(w.t, os.WriteFile(filepath.Join(w.dir, "model", "model.py"), []byte(py), 0o644))
}

// resolveModelID waits for the push to create the model and records its ID for
// predicting and cleanup, failing fast if the push exits first.
func (w *watchTest) resolveModelID(b *bgWatch) {
	w.t.Helper()
	b.waitFor(w.t, 60*time.Second, time.Second,
		fmt.Sprintf("model %q to be created by push", w.modelName), func() bool {
			out, _, err := cli(w.t, "model", "describe", "--model-name", w.modelName, "--output", "json")
			if err != nil {
				return false
			}
			var r struct {
				ID string `json:"id"`
			}
			if json.Unmarshal([]byte(out), &r) != nil {
				return false
			}
			w.modelID = r.ID
			return w.modelID != ""
		})
}

// notReadyPredictMarker is the substring the backend returns from predict while
// the development deployment is reloading the model (after a patch) or still
// deploying. The deployment status stays ACTIVE across a patch reload, so this
// inference-layer signal is the only way to tell the model is briefly unservable.
const notReadyPredictMarker = "Model is not ready"

// requireServesVersion polls the model's predict endpoint until it returns the
// given watch_version. The model is dev-only, so a plain predict (production
// routing) reaches its single development deployment. Each patch reloads the
// model, briefly returning the not-ready 400, so that is a retry (keep polling),
// as is a successful predict returning a stale version (the patch has not landed
// yet); any other predict error is fatal. Also fails if the watch exits first or
// the timeout elapses.
// It returns the serving process PID once the wanted version is observed, so
// callers can assert whether a patch restarted the process (cold) or not (hot).
func (w *watchTest) requireServesVersion(b *bgWatch, want string, timeout time.Duration) int {
	w.t.Helper()
	var pid int
	b.waitFor(w.t, timeout, time.Second,
		fmt.Sprintf("development deployment to serve watch_version %q", want), func() bool {
			out, errOut, err := cli(w.t, "model", "predict",
				"--model-name", w.modelName, "--data", "{}", "--output", "json")
			if err != nil {
				if strings.Contains(errOut, notReadyPredictMarker) || strings.Contains(out, notReadyPredictMarker) {
					return false
				}
				w.t.Fatalf("predict failed: %v\nstderr: %s\nstdout: %s", err, errOut, out)
			}
			var r struct {
				WatchVersion string `json:"watch_version"`
				PID          int    `json:"pid"`
			}
			require.NoError(w.t, json.Unmarshal([]byte(out), &r))
			if r.WatchVersion != want {
				return false
			}
			pid = r.PID
			return true
		})
	return pid
}

// startWatch launches a blocking watch command in a goroutine. cmd.Execute is
// invoked directly (not via the cli helper) so stderr is captured into a
// concurrency-safe buffer the test can poll for the ready marker.
func (w *watchTest) startWatch(args ...string) *bgWatch {
	w.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stderr := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		exit := 0
		err := cmd.Execute(ctx, cmd.ExecuteOptions{
			Args:  args,
			Stdin: strings.NewReader(""),
			// Tee to the real streams so the watch's progress is visible live
			// (e.g. piped to a logfile) while stderr is still scannable for the
			// ready marker.
			Stdout:       os.Stdout,
			Stderr:       io.MultiWriter(stderr, os.Stderr),
			ExitWithCode: func(c int) { exit = c },
		})
		if err == nil && exit != 0 {
			err = fmt.Errorf("baseten %s: exit %d", strings.Join(args, " "), exit)
		}
		done <- err
	}()
	return &bgWatch{cancel: cancel, done: done, stderr: stderr}
}

// bgWatch is a watch command running in the background. Cancelling its context
// (via stop) simulates Ctrl-C; the loop unwinds and the goroutine returns.
type bgWatch struct {
	cancel context.CancelFunc
	done   chan error
	stderr *syncBuffer
}

// waitFor calls check until it returns true. It runs on the calling (test)
// goroutine so it can fail the test directly: any exit of the watch command
// before stop is unexpected (it should run until we cancel it), so a value on
// done fails immediately rather than waiting out the timeout.
func (b *bgWatch) waitFor(t *testing.T, timeout, interval time.Duration, what string, check func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if check() {
			return
		}
		select {
		case err := <-b.done:
			t.Fatalf("watch exited before %s: %v\nstderr:\n%s", what, err, b.stderr.String())
		case <-deadline:
			t.Fatalf("timed out waiting for %s\nstderr:\n%s", what, b.stderr.String())
		case <-ticker.C:
		}
	}
}

// waitForMarker blocks until the watch logs that it has begun watching, or
// fails after timeout. runModelWatchLoop emits this only after the filesystem
// watcher is registered, so it is the clock-free handoff guaranteeing a
// subsequent source mutation will be observed (fsnotify only delivers events
// for watches that already exist).
func (b *bgWatch) waitForMarker(t *testing.T, timeout time.Duration) {
	t.Helper()
	b.waitFor(t, timeout, 200*time.Millisecond, "watch to start watching", func() bool {
		return strings.Contains(b.stderr.String(), "Watching for changes")
	})
}

// stop cancels the watch and waits for it to unwind, failing if it hangs.
func (b *bgWatch) stop(t *testing.T) {
	t.Helper()
	b.cancel()
	select {
	case err := <-b.done:
		t.Logf("watch exited: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatalf("watch did not exit after cancel; stderr:\n%s", b.stderr.String())
	}
}

// syncBuffer is an io.Writer safe for concurrent writes by the running command
// and reads by the test goroutine inspecting its output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
