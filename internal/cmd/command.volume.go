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
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/klauspost/compress/zstd"
	"github.com/zeebo/blake3"
)

func init() {
	Register("volume namespace list", commandVolumeNamespaceList)
	Register("volume list", commandVolumeList)
	Register("volume describe", commandVolumeDescribe)
	Register("volume push", commandVolumePush)
	Register("volume version list", commandVolumeVersionList)
	Register("volume version describe", commandVolumeVersionDescribe)
	Register("volume version download", commandVolumeVersionDownload)
}

func commandVolumeNamespaceList(ctx *CommandContext, flags *cmd.VolumeNamespaceListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	// Walk every page and aggregate rather than exposing cursors, matching
	// `org user list`.
	var items []string
	var params managementapi.GetV1VolumesNamespacesParams
	for {
		resp, err := cl.API().GetVolumesNamespaces(ctx, params)
		if err != nil {
			return fmt.Errorf("listing volume namespaces: %w", err)
		}
		items = append(items, resp.Items...)
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			break
		}
		params.Cursor = resp.Pagination.Cursor
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.VolumeNamespaceList{Items: items})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No volume namespaces found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, ns := range items {
		rows = append(rows, []string{ns})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"NAMESPACE"},
		Rows:    rows,
	})
	return nil
}

func commandVolumeList(ctx *CommandContext, flags *cmd.VolumeListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	var items []managementapi.Volume
	params := managementapi.GetV1VolumesParams{Namespace: flags.Namespace}
	for {
		resp, err := cl.API().GetVolumes(ctx, params)
		if err != nil {
			return fmt.Errorf("listing volumes in namespace %s: %w", flags.Namespace, err)
		}
		items = append(items, resp.Items...)
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			break
		}
		params.Cursor = resp.Pagination.Cursor
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.VolumeList{Items: items})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No volumes found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, v := range items {
		headSize := "-"
		if v.Head != nil {
			headSize = formatBytes(int64(v.Head.TotalSizeBytes))
		}
		rows = append(rows, []string{
			v.Name,
			volumeTagNames(v.Tags, v.TagCount),
			headSize,
			fmt.Sprint(v.VersionsAlive),
			v.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"NAME", "TAGS", "HEAD SIZE", "VERSIONS", "UPDATED"},
		Rows:                rows,
		RightAlignedColumns: []int{2, 3},
	})
	return nil
}

