//go:build e2e

package e2etests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// e2eVolumeNamespace holds every volume these tests create. Namespaces come
// into existence on first push and cannot be removed, so this is a fixed name
// rather than a per-run one; the volume within it is what varies.
const e2eVolumeNamespace = "cli-e2e"

// The tree pushed and read back. Nested so a listing has a directory to
// derive, a pull has one to recreate, and slash-boundary matching has depth to
// get wrong.
var e2eVolumeFiles = map[string]string{
	"weights.bin":         "not really weights, but bytes all the same\n",
	"tokenizer.json":      `{"e2e":"tokenizer"}` + "\n",
	"nested/config.json":  `{"e2e":"nested config"}` + "\n",
	"nested/notes/why.md": "# why\n\nA third level, so slash-boundary matching has depth to get wrong.\n",
}

// TestE2EVolumeLifecycle pushes a directory as a volume version, reads it back
// through every volume command, pulls it whole and narrowed, re-pushes to
// confirm the second push reuses what the first stored, and then deletes and
// restores its way back to an empty volume. Skips when the required env vars
// are absent.
//
// The organization behind the e2e key needs volumes enabled and the key needs
// organization-level model management permission. A missing prerequisite fails
// rather than skips, so a misconfigured environment does not read as a pass.
func TestE2EVolumeLifecycle(t *testing.T) {
	v := newVolumeLifecycle(t)
	t.Run("Push", v.Push)
	t.Run("Inventory", v.Inventory)
	t.Run("Entries", v.Entries)
	t.Run("Pull", v.Pull)
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

// volumePullResult mirrors the JSON `baseten volume pull` writes.
type volumePullResult struct {
	VersionRef    string   `json:"version_ref"`
	DestDir       string   `json:"dest_dir"`
	Files         int64    `json:"files"`
	Bytes         int64    `json:"bytes"`
	SelectedFiles int64    `json:"selected_files"`
	TotalFiles    int64    `json:"total_files"`
	ChunksFetched int64    `json:"chunks_fetched"`
	ChunksReused  int64    `json:"chunks_reused"`
	Warnings      []string `json:"warnings"`
}

// volumeEntryList mirrors the JSON `baseten volume ls` writes for a ref naming
// a volume or a version.
type volumeEntryList struct {
	VersionRef string `json:"version_ref"`
	Items      []struct {
		Path       string `json:"path"`
		Kind       string `json:"kind"`
		SizeBytes  int64  `json:"size_bytes"`
		Mode       string `json:"mode"`
		LinkTarget string `json:"link_target"`
	} `json:"items"`
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
	v.ref = "bdn:" + e2eVolumeNamespace + "/" + v.name
	v.sourceDir = writeVolumeTree(t)

	// The volume itself cannot be removed, only emptied, so a run that fails
	// partway leaves a named-for-the-run volume with no live versions rather
	// than one holding whatever it managed to push.
	t.Cleanup(func() {
		_, _, _ = cliCtx(t, t.Context(), "volume", "rm", "--recursive", "--yes", v.ref)
	})
	return v
}

// digestPrefix truncates the digest to hexChars hex characters, keeping the
// algorithm, since that is how a prefix selector is written too.
func (v *volumeLifecycle) digestPrefix(hexChars int) string {
	return v.digest[:len("b3:")+hexChars]
}

// untaggedDigest drops the algorithm, which is optional on input everywhere a
// digest is read even though nothing renders one without it.
func (v *volumeLifecycle) untaggedDigest() string {
	return strings.TrimPrefix(v.digest, "b3:")
}

// versionSelectors is every way of naming the one version Push published. All
// six resolve upstream, so a command that takes a version has to answer the
// same for each, which is what makes them worth running the whole set through.
func (v *volumeLifecycle) versionSelectors() []string {
	return []string{
		":" + v.tag,
		":head",
		"@" + v.digest,
		"@" + v.digestPrefix(12),
		"@" + v.untaggedDigest(),
		"@" + v.untaggedDigest()[:12],
	}
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
	out := mustCLI(t, "volume", "push", v.sourceDir, v.ref, "--tag", v.tag, "--output", "json")
	var result volumePushResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))

	// The published version is addressed by digest, which is what a config.yaml
	// pins to, so the ref it reports is the ref plus a digest and nothing else.
	// The digest carries its algorithm, the one spelling used everywhere.
	base, digest, found := strings.Cut(result.VersionRef, "@")
	require.True(t, found, "version ref %q carries no digest", result.VersionRef)
	require.Equal(t, v.ref, base)
	require.Regexp(t, `^b3:[0-9a-f]{64}$`, digest)

	// The first version of a new volume, so head moves to it and the tag we
	// asked for is applied at commit.
	require.True(t, result.HeadUpdated)
	require.False(t, result.HeadMoveDenied)
	require.Equal(t, []string{v.tag}, result.TagsApplied)
	require.Equal(t, int64(len(e2eVolumeFiles)), result.Files)
	require.Equal(t, expectedVolumeBytes(), result.Bytes)
	// The three counts partition the whole. Which way they fall is not
	// asserted: dedup is namespace-wide, so a run whose content an earlier run
	// already stored legitimately reports it as existing.
	require.Positive(t, result.Chunks)
	require.Equal(t, result.Chunks,
		result.ChunksUnique+result.ChunksReused+result.ChunksExisting)

	v.versionRef, v.digest = result.VersionRef, digest
	v.files, v.bytes = result.Files, result.Bytes
}

