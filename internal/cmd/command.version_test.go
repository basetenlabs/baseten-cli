package cmd_test

import (
	"testing"

	"github.com/basetenlabs/baseten-cli/cmd"
)

// Test_Version_JQ_TextConflict asserts that --jq with an explicit --output
// text fails fast with ErrUsage (exit code 2) before the runner is invoked.
func Test_Version_JQ_TextConflict(t *testing.T) {
	h := NewCommandHarness(t)
	_ = h.Execute("version", "--jq", ".version", "--output", "text")
	h.Require.True(h.Exited())
	h.Require.Equal(int(cmd.ExitUsage), h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "--jq cannot be used with --output text")
}

// Test_Version_JQ_ForcesJSON asserts that --jq without an explicit --output
// forces JSON, parses the expression, and applies it to the leaf's payload.
func Test_Version_JQ_ForcesJSON(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("version", "--jq", ".version")
	h.Require.NoError(err)
	h.Require.Equal("\"dev\"\n", h.Stdout.String())
}

// Test_Version_JQ_RuntimeError asserts that a jq runtime error (here, calling
// tonumber on a non-numeric string) surfaces as a non-usage error with the
// "jq error:" prefix and an exit code of 1.
func Test_Version_JQ_RuntimeError(t *testing.T) {
	h := NewCommandHarness(t)
	_ = h.Execute("version", "--jq", ".version | tonumber")
	h.Require.True(h.Exited())
	h.Require.Equal(int(cmd.ExitGeneric), h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "jq error")
}
