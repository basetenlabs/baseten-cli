package cmd_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/basetenlabs/baseten-go/client"
	"github.com/klauspost/compress/zstd"
)

func volumePayload() map[string]any {
	return map[string]any{
		"namespace":           "weights",
		"name":                "llama",
		"version_ref":         "bdn://weights/llama",
		"sequence":            12,
		"updated_at":          "2026-01-02T03:04:05Z",
		"tag_count":           2,
		"tags":                []any{map[string]any{"name": "prod", "digest": "b3:aaa"}},
		"head":                map[string]any{"digest": "b3:aaa", "total_size_bytes": 2048, "created_at": "2026-01-01T00:00:00Z"},
		"versions_alive":      3,
		"versions_tombstoned": 1,
		"versions_untagged":   0,
	}
}

func volumeVersionPayload() map[string]any {
	return map[string]any{
		"namespace":        "weights",
		"volume":           "llama",
		"version_ref":      "bdn://weights/llama@b3:aaa",
		"digest":           "b3:aaa",
		"sequence":         12,
		"total_size_bytes": 2048,
		"lifecycle":        "ALIVE",
		"is_head":          true,
		"tags":             []any{"prod"},
		"created_at":       "2026-01-02T03:04:05Z",
	}
}

func Test_Volume_Namespace_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes/namespaces", 200, map[string]any{
		"items":      []any{"weights", "datasets"},
		"pagination": map[string]any{"has_more": false, "cursor": nil},
	})

	h.Require.NoError(h.Execute("volume", "namespace", "list"))
	out := h.Stdout.String()
	h.Require.Contains(out, "NAMESPACE")
	h.Require.Contains(out, "weights")
	h.Require.Contains(out, "datasets")
}

func Test_Volume_Namespace_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes/namespaces", 200, map[string]any{
		"items":      []any{},
		"pagination": map[string]any{"has_more": false, "cursor": nil},
	})

	h.Require.NoError(h.Execute("volume", "namespace", "list"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No volume namespaces found.")
}

func Test_Volume_Namespace_List_AggregatesPages(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	// First request has no cursor, second carries the one the first returned.
	m.SetRouteFunc("GET", "/v1/volumes/namespaces", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"items":["weights"],"pagination":{"has_more":true,"cursor":"c1"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":["datasets"],"pagination":{"has_more":false,"cursor":null}}`))
	})

	h.Require.NoError(h.Execute("volume", "namespace", "list", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), "weights")
	h.Require.Contains(h.Stdout.String(), "datasets")
	h.Require.Len(m.Calls(), 2)
}

func Test_Volume_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes", 200, map[string]any{
		"items":      []any{volumePayload()},
		"pagination": map[string]any{"has_more": false, "cursor": nil},
	})

	h.Require.NoError(h.Execute("volume", "list", "--namespace", "weights"))
	out := h.Stdout.String()
	h.Require.Contains(out, "NAME")
	h.Require.Contains(out, "HEAD SIZE")
	h.Require.Contains(out, "llama")
	h.Require.Contains(out, "2.0 KiB")
	// tag_count exceeds the returned tags, so the partial list says so.
	h.Require.Contains(out, "prod (1 of 2)")
}

func Test_Volume_List_SendsNamespace(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/volumes", 200, map[string]any{
		"items":      []any{},
		"pagination": map[string]any{"has_more": false, "cursor": nil},
	})

	h.Require.NoError(h.Execute("volume", "list", "--namespace", "weights"))
	call := m.FindCall("GET", "/v1/volumes")
	h.Require.NotNil(call)
	h.Require.Contains(call.RawQuery, "namespace=weights")
}

func Test_Volume_Describe_ByRef(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes/weights/llama", 200, volumePayload())

	h.Require.NoError(h.Execute("volume", "describe", "--ref", "weights/llama"))
	out := h.Stdout.String()
	h.Require.Contains(out, "bdn://weights/llama")
	h.Require.Contains(out, "3 alive, 1 tombstoned, 0 untagged")
	h.Require.Contains(out, "Head size:   2.0 KiB")
}

