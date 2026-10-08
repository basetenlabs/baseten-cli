package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

var commandVolumeTag = Command{
	Name:    "tag",
	Summary: "Set and delete volume tags (PRE-RELEASE)",
	Description: volumePreRelease +
		"Manage the tags of a volume. A tag is a mutable name pointing at one version, so " +
		"setting it again moves it, and the version it pointed at before stays reachable by " +
		"digest. 'volume stat' and 'volume versions' show a volume's tags, and so does " +
		"'volume ls' on a namespace.\n\n" + volumeRefGrammar,
	Children: []Command{
		{
			Name:    "set",
			Summary: "Point a tag at a version (PRE-RELEASE)",
			Description: volumePreRelease +
				"Points --tag-name at the version --volume-ref names and creates the tag if it does " +
				"not exist. A volume ref with no selector tags the version head points at. A ':<tag>' " +
				"or '@<digest>' selector tags that version. A path is an error, since a tag names a " +
				"whole version.\n\n" +
				"Setting a tag that already exists overwrites it. Pass --expires-at to make the tag " +
				"expire at that time. Setting it again without --expires-at makes it permanent. A " +
				"version's or the volume's own expiration still deletes the tag.\n\n" +
				"--tag-name may be 'head'. Setting it changes which version a ref with no tag or " +
				"digest resolves to. 'head' cannot take --expires-at and cannot point at an expiring " +
				"version.\n\n" +
				volumeRefGrammar,
			Flags: VolumeTagSetFlags{},
			Output: &CommandOutput[managementapi.SetVolumeTagResponse]{
				TextDescription: "One field per line: the ref of the version the tag now points at, " +
					"the tag, the digest, when the tag expires, and the volume's sequence.",
				Examples: []CommandExample{
					{
						Description: "Point a tag at a version.",
						Command:     "baseten volume tag set --volume-ref bdn:<namespace>/<volume>@b3:<digest> --tag-name prod",
					},
					{
						Description: "Move a tag to the version head points at.",
						Command:     "baseten volume tag set --volume-ref bdn:<namespace>/<volume> --tag-name prod",
					},
					{
						Description: "Point a tag at the version another tag names, expiring at the start of 2030.",
						CommandLines: []string{
							"baseten volume tag set",
							"--volume-ref bdn:<namespace>/<volume>:canary",
							"--tag-name nightly",
							"--expires-at 2030-01-01T00:00:00Z",
						},
					},
					{
						Description: "Move head, which is what refs without a tag or digest resolve to.",
						Command:     "baseten volume tag set --volume-ref bdn:<namespace>/<volume>@b3:<digest> --tag-name head",
					},
				},
				JQExample: CommandExample{
					Description: "Print the ref of the version the tag now points at, pinned to its digest.",
					CommandLines: []string{
						"baseten volume tag set",
						"--volume-ref bdn:<namespace>/<volume>@b3:<digest>",
						"--tag-name prod",
						"--jq '.version_ref'",
					},
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a volume tag (PRE-RELEASE)",
			Description: volumePreRelease +
				"Deletes --tag-name from the volume --volume-ref names. The version it pointed at " +
				"stays, reachable by digest and by any other tag. Deleting a tag the volume does not " +
				"have succeeds and reports that nothing was deleted.\n\n" +
				"--volume-ref must name a volume, with no selector and no path. A tag belongs to the " +
				"volume, not to one version. Deleting 'head' makes refs without a tag or digest stop " +
				"resolving until head is set again.\n\n" + volumeRefGrammar,
			Flags: VolumeTagDeleteFlags{},
			Output: &CommandOutput[managementapi.DeleteVolumeTagResponse]{
				TextDescription: "One field per line: the tag, whether it existed and was deleted, " +
					"and the volume's sequence.",
				Examples: []CommandExample{
					{
						Description: "Delete a tag.",
						Command:     "baseten volume tag delete --volume-ref bdn:<namespace>/<volume> --tag-name prod",
					},
				},
				JQExample: CommandExample{
					Description: "Print whether the tag existed.",
					Command:     "baseten volume tag delete --volume-ref bdn:<namespace>/<volume> --tag-name prod --jq '.deleted'",
				},
			},
		},
	},
}

type VolumeTagSetFlags struct {
	CommandFlags
	VolumeRefFlags

	VolumeRef string               `flag:"volume-ref" desc:"Ref of the version to tag, as bdn:<namespace>/<volume> for the version head points at, or with a :<tag> or @<digest> selector for that version." required:"true"`
	TagName   string               `flag:"tag-name" desc:"Tag to point at the version. May be 'head'." required:"true"`
	ExpiresAt OptionalFlag[string] `flag:"expires-at" desc:"Time at which the tag expires, as an RFC 3339 date-time with a UTC offset, to the whole second, as in 2030-01-01T00:00:00Z. It must be in the future and at most ten years ahead. Omit it for a tag that does not expire. Omitting it on an existing tag clears its expiration time."`
}

type VolumeTagDeleteFlags struct {
	CommandFlags

	VolumeRef string `flag:"volume-ref" desc:"Ref of the volume the tag belongs to, as bdn:<namespace>/<volume>, with no selector and no path." required:"true"`
	TagName   string `flag:"tag-name" desc:"Tag to delete. May be 'head'." required:"true"`
}
