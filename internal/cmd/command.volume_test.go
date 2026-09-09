package cmd_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/basetenlabs/baseten-go/client"
)

// volumeTestTime is what every fixture records, so a test asserting on output
// never has to compute a timestamp.
var volumeTestTime = time.Date(2026, 9, 8, 17, 4, 5, 0, time.UTC)

// volumeVersionPayload is one version as the REST API renders it.
var volumeVersionPayload = map[string]any{
	"namespace": "weights", "volume": "llama", "digest": "b3:aaa",
	"version_ref": "bdn:weights/llama@aaa", "sequence": 9, "entry_count": 3,
	"total_size_bytes": 2048, "lifecycle": "ALIVE", "is_head": true,
	"tags": []string{"prod"}, "created_at": volumeTestTime, "volume_sequence": 9,
}

// fakeVolumeTransfer stands in for the volume service. It records the options
// each command built from its flags and answers from a manifest the test
// supplies, narrowed the way a real read narrows it, so a listing is judged on
// what the service would actually have returned.
type fakeVolumeTransfer struct {
	t *testing.T

	// Entries is the whole version, before any narrowing, and Contents holds
	// the bytes of whichever entries a test means to read.
	Entries  []client.VolumeEntry
	Contents map[string]string
	Digest   string

	PushResult *client.PushVolumeResult
	PullResult *client.PullVolumeResult

	PushOptions     *client.PushVolumeOptions
	PullOptions     *client.PullVolumeOptions
	ManifestOptions *client.FetchVolumeManifestOptions
}

func (f *fakeVolumeTransfer) PushVolume(
	_ context.Context, opts client.PushVolumeOptions,
) (*client.PushVolumeResult, error) {
	f.requireSeams(opts.Hasher, opts.Store)
	f.PushOptions = &opts
	result := f.PushResult
	if result == nil {
		result = &client.PushVolumeResult{VersionRef: f.versionRef(opts.Ref)}
	}
	return result, nil
}

func (f *fakeVolumeTransfer) PullVolume(
	ctx context.Context, opts client.PullVolumeOptions,
) (*client.PullVolumeResult, error) {
	f.requireSeams(opts.Hasher, opts.Store)
	f.PullOptions = &opts
	if opts.EntryHandler != nil {
		// A real pull is handed the directories leading to what it selected,
		// as well as the selection itself, so the destination tree can be
		// given their recorded modes.
		delivered := f.selected(opts.Ref, nil)
		for _, entry := range f.Entries {
			if strings.HasPrefix(opts.Ref.Path, entry.Path+"/") {
				delivered = append([]client.VolumeEntry{entry}, delivered...)
			}
		}
		for _, entry := range delivered {
			pulled := client.VolumePulledEntry{
				VolumeEntry: entry,
				Reader:      strings.NewReader(f.Contents[entry.Path]),
			}
			if err := opts.EntryHandler(ctx, pulled); err != nil {
				return nil, err
			}
		}
	}
	result := f.PullResult
	if result == nil {
		result = &client.PullVolumeResult{VersionRef: f.versionRef(opts.Ref)}
	}
	return result, nil
}

func (f *fakeVolumeTransfer) FetchVolumeManifest(
	ctx context.Context, opts client.FetchVolumeManifestOptions,
) (*client.VolumeManifest, error) {
	f.requireSeams(opts.Hasher, opts.Store)
	f.ManifestOptions = &opts
	entries := f.selected(opts.Ref, func(entry client.VolumeEntry) bool {
		return opts.EntryFilter == nil || opts.EntryFilter(ctx, entry)
	})
	return &client.VolumeManifest{
		VersionRef: f.versionRef(opts.Ref),
		EntryCount: int64(len(f.Entries)),
		Entries:    entries,
	}, nil
}

// requireSeams fails the test when a command omitted the hasher or the object
// store, which a real call needs and would reject the options without.
func (f *fakeVolumeTransfer) requireSeams(hasher any, store client.VolumeObjectStore) {
	f.t.Helper()
	if hasher == nil {
		f.t.Error("options carry no Hasher")
	}
	if store == nil {
		f.t.Error("options carry no Store")
	}
}

