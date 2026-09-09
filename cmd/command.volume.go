package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// volumeRefGrammar documents the address every command in this group takes.
// Appended to each command's description, since the ref is the argument and
// what a command accepts of it is what distinguishes these commands.
const volumeRefGrammar = "A ref is 'bdn:<namespace>/<volume>', optionally with a ':<tag>' or " +
	"'@b3:<digest>' version selector and a trailing path, as in " +
	"'bdn:weights/llama:prod/config/model.json'. The 'bdn:' prefix is required. " +
	"With no selector, the version the volume's head points at is used. A digest is written " +
	"the way every response spells one, 'b3:' and at least 12 hexadecimal characters."

var commandVolume = Command{
	Name:    "volume",
	Summary: "Manage volumes",
	Description: "Manage volumes: file trees stored once and mounted into models, training jobs, and Loops.\n\n" +
		"A volume holds versions. A version is an immutable file tree addressed by digest. A tag is a " +
		"mutable name pointing at a version.\n\n" +
		"These commands address volumes positionally, the way filesystem commands address paths, so " +
		"'ls', 'stat', 'cat', and 'rm' read at whatever level of the tree the ref names. " +
		volumeRefGrammar,
	Children: []Command{
		{
			Name:    "ls",
			Summary: "List namespaces, volumes, or the files in a version",
			Description: "Lists what a ref contains. With no ref, lists the namespaces holding volumes " +
				"your API key can read. A namespace ref lists its volumes. A volume or version ref " +
				"lists that version's file entries, and a trailing path narrows to the entries under " +
				"it, matched on slash boundaries so 'config' does not also match 'configuration.json'.\n\n" +
				"Entry listings show immediate children, with directory names and no aggregate size. " +
				"Pass --recursive for the flat list of everything beneath.\n\n" + volumeRefGrammar,
			ArgsUsage: "[REF]",
			MaxArgs:   1,
			Flags:     VolumeLsFlags{},
			Output: &CommandOutput[VolumeEntryList]{
				TextDescription: "For entries, a table with columns: NAME, KIND, MODE, SIZE, MODIFIED, " +
					"where a directory's name ends in '/' and carries no size. For namespaces, one column: " +
					"NAMESPACE. For volumes, a table with columns: NAME, TAGS, HEAD SIZE, VERSIONS, " +
					"UPDATED. Prints what was empty to stderr when a listing has no rows.",
				JSONDescription: "An object with version_ref and items, one entry per row, when the ref " +
					"names a volume or a version. The two inventory shapes follow.",
				JSONAlternatives: []CommandOutputAlternative{
					JSONAlternativeFor[VolumeNamespaceList]("command is given no ref"),
					JSONAlternativeFor[VolumeList]("ref names a namespace"),
				},
				Examples: []CommandExample{
					{
						Description: "List every namespace holding volumes.",
						Command:     "baseten volume ls",
					},
					{
						Description: "List the volumes in a namespace.",
						Command:     "baseten volume ls bdn:<namespace>",
					},
					{
						Description: "List the files in the version head points at.",
						Command:     "baseten volume ls bdn:<namespace>/<volume>",
					},
					{
						Description: "List everything under a directory of a tagged version.",
						Command:     "baseten volume ls --recursive bdn:<namespace>/<volume>:<tag>/<path>",
					},
				},
				JQExample: CommandExample{
					Description: "Print the path of every file in a version.",
					Command:     "baseten volume ls bdn:<namespace>/<volume> --jq '.items[].path'",
				},
			},
		},
		{
			Name:    "stat",
			Summary: "Describe a volume, a version, or one file",
			Description: "Describes what a ref names. A volume ref describes the volume: its tags, head, " +
				"and version counts. A version ref describes that version: its digest, size, entry " +
				"count, and when it was created. A ref with a trailing path describes that one entry.\n\n" +
				"A namespace ref is an error, since a namespace has nothing to describe beyond the " +
				"volumes 'ls' already lists.\n\n" + volumeRefGrammar,
			ArgsUsage: "REF",
			ExactArgs: 1,
			Flags:     VolumeStatFlags{},
			Output: &CommandOutput[managementapi.Volume]{
				TextDescription: "One field per line, describing the volume, the version, or the entry.",
				JSONDescription: "The volume, when the ref names one with no selector. The two other " +
					"shapes follow.",
				JSONAlternatives: []CommandOutputAlternative{
					JSONAlternativeFor[managementapi.VolumeVersionDetail]("ref selects a version"),
					JSONAlternativeFor[VolumeEntryDetail]("ref carries a path"),
				},
				Examples: []CommandExample{
					{
						Description: "Describe a volume.",
						Command:     "baseten volume stat bdn:<namespace>/<volume>",
					},
					{
						Description: "Describe one version.",
						Command:     "baseten volume stat bdn:<namespace>/<volume>@b3:<digest>",
					},
					{
						Description: "Describe one file in the version head points at.",
						Command:     "baseten volume stat bdn:<namespace>/<volume>/<path>",
					},
				},
				JQExample: CommandExample{
					Description: "Print the digest of the version head points at, to pin a config.yaml to it.",
					Command:     "baseten volume stat bdn:<namespace>/<volume> --jq '.head.digest'",
				},
			},
		},
		{
			Name:    "cat",
			Summary: "Write one file from a volume to stdout",
			Description: "Writes one file's bytes to stdout, so it can be piped or redirected. The ref " +
				"must carry a path naming a file: a ref naming a volume or a version has no single " +
				"file to write, and a path naming a directory is an error.\n\n" +
				"Chunks are verified against the digests the version records before they are written, " +
				"so a truncated or corrupted read fails rather than producing partial output that " +
				"looks complete.\n\n" +
				"A file's bytes have no JSON form, so '--output json' and '--jq' are rejected " +
				"here.\n\n" + volumeRefGrammar,
			ArgsUsage: "REF",
			ExactArgs: 1,
			Flags:     VolumeCatFlags{},
			Output: &CommandOutput[JSONUndefined]{
				TextDescription:       "The file's bytes, exactly as the volume holds them.",
				JSONOutputUnimportant: true,
				Examples: []CommandExample{
					{
						Description: "Print a file from the version head points at.",
						Command:     "baseten volume cat bdn:<namespace>/<volume>/<path>",
					},
					{
						Description: "Save a file from a tagged version under a different name.",
						Command:     "baseten volume cat bdn:<namespace>/<volume>:<tag>/<path> > local.json",
					},
				},
			},
		},
		{
			Name:    "push",
			Summary: "Publish a directory as a new version of a volume",
			Description: "Publishes DIR as a new version of the volume REF names, creating the volume if " +
				"it does not exist. Only content the volume does not already hold is uploaded, so " +
				"pushing a tree that mostly matches an existing version transfers only what differs.\n\n" +
				"Nothing is visible until the whole tree has been uploaded, so an interrupted push " +
				"publishes nothing, and what it did upload is not wasted.\n\n" +
				"The ref must name a volume: a version selector or a path is an error, and --tag is " +
				"how a tag is applied.\n\n" + volumeRefGrammar,
			ArgsUsage: "DIR REF",
			ExactArgs: 2,
			Flags:     VolumePushFlags{},
			Output: &CommandOutput[VolumePushResult]{
				TextDescription: "A summary of what was published: the version ref, the file and byte " +
					"counts, how many chunks were uploaded, and any tags applied. Transfer progress " +
					"goes to stderr.",
				Examples: []CommandExample{
					{
						Description: "Publish a directory as a new version.",
						Command:     "baseten volume push ./weights bdn:<namespace>/<volume>",
					},
					{
						Description: "Publish and tag the new version.",
						Command:     "baseten volume push ./weights bdn:<namespace>/<volume> --tag prod",
					},
				},
				JQExample: CommandExample{
					Description: "Print the ref of the published version, which is what config.yaml takes.",
					Command:     "baseten volume push ./weights bdn:<namespace>/<volume> --jq '.version_ref'",
				},
			},
		},
		{
			Name:    "pull",
			Summary: "Download a version of a volume into a directory",
			Description: "Downloads what REF names into DIR. A volume or version ref pulls the whole " +
				"tree; a trailing path pulls that subtree or that one file.\n\n" +
				"DIR is always a directory, and entries land at their volume-relative path under it, " +
				"so pulling 'bdn:weights/llama/config/model.json' into './out' writes " +
				"'./out/config/model.json'. Pass --strip-prefix to drop the path the ref named, " +
				"writing './out/model.json' instead.\n\n" +
				"Every chunk is verified against the digest the version records before it is written, " +
				"and an interrupted download picks up where it stopped.\n\n" + volumeRefGrammar,
			ArgsUsage: "REF DIR",
			ExactArgs: 2,
			Flags:     VolumePullFlags{},
			Output: &CommandOutput[VolumePullResult]{
				TextDescription: "A summary of what was written: the version ref, the destination, and " +
					"the file and byte counts. Transfer progress goes to stderr.",
				Examples: []CommandExample{
					{
						Description: "Download the version head points at.",
						Command:     "baseten volume pull bdn:<namespace>/<volume> ./out",
					},
					{
						Description: "Download one directory of a tagged version, without its leading path.",
						Command:     "baseten volume pull bdn:<namespace>/<volume>:<tag>/<path> ./out --strip-prefix",
					},
				},
				JQExample: CommandExample{
					Description: "Print how many files were written.",
					Command:     "baseten volume pull bdn:<namespace>/<volume> ./out --jq '.files'",
				},
			},
		},
		{
			Name:    "rm",
			Summary: "Delete a version, or every version of a volume",
			Description: "Deletes what REF names. A ref carrying a digest deletes that version. " +
				"A volume ref deletes every live version of the volume and requires --recursive, so a " +
				"ref that meant to name one version cannot take the whole volume with it.\n\n" +
				"A deleted version is recoverable with 'volume restore' for a limited window, which " +
				"the output reports.\n\n" +
				"A tag ref is an error while tags cannot be mutated, and a path is an error because " +
				"versions are immutable.\n\n" + volumeRefGrammar,
			ArgsUsage: "REF",
			ExactArgs: 1,
			Flags:     VolumeRmFlags{},
			Output: &CommandOutput[managementapi.DeleteVolumeVersionResponse]{
				TextDescription: "One field per line: what was deleted and until when it can be restored. " +
					"Prompts for confirmation first unless --yes is passed.",
				JSONDescription: "The deleted version, when the ref names one. The whole-volume shape " +
					"follows.",
				JSONAlternatives: []CommandOutputAlternative{
					JSONAlternativeFor[managementapi.DeleteVolumeResponse]("ref names a volume"),
				},
				Examples: []CommandExample{
					{
						Description: "Delete one version.",
						Command:     "baseten volume rm bdn:<namespace>/<volume>@b3:<digest>",
					},
					{
						Description: "Delete every live version of a volume, without prompting.",
						Command:     "baseten volume rm --recursive --yes bdn:<namespace>/<volume>",
					},
				},
				JQExample: CommandExample{
					Description: "Print when the deleted version stops being restorable.",
					Command:     "baseten volume rm --yes bdn:<namespace>/<volume>@b3:<digest> --jq '.delete_after'",
				},
			},
		},
		{
			Name:    "versions",
			Summary: "List the versions of a volume",
			Description: "Lists a volume's versions, newest first, with the digest, sequence, size, " +
				"lifecycle, tags, and which one head points at.\n\n" +
				"The ref must name a volume: a selector or a path names one point in the history " +
				"rather than the history itself.\n\n" + volumeRefGrammar,
			ArgsUsage: "REF",
			ExactArgs: 1,
			Flags:     VolumeVersionsFlags{},
			Output: &CommandOutput[managementapi.ListVolumeVersionsResponse]{
				TextDescription: "Table with columns: SEQUENCE, DIGEST, SIZE, LIFECYCLE, HEAD, TAGS, " +
					"CREATED. When the volume has no versions, prints \"No volume versions found.\" " +
					"to stderr.",
				Examples: []CommandExample{
					{
						Description: "List a volume's versions.",
						Command:     "baseten volume versions bdn:<namespace>/<volume>",
					},
					{
						Description: "Include the deleted versions still inside their recovery window.",
						Command:     "baseten volume versions bdn:<namespace>/<volume> --include-tombstoned",
					},
				},
				JQExample: CommandExample{
					Description: "Print the digest of every tagged version.",
					Command: "baseten volume versions bdn:<namespace>/<volume> " +
						"--jq '.versions[] | select(.tags | length > 0) | .digest'",
				},
			},
		},
		{
			Name:    "restore",
			Summary: "Return a deleted version to service",
			Description: "Restores a deleted version during its recovery window, which 'volume rm' " +
				"reports and 'volume versions --include-tombstoned' lists.\n\n" +
				"The ref must carry a digest, since deleting a version drops the tags that pointed " +
				"at it, so a tag no longer names one.\n\n" +
				volumeRefGrammar,
			ArgsUsage: "REF",
			ExactArgs: 1,
			Flags:     VolumeRestoreFlags{},
			Output: &CommandOutput[managementapi.RestoreVolumeVersionResponse]{
				TextDescription: "One field per line, describing the restored version.",
				Examples: []CommandExample{
					{
						Description: "Restore a deleted version.",
						Command:     "baseten volume restore bdn:<namespace>/<volume>@b3:<digest>",
					},
				},
				JQExample: CommandExample{
					Description: "Print the lifecycle state the version came back in.",
					Command:     "baseten volume restore bdn:<namespace>/<volume>@b3:<digest> --jq '.lifecycle'",
				},
			},
		},
	},
}