func Test_Volume_Describe_ByNamespaceAndVolume(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes/weights/llama", 200, volumePayload())

	h.Require.NoError(h.Execute("volume", "describe", "--namespace", "weights", "--volume", "llama"))
	h.Require.Contains(h.Stdout.String(), "bdn://weights/llama")
}

func Test_Volume_Describe_RefAcceptsScheme(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/volumes/weights/llama", 200, volumePayload())

	h.Require.NoError(h.Execute("volume", "describe", "--ref", "bdn://weights/llama"))
	h.Require.NotNil(m.FindCall("GET", "/v1/volumes/weights/llama"))
}

func Test_Volume_Describe_RefRejectsVersionSelector(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "describe", "--ref", "weights/llama:prod")
	h.Require.ErrorContains(err, "selects a version")
}

func Test_Volume_Describe_RefRejectsNamespaceFlag(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "describe", "--ref", "weights/llama", "--namespace", "weights")
	h.Require.ErrorContains(err, "cannot be combined")
}

func Test_Volume_Describe_RequiresVolumeWithNamespace(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "describe", "--namespace", "weights")
	h.Require.ErrorContains(err, "--namespace and --volume")
}

func Test_Volume_Describe_RejectsMalformedRef(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "describe", "--ref", "llama")
	h.Require.ErrorContains(err, "invalid --ref")
}

func Test_Volume_Version_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes/weights/llama/versions", 200, map[string]any{
		"versions": []any{volumeVersionPayload()},
	})

	h.Require.NoError(h.Execute("volume", "version", "list", "--ref", "weights/llama"))
	out := h.Stdout.String()
	h.Require.Contains(out, "SEQUENCE")
	h.Require.Contains(out, "LIFECYCLE")
	h.Require.Contains(out, "b3:aaa")
	h.Require.Contains(out, "ALIVE")
}

func Test_Volume_Version_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/volumes/weights/llama/versions", 200, map[string]any{
		"versions": []any{},
	})

	h.Require.NoError(h.Execute("volume", "version", "list", "--ref", "weights/llama"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No volume versions found.")
}

func Test_Volume_Version_Describe_DefaultsToHead(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/volumes/weights/llama/versions/head", 200, volumeVersionPayload())

	h.Require.NoError(h.Execute("volume", "version", "describe", "--ref", "weights/llama"))
	h.Require.NotNil(m.FindCall("GET", "/v1/volumes/weights/llama/versions/head"))
	h.Require.Contains(h.Stdout.String(), "bdn://weights/llama@b3:aaa")
}

func Test_Volume_Version_Describe_Tag(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/volumes/weights/llama/versions/:prod", 200, volumeVersionPayload())

	h.Require.NoError(h.Execute("volume", "version", "describe", "--ref", "weights/llama", "--tag", "prod"))
	h.Require.NotNil(m.FindCall("GET", "/v1/volumes/weights/llama/versions/:prod"))
}

func Test_Volume_Version_Describe_Digest(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/volumes/weights/llama/versions/@b3:aaa", 200, volumeVersionPayload())

	h.Require.NoError(h.Execute("volume", "version", "describe",
		"--namespace", "weights", "--volume", "llama", "--digest", "b3:aaa"))
	h.Require.NotNil(m.FindCall("GET", "/v1/volumes/weights/llama/versions/@b3:aaa"))
}

func Test_Volume_Version_Describe_InlineSelector(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/volumes/weights/llama/versions/@b3:aaa", 200, volumeVersionPayload())

	h.Require.NoError(h.Execute("volume", "version", "describe", "--ref", "bdn://weights/llama@b3:aaa"))
	h.Require.NotNil(m.FindCall("GET", "/v1/volumes/weights/llama/versions/@b3:aaa"))
}

func Test_Volume_Version_Describe_TagAndDigestConflict(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "version", "describe", "--ref", "weights/llama",
		"--tag", "prod", "--digest", "b3:aaa")
	h.Require.ErrorContains(err, "mutually exclusive")
}

