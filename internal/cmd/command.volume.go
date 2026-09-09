package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("volume ls", commandVolumeLs)
	Register("volume stat", commandVolumeStat)
	Register("volume rm", commandVolumeRm)
	Register("volume versions", commandVolumeVersions)
	Register("volume restore", commandVolumeRestore)
}

// volumeRefScheme is the whole of a ref that names nothing at all. Every
// parsed ref has at least a namespace, so `ls` takes this and an absent
// argument as the request to list namespaces before it parses anything.
const volumeRefScheme = "bdn:"

// volumeParseRef parses a positional ref, reporting a malformed one as a
// usage error so the grammar is printed alongside it.
func volumeParseRef(arg string) (client.VolumeRef, error) {
	ref, err := client.ParseVolumeRef(arg)
	if err != nil {
		return client.VolumeRef{}, cmd.NewErrUsagef("%s", err)
	}
	return ref, nil
}

func commandVolumeLs(ctx *CommandContext, flags *cmd.VolumeLsFlags) error {
	arg := ""
	if len(ctx.Args) > 0 {
		arg = strings.TrimSpace(ctx.Args[0])
	}
	if arg == "" || arg == volumeRefScheme {
		return volumeLsNamespaces(ctx)
	}
	ref, err := volumeParseRef(arg)
	if err != nil {
		return err
	}
	if ref.Level() == client.VolumeRefLevelNamespace {
		return volumeLsVolumes(ctx, ref.Namespace)
	}
	return volumeLsEntries(ctx, flags, ref)
}

func volumeLsNamespaces(ctx *CommandContext) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	// Every page is walked and aggregated rather than exposing cursors,
	// matching `org user list`.
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
	for _, namespace := range items {
		rows = append(rows, []string{namespace})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"NAMESPACE"},
		Rows:    rows,
	})
	return nil
}

func volumeLsVolumes(ctx *CommandContext, namespace string) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	var items []managementapi.Volume
	params := managementapi.GetV1VolumesParams{Namespace: namespace}
	for {
		resp, err := cl.API().GetVolumes(ctx, params)
		if err != nil {
			return fmt.Errorf("listing volumes in namespace %s: %w", namespace, err)
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
	for _, volume := range items {
		headSize := "-"
		if volume.Head != nil {
			headSize = formatBytes(int64(volume.Head.TotalSizeBytes))
		}
		rows = append(rows, []string{
			volume.Name,
			volumeTagNames(volume.Tags, volume.TagCount),
			headSize,
			fmt.Sprint(volume.VersionsAlive),
			volume.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"NAME", "TAGS", "HEAD SIZE", "VERSIONS", "UPDATED"},
		Rows:                rows,
		RightAlignedColumns: []int{2, 3},
	})
	return nil
}

func volumeLsEntries(ctx *CommandContext, flags *cmd.VolumeLsFlags, ref client.VolumeRef) error {
	transfer, err := ctx.NewVolumeTransfer()
	if err != nil {
		return err
	}
	manifest, err := transfer.FetchVolumeManifest(ctx, client.FetchVolumeManifestOptions{
		Ref:    ref,
		Hasher: volumeHasher,
		Store:  &volumeObjectStore{ctx: ctx},
	})
	if err != nil {
		return fmt.Errorf("listing %s: %w", ref, err)
	}

	dir := volumeListedDir(ref)
	var entries []client.VolumeEntry
	if flags.Recursive {
		// Nothing is synthesized here, unlike the children listing below: a
		// flat list shows every file beneath whatever the version records,
		// where a children listing would show a whole subtree as nothing.
		entries = make([]client.VolumeEntry, 0, len(manifest.Entries))
		for _, entry := range manifest.Entries {
			// The listed directory is among the entries, since a ref's path
			// selects the entry at it as well as those under it.
			if entry.Path != dir {
				entries = append(entries, entry)
			}
		}
	} else {
		entries = volumeChildEntries(manifest.Entries, dir)
	}

	if ctx.JSON {
		items := make([]cmd.VolumeEntry, 0, len(entries))
		for _, entry := range entries {
			items = append(items, volumeEntryOf(entry))
		}
		ctx.OutputJSON(cmd.VolumeEntryList{VersionRef: manifest.VersionRef.String(), Items: items})
		return nil
	}
	if len(entries) == 0 {
		ctx.LogLine("No entries found.")
		return nil
	}
	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimPrefix(entry.Path, dir+"/")
		size := "-"
		switch entry.Kind {
		case client.VolumeEntryKindDirectory:
			name += "/"
		case client.VolumeEntryKindFile:
			size = formatBytes(entry.Size)
		}
		modified := "-"
		if !entry.ModTime.IsZero() {
			modified = entry.ModTime.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{
			name, string(entry.Kind), volumeModeText(entry.Mode), size, modified,
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"NAME", "KIND", "MODE", "SIZE", "MODIFIED"},
		Rows:                rows,
		RightAlignedColumns: []int{3},
	})
	return nil
}