// VolumeTransferFlags bounds the data path of a push or a pull. Both default
// to what the transfer picks for itself; set them to cap the load on a shared
// machine or a metered link.
type VolumeTransferFlags struct {
	ChunkOperations int `flag:"chunk-operations" desc:"Maximum object operations in flight, honored exactly. Defaults to a count the transfer adapts to what the service will bear." group:"transfer" group-pri:"200"`
	MaxInFlightMiB  int `flag:"max-in-flight-mib" desc:"Cap on the chunk data held in memory, in MiB. Defaults to 2048." group:"transfer"`
}

type VolumeLsFlags struct {
	CommandFlags

	Recursive bool `flag:"recursive" short:"R" desc:"List every entry beneath the ref instead of only its immediate children."`
}

type VolumeStatFlags struct {
	CommandFlags
}

type VolumeCatFlags struct {
	CommandFlags
}

type VolumePushFlags struct {
	CommandFlags
	VolumeTransferFlags

	Tags []string `flag:"tag" desc:"Tag to apply to the new version at commit. May be repeated. This is the only place a tag is written rather than read."`

	SourceURI string `flag:"source-uri" desc:"Where the tree came from, for example 'hf://<repo>@<revision>'. Defaults to a file URI for DIR and is part of the version's digest, so a fixed value keeps the same tree at one version across directories."`

	FileJobs int `flag:"file-jobs" desc:"Number of files processed concurrently. Defaults to 16." group:"transfer"`
}

