package deploymentpatch

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/stretchr/testify/require"
)

// sourceManifest is one source tree (files, binary files, empty dirs) shared by
// both golden formats. A patch-point case is a single manifest; a patch-op case
// has a prev and a next manifest.
type sourceManifest struct {
	Files       map[string]string `json:"files"`
	BinaryFiles map[string]string `json:"binary_files"`
	EmptyDirs   []string          `json:"empty_dirs"`
}

// patchPointCase is one input manifest from fixturegen/patchpoint_cases.json.
type patchPointCase struct {
	Name string `json:"name"`
	sourceManifest
}

// patchPointResult is one expected vector from fixturegen/patchpoint_golden.json,
// produced by real Truss (see generate.py).
type patchPointResult struct {
	Name          string             `json:"name"`
	ContentHashes map[string]*string `json:"content_hashes"`
	Config        string             `json:"config"`
	Requirements  []string           `json:"requirements"`
}

// TestBuildPatchPointGoldenParity materializes each fixturegen case into a temp
// dir, runs BuildPatchPoint (which resolves ignores the same way Truss does: the
// case's .truss_ignore if present, else the bundled defaults), and asserts the
// output matches the committed golden vectors generated from real Truss.
func TestBuildPatchPointGoldenParity(t *testing.T) {
	var cases []patchPointCase
	loadJSON(t, "patchpoint_cases.json", &cases)
	var results []patchPointResult
	loadJSON(t, "patchpoint_golden.json", &results)
	require.Len(t, results, len(cases), "patchpoint_cases.json and patchpoint_golden.json out of sync")

	for i, c := range cases {
		want := results[i]
		require.Equal(t, c.Name, want.Name, "patchpoint cases/golden ordering mismatch")

		t.Run(c.Name, func(t *testing.T) {
			dir := t.TempDir()
			materialize(t, dir, c.sourceManifest)

			got, err := BuildPatchPoint(t.Context(), BuildPatchPointOptions{Dir: dir})
			require.NoError(t, err)

			require.Equal(t, want.ContentHashes, got.ContentHashes, "content_hashes diverge from Truss")
			require.Equal(t, want.Config, got.Config, "config diverges from Truss")
			require.NotNil(t, got.Requirements)
			require.Equal(t, want.Requirements, *got.Requirements, "requirements diverge from Truss")
		})
	}
}

// patchOpCase is one input from fixturegen/patchop_cases.json: a prev and next
// source tree that BuildPatchOps diffs.
type patchOpCase struct {
	Name string         `json:"name"`
	Prev sourceManifest `json:"prev"`
	Next sourceManifest `json:"next"`
}

// patchOpResult is one expected vector from fixturegen/patchop_golden.json,
// produced by real Truss's calc_truss_patch (see generate.py). Ops are the REST
// patch ops as JSON objects; NeedsFullDeploy marks a calc_truss_patch None.
type patchOpResult struct {
	Name            string            `json:"name"`
	NeedsFullDeploy bool              `json:"needs_full_deploy"`
	Ops             []json.RawMessage `json:"ops"`
}

// TestBuildPatchOpsGoldenParity materializes each prev/next pair, builds both
// patch points, runs BuildPatchOps, and asserts the resulting ops match the ops
// real Truss's calc_truss_patch produces (rendered into the REST shape). The
// comparison is order-insensitive because Truss's op order is set-driven.
func TestBuildPatchOpsGoldenParity(t *testing.T) {
	var cases []patchOpCase
	loadJSON(t, "patchop_cases.json", &cases)
	var results []patchOpResult
	loadJSON(t, "patchop_golden.json", &results)
	require.Len(t, results, len(cases), "patchop_cases.json and patchop_golden.json out of sync")

	for i, c := range cases {
		want := results[i]
		require.Equal(t, c.Name, want.Name, "patchop cases/golden ordering mismatch")

		t.Run(c.Name, func(t *testing.T) {
			prevDir := t.TempDir()
			nextDir := t.TempDir()
			materialize(t, prevDir, c.Prev)
			materialize(t, nextDir, c.Next)

			prev, err := BuildPatchPoint(t.Context(), BuildPatchPointOptions{Dir: prevDir})
			require.NoError(t, err)
			next, err := BuildPatchPoint(t.Context(), BuildPatchPointOptions{Dir: nextDir})
			require.NoError(t, err)

			ops, err := BuildPatchOps(t.Context(), BuildPatchOpsOptions{Dir: nextDir, Prev: prev, Next: next})
			if want.NeedsFullDeploy {
				require.ErrorIs(t, err, ErrNeedsFullDeploy)
				return
			}
			require.NoError(t, err)

			require.ElementsMatch(t, canonicalGoldenOps(t, want.Ops), canonicalBuiltOps(t, ops),
				"patch ops diverge from Truss")
		})
	}
}

// canonicalGoldenOps normalizes the golden op objects to compact JSON strings.
func canonicalGoldenOps(t *testing.T, ops []json.RawMessage) []string {
	t.Helper()
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, canonicalJSON(t, []byte(op)))
	}
	return out
}

// canonicalBuiltOps marshals each built union op to the same compact JSON form.
// The union marshals as its underlying body (with the "type" discriminator),
// which is exactly the shape generate.py renders.
func canonicalBuiltOps(t *testing.T, ops []managementapi.CreateDeploymentPatchRequest_PatchOps_Item) []string {
	t.Helper()
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		raw, err := json.Marshal(op)
		require.NoError(t, err)
		out = append(out, canonicalJSON(t, raw))
	}
	return out
}

// canonicalJSON round-trips JSON bytes through a map so key order and the
// "type": null discriminator placeholder do not affect comparison.
func canonicalJSON(t *testing.T, raw []byte) string {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	// The generated union sets "type" via the discriminator; drop any null type
	// so both sides compare on real fields only.
	if v, ok := m["type"]; ok && v == nil {
		delete(m, "type")
	}
	out, err := json.Marshal(m)
	require.NoError(t, err)
	return string(out)
}

func loadJSON(t *testing.T, name string, dst any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("fixturegen", name))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, dst))
}

func materialize(t *testing.T, dir string, m sourceManifest) {
	t.Helper()
	for rel, content := range m.Files {
		writeFile(t, dir, rel, []byte(content))
	}
	for rel, b64 := range m.BinaryFiles {
		raw, err := base64.StdEncoding.DecodeString(b64)
		require.NoError(t, err)
		writeFile(t, dir, rel, raw)
	}
	for _, rel := range m.EmptyDirs {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.FromSlash(rel)), 0o755))
	}
}

func writeFile(t *testing.T, dir, rel string, data []byte) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, data, 0o644))
}