// versionRef pins the addressed volume to a digest, as every real result does.
func (f *fakeVolumeTransfer) versionRef(ref client.VolumeRef) client.VolumeRef {
	digest := f.Digest
	if digest == "" {
		digest = "b3:a1b2c3d4e5f6"
	}
	return client.VolumeRef{Namespace: ref.Namespace, Volume: ref.Volume, Digest: digest}
}

// selected narrows the version to the entries at or under the ref's path and
// then applies the caller's filter, which is the order and the conjunction the
// real read uses.
func (f *fakeVolumeTransfer) selected(
	ref client.VolumeRef, keep func(client.VolumeEntry) bool,
) []client.VolumeEntry {
	prefix := strings.TrimSuffix(ref.Path, "/")
	entries := make([]client.VolumeEntry, 0, len(f.Entries))
	for _, entry := range f.Entries {
		under := prefix == "" ||
			entry.Path == prefix ||
			strings.HasPrefix(entry.Path, prefix+"/")
		if under && (keep == nil || keep(entry)) {
			entries = append(entries, entry)
		}
	}
	return entries
}

// withVolumeTransfer installs a fake answering from a version with a subtree,
// a symlink, a setuid file, and a directory no record describes, which is
// every shape a listing has to render. Entries are in canonical path order, as
// a real read returns them.
func withVolumeTransfer(t *testing.T, h *CommandHarness) *fakeVolumeTransfer {
	fake := &fakeVolumeTransfer{t: t, Entries: []client.VolumeEntry{
		{Path: "/config", Kind: client.VolumeEntryKindDirectory, Mode: 0o755, ModTime: volumeTestTime},
		{Path: "/config/current", Kind: client.VolumeEntryKindSymlink, Mode: 0o777,
			ModTime: volumeTestTime, LinkTarget: "model.json"},
		{Path: "/config/legacy/old.json", Kind: client.VolumeEntryKindFile, Size: 12,
			Mode: 0o644, ModTime: volumeTestTime},
		{Path: "/config/model.json", Kind: client.VolumeEntryKindFile, Size: 4211,
			Mode: 0o644, ModTime: volumeTestTime},
		{Path: "/config/run.sh", Kind: client.VolumeEntryKindFile, Size: 91,
			Mode: 0o4755, ModTime: volumeTestTime},
		{Path: "/config/tokenizer", Kind: client.VolumeEntryKindDirectory, Mode: 0o755,
			ModTime: volumeTestTime},
		{Path: "/config/tokenizer/vocab.json", Kind: client.VolumeEntryKindFile, Size: 1_912_314,
			Mode: 0o644, ModTime: volumeTestTime},
	}}
	h.Context = cmd.WithVolumeTransfer(h.Context, fake)
	return fake
}

func Test_Volume_Ls_Namespaces(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	// Two pages, to pin that the cursor is walked rather than surfaced.
	m.SetRouteFunc("GET", "/v1/volumes/namespaces", func(w http.ResponseWriter, r *http.Request) {
		page := map[string]any{
			"items":      []string{"weights"},
			"pagination": map[string]any{"has_more": false, "cursor": nil},
		}
		if r.URL.Query().Get("cursor") == "" {
			page = map[string]any{
				"items":      []string{"cli-e2e"},
				"pagination": map[string]any{"has_more": true, "cursor": "next"},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	})

	h.Require.NoError(h.Execute("volume", "ls"))
	out := h.Stdout.String()
	h.Require.Contains(out, "NAMESPACE")
	h.Require.Contains(out, "cli-e2e")
	h.Require.Contains(out, "weights")
}

func Test_Volume_Ls_Namespaces_SchemeOnly(t *testing.T) {
	// A ref with nothing after the scheme names no namespace to list, so it
	// lists them all, which is what a ref built by concatenation produces.
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes/namespaces", 200, map[string]any{
		"items":      []string{"weights"},
		"pagination": map[string]any{"has_more": false},
	})

	h.Require.NoError(h.Execute("volume", "ls", "bdn:"))
	h.Require.Contains(h.Stdout.String(), "weights")
}

