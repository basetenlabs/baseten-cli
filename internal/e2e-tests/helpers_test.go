//go:build e2e

package e2etests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/stretchr/testify/require"
)

// Minimal Truss source files baked into the test binary.
const trussConfigTmpl = `model_name: %s
python_version: py313
resources:
  cpu: 50m
  memory: 50Mi
  use_gpu: false
runtime:
  remote_ssh:
    enabled: true
`

// Marker tokens emitted by the test model's load() and asserted by the Logs
// phase. The shared marker scopes queries to our lines; the per-level words
// drive the min-level / includes / excludes assertions. A fixed marker is
// safe because we only ever query this deployment's logs.
const (
	e2eLogMarker      = "baseten-e2e-log"
	e2eLogInfoWord    = "apple"
	e2eLogWarningWord = "banana"
	e2eLogErrorWord   = "cherry"

	// e2ePageMarker tags the burst of lines the pagination sub-test filters to;
	// e2ePageTotalLineCount must match the loop count in trussModelPy.
	e2ePageMarker         = "baseten-e2e-pagination"
	e2ePageTotalLineCount = 30
)

const trussModelPy = `import logging
import time

from fastapi.responses import StreamingResponse

_logger = logging.getLogger(__name__)

class Model:
    def load(self):
        _logger.info("baseten-e2e-log info apple")
        _logger.warning("baseten-e2e-log warning banana")
        _logger.error("baseten-e2e-log error cherry")
        # Emit e2ePageTotalLineCount uniquely-numbered lines, spaced well past a
        # millisecond apart so each lands in its own timestamp regardless of the
        # logging pipeline's timestamp granularity; otherwise several lines could
        # share one millisecond and a small --page-size would trip the
        # single-millisecond-burst failure. The pagination sub-test filters to
        # these with --includes and pages them with a tiny --page-size.
        for i in range(30):
            time.sleep(0.02)
            _logger.info("baseten-e2e-pagination line %02d" % i)

    def predict(self, request):
        if request.get("style") == "streaming":
            chunks = request.get("chunks", ["alpha", "beta", "gamma"])
            def gen():
                for c in chunks:
                    yield c
            return gen()
        if request.get("style") == "sse":
            chunks = request.get("chunks", ["alpha", "beta", "gamma"])
            def gen():
                for c in chunks:
                    yield f"data: {c}\n\n"
            return StreamingResponse(gen(), media_type="text/event-stream")
        return {"got request": request}
`

// pushedDeployment is the JSON shape returned by `model push --output json`.
type pushedDeployment struct {
	Model struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"model"`
	Deployment struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"deployment"`
}

// repoRoot returns the module root (two levels up from this file in
// internal/e2e-tests), so commands like `go build ./cmd/baseten` run against
// the current source rather than whatever the working directory happens to be.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// cli runs the CLI in-process with the given args, returning captured stdout
// and stderr. Non-zero exits surface as a non-nil error.
func cli(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return cliCtx(t, t.Context(), args...)
}

// cliCtx is cli with an explicit context. Use this from t.Cleanup, since
// `t.Context()` is canceled before cleanup runs.
func cliCtx(t *testing.T, ctx context.Context, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return cliWithStdin(t, ctx, "", args...)
}

// cliWithStdin is cliCtx that pipes the given string to the command's stdin.
func cliWithStdin(t *testing.T, ctx context.Context, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var sout, serr bytes.Buffer
	exit := 0
	err = cmd.Execute(ctx, cmd.ExecuteOptions{
		Args:         args,
		Stdin:        strings.NewReader(stdin),
		Stdout:       &sout,
		Stderr:       &serr,
		ExitWithCode: func(c int) { exit = c },
	})
	if err == nil && exit != 0 {
		err = fmt.Errorf("baseten %s: exit %d", strings.Join(args, " "), exit)
	}
	return sout.String(), serr.String(), err
}

// mustCLI runs cli and fatals on error, returning stdout.
func mustCLI(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, err := cli(t, args...)
	if err != nil {
		t.Fatalf("baseten %s failed: %v\nstderr: %s", strings.Join(args, " "), err, errOut)
	}
	return out
}

// mustCLIStdin is mustCLI with a string piped to the command's stdin.
func mustCLIStdin(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	out, errOut, err := cliWithStdin(t, t.Context(), stdin, args...)
	if err != nil {
		t.Fatalf("baseten %s failed: %v\nstderr: %s", strings.Join(args, " "), err, errOut)
	}
	return out
}

// writeTruss materializes the baked-in Truss source into a temp dir with
// the given model name baked into config.yaml.
func writeTruss(t *testing.T, modelName string) string {
	t.Helper()
	dir := t.TempDir()
	cfg := fmt.Sprintf(trussConfigTmpl, modelName)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "model"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model", "model.py"), []byte(trussModelPy), 0o644))
	return dir
}

// lookupModelIDByName is the fallback used at cleanup when we couldn't parse
// the push response (e.g. push failed mid-way). Returns "" if not found.
func lookupModelIDByName(t *testing.T, name string) string {
	t.Helper()
	out, _, err := cli(t, "api", "management", "models")
	if err != nil {
		return ""
	}
	var resp struct {
		Models []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return ""
	}
	for _, m := range resp.Models {
		if m.Name == name {
			return m.ID
		}
	}
	return ""
}