// volumeListedDir is the directory a listing is rooted at. The version's root
// is the empty string rather than "/", so that no entry's path equals it and
// every entry's path begins with it plus a separator.
func volumeListedDir(ref client.VolumeRef) string {
	if ref.Path == "/" {
		return ""
	}
	return ref.Path
}

// volumeChildEntries reduces the entries at or under dir to its immediate
// children.
//
// A directory that no record describes is synthesized from the paths beneath
// it. A version published before directory records were required can describe
// a directory only that way, and a listing that dropped it would report an
// entire subtree as nothing.
func volumeChildEntries(entries []client.VolumeEntry, dir string) []client.VolumeEntry {
	children := make([]client.VolumeEntry, 0, len(entries))
	positions := make(map[string]int, len(entries))
	for _, entry := range entries {
		rest, ok := strings.CutPrefix(entry.Path, dir+"/")
		if !ok || rest == "" {
			continue
		}
		name, _, deeper := strings.Cut(rest, "/")
		child := entry
		if deeper {
			// Only the name is known, so the mode and modification time are
			// left absent, which is what marks the directory as implied.
			child = client.VolumeEntry{
				Path: dir + "/" + name,
				Kind: client.VolumeEntryKindDirectory,
			}
		}
		position, seen := positions[name]
		switch {
		case !seen:
			positions[name] = len(children)
			children = append(children, child)
		case !deeper:
			// A record for the directory itself replaces the synthesized one,
			// whichever order the two were reached in.
			children[position] = child
		}
	}
	return children
}

// volumeModeText renders recorded permission bits the way ls does, since what
// a volume records is what a mount presents. The kind has a column of its own,
// so the leading type character is left off. A zero mode means the version
// records none, which is what a directory implied only by the paths beneath it
// looks like.
func volumeModeText(mode uint32) string {
	if mode == 0 {
		return "-"
	}
	const permissions = "rwxrwxrwx"
	text := []byte("---------")
	for i := range text {
		if mode&(1<<(8-i)) != 0 {
			text[i] = permissions[i]
		}
	}
	// Setuid, setgid, and sticky replace the execute character of the class
	// they apply to, upper case when that class has no execute bit, which is
	// how ls spells them.
	for _, special := range []struct {
		bit       uint32
		position  int
		executes  byte
		otherwise byte
	}{
		{0o4000, 2, 's', 'S'},
		{0o2000, 5, 's', 'S'},
		{0o1000, 8, 't', 'T'},
	} {
		if mode&special.bit == 0 {
			continue
		}
		if text[special.position] == 'x' {
			text[special.position] = special.executes
		} else {
			text[special.position] = special.otherwise
		}
	}
	return string(text)
}

// volumeEntryOf renders one manifest entry for output. The mode and the
// modification time are absent when the version records none, which is what a
// directory implied only by the paths beneath it looks like.
func volumeEntryOf(entry client.VolumeEntry) cmd.VolumeEntry {
	out := cmd.VolumeEntry{
		Path:       entry.Path,
		Kind:       string(entry.Kind),
		SizeBytes:  entry.Size,
		LinkTarget: entry.LinkTarget,
	}
	if entry.Mode != 0 {
		out.Mode = fmt.Sprintf("%04o", entry.Mode)
	}
	if !entry.ModTime.IsZero() {
		modified := entry.ModTime.UTC()
		out.Modified = &modified
	}
	return out
}