func Test_Volume_Version_Describe_InlineSelectorAndTagConflict(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("volume", "version", "describe", "--ref", "weights/llama:staging", "--tag", "prod")
	h.Require.ErrorContains(err, "already selects a version")
}

// volumeFakeTransfer stands in for the management client's volume transfers.
// It captures the options a command built from its flags, hands back a canned
// result, and lets a test drive the progress, store, and hasher the command
// passed in. Nothing here knows how the volume service moves bytes.
type volumeFakeTransfer struct {
	pushOptions     client.PushVolumeOptions
	downloadOptions client.DownloadVolumeOptions

	pushResult     *client.PushVolumeResult
	downloadResult *client.DownloadVolumeResult
	err            error

	// drive runs against the seams the command supplied, before the result is
	// returned, so what the command passed in is exercised rather than only
	// asserted non-nil.
	drive func(progress func(client.VolumeProgress), store client.VolumeObjectStore, hasher func() hash.Hash) error
}

func (f *volumeFakeTransfer) PushVolume(
	_ context.Context, opts client.PushVolumeOptions,
) (*client.PushVolumeResult, error) {
	f.pushOptions = opts
	if f.drive != nil {
		if err := f.drive(opts.Progress, opts.Store, opts.Hasher); err != nil {
			return nil, err
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.pushResult != nil {
		return f.pushResult, nil
	}
	return &client.PushVolumeResult{ManifestDigest: volumeTestDigest}, nil
}

func (f *volumeFakeTransfer) DownloadVolume(
	_ context.Context, opts client.DownloadVolumeOptions,
) (*client.DownloadVolumeResult, error) {
	f.downloadOptions = opts
	if f.drive != nil {
		if err := f.drive(opts.Progress, opts.Store, opts.Hasher); err != nil {
			return nil, err
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.downloadResult != nil {
		return f.downloadResult, nil
	}
	return &client.DownloadVolumeResult{
		VersionRef:     "bdn://weights/llama@" + volumeTestDigest,
		ManifestDigest: volumeTestDigest,
	}, nil
}

const volumeTestDigest = "b3:" + "aa"

// newVolumeTransferHarness wires a harness whose volume transfers land on the
// returned fake.
func newVolumeTransferHarness(t *testing.T) (*CommandHarness, *volumeFakeTransfer) {
	h := NewCommandHarness(t)
	fake := &volumeFakeTransfer{}
	h.Context = cmd.WithVolumeTransfer(h.Context, fake)
	return h, fake
}

func Test_Volume_Push_PassesFlagsToTheTransfer(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	dir := t.TempDir()

	h.Require.NoError(h.Execute("volume", "push", "--ref", "weights/llama", "--dir", dir,
		"--tag", "prod", "--tag", "candidate", "--source-uri", "hf://org/model@abc",
		"--file-jobs", "4", "--chunk-operations", "8", "--max-in-flight-mib", "16"))

	opts := fake.pushOptions
	h.Require.Equal("weights", opts.Namespace)
	h.Require.Equal("llama", opts.Volume)
	h.Require.Equal(dir, opts.SourceDir)
	h.Require.Equal([]string{"prod", "candidate"}, opts.Tags)
	h.Require.Equal("hf://org/model@abc", opts.SourceURI)
	h.Require.Equal(4, opts.Concurrency.FileJobs)
	h.Require.Equal(8, opts.Concurrency.ChunkOperations)
	h.Require.Equal(int64(16*1024*1024), opts.Concurrency.MaxBytesInFlight)
	// Reading the previous version is what makes the push skip content the
	// volume already holds, so it is always supplied.
	h.Require.NotNil(opts.Store)
	h.Require.NotNil(opts.Hasher)
	h.Require.NotNil(opts.Progress)
}

func Test_Volume_Push_UnsetTransferFlagsStayZero(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)

	h.Require.NoError(h.Execute("volume", "push", "--ref", "weights/llama", "--dir", t.TempDir()))

	// Each limit defines its own meaning for zero, so none is defaulted here.
	h.Require.Zero(fake.pushOptions.Concurrency.FileJobs)
	h.Require.Zero(fake.pushOptions.Concurrency.ChunkOperations)
	h.Require.Zero(fake.pushOptions.Concurrency.MaxBytesInFlight)
	h.Require.Empty(fake.pushOptions.SourceURI)
}

func Test_Volume_Push_Summary(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	fake.pushResult = &client.PushVolumeResult{
		ManifestDigest: volumeTestDigest,
		Sequence:       9,
		HeadUpdated:    true,
		TagsApplied:    []string{"prod"},
		Files:          3,
		Bytes:          2048,
		Chunks:         10,
		Unique:         4,
		Reused:         5,
		Existing:       1,
	}

	h.Require.NoError(h.Execute("volume", "push", "--ref", "weights/llama", "--dir", t.TempDir()))
	out := h.Stdout.String()
	h.Require.Contains(out, "bdn://weights/llama@"+volumeTestDigest)
	h.Require.Contains(out, "3 files, 2.0 KiB")
	h.Require.Contains(out, "4 of 10 chunks")
	h.Require.Contains(out, "prod")
	h.Require.Contains(h.Stderr.String(), "Pushing")
}

func Test_Volume_Push_JSON(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	fake.pushResult = &client.PushVolumeResult{
		ManifestDigest: volumeTestDigest,
		Sequence:       9,
		TagsApplied:    []string{"prod"},
		Files:          3,
		Bytes:          2048,
		Chunks:         10,
		Unique:         4,
		Reused:         5,
		Existing:       1,
	}

	h.Require.NoError(h.Execute("volume", "push", "--ref", "weights/llama",
		"--dir", t.TempDir(), "--output", "json"))
	var got map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal("bdn://weights/llama@"+volumeTestDigest, got["version_ref"])
	h.Require.Equal(float64(9), got["sequence"])
	h.Require.Equal(false, got["head_updated"])
	h.Require.Equal(float64(10), got["chunks"])
	h.Require.Equal(float64(4), got["chunks_unique"])
	h.Require.Equal(float64(5), got["chunks_reused"])
	h.Require.Equal(float64(1), got["chunks_existing"])
}

func Test_Volume_Push_ReportsDeniedHeadMove(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	fake.pushResult = &client.PushVolumeResult{
		ManifestDigest: volumeTestDigest,
		HeadMoveDenied: true,
	}

	h.Require.NoError(h.Execute("volume", "push", "--ref", "weights/llama", "--dir", t.TempDir()))
	// The version was published, so this is a warning rather than a failure.
	h.Require.Contains(h.Stderr.String(), "could not move head")
}

func Test_Volume_Push_WrapsTransferFailure(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	fake.err = errors.New("upload session expired")

	err := h.Execute("volume", "push", "--ref", "weights/llama", "--dir", t.TempDir())
	h.Require.ErrorContains(err, "pushing volume weights/llama")
	h.Require.ErrorContains(err, "upload session expired")
}

func Test_Volume_Push_RequiresDir(t *testing.T) {
	h, _ := newVolumeTransferHarness(t)
	err := h.Execute("volume", "push", "--ref", "weights/llama")
	h.Require.ErrorContains(err, `required flag(s) "dir"`)
}

func Test_Volume_Push_RefRejectsVersionSelector(t *testing.T) {
	h, _ := newVolumeTransferHarness(t)
	err := h.Execute("volume", "push", "--ref", "weights/llama:prod", "--dir", t.TempDir())
	h.Require.ErrorContains(err, "selects a version")
}

func Test_Volume_Push_HasherIsUnkeyedBlake3(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	var digest string
	fake.drive = func(_ func(client.VolumeProgress), _ client.VolumeObjectStore, hasher func() hash.Hash) error {
		digest = hex.EncodeToString(hasher().Sum(nil))
		return nil
	}

	h.Require.NoError(h.Execute("volume", "push", "--ref", "weights/llama", "--dir", t.TempDir()))
	// The published BLAKE3 vector for the empty input, at the 32-byte output
	// length the content addressing is defined in terms of. A 64-byte
	// extended output is the mistake this catches.
	h.Require.Equal("af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262", digest)
}

func Test_Volume_Push_ProgressReportsPhasesAndThrottlesWithin(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	// A clock the reports step through: the second upload update lands inside
	// the throttle window, the third past it.
	start := time.Now()
	offsets := []time.Duration{0, 0, time.Millisecond, 5 * time.Second}
	var tick int
	h.Context = cmd.WithNow(h.Context, func() time.Time {
		now := start.Add(offsets[min(tick, len(offsets)-1)])
		tick++
		return now
	})
	fake.drive = func(progress func(client.VolumeProgress), _ client.VolumeObjectStore, _ func() hash.Hash) error {
		progress(client.VolumeProgress{Phase: client.VolumePhaseScan})
		progress(client.VolumeProgress{Phase: client.VolumePhaseUpload, Files: 1, TotalFiles: 2, Bytes: 512, TotalBytes: 1024})
		progress(client.VolumeProgress{Phase: client.VolumePhaseUpload, Files: 2, TotalFiles: 2, Bytes: 1024, TotalBytes: 1024})
		return nil
	}

	h.Require.NoError(h.Execute("volume", "push", "--ref", "weights/llama", "--dir", t.TempDir()))
	logs := h.Stderr.String()
	// A phase change always prints, whatever the clock says.
	h.Require.Contains(logs, "scan...")
	h.Require.Contains(logs, "upload: 1/2 files, 512 B/1.0 KiB")
	// Same phase inside the window, so the intermediate report is dropped.
	h.Require.NotContains(logs, "upload: 2/2 files")
}

func Test_Volume_Version_Download_PassesFlagsToTheTransfer(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	dir := t.TempDir()

	h.Require.NoError(h.Execute("volume", "version", "download", "--ref", "weights/llama",
		"--tag", "prod", "--out-dir", dir, "--overwrite",
		"--include", "tokenizer.json", "--include", "weights/",
		"--chunk-operations", "8", "--max-in-flight-mib", "16"))

	opts := fake.downloadOptions
	h.Require.Equal("bdn://weights/llama:prod", opts.Ref)
	h.Require.Equal(dir, opts.DestDir)
	h.Require.True(opts.Overwrite)
	h.Require.False(opts.Restart)
	h.Require.Equal([]string{"tokenizer.json", "weights/"}, opts.Include)
	h.Require.Equal(8, opts.Concurrency.ChunkOperations)
	h.Require.Equal(int64(16*1024*1024), opts.Concurrency.MaxBytesInFlight)
	// The store is the only way a download reads bytes.
	h.Require.NotNil(opts.Store)
	h.Require.NotNil(opts.Hasher)
}

func Test_Volume_Version_Download_SelectorFormsBecomeOneRef(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		ref  string
	}{
		// No selector is the head lookup, which a ref naming no version
		// already is, so `head` adds nothing to the ref.
		{"head", []string{"--ref", "weights/llama"}, "bdn://weights/llama"},
		{"tag", []string{"--ref", "weights/llama", "--tag", "prod"}, "bdn://weights/llama:prod"},
		{"digest", []string{"--ref", "weights/llama", "--digest", "b3:aaa"}, "bdn://weights/llama@b3:aaa"},
		{"inline tag", []string{"--ref", "bdn://weights/llama:prod"}, "bdn://weights/llama:prod"},
		{"inline digest", []string{"--ref", "weights/llama@b3:aaa"}, "bdn://weights/llama@b3:aaa"},
		{"parts", []string{"--namespace", "weights", "--volume", "llama"}, "bdn://weights/llama"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, fake := newVolumeTransferHarness(t)
			args := append([]string{"volume", "version", "download"}, tc.args...)
			h.Require.NoError(h.Execute(append(args, "--out-dir", t.TempDir())...))
			h.Require.Equal(tc.ref, fake.downloadOptions.Ref)
		})
	}
}

func Test_Volume_Version_Download_Summary(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	dir := t.TempDir()
	fake.downloadResult = &client.DownloadVolumeResult{
		VersionRef:     "bdn://weights/llama@" + volumeTestDigest,
		ManifestDigest: volumeTestDigest,
		Files:          2,
		Bytes:          1024,
		SelectedFiles:  2,
		TotalFiles:     7,
	}

	h.Require.NoError(h.Execute("volume", "version", "download", "--ref", "weights/llama", "--out-dir", dir))
	out := h.Stdout.String()
	h.Require.Contains(out, "bdn://weights/llama@"+volumeTestDigest)
	h.Require.Contains(out, dir)
	h.Require.Contains(out, "2 files, 1.0 KiB")
	// Reported only when --include narrowed the download.
	h.Require.Contains(out, "2 of 7 files")
}

func Test_Volume_Version_Download_JSONCarriesWarnings(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	fake.downloadResult = &client.DownloadVolumeResult{
		VersionRef:     "bdn://weights/llama@" + volumeTestDigest,
		ManifestDigest: volumeTestDigest,
		Files:          1,
		Bytes:          512,
		SelectedFiles:  1,
		TotalFiles:     1,
		Warnings: []client.VolumeWarning{{
			Path:   "link",
			Kind:   client.VolumeWarningKindDanglingLink,
			Detail: "target resolves to nothing in the volume",
		}},
	}

	h.Require.NoError(h.Execute("volume", "version", "download", "--ref", "weights/llama",
		"--out-dir", t.TempDir(), "--output", "json"))
	var got map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal([]any{"link: target resolves to nothing in the volume"}, got["warnings"])
	h.Require.Equal(float64(1), got["total_files"])
}

func Test_Volume_Version_Download_WarnsInTextMode(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	fake.downloadResult = &client.DownloadVolumeResult{
		VersionRef: "bdn://weights/llama@" + volumeTestDigest,
		Warnings: []client.VolumeWarning{{
			Path:   "link",
			Kind:   client.VolumeWarningKindDanglingLink,
			Detail: "target resolves to nothing in the volume",
		}},
	}

	h.Require.NoError(h.Execute("volume", "version", "download", "--ref", "weights/llama",
		"--out-dir", t.TempDir()))
	h.Require.Contains(h.Stderr.String(), "Warning: link: target resolves to nothing in the volume")
}

func Test_Volume_Version_Download_WrapsTransferFailure(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	fake.err = errors.New("pin not found")

	err := h.Execute("volume", "version", "download", "--ref", "weights/llama",
		"--tag", "prod", "--out-dir", t.TempDir())
	h.Require.ErrorContains(err, "downloading bdn://weights/llama:prod")
	h.Require.ErrorContains(err, "pin not found")
}

func Test_Volume_Version_Download_RequiresOutDir(t *testing.T) {
	h, _ := newVolumeTransferHarness(t)
	err := h.Execute("volume", "version", "download", "--ref", "weights/llama")
	h.Require.ErrorContains(err, `required flag(s) "out-dir"`)
}

// volumeFakeS3 satisfies transfermanager.S3APIClient via embedding; only
// GetObject is implemented, so any other call panics.
type volumeFakeS3 struct {
	transfermanager.S3APIClient

	mu      sync.Mutex
	configs []aws.Config
	buckets []string
	keys    []string
	body    string
}

func (f *volumeFakeS3) GetObject(
	_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buckets = append(f.buckets, aws.ToString(in.Bucket))
	f.keys = append(f.keys, aws.ToString(in.Key))
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(strings.NewReader(f.body)),
		ContentType:   aws.String("application/vnd.baseten.bdn.chunk"),
		ContentLength: aws.Int64(int64(len(f.body))),
	}, nil
}

