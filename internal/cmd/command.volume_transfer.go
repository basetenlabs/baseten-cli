package cmd

import (
	"context"
	"fmt"
	"hash"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client"
	"github.com/klauspost/compress/zstd"
	"github.com/zeebo/blake3"
)

func init() {
	Register("volume cat", commandVolumeCat)
	Register("volume push", commandVolumePush)
	Register("volume pull", commandVolumePull)
}

func commandVolumeCat(ctx *CommandContext, flags *cmd.VolumeCatFlags) error {
	// A file's bytes are whatever the volume holds, so there is nothing to
	// render as JSON and nothing for --jq to read. Refused rather than
	// ignored, since the alternative is a pipe fed something that is not JSON.
	if ctx.JSON {
		return cmd.NewErrUsagef("this command writes a file's bytes, which have no JSON form")
	}
	ref, err := volumeParseRef(ctx.Args[0])
	if err != nil {
		return err
	}
	if ref.Level() != client.VolumeRefLevelPath || ref.Path == "/" {
		return cmd.NewErrUsagef(
			"ref %s names no file to write; add the path of a file within the version, "+
				"as in '%s/config.json'", ref, ref)
	}
	transfer, err := ctx.NewVolumeTransfer()
	if err != nil {
		return err
	}

	// No progress reporter: this writes bytes to stdout to be piped, so
	// stderr stays as quiet as the unix namesake.
	written := false
	_, err = transfer.PullVolume(ctx, client.PullVolumeOptions{
		Ref: ref,
		EntryHandler: func(_ context.Context, entry client.VolumePulledEntry) error {
			switch {
			// A pull is also handed the directories leading to what it
			// selected, so a destination tree can be given their recorded
			// modes. There is no destination here, so they are passed over.
			case strings.HasPrefix(ref.Path, entry.Path+"/"):
				return nil
			// What is left is the entry at the path and whatever lies under
			// it, so anything but an exact match means the path named a
			// directory. Judged on the path rather than the kind, which also
			// covers a directory no record describes.
			case entry.Path != ref.Path:
				return fmt.Errorf("%s names a directory, which holds %s", ref, entry.Path)
			case entry.Kind != client.VolumeEntryKindFile:
				return fmt.Errorf("%s is a %s, and only a file has bytes to write", ref, entry.Kind)
			case written:
				// One ref, one file: a version describing the same path twice
				// would otherwise be written out as the two concatenated.
				return fmt.Errorf("%s is described more than once by the version", ref)
			}
			written = true
			_, err := io.Copy(ctx.Stdout, entry.Reader)
			return err
		},
		Hasher: volumeHasher,
		Store:  &volumeObjectStore{ctx: ctx},
	})
	if err != nil {
		return fmt.Errorf("reading %s: %w", ref, err)
	}
	return nil
}