func commandVolumeStat(ctx *CommandContext, flags *cmd.VolumeStatFlags) error {
	ref, err := volumeParseRef(ctx.Args[0])
	if err != nil {
		return err
	}
	switch ref.Level() {
	case client.VolumeRefLevelNamespace:
		return cmd.NewErrUsagef(
			"ref %s names a namespace, which has nothing to describe; "+
				"list its volumes with 'baseten volume ls %s'", ref, ref)
	case client.VolumeRefLevelVolume:
		return volumeStatVolume(ctx, ref)
	case client.VolumeRefLevelPoint:
		return volumeStatVersion(ctx, ref)
	default:
		return volumeStatEntry(ctx, ref)
	}
}

func volumeStatVolume(ctx *CommandContext, ref client.VolumeRef) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	volume, err := cl.API().GetVolumesVolumeName(ctx, ref.Namespace, ref.Volume)
	if err != nil {
		return fmt.Errorf("describing %s: %w", ref, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(volume)
		return nil
	}
	ctx.Outputf("Ref:         %s\n", volume.VersionRef)
	ctx.Outputf("Sequence:    %d\n", volume.Sequence)
	ctx.Outputf("Updated:     %s\n", volume.UpdatedAt.UTC().Format(time.RFC3339))
	ctx.Outputf("Versions:    %d alive, %d tombstoned, %d untagged\n",
		volume.VersionsAlive, volume.VersionsTombstoned, volume.VersionsUntagged)
	ctx.Outputf("Tags:        %s\n", volumeTagNames(volume.Tags, volume.TagCount))
	if volume.Head != nil {
		ctx.Outputf("Head:        %s\n", volume.Head.Digest)
		ctx.Outputf("Head size:   %s\n", formatBytes(int64(volume.Head.TotalSizeBytes)))
		ctx.Outputf("Head pushed: %s\n", volume.Head.CreatedAt.UTC().Format(time.RFC3339))
	}
	return nil
}

func volumeStatVersion(ctx *CommandContext, ref client.VolumeRef) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	version, err := cl.API().GetVolumesVersionsVolumeVersion(
		ctx, ref.Namespace, ref.Volume, volumeVersionSelector(ref))
	if err != nil {
		return fmt.Errorf("describing %s: %w", ref, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(version)
		return nil
	}
	ctx.Outputf("Ref:              %s\n", version.VersionRef)
	ctx.Outputf("Digest:           %s\n", version.Digest)
	if version.Sequence != nil {
		ctx.Outputf("Sequence:         %d\n", *version.Sequence)
	}
	if version.TotalSizeBytes != nil {
		ctx.Outputf("Size:             %s\n", formatBytes(int64(*version.TotalSizeBytes)))
	}
	if version.EntryCount != nil {
		ctx.Outputf("Entries:          %d\n", *version.EntryCount)
	}
	ctx.Outputf("Lifecycle:        %s\n", version.Lifecycle)
	if version.DeleteAfter != nil {
		ctx.Outputf("Restorable until: %s\n", version.DeleteAfter.UTC().Format(time.RFC3339))
	}
	if version.IsHead {
		ctx.OutputLine("Head:             yes")
	}
	ctx.Outputf("Tags:             %s\n", volumeJoin(version.Tags))
	ctx.Outputf("Created:          %s\n", version.CreatedAt.UTC().Format(time.RFC3339))
	ctx.Outputf("Volume sequence:  %d\n", version.VolumeSequence)
	return nil
}