func Test_Volume_Ls_Namespaces_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes/namespaces", 200, map[string]any{
		"items":      []string{},
		"pagination": map[string]any{"has_more": false},
	})

	h.Require.NoError(h.Execute("volume", "ls"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No volume namespaces found.")
}

func Test_Volume_Ls_Volumes(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/volumes", 200, map[string]any{
		"items": []any{map[string]any{
			"name": "llama", "namespace": "weights", "sequence": 7,
			"updated_at": volumeTestTime, "version_ref": "bdn:weights/llama",
			"versions_alive": 4, "versions_tombstoned": 1, "versions_untagged": 0,
			// One readable tag out of three, so the partial count is rendered.
			"tag_count": 3,
			"tags":      []any{map[string]any{"name": "prod", "digest": "b3:aaa"}},
			"head":      map[string]any{"digest": "b3:aaa", "total_size_bytes": 2048, "created_at": volumeTestTime},
		}},
		"pagination": map[string]any{"has_more": false},
	})

	h.Require.NoError(h.Execute("volume", "ls", "bdn:weights"))
	out := h.Stdout.String()
	h.Require.Contains(out, "HEAD SIZE")
	h.Require.Contains(out, "llama")
	h.Require.Contains(out, "prod (1 of 3)")
	h.Require.Contains(out, "2.0 KiB")
	h.Require.Equal("weights", m.FindCall("GET", "/v1/volumes").Query().Get("namespace"))
}

func Test_Volume_Ls_Entries_Children(t *testing.T) {
	h := NewCommandHarness(t)
	fake := withVolumeTransfer(t, h)

	h.Require.NoError(h.Execute("volume", "ls", "bdn:weights/llama/config"))
	out := h.Stdout.String()
	// Immediate children only, with the names relative to the listed
	// directory and the directory itself absent.
	h.Require.Contains(out, "model.json")
	h.Require.Contains(out, "tokenizer/")
	h.Require.NotContains(out, "vocab.json")
	// A directory no record describes, derived from the path beneath it, so
	// it carries neither a mode nor a modification time.
	h.Require.Contains(out, "legacy/")
	h.Require.Regexp(`legacy/\s+directory\s+-\s+-\s+-`, out)
	// The mode is spelled the way ls spells it, setuid included.
	h.Require.Contains(out, "rwsr-xr-x")
	h.Require.Equal("/config", fake.ManifestOptions.Ref.Path)
}

func Test_Volume_Ls_Entries_Recursive(t *testing.T) {
	h := NewCommandHarness(t)
	withVolumeTransfer(t, h)

	h.Require.NoError(h.Execute("volume", "ls", "--recursive", "bdn:weights/llama/config"))
	out := h.Stdout.String()
	h.Require.Contains(out, "tokenizer/vocab.json")
	h.Require.Contains(out, "legacy/old.json")
	h.Require.Contains(out, "1.8 MiB")
}

func Test_Volume_Ls_Entries_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	withVolumeTransfer(t, h)

	h.Require.NoError(h.Execute("volume", "ls", "bdn:weights/llama/config", "--output", "json"))
	out := h.Stdout.String()
	// Pinned to the digest the read resolved to, and paths are the version's
	// own, so appending one to the version ref names the entry.
	h.Require.Contains(out, `"version_ref": "bdn:weights/llama@b3:a1b2c3d4e5f6"`)
	h.Require.Contains(out, `"path": "/config/model.json"`)
	h.Require.Contains(out, `"mode": "0644"`)
	// The implied directory carries no mode, which is what says it is implied.
	h.Require.Contains(out, `"path": "/config/legacy"`)
}

func Test_Volume_Ls_Entries_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	fake := withVolumeTransfer(t, h)
	fake.Entries = nil

	h.Require.NoError(h.Execute("volume", "ls", "bdn:weights/llama"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No entries found.")
}

func Test_Volume_Ls_MalformedRef(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "ls", "weights/llama")
	h.Require.ErrorContains(err, `want a ref beginning "bdn:"`)
}

func Test_Volume_Ls_UntaggedDigest(t *testing.T) {
	// A digest has one spelling, and it is the one every response carries, so
	// the shorter form is refused rather than quietly accepted.
	h := NewCommandHarness(t)
	err := h.Execute("volume", "ls", "bdn:weights/llama@a1b2c3d4e5f6")
	h.Require.ErrorContains(err, `digest "a1b2c3d4e5f6" must begin "b3:"`)
}

