package cmd_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func init() {
	// Use an in-memory keyring for the entire test package so auth commands
	// never touch the developer's real system keychain.
	keyring.MockInit()
}

// CommandHarness runs CLI commands and captures output for testing.
type CommandHarness struct {
	T       *testing.T
	Require *require.Assertions
	Context context.Context
	Stdin   bytes.Buffer
	Stdout  bytes.Buffer
	Stderr  bytes.Buffer

	ExitCode int
	exited   bool
}

// NewCommandHarness sets sensible env defaults and returns a fresh harness.
// Tests can override any of these with a subsequent t.Setenv before Execute.
func NewCommandHarness(t *testing.T) *CommandHarness {
	t.Setenv("BASETEN_API_KEY", "test-key")
	t.Setenv("BASETEN_REMOTE_URL", "http://127.0.0.1:1")
	t.Setenv("BASETEN_MANAGEMENT_API_URL_OVERRIDE", "http://127.0.0.1:1")
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())
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
		Stdin:  &h.Stdin,
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
	if err == nil && h.exited && h.ExitCode != 0 {
		return fmt.Errorf("command exited with code %d: %s", h.ExitCode, h.Stderr.String())
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