func volumeStatEntry(ctx *CommandContext, ref client.VolumeRef) error {
	transfer, err := ctx.NewVolumeTransfer()
	if err != nil {
		return err
	}
	// Only the entry at the path is kept, so a directory holding a large
	// subtree costs no more to describe than a file. What lies beneath it is
	// counted instead, since an entry under the path is proof the path is a
	// directory even when no record describes it.
	var beneath int
	manifest, err := transfer.FetchVolumeManifest(ctx, client.FetchVolumeManifestOptions{
		Ref: ref,
		EntryFilter: func(_ context.Context, entry client.VolumeEntry) bool {
			if entry.Path == ref.Path {
				return true
			}
			if strings.HasPrefix(entry.Path, ref.Path+"/") {
				beneath++
			}
			return false
		},
		Hasher: volumeHasher,
		Store:  &volumeObjectStore{ctx: ctx},
	})
	if err != nil {
		return fmt.Errorf("describing %s: %w", ref, err)
	}

	// Found by path rather than taken positionally, so what is described is
	// the entry the ref named and nothing that merely survived the filter.
	var entry client.VolumeEntry
	for _, candidate := range manifest.Entries {
		if candidate.Path == ref.Path {
			entry = candidate
			break
		}
	}
	if entry.Path == "" {
		if beneath == 0 {
			return fmt.Errorf("version %s has no entry at %s", manifest.VersionRef, ref.Path)
		}
		entry = client.VolumeEntry{
			Path: ref.Path,
			Kind: client.VolumeEntryKindDirectory,
		}
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.VolumeEntryDetail{
			VersionRef:  manifest.VersionRef.String(),
			VolumeEntry: volumeEntryOf(entry),
		})
		return nil
	}
	entryRef := manifest.VersionRef
	entryRef.Path = entry.Path
	ctx.Outputf("Ref:         %s\n", entryRef)
	ctx.Outputf("Kind:        %s\n", entry.Kind)
	if entry.Kind == client.VolumeEntryKindFile {
		ctx.Outputf("Size:        %s\n", formatBytes(entry.Size))
	}
	if entry.Mode != 0 {
		ctx.Outputf("Mode:        %s (%04o)\n", volumeModeText(entry.Mode), entry.Mode)
	}
	if !entry.ModTime.IsZero() {
		ctx.Outputf("Modified:    %s\n", entry.ModTime.UTC().Format(time.RFC3339))
	}
	if entry.LinkTarget != "" {
		ctx.Outputf("Link target: %s\n", entry.LinkTarget)
	}
	return nil
}

