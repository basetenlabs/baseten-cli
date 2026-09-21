package harness

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

type detectionExecer struct {
	version string
	missing bool
	fail    bool
}

func (e detectionExecer) LookPath(name string) (string, error) {
	if e.missing {
		return "", exec.ErrNotFound
	}
	return filepath.Join(string(filepath.Separator), "fake", name), nil
}
func (e detectionExecer) Exec(command *exec.Cmd) error {
	if e.fail {
		return errors.New("version failed")
	}
	_, err := fmt.Fprint(command.Stdout, e.version)
	return err
}
func TestDetection(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		for _, tc := range []struct {
			label                string
			executor             detectionExecer
			installed, supported bool
		}{
			{"missing", detectionExecer{missing: true}, false, false},
			{"failed", detectionExecer{fail: true}, true, false},
			{"unknown", detectionExecer{version: "unknown"}, true, false},
			{"control", detectionExecer{version: "1.0\nextra"}, true, false},
			{"tested", detectionExecer{version: TestedVersions[name]}, true, runtime.GOOS == "darwin"},
		} {
			t.Run(name+"/"+tc.label, func(t *testing.T) {
				d, err := Detect(t.Context(), tc.executor, name, filepath.Join(t.TempDir(), "config"))
				require.NoError(t, err)
				require.Equal(t, tc.installed, d.Installed)
				require.Equal(t, tc.supported, d.Supported)
			})
		}
	}
}

func TestDetectionOpenCodeJSONC(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	executor := detectionExecer{version: TestedVersions["opencode"]}
	detection, err := Detect(t.Context(), executor, "opencode", "")
	require.NoError(t, err)
	jsonPath := filepath.Join(dir, "opencode", "opencode.json")
	require.Equal(t, jsonPath, detection.Path)
	require.NoError(t, os.MkdirAll(filepath.Dir(jsonPath), 0700))
	require.NoError(t, os.WriteFile(jsonPath+"c", []byte("{}"), 0600))
	detection, err = Detect(t.Context(), executor, "opencode", "")
	require.NoError(t, err)
	require.Equal(t, jsonPath+"c", detection.Path)
	require.NoError(t, os.WriteFile(jsonPath, []byte("{}"), 0600))
	detection, err = Detect(t.Context(), executor, "opencode", "")
	require.NoError(t, err)
	require.Equal(t, jsonPath+"c", detection.Path)
	detection, err = Detect(t.Context(), executor, "opencode", jsonPath)
	require.NoError(t, err)
	require.Equal(t, jsonPath, detection.Path)
}