func Test_Volume_Ls_LegacyScheme(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "ls", "bdn://weights/llama")
	h.Require.ErrorContains(err, "bdn:// is not supported")
}

func Test_Volume_Stat_Namespace(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "stat", "bdn:weights")
	h.Require.ErrorContains(err, "names a namespace, which has nothing to describe")
}

func Test_Volume_Stat_Volume(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes/weights/llama", 200, map[string]any{
		"name": "llama", "namespace": "weights", "sequence": 7,
		"updated_at": volumeTestTime, "version_ref": "bdn:weights/llama",
		"versions_alive": 4, "versions_tombstoned": 1, "versions_untagged": 2,
		"tag_count": 1, "tags": []any{map[string]any{"name": "prod", "digest": "b3:aaa"}},
		"head": map[string]any{"digest": "b3:aaa", "total_size_bytes": 2048, "created_at": volumeTestTime},
	})

	h.Require.NoError(h.Execute("volume", "stat", "bdn:weights/llama"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Ref:         bdn:weights/llama")
	h.Require.Contains(out, "4 alive, 1 tombstoned, 2 untagged")
	h.Require.Contains(out, "Head size:   2.0 KiB")
}

func Test_Volume_Stat_Version_Tag(t *testing.T) {
	h := NewCommandHarness(t)
	// The tag rides in the path segment as the selector syntax upstream takes.
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes/weights/llama/versions/:prod", 200,
		volumeVersionPayload)

	h.Require.NoError(h.Execute("volume", "stat", "bdn:weights/llama:prod"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Digest:           b3:aaa")
	h.Require.Contains(out, "Entries:          3")
	h.Require.Contains(out, "Volume sequence:  9")
}

func Test_Volume_Stat_Version_Digest(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes/weights/llama/versions/@b3:aabbccddeeff", 200,
		volumeVersionPayload)

	h.Require.NoError(h.Execute("volume", "stat", "bdn:weights/llama@b3:aabbccddeeff"))
	h.Require.Contains(h.Stdout.String(), "Lifecycle:        ALIVE")
}

func Test_Volume_Stat_Version_NoSelector(t *testing.T) {
	// A volume ref with no selector describes the volume, so the version
	// route is only reached through the reserved head lookup, which is what a
	// tagless version ref cannot be. Covered here via a digest-free path.
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes/weights/llama/versions/:head", 200,
		volumeVersionPayload)

	h.Require.NoError(h.Execute("volume", "stat", "bdn:weights/llama:head"))
	h.Require.Contains(h.Stdout.String(), "Digest:           b3:aaa")
}