func Test_Volume_Push_StoreReadsObjectsThroughS3(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	s3Fake := &volumeFakeS3{body: "chunk bytes"}
	var configs []aws.Config
	h.Context = cmd.WithS3APIClientFactory(h.Context, func(cfg aws.Config) transfermanager.S3APIClient {
		configs = append(configs, cfg)
		return s3Fake
	})

	var result *client.VolumeObjectResult
	fake.drive = func(_ func(client.VolumeProgress), store client.VolumeObjectStore, _ func() hash.Hash) error {
		req := client.VolumeObjectDownload{
			Endpoint: "https://objects.example.com",
			Region:   "us-west-2",
			Bucket:   "bdn-objects",
			Key:      "objects/b3/aa/bb/aabb",
			Credentials: client.VolumeObjectCredentials{
				AccessKeyID: "AKIA", SecretAccessKey: "secret", SessionToken: "token",
			},
			ExpectedSize: 11,
		}
		var err error
		result, err = store.DownloadObject(h.Context, req)
		if err != nil {
			return err
		}
		// A transfer reads many objects under one lease, so the client is
		// built once and reused.
		if _, err = store.DownloadObject(h.Context, req); err != nil {
			return err
		}
		// Same lease, another namespace's bucket: still one client.
		req.Bucket = "other-objects"
		_, err = store.DownloadObject(h.Context, req)
		return err
	}

	h.Require.NoError(h.Execute("volume", "push", "--ref", "weights/llama", "--dir", t.TempDir()))

	body, err := io.ReadAll(result.Body)
	h.Require.NoError(err)
	h.Require.Equal("chunk bytes", string(body))
	h.Require.Equal("application/vnd.baseten.bdn.chunk", result.ContentType)
	h.Require.Equal(int64(11), result.Size)
	h.Require.Equal([]string{"bdn-objects", "bdn-objects", "other-objects"}, s3Fake.buckets)
	h.Require.Equal([]string{"objects/b3/aa/bb/aabb"}, s3Fake.keys[:1])
	h.Require.Len(configs, 1)
	h.Require.Equal("us-west-2", configs[0].Region)
	h.Require.Equal("https://objects.example.com", aws.ToString(configs[0].BaseEndpoint))
	creds, err := configs[0].Credentials.Retrieve(h.Context)
	h.Require.NoError(err)
	h.Require.Equal("AKIA", creds.AccessKeyID)
	h.Require.Equal("token", creds.SessionToken)
}