type VolumePullFlags struct {
	CommandFlags
	VolumeTransferFlags

	Overwrite bool `flag:"overwrite" desc:"Allow writing into a non-empty directory. Files already there that the version does not describe are left alone."`

	Include []string `flag:"include" desc:"Restrict the download to this path in the volume, either a file or a directory whose contents are wanted, matched on slash boundaries. May be repeated, and is relative to the version's root whatever path the ref carries. One that matches nothing fails the download."`
	Restart bool     `flag:"restart" desc:"Discard a partly downloaded tree from an earlier attempt instead of continuing it."`

	StripPrefix bool `flag:"strip-prefix" desc:"Write the contents of the directory the ref names directly into DIR, instead of under the path that led to them. Requires a path on the ref."`
}

type VolumeRmFlags struct {
	CommandFlags

	Recursive bool `flag:"recursive" short:"r" desc:"Delete every live version of the volume. Required for a ref that names a volume rather than one version."`
	Yes       bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

type VolumeVersionsFlags struct {
	CommandFlags

	IncludeTombstoned bool `flag:"include-tombstoned" desc:"Include deleted versions that are still inside their recovery window."`
}

type VolumeRestoreFlags struct {
	CommandFlags
}

// VolumeNamespaceList is the JSON output of `baseten volume ls` with no ref:
// the namespaces aggregated across all pages.
type VolumeNamespaceList struct {
	Items []string `json:"items"`
}

// VolumeList is the JSON output of `baseten volume ls` for a namespace ref:
// the namespace's volumes aggregated across all pages.
type VolumeList struct {
	Items []managementapi.Volume `json:"items"`
}

// VolumeEntry is one entry of a version's file tree.
//
// There is deliberately no per-entry digest. A version's digest names the
// whole tree, and what a file carries depends on its size: one small enough to
// fit a single chunk carries that chunk's digest, which is its content, while
// a larger one carries the digest of the document listing its chunks, which is
// not.
type VolumeEntry struct {
	// Path is the entry's path within the version, slash-prefixed and with no
	// trailing slash, which is the form a ref takes: appending it to the ref
	// this listing was asked for names the entry.
	Path string `json:"path"`
	// Kind is "file", "directory", or "symlink".
	Kind string `json:"kind"`
	// SizeBytes is a file's length, and zero for the other kinds.
	SizeBytes int64 `json:"size_bytes"`
	// Mode is the recorded permission bits in octal, as "0644". Absent for a
	// directory the version describes only by the paths beneath it.
	Mode string `json:"mode,omitempty"`
	// Modified is when the entry was last modified, as recorded at push.
	// Absent when the version records none.
	Modified *time.Time `json:"modified,omitempty"`
	// LinkTarget is a symlink's target exactly as recorded, and absent for
	// every other kind. It may point outside the volume.
	LinkTarget string `json:"link_target,omitempty"`
}

// VolumeEntryList is the JSON output of `baseten volume ls` for a ref that
// names a volume or a version.
type VolumeEntryList struct {
	// VersionRef is the version the entries were read from, pinned to its
	// digest, so the same listing can be read again.
	VersionRef string        `json:"version_ref"`
	Items      []VolumeEntry `json:"items"`
}

// VolumeEntryDetail is the JSON output of `baseten volume stat` for a ref that
// carries a path.
type VolumeEntryDetail struct {
	// VersionRef is the version the entry was read from, pinned to its digest.
	VersionRef string `json:"version_ref"`
	VolumeEntry
}

// VolumePushResult is the JSON output of `baseten volume push`.
type VolumePushResult struct {
	// VersionRef is the published version, pinned to its digest, which is what
	// config.yaml takes to mount exactly this tree.
	VersionRef string `json:"version_ref"`
	Sequence   int64  `json:"sequence"`
	// HeadUpdated is false when head already pointed at this exact version,
	// which is what re-pushing an unchanged tree does. HeadMoveDenied means
	// the version was published but head was left where it was, because the
	// credential's grants did not cover moving it.
	HeadUpdated    bool     `json:"head_updated"`
	HeadMoveDenied bool     `json:"head_move_denied"`
	TagsApplied    []string `json:"tags_applied"`
	Files          int64    `json:"files"`
	Bytes          int64    `json:"bytes"`
	// Chunks counts every chunk the push accounted for, and the three below
	// partition it: ChunksUnique were uploaded, ChunksReused never reached the
	// network because a previous version or an earlier file in this push
	// already had those bytes, and ChunksExisting were offered and the volume
	// already had them.
	Chunks         int64 `json:"chunks"`
	ChunksUnique   int64 `json:"chunks_unique"`
	ChunksReused   int64 `json:"chunks_reused"`
	ChunksExisting int64 `json:"chunks_existing"`
}

// VolumePullResult is the JSON output of `baseten volume pull`.
type VolumePullResult struct {
	// VersionRef is the version that was downloaded, pinned to its digest.
	VersionRef string `json:"version_ref"`
	DestDir    string `json:"dest_dir"`
	Files      int64  `json:"files"`
	Bytes      int64  `json:"bytes"`
	// SelectedFiles and TotalFiles report what a path or --include narrowed
	// to, and are equal when the whole version was downloaded.
	SelectedFiles int64 `json:"selected_files"`
	TotalFiles    int64 `json:"total_files"`
	// ChunksFetched counts the chunks this download transferred; ChunksReused
	// counts those already on disk from an earlier attempt.
	ChunksFetched int64 `json:"chunks_fetched"`
	ChunksReused  int64 `json:"chunks_reused"`
	// Warnings are containment findings that did not stop the download, which
	// only volumes published before the containment rule carry.
	Warnings []string `json:"warnings"`
}
