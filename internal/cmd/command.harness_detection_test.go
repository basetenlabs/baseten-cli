package cmd_test

import (
	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
	"os"
	"path/filepath"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/stretchr/testify/require"
)

func Test_Harness_Setup_HarnessSetupOptions(t *testing.T) {
	options, summary := internalcmd.HarnessSetupOptionsForTest([]harness.Detection{
		{Name: "claude-code", Installed: true, Version: "2.1.272", Supported: true, Path: "/test/settings.json"},
		{Name: "codex", Installed: true, Version: "0.135.0"},
		{Name: "opencode"},
	})
	require.Len(t, options, 1)
	require.Equal(t, "claude-code   2.1.272     /test/settings.json", options[0].String())
	require.Contains(t, summary, "codex         0.135.0")
	require.Contains(t, summary, "[-] opencode      not installed or not on PATH")
}

func Test_Harness_Setup_HarnessSetupOptionsUnavailable(t *testing.T) {
	options, summary := internalcmd.HarnessSetupOptionsForTest([]harness.Detection{
		{Name: "claude-code", Installed: true}, {Name: "codex"}, {Name: "opencode"},
	})
	require.Empty(t, options)
	require.Contains(t, summary, "(version unavailable)")
	require.Contains(t, summary, "[-] codex         not installed or not on PATH")
}

func Test_Harness_Setup_HarnessDetectionConfigPath(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	d := harness.Detection{Name: "codex", Installed: true, Supported: true, Version: "0.134.0", Path: filepath.Join(home, ".codex", "config.toml")}
	require.Contains(t, internalcmd.HarnessDetectionLabelForTest(d), filepath.Join("~", ".codex", "config.toml"))
	d.Path = home + "-other" + string(filepath.Separator) + "config.toml"
	require.Contains(t, internalcmd.HarnessDetectionLabelForTest(d), d.Path)
}
