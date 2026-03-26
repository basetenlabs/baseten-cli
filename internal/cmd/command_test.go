package cmd_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/stretchr/testify/require"
)

// CommandHarness runs CLI commands and captures output for testing.
type CommandHarness struct {
	T       *testing.T
	Require *require.Assertions
	Context context.Context
	Stdout  bytes.Buffer
	Stderr  bytes.Buffer

	ExitCode int
	exited   bool
}

func NewCommandHarness(t *testing.T) *CommandHarness {
	t.Setenv("BASETEN_API_KEY", "test-key")
	t.Setenv("BASETEN_BASE_URL", "http://127.0.0.1:1")
	return &CommandHarness{T: t, Require: require.New(t), Context: t.Context()}
}

func (h *CommandHarness) Execute(args ...string) error {
	h.Stdout.Reset()
	h.Stderr.Reset()
	h.ExitCode = 0
	h.exited = false
	cmd.VerifyRunners()
	err := cmd.Execute(h.Context, cmd.ExecuteOptions{
		Args:   args,
		Stdout: &h.Stdout,
		Stderr: &h.Stderr,
		ExitWithCode: func(code int) {
			h.ExitCode = code
			h.exited = true
		},
	})
	if err != nil && !h.exited {
		h.ExitCode = 1
	}
	return err
}

func (h *CommandHarness) Exited() bool {
	return h.exited
}

func TestVerifyRunners(t *testing.T) {
	cmd.VerifyRunners()
}

func TestHelpOutput(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("--help")
	h.Require.NoError(err)
	h.Require.Contains(h.Stdout.String(), "Available Commands")
}

func TestOutputEnumValidation(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("api", "management", "--output", "invalid", "some/path")
	h.Require.ErrorContains(err, "must be one of")
}
