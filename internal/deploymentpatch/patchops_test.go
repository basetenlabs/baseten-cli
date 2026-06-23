package deploymentpatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string { return &s }

// writeModelFile materializes a model-code file in a fresh dir and returns the
// dir, for ops that read content off disk.
func writeModelFile(t *testing.T, rel, content string) string {
	t.Helper()
	dir := t.TempDir()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	return dir
}

// decodeModelCodeHotReload returns the hot_reload flags of every model_code op,
// in order, reading them back off the marshaled union.
func decodeModelCodeHotReload(t *testing.T, ops []managementapi.CreateDeploymentPatchRequest_PatchOps_Item) []*bool {
	t.Helper()
	var flags []*bool
	for _, op := range ops {
		raw, err := json.Marshal(op)
		require.NoError(t, err)
		var decoded struct {
			Type      string `json:"type"`
			HotReload *bool  `json:"hot_reload"`
		}
		require.NoError(t, json.Unmarshal(raw, &decoded))
		if decoded.Type == "model_code" {
			flags = append(flags, decoded.HotReload)
		}
	}
	return flags
}

// TestBuildPatchOpsHotReload covers the hot-reload flag, which the golden cases
// do not exercise (Truss only sets it after calc, in remote.py _patch): it is
// applied only when HotReload is requested AND every op is a model-code change.
func TestBuildPatchOpsHotReload(t *testing.T) {
	const sameConfig = "model_name: m\n"
	const reqPrevConfig = "model_name: m\nrequirements:\n  - a==1\n"
	const reqNextConfig = "model_name: m\nrequirements:\n  - a==2\n"

	t.Run("all model code, hot reload requested", func(t *testing.T) {
		dir := writeModelFile(t, "model/model.py", "x = 2\n")
		prev := &managementapi.DeploymentPatchPoint{
			ContentHashes: map[string]*string{"model": nil, "model/model.py": strPtr("aaa")},
			Config:        sameConfig,
		}
		next := &managementapi.DeploymentPatchPoint{
			ContentHashes: map[string]*string{"model": nil, "model/model.py": strPtr("bbb")},
			Config:        sameConfig,
		}
		ops, err := BuildPatchOps(t.Context(), BuildPatchOpsOptions{Dir: dir, Prev: prev, Next: next, HotReload: true})
		require.NoError(t, err)
		flags := decodeModelCodeHotReload(t, ops)
		require.Len(t, flags, 1)
		require.NotNil(t, flags[0])
		require.True(t, *flags[0])
	})

	t.Run("mixed ops, hot reload requested falls back to cold restart", func(t *testing.T) {
		dir := writeModelFile(t, "model/model.py", "x = 2\n")
		// A requirements change makes this a config + requirement + model-code
		// patch, so hot reload must not be applied.
		prev := &managementapi.DeploymentPatchPoint{
			ContentHashes: map[string]*string{"model": nil, "model/model.py": strPtr("aaa")},
			Config:        reqPrevConfig,
		}
		next := &managementapi.DeploymentPatchPoint{
			ContentHashes: map[string]*string{"model": nil, "model/model.py": strPtr("bbb")},
			Config:        reqNextConfig,
		}
		ops, err := BuildPatchOps(t.Context(), BuildPatchOpsOptions{Dir: dir, Prev: prev, Next: next, HotReload: true})
		require.NoError(t, err)
		flags := decodeModelCodeHotReload(t, ops)
		require.Len(t, flags, 1)
		require.Nil(t, flags[0], "hot_reload must be unset on a mixed patch")
	})

	t.Run("all model code, hot reload not requested", func(t *testing.T) {
		dir := writeModelFile(t, "model/model.py", "x = 2\n")
		prev := &managementapi.DeploymentPatchPoint{
			ContentHashes: map[string]*string{"model": nil, "model/model.py": strPtr("aaa")},
			Config:        sameConfig,
		}
		next := &managementapi.DeploymentPatchPoint{
			ContentHashes: map[string]*string{"model": nil, "model/model.py": strPtr("bbb")},
			Config:        sameConfig,
		}
		ops, err := BuildPatchOps(t.Context(), BuildPatchOpsOptions{Dir: dir, Prev: prev, Next: next, HotReload: false})
		require.NoError(t, err)
		flags := decodeModelCodeHotReload(t, ops)
		require.Len(t, flags, 1)
		require.Nil(t, flags[0])
	})
}

func TestBuildPatchOpsValidation(t *testing.T) {
	point := &managementapi.DeploymentPatchPoint{Config: "model_name: m\n"}

	_, err := BuildPatchOps(t.Context(), BuildPatchOpsOptions{Prev: point, Next: point})
	require.ErrorContains(t, err, "Dir is required")

	_, err = BuildPatchOps(t.Context(), BuildPatchOpsOptions{Dir: t.TempDir(), Next: point})
	require.ErrorContains(t, err, "Prev and Next are required")

	_, err = BuildPatchOps(t.Context(), BuildPatchOpsOptions{Dir: t.TempDir(), Prev: point})
	require.ErrorContains(t, err, "Prev and Next are required")
}