func commandVolumePush(ctx *CommandContext, flags *cmd.VolumePushFlags) error {
	dir := ctx.Args[0]
	ref, err := volumeParseRef(ctx.Args[1])
	if err != nil {
		return err
	}
	// A tag is the one selector a push can honor, since a push writes tags
	// rather than reading them. A digest or a path names something a new
	// version cannot be.
	if ref.Volume == "" || ref.Digest != "" || ref.Path != "" {
		return cmd.NewErrUsagef(
			"ref %s names a %s, and a push publishes a whole tree as a new version of a volume; "+
				"write 'bdn:%s/%s', optionally with a tag to apply to what it publishes",
			ref, ref.Level(), ref.Namespace, ref.Volume)
	}
	// Taken off the ref and applied like any other tag, so writing one here
	// and passing --tag as well applies both.
	tags := flags.Tags
	if ref.Tag != "" {
		tags = append(append([]string(nil), tags...), ref.Tag)
		ref.Tag = ""
	}
	transfer, err := ctx.NewVolumeTransfer()
	if err != nil {
		return err
	}

	ctx.Logf("Pushing %s to volume %s...\n", dir, ref)
	result, err := transfer.PushVolume(ctx, client.PushVolumeOptions{
		Ref:       ref,
		SourceDir: dir,
		SourceURI: flags.SourceURI,
		Tags:      tags,
		Hasher:    volumeHasher,
		// Supplied so the push can read the volume's previous version and
		// skip uploading content it already holds.
		Store:       &volumeObjectStore{ctx: ctx},
		Progress:    volumeProgressLogger(ctx),
		Concurrency: volumeConcurrency(flags.VolumeTransferFlags, flags.FileJobs),
	})
	if err != nil {
		return fmt.Errorf("pushing %s: %w", ref, err)
	}

	if result.HeadMoveDenied {
		ctx.LogLine(
			"The credential could not move head, so refs without a tag still resolve to the previous version.")
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.VolumePushResult{
			VersionRef:     result.VersionRef.String(),
			Sequence:       result.Sequence,
			HeadUpdated:    result.HeadUpdated,
			HeadMoveDenied: result.HeadMoveDenied,
			TagsApplied:    result.TagsApplied,
			Files:          result.Files,
			Bytes:          result.Bytes,
			Chunks:         result.Chunks,
			ChunksUnique:   result.Unique,
			ChunksReused:   result.Reused,
			ChunksExisting: result.Existing,
		})
		return nil
	}
	ctx.Outputf("✨ Volume %s was successfully pushed ✨\n\n", ref)
	ctx.Outputf("Ref:      %s\n", volumeRefText(result.VersionRef, flags.VolumeRefFlags))
	ctx.Outputf("Digest:   %s\n", result.VersionRef.Digest)
	ctx.Outputf("Contents: %d files, %s\n", result.Files, formatBytes(result.Bytes))
	ctx.Outputf("Uploaded: %d of %d chunks\n", result.Unique, result.Chunks)
	if len(result.TagsApplied) > 0 {
		ctx.Outputf("Tags:     %s\n", volumeJoin(result.TagsApplied))
	}
	return nil
}

func commandVolumePull(ctx *CommandContext, flags *cmd.VolumePullFlags) error {
	ref, err := volumeParseRef(ctx.Args[0])
	if err != nil {
		return err
	}
	dir := ctx.Args[1]
	if ref.Level() == client.VolumeRefLevelNamespace {
		return cmd.NewErrUsagef(
			"ref %s names a namespace, and there is no tree to download; name a volume, "+
				"a version, or a path within one", ref)
	}
	transfer, err := ctx.NewVolumeTransfer()
	if err != nil {
		return err
	}

	ctx.Logf("Downloading %s into %s...\n", ref, dir)
	result, err := transfer.PullVolume(ctx, client.PullVolumeOptions{
		Ref:          ref,
		DestDir:      dir,
		Overwrite:    flags.Overwrite,
		Include:      flags.Include,
		Restart:      flags.Restart,
		StripRefPath: flags.StripPrefix,
		Hasher:       volumeHasher,
		Store:        &volumeObjectStore{ctx: ctx},
		Progress:     volumeProgressLogger(ctx),
		Concurrency:  volumeConcurrency(flags.VolumeTransferFlags, 0),
	})
	if err != nil {
		return fmt.Errorf("downloading %s: %w", ref, err)
	}

	// Containment findings a volume published before the containment rule can
	// carry. Reported rather than swallowed, since they describe what was
	// written to disk.
	warnings := make([]string, 0, len(result.Warnings))
	for _, warning := range result.Warnings {
		warnings = append(warnings, warning.String())
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.VolumePullResult{
			VersionRef:    result.VersionRef.String(),
			DestDir:       dir,
			Files:         result.Files,
			Bytes:         result.Bytes,
			SelectedFiles: result.SelectedFiles,
			TotalFiles:    result.TotalFiles,
			ChunksFetched: result.ChunksFetched,
			ChunksReused:  result.ChunksReused,
			Warnings:      warnings,
		})
		return nil
	}
	for _, warning := range warnings {
		ctx.Logf("Warning: %s\n", warning)
	}
	ctx.Outputf("✨ Volume version was successfully downloaded ✨\n\n")
	ctx.Outputf("Ref:         %s\n", volumeRefText(result.VersionRef, flags.VolumeRefFlags))
	ctx.Outputf("Digest:      %s\n", result.VersionRef.Digest)
	ctx.Outputf("Destination: %s\n", dir)
	ctx.Outputf("Written:     %d files, %s\n", result.Files, formatBytes(result.Bytes))
	if result.SelectedFiles != result.TotalFiles {
		ctx.Outputf("Selected:    %d of %d files\n", result.SelectedFiles, result.TotalFiles)
	}
	return nil
}

