//go:build e2e

package e2etests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// e2eVolumeNamespace holds every volume these tests create. Namespaces come
// into existence on first push and cannot be removed, so this is a fixed name
// rather than a per-run one; the volume within it is what varies.
const e2eVolumeNamespace = "cli-e2e"

// The tree pushed and downloaded back. Nested so the download has a directory
// to recreate, and so --include has something to narrow to.
var e2eVolumeFiles = map[string]string{
	"weights.bin":         "not really weights, but bytes all the same\n",
	"tokenizer.json":      `{"e2e":"tokenizer"}` + "\n",
	"nested/config.json":  `{"e2e":"nested config"}` + "\n",
	"nested/notes/why.md": "# why\n\nA third level, so slash-boundary matching has depth to get wrong.\n",
}

// TestE2EVolumeLifecycle pushes a directory as a volume version, reads it back
// through every volume command, downloads it whole and narrowed, and re-pushes
// to confirm the second push reuses what the first one stored. Skips when the
// required env vars are absent.
//
// The organization behind the e2e key needs volumes enabled and the key needs
// organization-level model management permission. A missing prerequisite fails
// rather than skips, so a misconfigured environment does not read as a pass.
func TestE2EVolumeLifecycle(t *testing.T) {
	v := newVolumeLifecycle(t)
	t.Run("Push", v.Push)
	t.Run("Reads", v.Reads)
	t.Run("Download", v.Download)
	t.Run("Repush", v.Repush)
	t.Run("Delete", v.Delete)
}

// volumeLifecycle holds the state shared across the volume sub-tests. Created
// by [newVolumeLifecycle], which also materializes the source tree.
type volumeLifecycle struct {
	name      string
	ref       string
	tag       string
	sourceDir string

	// Captured by Push and asserted against by everything after it.
	versionRef string
	digest     string
	files      int64
	bytes      int64
}

// volumePushResult mirrors the JSON `baseten volume push` writes.
type volumePushResult struct {
	VersionRef     string   `json:"version_ref"`
	ManifestDigest string   `json:"manifest_digest"`
	Sequence       int64    `json:"sequence"`
	HeadUpdated    bool     `json:"head_updated"`
	HeadMoveDenied bool     `json:"head_move_denied"`
	TagsApplied    []string `json:"tags_applied"`
	Files          int64    `json:"files"`
	Bytes          int64    `json:"bytes"`
	Chunks         int64    `json:"chunks"`
	ChunksUnique   int64    `json:"chunks_unique"`
	ChunksReused   int64    `json:"chunks_reused"`
	ChunksExisting int64    `json:"chunks_existing"`
}

// volumeDownloadResult mirrors the JSON `baseten volume version download` writes.
type volumeDownloadResult struct {
	VersionRef     string   `json:"version_ref"`
	ManifestDigest string   `json:"manifest_digest"`
	OutDir         string   `json:"out_dir"`
	Files          int64    `json:"files"`
	Bytes          int64    `json:"bytes"`
	SelectedFiles  int64    `json:"selected_files"`
	TotalFiles     int64    `json:"total_files"`
	ChunksFetched  int64    `json:"chunks_fetched"`
	ChunksReused   int64    `json:"chunks_reused"`
	Warnings       []string `json:"warnings"`
}

func newVolumeLifecycle(t *testing.T) *volumeLifecycle {
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")

	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())

	suffix := randomSuffix(t)
	v := &volumeLifecycle{
		name: "vol-" + suffix,
		tag:  "e2e-" + suffix,
	}
	v.ref = e2eVolumeNamespace + "/" + v.name
	v.sourceDir = writeVolumeTree(t)
	return v
}

// writeVolumeTree materializes the source tree and returns its directory.
func writeVolumeTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for path, contents := range e2eVolumeFiles {
		full := filepath.Join(dir, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(contents), 0o644))
	}
	return dir
}