func Test_Volume_Push_StoreRebuildsClientWhenCredentialsRotate(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	var configs []aws.Config
	h.Context = cmd.WithS3APIClientFactory(h.Context, func(cfg aws.Config) transfermanager.S3APIClient {
		configs = append(configs, cfg)
		return &volumeFakeS3{body: "chunk bytes"}
	})

	fake.drive = func(_ func(client.VolumeProgress), store client.VolumeObjectStore, _ func() hash.Hash) error {
		req := client.VolumeObjectDownload{
			Region: "us-west-2",
			Bucket: "bdn-objects",
			Key:    "objects/b3/aa/bb/aabb",
			Credentials: client.VolumeObjectCredentials{
				AccessKeyID: "AKIA", SecretAccessKey: "secret", SessionToken: "first",
			},
		}
		if _, err := store.DownloadObject(h.Context, req); err != nil {
			return err
		}
		// The volume service leases credentials per request, so a rotation
		// mid-transfer must not keep signing with the expired ones.
		req.Credentials.SessionToken = "second"
		_, err := store.DownloadObject(h.Context, req)
		return err
	}

	h.Require.NoError(h.Execute("volume", "push", "--ref", "weights/llama", "--dir", t.TempDir()))
	h.Require.Len(configs, 2)
	// No endpoint means AWS itself, where the SDK resolves it from the region.
	h.Require.Nil(configs[0].BaseEndpoint)
}

func Test_Volume_Push_StoreDecompressesZstd(t *testing.T) {
	h, fake := newVolumeTransferHarness(t)
	encoder, err := zstd.NewWriter(nil)
	h.Require.NoError(err)
	compressed := encoder.EncodeAll([]byte("the original bytes"), nil)

	var decompressed string
	fake.drive = func(_ func(client.VolumeProgress), store client.VolumeObjectStore, _ func() hash.Hash) error {
		reader, err := store.Decompressor(bytes.NewReader(compressed))
		if err != nil {
			return err
		}
		defer reader.Close()
		out, err := io.ReadAll(reader)
		decompressed = string(out)
		return err
	}

	h.Require.NoError(h.Execute("volume", "push", "--ref", "weights/llama", "--dir", t.TempDir()))
	h.Require.Equal("the original bytes", decompressed)
}