func commandVolumeDescribe(ctx *CommandContext, flags *cmd.VolumeDescribeFlags) error {
	ref, err := resolveVolumeRef(flags.VolumeRefFlags, false)
	if err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	volume, err := cl.API().GetVolumesVolumeName(ctx, ref.Namespace, ref.Volume)
	if err != nil {
		return fmt.Errorf("describe volume %s/%s: %w", ref.Namespace, ref.Volume, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(volume)
		return nil
	}
	ctx.Outputf("Version ref: %s\n", volume.VersionRef)
	ctx.Outputf("Namespace:   %s\n", volume.Namespace)
	ctx.Outputf("Name:        %s\n", volume.Name)
	ctx.Outputf("Sequence:    %d\n", volume.Sequence)
	ctx.Outputf("Updated:     %s\n", volume.UpdatedAt.UTC().Format(time.RFC3339))
	ctx.Outputf("Versions:    %d alive, %d tombstoned, %d untagged\n",
		volume.VersionsAlive, volume.VersionsTombstoned, volume.VersionsUntagged)
	ctx.Outputf("Tags:        %s\n", volumeTagNames(volume.Tags, volume.TagCount))
	if volume.Head != nil {
		ctx.Outputf("Head digest: %s\n", volume.Head.Digest)
		ctx.Outputf("Head size:   %s\n", formatBytes(int64(volume.Head.TotalSizeBytes)))
		ctx.Outputf("Head pushed: %s\n", volume.Head.CreatedAt.UTC().Format(time.RFC3339))
	}
	return nil
}

func commandVolumePush(ctx *CommandContext, flags *cmd.VolumePushFlags) error {
	ref, err := resolveVolumeRef(flags.VolumeRefFlags, false)
	if err != nil {
		return err
	}
	transfer, err := ctx.NewVolumeTransfer()
	if err != nil {
		return err
	}

	ctx.Logf("Pushing %s to volume %s/%s...\n", flags.Dir, ref.Namespace, ref.Volume)
	result, err := transfer.PushVolume(ctx, client.PushVolumeOptions{
		Namespace: ref.Namespace,
		Volume:    ref.Volume,
		SourceDir: flags.Dir,
		SourceURI: flags.SourceURI,
		Tags:      flags.Tags,
		Hasher:    volumeHasher,
		// Supplied so the push can read the volume's previous version and
		// skip uploading content it already holds.
		Store:       &volumeObjectStore{ctx: ctx},
		Progress:    volumeProgressLogger(ctx),
		Concurrency: volumeConcurrency(flags.VolumeTransferFlags, flags.FileJobs),
	})
	if err != nil {
		return fmt.Errorf("pushing volume %s/%s: %w", ref.Namespace, ref.Volume, err)
	}

	versionRef := fmt.Sprintf("%s%s/%s@%s", volumeRefScheme, ref.Namespace, ref.Volume, result.ManifestDigest)
	if result.HeadMoveDenied {
		ctx.LogLine("The credential could not move head, so refs without a tag still resolve to the previous version.")
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.VolumePushResult{
			VersionRef:     versionRef,
			ManifestDigest: result.ManifestDigest,
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
	ctx.Outputf("✨ Volume %s/%s was successfully pushed ✨\n\n", ref.Namespace, ref.Volume)
	ctx.Outputf("Version:  %s\n", versionRef)
	ctx.Outputf("Contents: %d files, %s\n", result.Files, formatBytes(result.Bytes))
	ctx.Outputf("Uploaded: %d of %d chunks\n", result.Unique, result.Chunks)
	if len(result.TagsApplied) > 0 {
		ctx.Outputf("Tags:     %s\n", volumeJoin(result.TagsApplied))
	}
	return nil
}

func commandVolumeVersionList(ctx *CommandContext, flags *cmd.VolumeVersionListFlags) error {
	ref, err := resolveVolumeRef(flags.VolumeRefFlags, false)
	if err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	// Unpaginated upstream, so unlike the other lists there is nothing to walk.
	resp, err := cl.API().GetVolumesVersions(ctx, ref.Namespace, ref.Volume)
	if err != nil {
		return fmt.Errorf("listing versions of volume %s/%s: %w", ref.Namespace, ref.Volume, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	if len(resp.Versions) == 0 {
		ctx.LogLine("No volume versions found.")
		return nil
	}
	rows := make([][]string, 0, len(resp.Versions))
	for _, v := range resp.Versions {
		head := ""
		if v.IsHead {
			head = "yes"
		}
		rows = append(rows, []string{
			volumeOptionalInt(v.Sequence),
			v.Digest,
			volumeOptionalSize(v.TotalSizeBytes),
			v.Lifecycle,
			head,
			volumeJoin(v.Tags),
			v.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"SEQUENCE", "DIGEST", "SIZE", "LIFECYCLE", "HEAD", "TAGS", "CREATED"},
		Rows:                rows,
		RightAlignedColumns: []int{0, 2},
	})
	return nil
}

func commandVolumeVersionDescribe(ctx *CommandContext, flags *cmd.VolumeVersionDescribeFlags) error {
	ref, selector, err := resolveVolumeVersionRef(flags.VolumeVersionRefFlags)
	if err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	version, err := cl.API().GetVolumesVersionsVolumeVersion(ctx, ref.Namespace, ref.Volume, selector)
	if err != nil {
		return fmt.Errorf("describe version %s of volume %s/%s: %w",
			selector, ref.Namespace, ref.Volume, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(version)
		return nil
	}
	ctx.Outputf("Version ref: %s\n", version.VersionRef)
	ctx.Outputf("Namespace:   %s\n", version.Namespace)
	ctx.Outputf("Volume:      %s\n", version.Volume)
	ctx.Outputf("Digest:      %s\n", version.Digest)
	if version.Sequence != nil {
		ctx.Outputf("Sequence:    %d\n", *version.Sequence)
	}
	if version.TotalSizeBytes != nil {
		ctx.Outputf("Size:        %s\n", formatBytes(int64(*version.TotalSizeBytes)))
	}
	ctx.Outputf("Lifecycle:   %s\n", version.Lifecycle)
	if version.IsHead {
		ctx.OutputLine("Head:        yes")
	}
	ctx.Outputf("Tags:        %s\n", volumeJoin(version.Tags))
	ctx.Outputf("Created:     %s\n", version.CreatedAt.UTC().Format(time.RFC3339))
	return nil
}

func commandVolumeVersionDownload(ctx *CommandContext, flags *cmd.VolumeVersionDownloadFlags) error {
	ref, selector, err := resolveVolumeVersionRef(flags.VolumeVersionRefFlags)
	if err != nil {
		return err
	}
	transfer, err := ctx.NewVolumeTransfer()
	if err != nil {
		return err
	}

	downloadRef := volumeLibraryRef(ref, selector)
	ctx.Logf("Downloading %s into %s...\n", downloadRef, flags.OutDir)
	result, err := transfer.DownloadVolume(ctx, client.DownloadVolumeOptions{
		Ref:         downloadRef,
		DestDir:     flags.OutDir,
		Overwrite:   flags.Overwrite,
		Include:     flags.Include,
		Restart:     flags.Restart,
		Hasher:      volumeHasher,
		Store:       &volumeObjectStore{ctx: ctx},
		Progress:    volumeProgressLogger(ctx),
		Concurrency: volumeConcurrency(flags.VolumeTransferFlags, 0),
	})
	if err != nil {
		return fmt.Errorf("downloading %s: %w", downloadRef, err)
	}

	// Containment findings a legacy volume carries. Reported rather than
	// swallowed, since they describe what was written to disk.
	warnings := make([]string, 0, len(result.Warnings))
	for _, warning := range result.Warnings {
		warnings = append(warnings, warning.String())
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.VolumeVersionDownloadResult{
			VersionRef:     result.VersionRef,
			ManifestDigest: result.ManifestDigest,
			OutDir:         flags.OutDir,
			Files:          result.Files,
			Bytes:          result.Bytes,
			SelectedFiles:  result.SelectedFiles,
			TotalFiles:     result.TotalFiles,
			ChunksFetched:  result.ChunksFetched,
			ChunksReused:   result.ChunksReused,
			Warnings:       warnings,
		})
		return nil
	}
	for _, warning := range warnings {
		ctx.Logf("Warning: %s\n", warning)
	}
	ctx.Outputf("✨ Volume version was successfully downloaded ✨\n\n")
	ctx.Outputf("Version:     %s\n", result.VersionRef)
	ctx.Outputf("Destination: %s\n", flags.OutDir)
	ctx.Outputf("Written:     %d files, %s\n", result.Files, formatBytes(result.Bytes))
	if result.SelectedFiles != result.TotalFiles {
		ctx.Outputf("Selected:    %d of %d files\n", result.SelectedFiles, result.TotalFiles)
	}
	return nil
}

// volumeLibraryRef renders a volume and one of our version selectors as the
// `bdn://` ref the transfer takes. `head` needs no selector: a ref naming no
// version is the head lookup.
func volumeLibraryRef(ref volumeRef, selector string) string {
	base := volumeRefScheme + ref.Namespace + "/" + ref.Volume
	if selector == volumeHeadSelector {
		return base
	}
	return base + selector
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
// within one phase. Every callback would be one line per chunk.
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
// gets its bytes and how a push reads the version it is deduplicating
// against. The volume service leases the credentials per request, so the S3
// client is rebuilt whenever they change and cached in between: a transfer
// reads many objects under one lease.
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

// volumeRefScheme prefixes a full volume reference. Accepted on input and
// printed back by the API in every version_ref.
const volumeRefScheme = "bdn://"

// volumeHeadSelector addresses the version a reference with no tag or digest
// resolves to. Reserved upstream, so it needs no leading punctuation.
const volumeHeadSelector = "head"

// volumeRef is a volume named on the command line, with the version selector
// when one was supplied.
type volumeRef struct {
	Namespace string
	Volume    string
	// Selector is upstream selector syntax, ":<tag>" or "@<digest>". Empty
	// when the reference named no version.
	Selector string
}

// parseVolumeRef splits a --ref value into its parts. The digest form carries
// its own colon (`@b3:<hex>`), so an "@" is looked for before a ":".
func parseVolumeRef(ref string) (volumeRef, error) {
	rest := strings.TrimPrefix(ref, volumeRefScheme)
	var selector string
	if i := strings.Index(rest, "@"); i >= 0 {
		rest, selector = rest[:i], rest[i:]
	} else if i := strings.Index(rest, ":"); i >= 0 {
		rest, selector = rest[:i], rest[i:]
	}
	namespace, volume, found := strings.Cut(rest, "/")
	if !found || namespace == "" || volume == "" || strings.Contains(volume, "/") {
		return volumeRef{}, cmd.NewErrUsagef(
			"invalid --ref %q: expected '<namespace>/<volume>', optionally prefixed with %q",
			ref, volumeRefScheme)
	}
	return volumeRef{Namespace: namespace, Volume: volume, Selector: selector}, nil
}

// resolveVolumeRef settles which volume the caller named. A selector is only
// accepted on the commands that address a single version.
func resolveVolumeRef(flags cmd.VolumeRefFlags, allowSelector bool) (volumeRef, error) {
	if flags.Ref == "" {
		if flags.Namespace == "" || flags.Volume == "" {
			return volumeRef{}, cmd.NewErrUsagef(
				"name the volume with --ref, or with both --namespace and --volume")
		}
		return volumeRef{Namespace: flags.Namespace, Volume: flags.Volume}, nil
	}
	if flags.Namespace != "" || flags.Volume != "" {
		return volumeRef{}, cmd.NewErrUsagef("--ref cannot be combined with --namespace or --volume")
	}
	ref, err := parseVolumeRef(flags.Ref)
	if err != nil {
		return volumeRef{}, err
	}
	if ref.Selector != "" && !allowSelector {
		return volumeRef{}, cmd.NewErrUsagef(
			"--ref %q selects a version, which this command does not address", flags.Ref)
	}
	return ref, nil
}

// resolveVolumeVersionRef settles which volume and which of its versions the
// caller named, returning the selector to address the version with.
func resolveVolumeVersionRef(flags cmd.VolumeVersionRefFlags) (volumeRef, string, error) {
	ref, err := resolveVolumeRef(flags.VolumeRefFlags, true)
	if err != nil {
		return volumeRef{}, "", err
	}
	switch {
	case flags.Tag != "" && flags.Digest != "":
		return volumeRef{}, "", cmd.NewErrUsagef("--tag and --digest are mutually exclusive")
	case ref.Selector != "" && (flags.Tag != "" || flags.Digest != ""):
		return volumeRef{}, "", cmd.NewErrUsagef(
			"--ref %q already selects a version, so --tag and --digest are not accepted", flags.Ref)
	case ref.Selector != "":
		return ref, ref.Selector, nil
	case flags.Tag != "":
		return ref, ":" + flags.Tag, nil
	case flags.Digest != "":
		return ref, "@" + flags.Digest, nil
	default:
		return ref, volumeHeadSelector, nil
	}
}

// volumeTagNames renders a volume's readable tags. total is the volume's own
// count, which exceeds what was returned when the API key cannot read them
// all, so say as much rather than presenting a partial list as complete.
func volumeTagNames(tags []managementapi.VolumeTag, total int) string {
	names := make([]string, 0, len(tags))
	for _, t := range tags {
		names = append(names, t.Name)
	}
	joined := volumeJoin(names)
	if total > len(tags) {
		return fmt.Sprintf("%s (%d of %d)", joined, len(tags), total)
	}
	return joined
}

func volumeJoin(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ", ")
}

func volumeOptionalInt(v *int) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(*v)
}

func volumeOptionalSize(v *int) string {
	if v == nil {
		return "-"
	}
	return formatBytes(int64(*v))
}
