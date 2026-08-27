package cmd_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

// trussFakeExecer implements cmd.Execer for the commands that shell out to the
// truss CLI, recording invocations and optionally writing simulated truss
// output so tests can tell stdout from stderr.
type trussFakeExecer struct {
	available map[string]bool
	stdout    string
	exitCode  int
	calls     []*exec.Cmd
}

func newTrussFakeExecer(available ...string) *trussFakeExecer {
	m := map[string]bool{}
	for _, a := range available {
		m[a] = true
	}
	return &trussFakeExecer{available: m}
}

func (f *trussFakeExecer) LookPath(name string) (string, error) {
	if f.available[name] {
		if filepath.IsAbs(name) {
			return name, nil
		}
		return "/fake/" + name, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func (f *trussFakeExecer) Exec(c *exec.Cmd) error {
	f.calls = append(f.calls, c)
	if f.stdout != "" && c.Stdout != nil {
		if _, err := fmt.Fprint(c.Stdout, f.stdout); err != nil {
			return err
		}
	}
	if f.exitCode != 0 {
		return &cmd.ErrSubprocess{
			Err:  errors.New("truss: bad arguments"),
			Code: f.exitCode,
		}
	}
	return nil
}

// only returns the single recorded invocation, failing if there is not exactly one.
func (f *trussFakeExecer) only(t *testing.T) *exec.Cmd {
	t.Helper()
	if len(f.calls) != 1 {
		t.Fatalf("expected exactly one command, got %d", len(f.calls))
	}
	return f.calls[0]
}

// newTrussHarness returns a harness whose truss invocations are recorded rather
// than run, with uv available so the default resolution path works.
func newTrussHarness(t *testing.T, available ...string) (*CommandHarness, *trussFakeExecer) {
	h := NewCommandHarness(t)
	if len(available) == 0 {
		available = []string{"uv"}
	}
	fake := newTrussFakeExecer(available...)
	h.Context = cmd.WithExecer(h.Context, fake)
	return h, fake
}

func Test_Truss_UVNotOnPath(t *testing.T) {
	h, _ := newTrussHarness(t, "truss")
	_ = h.Execute("truss", "push")
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), "uv not found")
}

func Test_Truss_ForwardsArgsAndAuth(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute("truss", "push", "--publish", "--help"))

	c := fake.only(t)
	h.Require.Equal([]string{"uv", "tool", "run", "truss@latest", "push", "--publish", "--help"}, c.Args)
	h.Require.Contains(c.Env, "TRUSS_NO_UPDATE_CHECK=1")
	// The harness authenticates with BASETEN_API_KEY against BASETEN_REMOTE_URL.
	h.Require.Contains(c.Env, "BASETEN_TRUSS_AUTH_API_KEY=test-key")
	h.Require.Contains(c.Env, "BASETEN_TRUSS_AUTH_REMOTE_URL=http://127.0.0.1:1")
}

func Test_Truss_ExtractsOwnFlagsAnywhere(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute(
		"truss", "push", "--truss-version", "0.18.26", "./model", "--truss-no-forward-auth", "--publish"))

	c := fake.only(t)
	// Our flags are consumed wherever they appear; everything else keeps its order.
	h.Require.Equal([]string{"uv", "tool", "run", "truss@0.18.26", "push", "./model", "--publish"}, c.Args)
	h.Require.NotContains(strings.Join(c.Env, " "), "BASETEN_TRUSS_AUTH_")
}

func Test_Truss_ExtractsInlineFlagValue(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute("truss", "--truss-version=0.18.26", "push"))

	h.Require.Equal("truss@0.18.26", fake.only(t).Args[3])
}

func Test_Truss_FlagNeedingValueAtEnd(t *testing.T) {
	h, _ := newTrussHarness(t)
	_ = h.Execute("truss", "push", "--truss-version")
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), "needs a value")
}

func Test_Truss_FlagNeedingValueFollowedByFlag(t *testing.T) {
	h, _ := newTrussHarness(t)
	// Consuming the next flag as the value would run truss@--help and swallow
	// the argument meant for truss.
	_ = h.Execute("truss", "push", "--truss-version", "--help")
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), "needs a value")
}

func Test_Truss_Executable(t *testing.T) {
	h, fake := newTrussHarness(t, ".venv/bin/truss")

	h.Require.NoError(h.Execute("truss", "--truss-executable", ".venv/bin/truss", "chains", "push"))

	c := fake.only(t)
	h.Require.Equal([]string{"/fake/.venv/bin/truss", "chains", "push"}, c.Args)
}

func Test_Truss_ExecutableNotFound(t *testing.T) {
	h, _ := newTrussHarness(t)
	_ = h.Execute("truss", "--truss-executable", "nope/truss", "push")
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), `truss executable "nope/truss" not found`)
}