func (v *volumeLifecycle) Push(t *testing.T) {
	out := mustCLI(t, "volume", "push", "--ref", v.ref, "--dir", v.sourceDir,
		"--tag", v.tag, "--output", "json")
	var result volumePushResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))

	require.NotEmpty(t, result.ManifestDigest)
	require.Equal(t, "bdn://"+v.ref+"@"+result.ManifestDigest, result.VersionRef)
	// The first version of a new volume, so head moves to it and the tag we
	// asked for is applied at commit.
	require.True(t, result.HeadUpdated)
	require.False(t, result.HeadMoveDenied)
	require.Equal(t, []string{v.tag}, result.TagsApplied)
	require.Equal(t, int64(len(e2eVolumeFiles)), result.Files)
	require.Equal(t, expectedVolumeBytes(), result.Bytes)
	// Every chunk is new to the volume, since nothing was there before.
	require.Positive(t, result.Chunks)
	require.Zero(t, result.ChunksExisting)

	v.versionRef, v.digest = result.VersionRef, result.ManifestDigest
	v.files, v.bytes = result.Files, result.Bytes
}

func (v *volumeLifecycle) Reads(t *testing.T) {
	require.NotEmpty(t, v.digest, "Push did not record a digest")

	t.Run("NamespaceListIncludesNamespace", func(t *testing.T) {
		out := mustCLI(t, "volume", "namespace", "list", "--output", "json")
		var resp struct {
			Items []string `json:"items"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Contains(t, resp.Items, e2eVolumeNamespace)
	})

	t.Run("ListIncludesVolume", func(t *testing.T) {
		out := mustCLI(t, "volume", "list", "--namespace", e2eVolumeNamespace, "--output", "json")
		var resp struct {
			Items []struct {
				Name string `json:"name"`
				Head *struct {
					Digest string `json:"digest"`
				} `json:"head"`
			} `json:"items"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		for _, item := range resp.Items {
			if item.Name == v.name {
				require.NotNil(t, item.Head)
				require.Equal(t, v.digest, item.Head.Digest)
				return
			}
		}
		t.Fatalf("volume %q not in the namespace listing", v.name)
	})

	t.Run("DescribeReportsHead", func(t *testing.T) {
		out := mustCLI(t, "volume", "describe", "--ref", v.ref, "--output", "json")
		var resp struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
			Head      *struct {
				Digest         string `json:"digest"`
				TotalSizeBytes int64  `json:"total_size_bytes"`
			} `json:"head"`
			VersionsAlive int `json:"versions_alive"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, e2eVolumeNamespace, resp.Namespace)
		require.Equal(t, v.name, resp.Name)
		require.NotNil(t, resp.Head)
		require.Equal(t, v.digest, resp.Head.Digest)
		require.Equal(t, v.bytes, resp.Head.TotalSizeBytes)
		require.Equal(t, 1, resp.VersionsAlive)
	})

	t.Run("VersionListHasTheOneVersion", func(t *testing.T) {
		out := mustCLI(t, "volume", "version", "list", "--ref", v.ref, "--output", "json")
		var resp struct {
			Versions []struct {
				Digest    string   `json:"digest"`
				Lifecycle string   `json:"lifecycle"`
				IsHead    bool     `json:"is_head"`
				Tags      []string `json:"tags"`
			} `json:"versions"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Len(t, resp.Versions, 1)
		version := resp.Versions[0]
		require.Equal(t, v.digest, version.Digest)
		require.Equal(t, "ALIVE", version.Lifecycle)
		require.True(t, version.IsHead)
		require.Contains(t, version.Tags, v.tag)
	})

	// The three selector forms address one version, so all three answer with
	// the digest the push published.
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"Head", []string{"--ref", v.ref}},
		{"Tag", []string{"--ref", v.ref, "--tag", v.tag}},
		{"Digest", []string{"--ref", v.ref, "--digest", v.digest}},
	} {
		t.Run("VersionDescribeBy"+tc.name, func(t *testing.T) {
			args := append([]string{"volume", "version", "describe"}, tc.args...)
			out := mustCLI(t, append(args, "--output", "json")...)
			var resp struct {
				Digest     string `json:"digest"`
				EntryCount *int   `json:"entry_count"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &resp))
			require.Equal(t, v.digest, resp.Digest)
			if resp.EntryCount != nil {
				require.Equal(t, len(e2eVolumeFiles), *resp.EntryCount)
			}
		})
	}
}

func (v *volumeLifecycle) Download(t *testing.T) {
	require.NotEmpty(t, v.digest, "Push did not record a digest")

	t.Run("Whole", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "download")
		out := mustCLI(t, "volume", "version", "download", "--ref", v.ref,
			"--digest", v.digest, "--out-dir", outDir, "--output", "json")
		var result volumeDownloadResult
		require.NoError(t, json.Unmarshal([]byte(out), &result))

		require.Equal(t, v.versionRef, result.VersionRef)
		require.Equal(t, v.files, result.Files)
		require.Equal(t, v.bytes, result.Bytes)
		require.Equal(t, result.TotalFiles, result.SelectedFiles)
		require.Empty(t, result.Warnings)

		// What is actually on disk, which is the only thing that proves the
		// transfer rather than the counts it reported.
		require.Equal(t, e2eVolumeFiles, readVolumeTree(t, outDir))
	})

	t.Run("Include", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "download")
		// One exact file and one directory whose contents are wanted, so both
		// forms of --include are exercised in the same download.
		out := mustCLI(t, "volume", "version", "download", "--ref", v.ref,
			"--digest", v.digest, "--out-dir", outDir,
			"--include", "tokenizer.json", "--include", "nested/notes",
			"--output", "json")
		var result volumeDownloadResult
		require.NoError(t, json.Unmarshal([]byte(out), &result))

		want := map[string]string{
			"tokenizer.json":      e2eVolumeFiles["tokenizer.json"],
			"nested/notes/why.md": e2eVolumeFiles["nested/notes/why.md"],
		}
		require.Equal(t, int64(len(want)), result.SelectedFiles)
		require.Equal(t, v.files, result.TotalFiles)
		require.Equal(t, want, readVolumeTree(t, outDir))
	})
}

func (v *volumeLifecycle) Repush(t *testing.T) {
	require.NotEmpty(t, v.digest, "Push did not record a digest")

	// The same tree from the same directory, so the source URI the library
	// derives is the same too and this is the identical version rather than a
	// new one that happens to hold the same files.
	out := mustCLI(t, "volume", "push", "--ref", v.ref, "--dir", v.sourceDir, "--output", "json")
	var result volumePushResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))

	require.Equal(t, v.digest, result.ManifestDigest)
	// Nothing had to move, because head already pointed here.
	require.False(t, result.HeadUpdated)
	// The volume already holds every chunk, so none is uploaded as new: they
	// are either recognized locally against the previous version or offered
	// and found to be present.
	require.Zero(t, result.ChunksUnique)
	require.Positive(t, result.ChunksReused+result.ChunksExisting)

	// Still one version, since a repush of an unchanged tree publishes no new one.
	list := mustCLI(t, "volume", "version", "list", "--ref", v.ref, "--output", "json")
	var versions struct {
		Versions []struct {
			Digest string `json:"digest"`
		} `json:"versions"`
	}
	require.NoError(t, json.Unmarshal([]byte(list), &versions))
	require.Len(t, versions.Versions, 1)
}

func (v *volumeLifecycle) Delete(t *testing.T) {
	// Nothing can remove a volume yet, so this test leaves one behind on every
	// run, under a name unique to the run. Cleanup belongs here once the
	// delete and restore endpoints ship: delete the version, confirm the
	// tombstone, restore it, then delete the volume.
	t.Skip("volume and version deletion are not released yet; this run leaves " +
		"bdn://" + v.ref + " in place")
}

// expectedVolumeBytes totals the source tree, which is what the push reports
// and the download writes back.
func expectedVolumeBytes() int64 {
	var total int64
	for _, contents := range e2eVolumeFiles {
		total += int64(len(contents))
	}
	return total
}

// readVolumeTree reads every file under dir into a slash-keyed map, so a
// downloaded tree can be compared against the one that was pushed.
func readVolumeTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		got[filepath.ToSlash(relative)] = string(contents)
		return nil
	}))
	return got
}