// volumeHasher is the hash the content addressing is defined in terms of: an
// unkeyed BLAKE3 with a 32-byte digest. Checked against the published test
// vectors before a transfer starts, so a wrong one fails rather than
// producing a volume nothing else can read.
func volumeHasher() hash.Hash {
	return blake3.New()
}

// volumeConcurrency translates the transfer flags. Zero stays zero: each
// limit has its own meaning for it, documented on the options, and none of
// them is "no limit".
func volumeConcurrency(flags cmd.VolumeTransferFlags, fileJobs int) client.VolumeConcurrencyOptions {
	return client.VolumeConcurrencyOptions{
		FileJobs:         fileJobs,
		ChunkOperations:  flags.ChunkOperations,
		MaxBytesInFlight: int64(flags.MaxInFlightMiB) * 1024 * 1024,
	}
}

// volumeProgressInterval is how often a transfer's progress is reprinted
// within one phase. Every callback would be one line per file.
const volumeProgressInterval = 2 * time.Second

// volumeProgressLogger reports a transfer's progress to stderr, on each phase
// change and periodically within a phase.
func volumeProgressLogger(ctx *CommandContext) func(client.VolumeProgress) {
	var phase client.VolumePhase
	var last time.Time
	return func(p client.VolumeProgress) {
		now := ctx.Now()
		if p.Phase == phase && now.Sub(last) < volumeProgressInterval {
			return
		}
		phase, last = p.Phase, now
		switch {
		case p.TotalBytes > 0:
			ctx.Logf("  %s: %d/%d files, %s/%s\n", p.Phase, p.Files, p.TotalFiles,
				formatBytes(p.Bytes), formatBytes(p.TotalBytes))
		case p.TotalFiles > 0:
			ctx.Logf("  %s: %d/%d files\n", p.Phase, p.Files, p.TotalFiles)
		default:
			ctx.Logf("  %s...\n", p.Phase)
		}
	}
}

// volumeObjectStore reads a volume's stored objects, which is how a download
// gets its bytes, how a manifest read gets its document, and how a push reads
// the version it is deduplicating against. The volume service leases the
// credentials per request, so the S3 client is rebuilt whenever they change
// and cached in between: one transfer reads many objects under one lease.
type volumeObjectStore struct {
	ctx *CommandContext

	mu     sync.Mutex
	key    string
	client transfermanager.S3APIClient
}

func (s *volumeObjectStore) DownloadObject(
	ctx context.Context, req client.VolumeObjectDownload,
) (*client.VolumeObjectResult, error) {
	out, err := s.clientFor(req).GetObject(ctx, &s3.GetObjectInput{
		Bucket: &req.Bucket,
		Key:    &req.Key,
	})
	if err != nil {
		return nil, fmt.Errorf("reading object %s: %w", req.Key, err)
	}
	return &client.VolumeObjectResult{
		Body:        out.Body,
		ContentType: aws.ToString(out.ContentType),
		Size:        aws.ToInt64(out.ContentLength),
	}, nil
}

func (s *volumeObjectStore) Decompressor(r io.Reader) (io.ReadCloser, error) {
	decoder, err := zstd.NewReader(r)
	if err != nil {
		return nil, err
	}
	return decoder.IOReadCloser(), nil
}

func (s *volumeObjectStore) clientFor(req client.VolumeObjectDownload) transfermanager.S3APIClient {
	// Keyed on what the client is built from. The bucket is not part of it:
	// it rides on each request, and one namespace's objects can span buckets.
	key := strings.Join([]string{
		req.Endpoint, req.Region, req.Credentials.AccessKeyID, req.Credentials.SessionToken,
	}, "\x00")

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil && s.key == key {
		return s.client
	}
	cfg := aws.Config{
		Region: req.Region,
		Credentials: awscreds.NewStaticCredentialsProvider(
			req.Credentials.AccessKeyID, req.Credentials.SecretAccessKey, req.Credentials.SessionToken),
	}
	// Empty for AWS itself, where the SDK resolves the endpoint from the
	// region, and a base URL for anything else.
	if req.Endpoint != "" {
		cfg.BaseEndpoint = &req.Endpoint
	}
	s.key, s.client = key, s.ctx.newS3APIClient(cfg)
	return s.client
}