func Test_Truss_VersionAndExecutableMutuallyExclusive(t *testing.T) {
	h, _ := newTrussHarness(t, "uv", "truss")
	_ = h.Execute("truss", "--truss-version", "0.18.26", "--truss-executable", "truss", "push")
	h.Require.True(h.Exited())
	h.Require.Contains(h.Stderr.String(), "mutually exclusive")
}

func Test_Truss_EnvDefaultsSelectTruss(t *testing.T) {
	h, fake := newTrussHarness(t)
	h.T.Setenv("BASETEN_TRUSS_VERSION", "0.18.26")

	h.Require.NoError(h.Execute("truss", "push"))

	h.Require.Equal("truss@0.18.26", fake.only(t).Args[3])
}

func Test_Truss_EnvExecutableDefault(t *testing.T) {
	h, fake := newTrussHarness(t, "my-truss")
	h.T.Setenv("BASETEN_TRUSS_EXECUTABLE", "my-truss")

	h.Require.NoError(h.Execute("truss", "push"))

	h.Require.Equal("/fake/my-truss", fake.only(t).Args[0])
}

func Test_Truss_FlagOverridesOtherEnvDefault(t *testing.T) {
	h, fake := newTrussHarness(t, "my-truss")
	h.T.Setenv("BASETEN_TRUSS_VERSION", "0.18.26")

	// A stored version default must not collide with an explicit executable.
	h.Require.NoError(h.Execute("truss", "--truss-executable", "my-truss", "push"))

	h.Require.Equal("/fake/my-truss", fake.only(t).Args[0])
}

func Test_Truss_RejectsTrussAuthEnv(t *testing.T) {
	for _, name := range []string{"BASETEN_TRUSS_AUTH_REMOTE_URL", "BASETEN_TRUSS_AUTH_API_KEY"} {
		t.Run(name, func(t *testing.T) {
			h, _ := newTrussHarness(t)
			h.T.Setenv(name, "value")
			_ = h.Execute("truss", "push")
			h.Require.True(h.Exited())
			h.Require.Contains(h.Stderr.String(), name+" is set")
			h.Require.Contains(h.Stderr.String(), "BASETEN_API_KEY")
		})
	}
}

func Test_Truss_NoArgsShowsHelp(t *testing.T) {
	h, fake := newTrussHarness(t)

	h.Require.NoError(h.Execute("truss"))

	// Nothing to forward, so our own help renders instead, which is the only
	// place the --truss-* flags are documented.
	h.Require.Empty(fake.calls)
	h.Require.Contains(h.Stdout.String(), "--truss-version")
}

func Test_Truss_PassthroughStdoutIsVerbatim(t *testing.T) {
	h, fake := newTrussHarness(t)
	fake.stdout = "truss output\n"

	h.Require.NoError(h.Execute("truss", "push"))

	// `baseten truss` reserves nothing: truss's stdout is the command's stdout.
	h.Require.Equal("truss output\n", h.Stdout.String())
}

func Test_Truss_Delegated_JSONReservesStdout(t *testing.T) {
	h, fake := newTrussHarness(t)
	fake.stdout = "job created\n"

	h.Require.NoError(h.Execute("train", "init", "--dir", "./out", "--output", "json"))

	// Under JSON output, stdout is the result object and truss's text moves to stderr.
	result := map[string]any{}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Empty(result)
	h.Require.Contains(h.Stderr.String(), "job created")
}

func Test_Truss_Delegated_TextPassesOutputThrough(t *testing.T) {
	h, fake := newTrussHarness(t)
	fake.stdout = "job created\n"

	h.Require.NoError(h.Execute("train", "init", "--dir", "./out"))

	h.Require.Equal("job created\n", h.Stdout.String())
}

func Test_Truss_ExitTwoDoesNotPrintUsage(t *testing.T) {
	h, fake := newTrussHarness(t)
	fake.exitCode = 2

	// truss exits 2 for its own validation errors, which collides with the
	// CLI's own usage exit code; the child's failure is not a usage error here.
	h.Require.Error(h.Execute("truss", "no-such-command"))
	h.Require.Equal(2, h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "truss: bad arguments")
	h.Require.NotContains(h.Stdout.String(), "Usage:")
	h.Require.NotContains(h.Stderr.String(), "Usage:")
}

func Test_Truss_Delegated_ExitTwoKeepsJSONStdout(t *testing.T) {
	h, fake := newTrussHarness(t)
	fake.exitCode = 2

	h.Require.Error(h.Execute("train", "init", "--dir", "./out", "--output", "json"))
	h.Require.Equal(2, h.ExitCode)
	h.Require.Empty(h.Stdout.String())
}