func commandVolumeVersions(ctx *CommandContext, flags *cmd.VolumeVersionsFlags) error {
	ref, err := volumeParseRef(ctx.Args[0])
	if err != nil {
		return err
	}
	if ref.Level() != client.VolumeRefLevelVolume {
		return cmd.NewErrUsagef(
			"ref %s names one point in a volume's history rather than the volume; "+
				"write 'bdn:%s/%s'", ref, ref.Namespace, ref.Volume)
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	// Unpaginated upstream, so unlike the other listings there is no cursor
	// to walk.
	resp, err := cl.API().GetVolumesVersions(ctx, ref.Namespace, ref.Volume,
		managementapi.GetV1VolumesVolumeNamespaceVolumeNameVersionsParams{
			IncludeTombstoned: &flags.IncludeTombstoned,
		})
	if err != nil {
		return fmt.Errorf("listing versions of %s: %w", ref, err)
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
	for _, version := range resp.Versions {
		sequence := "-"
		if version.Sequence != nil {
			sequence = fmt.Sprint(*version.Sequence)
		}
		size := "-"
		if version.TotalSizeBytes != nil {
			size = formatBytes(int64(*version.TotalSizeBytes))
		}
		head := ""
		if version.IsHead {
			head = "yes"
		}
		rows = append(rows, []string{
			sequence,
			version.Digest,
			size,
			version.Lifecycle,
			head,
			volumeJoin(version.Tags),
			version.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"SEQUENCE", "DIGEST", "SIZE", "LIFECYCLE", "HEAD", "TAGS", "CREATED"},
		Rows:                rows,
		RightAlignedColumns: []int{0, 2},
	})
	return nil
}

func commandVolumeRm(ctx *CommandContext, flags *cmd.VolumeRmFlags) error {
	ref, err := volumeParseRef(ctx.Args[0])
	if err != nil {
		return err
	}
	switch {
	case ref.Level() == client.VolumeRefLevelNamespace:
		return cmd.NewErrUsagef(
			"ref %s names a namespace, and a namespace cannot be deleted; "+
				"name a volume or one of its versions", ref)
	case ref.Level() == client.VolumeRefLevelPath:
		return cmd.NewErrUsagef(
			"ref %s names a path, and a version's contents are immutable; "+
				"name the version itself", ref)
	case ref.Tag != "":
		return cmd.NewErrUsagef(
			"ref %s names a tag, and deleting through one would delete the "+
				"version it points at; name that version by digest", ref)
	case ref.Digest != "":
		return volumeRmVersion(ctx, flags, ref)
	case !flags.Recursive:
		return cmd.NewErrUsagef(
			"ref %s names the whole volume, so every live version of it would "+
				"be deleted; pass --recursive to do that, or name one version "+
				"by digest", ref)
	default:
		return volumeRmVolume(ctx, flags, ref)
	}
}

func volumeRmVersion(ctx *CommandContext, flags *cmd.VolumeRmFlags, ref client.VolumeRef) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete version %s?", ref)); err != nil {
			return err
		}
	}

	resp, err := cl.API().DeleteVolumesVersions(ctx, ref.Namespace, ref.Volume,
		volumeVersionSelector(ref), managementapi.DeleteVolumeVersionRequest{})
	if err != nil {
		return fmt.Errorf("deleting %s: %w", ref, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Outputf("Ref:              %s\n", resp.VersionRef)
	ctx.Outputf("Digest:           %s\n", resp.Digest)
	ctx.Outputf("Lifecycle:        %s\n", resp.Lifecycle)
	ctx.Outputf("Restorable until: %s\n", resp.DeleteAfter.UTC().Format(time.RFC3339))
	ctx.Outputf("Volume sequence:  %d\n", resp.VolumeSequence)
	return nil
}

func volumeRmVolume(ctx *CommandContext, flags *cmd.VolumeRmFlags, ref client.VolumeRef) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(
			fmt.Sprintf("Delete every live version of volume %s?", ref)); err != nil {
			return err
		}
	}

	resp, err := cl.API().DeleteVolumes(ctx, ref.Namespace, ref.Volume,
		managementapi.DeleteVolumeRequest{})
	if err != nil {
		return fmt.Errorf("deleting %s: %w", ref, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Outputf("Namespace: %s\n", resp.Namespace)
	ctx.Outputf("Volume:    %s\n", resp.Name)
	ctx.Outputf("Versions deleted: %d\n", resp.VersionsDeleted)
	ctx.Outputf("Volume sequence:  %d\n", resp.VolumeSequence)
	return nil
}

func commandVolumeRestore(ctx *CommandContext, flags *cmd.VolumeRestoreFlags) error {
	ref, err := volumeParseRef(ctx.Args[0])
	if err != nil {
		return err
	}
	if ref.Digest == "" || ref.Path != "" {
		return cmd.NewErrUsagef(
			"ref %s does not name a deleted version; write 'bdn:%s/%s@<digest>'",
			ref, ref.Namespace, ref.Volume)
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	resp, err := cl.API().PostVolumesVersionsRestore(ctx, ref.Namespace, ref.Volume,
		volumeVersionSelector(ref), managementapi.RestoreVolumeVersionRequest{})
	if err != nil {
		return fmt.Errorf("restoring %s: %w", ref, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Outputf("Ref:       %s\n", resp.VersionRef)
	ctx.Outputf("Digest:    %s\n", resp.Digest)
	ctx.Outputf("Lifecycle: %s\n", resp.Lifecycle)
	ctx.Outputf("Volume sequence: %d\n", resp.VolumeSequence)
	return nil
}

// volumeVersionSelector renders the version a ref selects in the syntax the
// REST path segment takes. A ref selecting nothing is the head lookup, which
// upstream spells as a reserved tag needing no punctuation.
func volumeVersionSelector(ref client.VolumeRef) string {
	switch {
	case ref.Digest != "":
		return "@" + ref.Digest
	case ref.Tag != "":
		return ":" + ref.Tag
	default:
		return "head"
	}
}

// volumeTagNames renders a volume's readable tags. total is the volume's own
// count, which exceeds what was returned when the API key cannot read them
// all, so say as much rather than presenting a partial list as complete.
func volumeTagNames(tags []managementapi.VolumeTag, total int) string {
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
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
