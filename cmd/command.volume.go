package cmd

import (
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// volumeRefDescription documents the exactly-one-of group shared by every
// command that names a volume. Appended to those commands' descriptions rather
// than enforced with `oneof`, which cannot express "--ref, or both of
// --namespace and --volume".
const volumeRefDescription = "Name the volume with either --ref, or --namespace plus --volume. " +
	"--ref accepts 'bdn://<namespace>/<volume>' or '<namespace>/<volume>'."

// volumeVersionSelectorDescription documents how the version is selected on the
// commands that address a single version.
const volumeVersionSelectorDescription = "Select the version with --tag or --digest, which are mutually " +
	"exclusive, or carry the selector inline in --ref as '<namespace>/<volume>:<tag>' or " +
	"'<namespace>/<volume>@<digest>'. An inline selector rejects --tag and --digest. With no selector, " +
	"the version the volume's head points at is used."

var commandVolume = Command{
	Name:    "volume",
	Summary: "Manage volumes",
	Description: "Manage volumes: file trees stored once and mounted into models, training jobs, and Loops.\n\n" +
		"A volume holds versions. A version is an immutable file tree addressed by digest. A tag is a " +
		"mutable name pointing at a version. Every command prints the full version ref, which is what " +
		"config.yaml takes.",
	Children: []Command{
		{
			Name:    "namespace",
			Summary: "View volume namespaces",
			Children: []Command{
				{
					Name:        "list",
					Summary:     "List volume namespaces",
					Description: "List the namespaces that hold volumes your API key can read.",
					Flags:       VolumeNamespaceListFlags{},
					Output: &CommandOutput[VolumeNamespaceList]{
						TextDescription: "Table with one column: NAMESPACE. When no namespaces exist, " +
							"prints \"No volume namespaces found.\" to stderr.",
						JSONDescription: "An object with items: every namespace name, aggregated across pages.",
						Examples: []CommandExample{
							{
								Description: "List every namespace holding volumes.",
								Command:     "baseten volume namespace list",
							},
						},
						JQExample: CommandExample{
							Description: "Print just the namespace names.",
							Command:     "baseten volume namespace list --jq '.items[]'",
						},
					},
				},
			},
		},
		{
			Name:    "list",
			Summary: "List volumes in a namespace",
			Description: "List the volumes in a namespace, with their tags, head version, and version " +
				"counts. --namespace is required: the volume service has no cross-namespace inventory.",
			Flags: VolumeListFlags{},
			Output: &CommandOutput[VolumeList]{
				TextDescription: "Table with columns: NAME, TAGS, HEAD SIZE, VERSIONS, UPDATED. When the " +
					"namespace holds no volumes, prints \"No volumes found.\" to stderr.",
				JSONDescription: "An object with items: one volume per entry, aggregated across pages.",
				Examples: []CommandExample{
					{
						Description: "List the volumes in a namespace.",
						Command:     "baseten volume list --namespace <namespace>",
					},
				},
				JQExample: CommandExample{
					Description: "Print each volume's ref and head digest.",
					Command:     `baseten volume list --namespace <namespace> --jq '.items[] | "\(.version_ref) \(.head.digest)"'`,
				},
			},
		},
		{
			Name:        "describe",
			Summary:     "Describe a volume",
			Description: "Describe a volume: its tags, head version, and version counts.\n\n" + volumeRefDescription,
			Flags:       VolumeDescribeFlags{},
			Output: &CommandOutput[managementapi.Volume]{
				TextDescription: "Field-per-line summary of the volume, then its tags and head version.",
				Examples: []CommandExample{
					{
						Description: "Describe a volume by ref.",
						Command:     "baseten volume describe --ref <namespace>/<volume>",
					},
					{
						Description: "Describe a volume by namespace and name.",
						Command:     "baseten volume describe --namespace <namespace> --volume <volume>",
					},
				},
				JQExample: CommandExample{
					Description: "Print the digest the volume's head points at.",
					Command:     "baseten volume describe --ref <namespace>/<volume> --jq '.head.digest'",
				},
			},
		},
		{
			Name:    "push",
			Summary: "Push a directory as a new volume version",
			Description: "Push a directory as a new volume version. The volume does not have to exist " +
				"yet; the first push into a namespace creates it. Only content the volume does not " +
				"already hold is uploaded.\n\n" +
				"--tag applies a tag to the new version at commit and may be repeated. This is the only " +
				"command where --tag sets a tag rather than selects one.\n\n" + volumeRefDescription,
			Flags: VolumePushFlags{},
			Output: &CommandOutput[VolumePushResult]{
				TextDescription: "Progress to stderr while scanning and uploading, then a summary on " +
					"stdout with the new version ref, what it holds, and how much was uploaded.",
				JSONDescription: "The new version's ref, digest, and sequence, whether head moved, the " +
					"tags applied, and file, byte, and chunk counts for the transfer.",
				Examples: []CommandExample{
					{
						Description: "Push a directory as a new version.",
						Command:     "baseten volume push --ref <namespace>/<volume> --dir ./weights",
					},
					{
						Description: "Push a directory and tag the new version.",
						Command:     "baseten volume push --ref <namespace>/<volume> --dir ./weights --tag prod",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the new version ref.",
					Command:     "baseten volume push --ref <namespace>/<volume> --dir ./weights --jq '.version_ref'",
				},
			},
		},
		{
			Name:    "version",
			Summary: "Manage volume versions",
			Children: []Command{
				{
					Name:    "list",
					Summary: "List a volume's versions",
					Description: "List every version of a volume, newest first. Deleted versions are " +
						"included and carry a tombstoned lifecycle.\n\n" + volumeRefDescription,
					Flags: VolumeVersionListFlags{},
					Output: &CommandOutput[managementapi.ListVolumeVersionsResponse]{
						TextDescription: "Table with columns: SEQUENCE, DIGEST, SIZE, LIFECYCLE, HEAD, " +
							"TAGS, CREATED. When the volume has no versions, prints \"No volume versions " +
							"found.\" to stderr.",
						Examples: []CommandExample{
							{
								Description: "List a volume's versions.",
								Command:     "baseten volume version list --ref <namespace>/<volume>",
							},
						},
						JQExample: CommandExample{
							Description: "Print the refs of the live versions.",
							Command:     `baseten volume version list --ref <namespace>/<volume> --jq '.versions[] | select(.lifecycle == "alive") | .version_ref'`,
						},
					},
				},
				{
					Name:        "describe",
					Summary:     "Describe one version of a volume",
					Description: "Describe a single version of a volume.\n\n" + volumeRefDescription + "\n\n" + volumeVersionSelectorDescription,
					Flags:       VolumeVersionDescribeFlags{},
					Output: &CommandOutput[managementapi.VolumeVersion]{
						TextDescription: "Field-per-line summary of the version.",
						Examples: []CommandExample{
							{
								Description: "Describe the version the head points at.",
								Command:     "baseten volume version describe --ref <namespace>/<volume>",
							},
							{
								Description: "Describe the version a tag points at.",
								Command:     "baseten volume version describe --ref <namespace>/<volume> --tag prod",
							},
							{
								Description: "Describe a version by digest.",
								Command:     "baseten volume version describe --namespace <namespace> --volume <volume> --digest <digest>",
							},
						},
						JQExample: CommandExample{
							Description: "Print the version's total size in bytes.",
							Command:     "baseten volume version describe --ref <namespace>/<volume> --jq '.total_size_bytes'",
						},
					},
				},
				{
					Name:    "download",
					Summary: "Download a volume version",
					Description: "Download a volume version into a directory. Content already on disk is " +
						"reused, so re-running after an interruption fetches only what is missing.\n\n" +
						volumeRefDescription + "\n\n" + volumeVersionSelectorDescription,
					Flags: VolumeVersionDownloadFlags{},
					Output: &CommandOutput[VolumeVersionDownloadResult]{
						TextDescription: "Progress to stderr while downloading, then a summary on stdout " +
							"with the version downloaded and what was written.",
						JSONDescription: "The downloaded version's ref and digest, the destination " +
							"directory, file, byte, and chunk counts for the transfer, and any " +
							"containment warnings.",
						Examples: []CommandExample{
							{
								Description: "Download the version the head points at.",
								Command:     "baseten volume version download --ref <namespace>/<volume> --out-dir ./weights",
							},
							{
								Description: "Download the version a tag points at into a non-empty directory.",
								Command:     "baseten volume version download --ref <namespace>/<volume> --tag prod --out-dir ./weights --overwrite",
							},
						},
						JQExample: CommandExample{
							Description: "Print the number of bytes written.",
							Command:     "baseten volume version download --ref <namespace>/<volume> --out-dir ./weights --jq '.bytes'",
						},
					},
				},
			},
		},
	},
}

// VolumeRefFlags names a volume, either as a whole ref or as its two parts.
// Shared by every command that addresses one.
type VolumeRefFlags struct {
	Ref       string `flag:"ref" desc:"Volume reference, as 'bdn://<namespace>/<volume>' or '<namespace>/<volume>'. Mutually exclusive with --namespace and --volume."`
	Namespace string `flag:"namespace" desc:"Namespace holding the volume. Requires --volume."`
	Volume    string `flag:"volume" desc:"Name of the volume within the namespace. Requires --namespace."`
}

// VolumeVersionRefFlags names one version of a volume.
type VolumeVersionRefFlags struct {
	VolumeRefFlags

	Tag    string `flag:"tag" desc:"Select the version this tag points at. Mutually exclusive with --digest and with a selector carried in --ref."`
	Digest string `flag:"digest" desc:"Select the version with this content digest, at least 12 hexadecimal characters. Mutually exclusive with --tag and with a selector carried in --ref."`
}

// VolumeTransferFlags bounds the data path of a push or a download. Both
// default to what the transfer picks for itself; set them to cap the load on a
// shared machine or a metered link.
type VolumeTransferFlags struct {
	ChunkOperations int `flag:"chunk-operations" desc:"Maximum object operations in flight, honored exactly. Defaults to a count the transfer adapts to what the service will bear." group:"transfer" group-pri:"200"`
	MaxInFlightMiB  int `flag:"max-in-flight-mib" desc:"Cap on the chunk data held in memory, in MiB. Defaults to 2048." group:"transfer"`
}

type VolumeNamespaceListFlags struct {
	CommandFlags
}

type VolumeListFlags struct {
	CommandFlags

	Namespace string `flag:"namespace" desc:"Namespace to list volumes in." required:"true"`
}

type VolumeDescribeFlags struct {
	CommandFlags
	VolumeRefFlags
}

type VolumePushFlags struct {
	CommandFlags
	VolumeRefFlags
	VolumeTransferFlags

	// Required rather than defaulted to the current directory: any directory
	// is a valid volume, so a default would silently push the wrong tree.
	Dir  string   `flag:"dir" desc:"Directory to push as the new version." required:"true"`
	Tags []string `flag:"tag" desc:"Tag to apply to the new version at commit. May be repeated."`

	SourceURI string `flag:"source-uri" desc:"Where the tree came from, for example 'hf://<repo>@<revision>'. Defaults to a file URI for --dir and is part of the version's digest, so a fixed value keeps the same tree at one version across directories."`

	FileJobs int `flag:"file-jobs" desc:"Number of files processed concurrently. Defaults to 16." group:"transfer"`
}

type VolumeVersionListFlags struct {
	CommandFlags
	VolumeRefFlags
}

type VolumeVersionDescribeFlags struct {
	CommandFlags
	VolumeVersionRefFlags
}

type VolumeVersionDownloadFlags struct {
	CommandFlags
	VolumeVersionRefFlags
	VolumeTransferFlags

	OutDir    string `flag:"out-dir" desc:"Directory to write the version's files into." required:"true"`
	Overwrite bool   `flag:"overwrite" desc:"Allow writing into a non-empty directory. Files already there that the version does not describe are left alone."`

	Include []string `flag:"include" desc:"Restrict the download to this path in the volume, either a file or a directory whose contents are wanted, matched on slash boundaries. May be repeated. One that matches nothing fails the download."`
	Restart bool     `flag:"restart" desc:"Discard a partly downloaded tree from an earlier attempt instead of continuing it."`
}

// VolumeNamespaceList is the JSON output of `baseten volume namespace list`:
// the namespaces aggregated across all pages.
type VolumeNamespaceList struct {
	Items []string `json:"items"`
}

// VolumeList is the JSON output of `baseten volume list`: the namespace's
// volumes aggregated across all pages.
type VolumeList struct {
	Items []managementapi.Volume `json:"items"`
}

// VolumePushResult is the JSON output of `baseten volume push`.
type VolumePushResult struct {
	VersionRef     string `json:"version_ref"`
	ManifestDigest string `json:"manifest_digest"`
	Sequence       int64  `json:"sequence"`
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

// VolumeVersionDownloadResult is the JSON output of `baseten volume version
// download`.
type VolumeVersionDownloadResult struct {
	VersionRef     string `json:"version_ref"`
	ManifestDigest string `json:"manifest_digest"`
	OutDir         string `json:"out_dir"`
	Files          int64  `json:"files"`
	Bytes          int64  `json:"bytes"`
	// SelectedFiles and TotalFiles report what --include narrowed to, and are
	// equal when the whole version was downloaded.
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