func Test_Volume_Stat_Entry_File(t *testing.T) {
	h := NewCommandHarness(t)
	withVolumeTransfer(t, h)

	h.Require.NoError(h.Execute("volume", "stat", "bdn:weights/llama/config/run.sh"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Ref:         bdn:weights/llama@b3:a1b2c3d4e5f6/config/run.sh")
	h.Require.Contains(out, "Kind:        file")
	h.Require.Contains(out, "Size:        91 B")
	h.Require.Contains(out, "Mode:        rwsr-xr-x (4755)")
}

func Test_Volume_Stat_Entry_ImpliedDirectory(t *testing.T) {
	// Nothing describes /config/legacy, but a file lives under it, so the
	// path is a directory the version implies rather than records.
	h := NewCommandHarness(t)
	withVolumeTransfer(t, h)

	h.Require.NoError(h.Execute("volume", "stat", "bdn:weights/llama/config/legacy"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Kind:        directory")
	h.Require.NotContains(out, "Mode:")
}

func Test_Volume_Stat_Entry_Missing(t *testing.T) {
	h := NewCommandHarness(t)
	withVolumeTransfer(t, h)

	err := h.Execute("volume", "stat", "bdn:weights/llama/nope.json")
	h.Require.ErrorContains(err, "has no entry at /nope.json")
}

func Test_Volume_Versions_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/volumes/weights/llama/versions", 200, map[string]any{
		"volume_sequence": 9,
		"versions": []any{
			map[string]any{
				"namespace": "weights", "volume": "llama", "digest": "b3:aaa",
				"version_ref": "bdn:weights/llama@aaa", "sequence": 9,
				"total_size_bytes": 2048, "lifecycle": "ALIVE", "is_head": true,
				"tags": []string{"prod"}, "created_at": volumeTestTime,
			},
			map[string]any{
				"namespace": "weights", "volume": "llama", "digest": "b3:bbb",
				"version_ref": "bdn:weights/llama@bbb", "sequence": nil,
				"total_size_bytes": nil, "lifecycle": "TOMBSTONED", "is_head": false,
				"tags": []string{}, "created_at": volumeTestTime,
			},
		},
	})

	h.Require.NoError(h.Execute("volume", "versions", "bdn:weights/llama"))
	out := h.Stdout.String()
	h.Require.Contains(out, "LIFECYCLE")
	h.Require.Contains(out, "TOMBSTONED")
	h.Require.Contains(out, "prod")
	// A version committed before the service recorded a sequence or a size
	// renders as absent rather than as zero.
	h.Require.Regexp(`-\s+b3:bbb\s+-\s+TOMBSTONED`, out)
	h.Require.Equal("false",
		m.FindCall("GET", "/v1/volumes/weights/llama/versions").Query().Get("include_tombstoned"))
}

func Test_Volume_Versions_IncludeTombstoned(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/volumes/weights/llama/versions", 200, map[string]any{
		"volume_sequence": 9, "versions": []any{},
	})

	h.Require.NoError(h.Execute("volume", "versions", "bdn:weights/llama", "--include-tombstoned"))
	h.Require.Equal("true",
		m.FindCall("GET", "/v1/volumes/weights/llama/versions").Query().Get("include_tombstoned"))
	h.Require.Contains(h.Stderr.String(), "No volume versions found.")
}

func Test_Volume_Versions_WrongLevel(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "versions", "bdn:weights/llama:prod")
	h.Require.ErrorContains(err, "names one point in a volume's history rather than the volume")
}

func Test_Volume_Rm_Version(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("DELETE", "/v1/volumes/weights/llama/versions/@b3:aabbccddeeff", 200, map[string]any{
		"namespace": "weights", "volume": "llama", "digest": "b3:aabbccddeeff",
		"version_ref": "bdn:weights/llama@b3:aabbccddeeff", "lifecycle": "TOMBSTONED",
		"delete_after": volumeTestTime, "volume_sequence": 10,
	})

	h.Require.NoError(h.Execute("volume", "rm", "--yes", "bdn:weights/llama@b3:aabbccddeeff"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Lifecycle:        TOMBSTONED")
	h.Require.Contains(out, "Restorable until: 2026-09-08T17:04:05Z")
	// No compare-and-swap unless one is asked for, which no flag does.
	h.Require.Equal("{}",
		strings.TrimSpace(m.FindCall("DELETE", "/v1/volumes/weights/llama/versions/@b3:aabbccddeeff").Body))
}

func Test_Volume_Rm_Volume_Recursive(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("DELETE", "/v1/volumes/weights/llama", 200, map[string]any{
		"namespace": "weights", "name": "llama", "versions_deleted": 4, "volume_sequence": 11,
	})

	h.Require.NoError(h.Execute("volume", "rm", "--recursive", "--yes", "bdn:weights/llama"))
	h.Require.Contains(h.Stdout.String(), "Versions deleted: 4")
}

func Test_Volume_Rm_Volume_RequiresRecursive(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "rm", "--yes", "bdn:weights/llama")
	h.Require.ErrorContains(err, "pass --recursive")
}

func Test_Volume_Rm_Tag(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "rm", "--yes", "bdn:weights/llama:prod")
	h.Require.ErrorContains(err, "name that version by digest")
}

func Test_Volume_Rm_Path(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "rm", "--yes", "bdn:weights/llama/config/model.json")
	h.Require.ErrorContains(err, "a version's contents are immutable")
}