func (v *volumeLifecycle) Inventory(t *testing.T) {
	require.NotEmpty(t, v.digest, "Push did not record a digest")

	t.Run("NamespacesIncludeOurs", func(t *testing.T) {
		out := mustCLI(t, "volume", "ls", "--output", "json")
		var resp struct {
			Items []string `json:"items"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Contains(t, resp.Items, e2eVolumeNamespace)
	})

	t.Run("SchemeAloneListsNamespaces", func(t *testing.T) {
		// What a ref built by concatenation degrades to, and it names no
		// namespace, so it lists them all.
		out := mustCLI(t, "volume", "ls", "bdn:", "--output", "json")
		var resp struct {
			Items []string `json:"items"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Contains(t, resp.Items, e2eVolumeNamespace)
	})

	t.Run("NamespaceListsOurVolume", func(t *testing.T) {
		out := mustCLI(t, "volume", "ls", "bdn:"+e2eVolumeNamespace, "--output", "json")
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

	t.Run("StatVolumeReportsHead", func(t *testing.T) {
		out := mustCLI(t, "volume", "stat", v.ref, "--output", "json")
		var resp struct {
			Namespace  string `json:"namespace"`
			Name       string `json:"name"`
			VersionRef string `json:"version_ref"`
			Head       *struct {
				Digest         string `json:"digest"`
				TotalSizeBytes int64  `json:"total_size_bytes"`
			} `json:"head"`
			VersionsAlive int `json:"versions_alive"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, e2eVolumeNamespace, resp.Namespace)
		require.Equal(t, v.name, resp.Name)
		// A volume names no version, so its own ref is what was passed in.
		require.Equal(t, v.ref, resp.VersionRef)
		require.NotNil(t, resp.Head)
		require.Equal(t, v.digest, resp.Head.Digest)
		require.Equal(t, v.bytes, resp.Head.TotalSizeBytes)
		require.Equal(t, 1, resp.VersionsAlive)
	})

	t.Run("VersionsHasTheOneVersion", func(t *testing.T) {
		out := mustCLI(t, "volume", "versions", v.ref, "--output", "json")
		var resp struct {
			Versions []struct {
				Digest     string   `json:"digest"`
				VersionRef string   `json:"version_ref"`
				Lifecycle  string   `json:"lifecycle"`
				IsHead     bool     `json:"is_head"`
				Tags       []string `json:"tags"`
			} `json:"versions"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Len(t, resp.Versions, 1)
		version := resp.Versions[0]
		require.Equal(t, v.digest, version.Digest)
		// The ref this row carries is the one push reported and the one the
		// CLI renders, character for character.
		require.Equal(t, v.versionRef, version.VersionRef)
		require.Equal(t, "ALIVE", version.Lifecycle)
		require.True(t, version.IsHead)
		require.Contains(t, version.Tags, v.tag)
	})

	// Every selector form addresses the same one version, so all of them
	// answer with the digest the push published, and with the same ref. A bare
	// volume ref is absent on purpose: that names the volume, which the
	// sub-test above covers.
	for _, selector := range v.versionSelectors() {
		t.Run("StatVersionBy"+selector, func(t *testing.T) {
			out := mustCLI(t, "volume", "stat", v.ref+selector, "--output", "json")
			var resp struct {
				Digest     string `json:"digest"`
				VersionRef string `json:"version_ref"`
				EntryCount *int   `json:"entry_count"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &resp))
			require.Equal(t, v.digest, resp.Digest)
			// Whichever selector went in, the ref that comes back is the
			// pinned one, spelled as the CLI spells it.
			require.Equal(t, v.versionRef, resp.VersionRef)
			if resp.EntryCount != nil {
				// Directories are entries too, and the push records one for
				// each, so this exceeds the file count.
				require.Greater(t, *resp.EntryCount, len(e2eVolumeFiles))
			}
		})
	}
}

// Entries covers what a listing reads straight from the volume service rather
// than from the REST API: the manifest of one version, and one file's bytes.
func (v *volumeLifecycle) Entries(t *testing.T) {
	require.NotEmpty(t, v.digest, "Push did not record a digest")

	t.Run("ChildrenOfTheRoot", func(t *testing.T) {
		out := mustCLI(t, "volume", "ls", v.ref, "--output", "json")
		list := parseVolumeEntryList(t, out)
		// Pinned to the digest head resolved to, so the same listing can be
		// read again even after head moves.
		require.Equal(t, v.versionRef, list.VersionRef)
		require.Equal(t, map[string]string{
			"/nested":         "directory",
			"/tokenizer.json": "file",
			"/weights.bin":    "file",
		}, volumeEntryKinds(list))
	})

	// Every way of naming the same version lists the same tree, which is the
	// resolution the volume service does rather than anything local.
	for _, selector := range v.versionSelectors() {
		t.Run("ChildrenOfTheVersionBy"+selector, func(t *testing.T) {
			out := mustCLI(t, "volume", "ls", v.ref+selector, "--output", "json")
			list := parseVolumeEntryList(t, out)
			require.Equal(t, v.versionRef, list.VersionRef)
			require.Equal(t, map[string]string{
				"/nested":         "directory",
				"/tokenizer.json": "file",
				"/weights.bin":    "file",
			}, volumeEntryKinds(list))
		})
	}

	t.Run("ChildrenOfASubdirectory", func(t *testing.T) {
		out := mustCLI(t, "volume", "ls", v.ref+"/nested", "--output", "json")
		require.Equal(t, map[string]string{
			"/nested/config.json": "file",
			"/nested/notes":       "directory",
		}, volumeEntryKinds(parseVolumeEntryList(t, out)))
	})

	t.Run("TrailingSlashAndSelectorTogether", func(t *testing.T) {
		// A trailing slash is accepted and means nothing, and a path composes
		// with a selector.
		out := mustCLI(t, "volume", "ls", v.ref+":"+v.tag+"/nested/", "--output", "json")
		require.Equal(t, map[string]string{
			"/nested/config.json": "file",
			"/nested/notes":       "directory",
		}, volumeEntryKinds(parseVolumeEntryList(t, out)))
	})

	t.Run("RecursiveListsEveryFile", func(t *testing.T) {
		out := mustCLI(t, "volume", "ls", "--recursive", v.ref, "--output", "json")
		kinds := volumeEntryKinds(parseVolumeEntryList(t, out))
		for path := range e2eVolumeFiles {
			require.Equal(t, "file", kinds["/"+path])
		}
		require.Equal(t, "directory", kinds["/nested/notes"])
	})

	t.Run("RecursiveNarrowsToASubdirectory", func(t *testing.T) {
		out := mustCLI(t, "volume", "ls", "-R", v.ref+"/nested", "--output", "json")
		require.Equal(t, map[string]string{
			"/nested/config.json":  "file",
			"/nested/notes":        "directory",
			"/nested/notes/why.md": "file",
		}, volumeEntryKinds(parseVolumeEntryList(t, out)))
	})

	t.Run("TextListingIsATable", func(t *testing.T) {
		out := mustCLI(t, "volume", "ls", v.ref)
		require.Contains(t, out, "MODE")
		// Names are relative to what was listed, and a directory carries the
		// trailing slash rather than a size.
		require.Regexp(t, `nested/\s+directory\s+rwx`, out)
		require.Regexp(t, `weights.bin\s+file\s+rw-r--r--`, out)
	})

	t.Run("StatOneFile", func(t *testing.T) {
		out := mustCLI(t, "volume", "stat", v.ref+"/nested/notes/why.md", "--output", "json")
		var resp struct {
			VersionRef string `json:"version_ref"`
			Path       string `json:"path"`
			Kind       string `json:"kind"`
			SizeBytes  int64  `json:"size_bytes"`
			Mode       string `json:"mode"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, v.versionRef, resp.VersionRef)
		require.Equal(t, "/nested/notes/why.md", resp.Path)
		require.Equal(t, "file", resp.Kind)
		require.Equal(t, int64(len(e2eVolumeFiles["nested/notes/why.md"])), resp.SizeBytes)
		require.Equal(t, "0644", resp.Mode)
	})

	t.Run("StatANamespace", func(t *testing.T) {
		_, stderr, err := cli(t, "volume", "stat", "bdn:"+e2eVolumeNamespace)
		require.Error(t, err)
		require.Contains(t, stderr, "nothing to describe")
	})

	t.Run("StatAMissingPath", func(t *testing.T) {
		_, stderr, err := cli(t, "volume", "stat", v.ref+"/nope.json")
		require.Error(t, err)
		require.Contains(t, stderr, "no entry at /nope.json")
	})

	// Every addressing form of the same file, since a selector and a path
	// compose and the path is what cannery does not parse itself. The bare
	// volume is in the list as well, which the version selectors are not.
	for _, selector := range append([]string{""}, v.versionSelectors()...) {
		t.Run("CatOneFileBy"+selector, func(t *testing.T) {
			// Exactly the bytes and nothing else, which is what makes it
			// pipeable.
			out := mustCLI(t, "volume", "cat", v.ref+selector+"/nested/config.json")
			require.Equal(t, e2eVolumeFiles["nested/config.json"], out)
		})
	}

	t.Run("CatADirectory", func(t *testing.T) {
		stdout, stderr, err := cli(t, "volume", "cat", v.ref+"/nested")
		require.Error(t, err)
		require.Contains(t, stderr, "only a file has bytes to write")
		require.Empty(t, stdout)
	})

	t.Run("CatAMissingPath", func(t *testing.T) {
		_, _, err := cli(t, "volume", "cat", v.ref+"/nope.json")
		require.Error(t, err)
	})
}

func (v *volumeLifecycle) Pull(t *testing.T) {
	require.NotEmpty(t, v.digest, "Push did not record a digest")

	t.Run("Whole", func(t *testing.T) {
		destDir := filepath.Join(t.TempDir(), "pull")
		// The ref exactly as it was reported, fed straight back in, which is
		// what a caller pasting one out of a response does.
		out := mustCLI(t, "volume", "pull", v.versionRef, destDir, "--output", "json")
		var result volumePullResult
		require.NoError(t, json.Unmarshal([]byte(out), &result))

		require.Equal(t, v.versionRef, result.VersionRef)
		require.Equal(t, destDir, result.DestDir)
		require.Equal(t, v.files, result.Files)
		require.Equal(t, v.bytes, result.Bytes)
		require.Equal(t, result.TotalFiles, result.SelectedFiles)
		require.Empty(t, result.Warnings)

		// What is actually on disk, which is the only thing that proves the
		// transfer rather than the counts it reported.
		require.Equal(t, e2eVolumeFiles, readVolumeTree(t, destDir))
	})

	t.Run("Include", func(t *testing.T) {
		destDir := filepath.Join(t.TempDir(), "pull")
		// One exact file and one directory whose contents are wanted, so both
		// forms of --include are exercised in the same pull.
		out := mustCLI(t, "volume", "pull", v.ref, destDir,
			"--include", "tokenizer.json", "--include", "nested/notes", "--output", "json")
		var result volumePullResult
		require.NoError(t, json.Unmarshal([]byte(out), &result))

		want := map[string]string{
			"tokenizer.json":      e2eVolumeFiles["tokenizer.json"],
			"nested/notes/why.md": e2eVolumeFiles["nested/notes/why.md"],
		}
		require.Equal(t, int64(len(want)), result.SelectedFiles)
		require.Equal(t, v.files, result.TotalFiles)
		require.Equal(t, want, readVolumeTree(t, destDir))
	})

	t.Run("SubtreeStripped", func(t *testing.T) {
		destDir := filepath.Join(t.TempDir(), "pull")
		// The ref's own path narrows the pull, and --strip-prefix drops it, so
		// the subtree lands at the destination root.
		mustCLI(t, "volume", "pull", v.ref+"/nested/notes", destDir, "--strip-prefix")
		require.Equal(t, map[string]string{
			"why.md": e2eVolumeFiles["nested/notes/why.md"],
		}, readVolumeTree(t, destDir))
	})

	t.Run("ByShorthandDigest", func(t *testing.T) {
		destDir := filepath.Join(t.TempDir(), "pull")
		// A digest written the short way, which is the form a shorthand
		// rendering prints and the only one a pull has not been given yet.
		out := mustCLI(t, "volume", "pull", v.ref+"@"+v.untaggedDigest()[:12], destDir,
			"--include", "tokenizer.json", "--output", "json")
		var result volumePullResult
		require.NoError(t, json.Unmarshal([]byte(out), &result))
		// Whichever way it was addressed, the version it resolved to is
		// reported whole.
		require.Equal(t, v.versionRef, result.VersionRef)
		require.Equal(t, map[string]string{
			"tokenizer.json": e2eVolumeFiles["tokenizer.json"],
		}, readVolumeTree(t, destDir))
	})
}

func (v *volumeLifecycle) Repush(t *testing.T) {
	require.NotEmpty(t, v.digest, "Push did not record a digest")

	// The same tree from the same directory, so the source URI the library
	// derives is the same too and this is the identical version rather than a
	// new one that happens to hold the same files. The tag is written on the
	// ref rather than passed to --tag, which is the other way to ask for one.
	out := mustCLI(t, "volume", "push", v.sourceDir, v.ref+":"+v.tag+"-again", "--output", "json")
	var result volumePushResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))

	require.Equal(t, v.versionRef, result.VersionRef)
	require.Equal(t, []string{v.tag + "-again"}, result.TagsApplied)
	// Nothing had to move, because head already pointed here.
	require.False(t, result.HeadUpdated)
	// The volume already holds every chunk, so none is uploaded as new: they
	// are either recognized locally against the previous version or offered
	// and found to be present.
	require.Zero(t, result.ChunksUnique)
	require.Positive(t, result.ChunksReused+result.ChunksExisting)

	// Still one version, since a repush of an unchanged tree publishes no new one.
	list := mustCLI(t, "volume", "versions", v.ref, "--output", "json")
	var versions struct {
		Versions []struct {
			Digest string `json:"digest"`
		} `json:"versions"`
	}
	require.NoError(t, json.Unmarshal([]byte(list), &versions))
	require.Len(t, versions.Versions, 1)
}

func (v *volumeLifecycle) Delete(t *testing.T) {
	require.NotEmpty(t, v.digest, "Push did not record a digest")

	// A mutating route resolves a selector the way a read does, so each round
	// runs against both spellings: the ref the reads reported, and the short
	// bare digest a shorthand rendering prints. Each round ends where it
	// started, which is what lets the next one run and leaves one live version
	// for the whole-volume delete below.
	for _, addressed := range []struct{ name, ref string }{
		{name: "AsReported", ref: v.versionRef},
		{name: "ByShorthandDigest", ref: v.ref + "@" + v.untaggedDigest()[:12]},
	} {
		t.Run("Version"+addressed.name, func(t *testing.T) {
			var result struct {
				Digest      string `json:"digest"`
				VersionRef  string `json:"version_ref"`
				Lifecycle   string `json:"lifecycle"`
				DeleteAfter string `json:"delete_after"`
			}
			out := mustCLI(t, "volume", "rm", "--yes", addressed.ref, "--output", "json")
			require.NoError(t, json.Unmarshal([]byte(out), &result))
			require.Equal(t, v.digest, result.Digest)
			// However it was addressed, what comes back names the version whole.
			require.Equal(t, v.versionRef, result.VersionRef)
			require.Equal(t, "TOMBSTONED", result.Lifecycle)
			require.NotEmpty(t, result.DeleteAfter)

			t.Run("TombstonedIsHiddenUnlessAsked", func(t *testing.T) {
				type versionList struct {
					Versions []struct {
						Digest    string `json:"digest"`
						Lifecycle string `json:"lifecycle"`
					} `json:"versions"`
				}
				var hidden versionList
				require.NoError(t, json.Unmarshal(
					[]byte(mustCLI(t, "volume", "versions", v.ref, "--output", "json")), &hidden))
				require.Empty(t, hidden.Versions)

				var shown versionList
				require.NoError(t, json.Unmarshal(
					[]byte(mustCLI(t, "volume", "versions", v.ref, "--include-tombstoned", "--output", "json")),
					&shown))
				require.Len(t, shown.Versions, 1)
				require.Equal(t, "TOMBSTONED", shown.Versions[0].Lifecycle)
			})

			t.Run("Restore", func(t *testing.T) {
				out := mustCLI(t, "volume", "restore", addressed.ref, "--output", "json")
				require.NoError(t, json.Unmarshal([]byte(out), &result))
				require.Equal(t, v.digest, result.Digest)
				require.Equal(t, v.versionRef, result.VersionRef)
				require.Equal(t, "ALIVE", result.Lifecycle)
			})
		})
	}

	t.Run("WholeVolume", func(t *testing.T) {
		out := mustCLI(t, "volume", "rm", "--recursive", "--yes", v.ref, "--output", "json")
		var result struct {
			VersionsDeleted int `json:"versions_deleted"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &result))
		require.Equal(t, 1, result.VersionsDeleted)
	})
}

// parseVolumeEntryList decodes a `volume ls` entry listing.
func parseVolumeEntryList(t *testing.T, out string) volumeEntryList {
	t.Helper()
	var list volumeEntryList
	require.NoError(t, json.Unmarshal([]byte(out), &list))
	return list
}

// volumeEntryKinds reduces a listing to path-to-kind, which is what a listing
// assertion is about; sizes and modes are asserted where one entry is read.
func volumeEntryKinds(list volumeEntryList) map[string]string {
	kinds := make(map[string]string, len(list.Items))
	for _, item := range list.Items {
		kinds[item.Path] = item.Kind
	}
	return kinds
}

// expectedVolumeBytes totals the source tree, which is what the push reports
// and the pull writes back.
func expectedVolumeBytes() int64 {
	var total int64
	for _, contents := range e2eVolumeFiles {
		total += int64(len(contents))
	}
	return total
}

// readVolumeTree reads every file under dir into a slash-keyed map, so a
// pulled tree can be compared against the one that was pushed.
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