func Test_Volume_Rm_RequiresYesOffTerminal(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "rm", "bdn:weights/llama@b3:aabbccddeeff")
	h.Require.ErrorContains(err, "pass --yes")
}

func Test_Volume_Restore_Version(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute(
		"POST", "/v1/volumes/weights/llama/versions/@b3:aabbccddeeff/restore", 200, map[string]any{
			"namespace": "weights", "volume": "llama", "digest": "b3:aabbccddeeff",
			"version_ref": "bdn:weights/llama@b3:aabbccddeeff", "lifecycle": "ALIVE",
			"volume_sequence": 12,
		})

	h.Require.NoError(h.Execute("volume", "restore", "bdn:weights/llama@b3:aabbccddeeff"))
	h.Require.Contains(h.Stdout.String(), "Lifecycle: ALIVE")
}

func Test_Volume_Restore_RequiresDigest(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "restore", "bdn:weights/llama:prod")
	h.Require.ErrorContains(err, "does not name a deleted version")
}

func Test_Volume_Cat_File(t *testing.T) {
	h := NewCommandHarness(t)
	fake := withVolumeTransfer(t, h)
	fake.Contents = map[string]string{"/config/model.json": `{"hidden_size":4096}`}

	h.Require.NoError(h.Execute("volume", "cat", "bdn:weights/llama/config/model.json"))
	// Exactly the bytes, with nothing added, so it can be redirected.
	h.Require.Equal(`{"hidden_size":4096}`, h.Stdout.String())
	// A handler replaces the destination directory, which is what makes this
	// a pipe rather than a download.
	h.Require.Empty(fake.PullOptions.DestDir)
}

func Test_Volume_Cat_Directory(t *testing.T) {
	h := NewCommandHarness(t)
	withVolumeTransfer(t, h)

	err := h.Execute("volume", "cat", "bdn:weights/llama/config/tokenizer")
	h.Require.ErrorContains(err, "is a directory, and only a file has bytes to write")
	h.Require.Empty(h.Stdout.String())
}

func Test_Volume_Cat_ImpliedDirectory(t *testing.T) {
	// Nothing describes /config/legacy, so the first entry delivered is the
	// file beneath it, which the path check catches rather than writing.
	h := NewCommandHarness(t)
	withVolumeTransfer(t, h)

	err := h.Execute("volume", "cat", "bdn:weights/llama/config/legacy")
	h.Require.ErrorContains(err, "names a directory, which holds /config/legacy/old.json")
}

func Test_Volume_Cat_NoPath(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "cat", "bdn:weights/llama")
	h.Require.ErrorContains(err, "names no file to write")
}

func Test_Volume_Cat_RejectsJSON(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "cat", "bdn:weights/llama/config/model.json", "--output", "json")
	h.Require.ErrorContains(err, "writes a file's bytes, which have no JSON form")
}

func Test_Volume_Push(t *testing.T) {
	h := NewCommandHarness(t)
	fake := withVolumeTransfer(t, h)
	fake.PushResult = &client.PushVolumeResult{
		VersionRef: client.VolumeRef{
			Namespace: "weights", Volume: "llama", Digest: "b3:a1b2c3d4e5f6",
		},
		Sequence: 13, HeadUpdated: true, TagsApplied: []string{"prod"},
		Files: 3, Bytes: 4096, Chunks: 9, Unique: 4, Reused: 3, Existing: 2,
	}
	dir := t.TempDir()

	h.Require.NoError(h.Execute("volume", "push", dir, "bdn:weights/llama",
		"--tag", "prod", "--source-uri", "hf://meta/llama@main",
		"--file-jobs", "4", "--chunk-operations", "8", "--max-in-flight-mib", "512"))

	opts := fake.PushOptions
	h.Require.Equal(dir, opts.SourceDir)
	h.Require.Equal("bdn:weights/llama", opts.Ref.String())
	h.Require.Equal([]string{"prod"}, opts.Tags)
	h.Require.Equal("hf://meta/llama@main", opts.SourceURI)
	h.Require.Equal(4, opts.Concurrency.FileJobs)
	h.Require.Equal(8, opts.Concurrency.ChunkOperations)
	h.Require.Equal(int64(512*1024*1024), opts.Concurrency.MaxBytesInFlight)

	out := h.Stdout.String()
	h.Require.Contains(out, "Version:  bdn:weights/llama@b3:a1b2c3d4e5f6")
	h.Require.Contains(out, "Contents: 3 files, 4.0 KiB")
	h.Require.Contains(out, "Uploaded: 4 of 9 chunks")
	h.Require.Contains(out, "Tags:     prod")
}

func Test_Volume_Push_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	fake := withVolumeTransfer(t, h)
	fake.PushResult = &client.PushVolumeResult{
		VersionRef: client.VolumeRef{Namespace: "weights", Volume: "llama", Digest: "b3:a1b2"},
		Chunks:     9, Unique: 4, Reused: 3, Existing: 2, HeadMoveDenied: true,
	}

	h.Require.NoError(h.Execute("volume", "push", t.TempDir(), "bdn:weights/llama",
		"--output", "json"))
	out := h.Stdout.String()
	// The four-way chunk partition, rather than a lossy uploaded/reused split.
	h.Require.Contains(out, `"chunks": 9`)
	h.Require.Contains(out, `"chunks_unique": 4`)
	h.Require.Contains(out, `"chunks_reused": 3`)
	h.Require.Contains(out, `"chunks_existing": 2`)
	h.Require.Contains(out, `"head_move_denied": true`)
	h.Require.Contains(h.Stderr.String(), "could not move head")
}

func Test_Volume_Push_RefSelectsAVersion(t *testing.T) {
	h := NewCommandHarness(t)
	withVolumeTransfer(t, h)

	err := h.Execute("volume", "push", t.TempDir(), "bdn:weights/llama:prod")
	h.Require.ErrorContains(err, "apply tags with --tag")
}

func Test_Volume_Pull(t *testing.T) {
	h := NewCommandHarness(t)
	fake := withVolumeTransfer(t, h)
	fake.PullResult = &client.PullVolumeResult{
		VersionRef: client.VolumeRef{Namespace: "weights", Volume: "llama", Digest: "b3:a1b2"},
		Files:      2, Bytes: 2048, SelectedFiles: 2, TotalFiles: 7,
		ChunksFetched: 5, ChunksReused: 1,
	}
	dir := t.TempDir()

	h.Require.NoError(h.Execute("volume", "pull", "bdn:weights/llama:prod/config", dir,
		"--strip-prefix", "--include", "tokenizer", "--overwrite"))

	opts := fake.PullOptions
	h.Require.Equal(dir, opts.DestDir)
	h.Require.Equal("/config", opts.Ref.Path)
	h.Require.Equal("prod", opts.Ref.Tag)
	h.Require.True(opts.StripRefPath)
	h.Require.True(opts.Overwrite)
	h.Require.Equal([]string{"tokenizer"}, opts.Include)

	out := h.Stdout.String()
	h.Require.Contains(out, "Written:     2 files, 2.0 KiB")
	h.Require.Contains(out, "Selected:    2 of 7 files")
}

func Test_Volume_Pull_Namespace(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "pull", "bdn:weights", t.TempDir())
	h.Require.ErrorContains(err, "names a namespace, and there is no tree to download")
}

func Test_Volume_Pull_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	fake := withVolumeTransfer(t, h)
	fake.PullResult = &client.PullVolumeResult{
		VersionRef: client.VolumeRef{Namespace: "weights", Volume: "llama", Digest: "b3:a1b2"},
		Files:      2, Bytes: 2048, SelectedFiles: 2, TotalFiles: 2,
		Warnings: []client.VolumeWarning{{
			Path: "/config/current", Kind: client.VolumeWarningKindDanglingLink, Detail: "points outside",
		}},
	}
	dir := t.TempDir()

	h.Require.NoError(h.Execute("volume", "pull", "bdn:weights/llama", dir, "--output", "json"))
	out := h.Stdout.String()
	h.Require.Contains(out, `"version_ref": "bdn:weights/llama@b3:a1b2"`)
	h.Require.Contains(out, fmt.Sprintf(`"dest_dir": %q`, dir))
	// Containment findings describe what was written, so they are reported
	// rather than swallowed.
	h.Require.Contains(out, "/config/current")
}
