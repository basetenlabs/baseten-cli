package cmd

import (
	"fmt"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("volume tag set", commandVolumeTagSet)
	Register("volume tag delete", commandVolumeTagDelete)
}

func commandVolumeTagSet(ctx *CommandContext, flags *cmd.VolumeTagSetFlags) error {
	ref, err := volumeParseRef(flags.VolumeRef)
	if err != nil {
		return err
	}
	// A path is refused here because volumeVersionSelector ignores it.
	switch ref.Level() {
	case client.VolumeRefLevelNamespace:
		return cmd.NewErrUsagef(
			"--volume-ref %s names a namespace, and a tag belongs to a volume; write "+
				"'bdn:%s/<volume>', with a selector to pick a version other than head", ref, ref.Namespace)
	case client.VolumeRefLevelPath:
		return cmd.NewErrUsagef(
			"--volume-ref %s names a path, and a tag names a whole version; drop the path", ref)
	}
	tag := flags.TagName
	if err := volumeTagArg(ref, tag); err != nil {
		return err
	}
	body := managementapi.SetVolumeTagRequest{Tag: tag, Version: volumeVersionSelector(ref)}
	if flags.ExpiresAt.IsSet() {
		expiresAt, err := volumeExpiresAtArg(*flags.ExpiresAt.Pointer())
		if err != nil {
			return err
		}
		body.ExpiresAt = &expiresAt
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	resp, err := cl.API().PostVolumesTags(ctx, ref.Namespace, ref.Volume, body)
	if err != nil {
		return fmt.Errorf("setting tag %s on %s: %w", tag, ref, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Outputf("Ref:             %s\n", volumeRefTextOf(resp.VersionRef, flags.VolumeRefFlags))
	ctx.Outputf("Tag:             %s\n", resp.Tag)
	ctx.Outputf("Digest:          %s\n", resp.Digest)
	ctx.Outputf("Expires:         %s\n", volumeExpiryText(resp.ExpiresAt))
	ctx.Outputf("Volume sequence: %d\n", resp.VolumeSequence)
	return nil
}

func commandVolumeTagDelete(ctx *CommandContext, flags *cmd.VolumeTagDeleteFlags) error {
	ref, err := volumeParseRef(flags.VolumeRef)
	if err != nil {
		return err
	}
	switch ref.Level() {
	case client.VolumeRefLevelNamespace:
		return cmd.NewErrUsagef(
			"--volume-ref %s names a namespace, and a tag belongs to a volume; write "+
				"'bdn:%s/<volume>' and the tag in --tag-name", ref, ref.Namespace)
	case client.VolumeRefLevelPoint:
		return cmd.NewErrUsagef(
			"--volume-ref %s names a version, and a tag is deleted from the volume, not from a "+
				"version; write 'bdn:%s/%s' and the tag in --tag-name", ref, ref.Namespace, ref.Volume)
	case client.VolumeRefLevelPath:
		return cmd.NewErrUsagef(
			"--volume-ref %s names a path, and a tag belongs to the whole volume; write "+
				"'bdn:%s/%s' and the tag in --tag-name", ref, ref.Namespace, ref.Volume)
	}
	tag := flags.TagName
	if err := volumeTagArg(ref, tag); err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	resp, err := cl.API().DeleteVolumesTags(ctx, ref.Namespace, ref.Volume, tag,
		managementapi.DeleteVolumeTagRequest{})
	if err != nil {
		return fmt.Errorf("deleting tag %s from %s: %w", tag, ref, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	deleted := "no"
	if resp.Deleted {
		deleted = "yes"
	}
	ctx.Outputf("Tag:             %s\n", resp.Tag)
	ctx.Outputf("Deleted:         %s\n", deleted)
	ctx.Outputf("Volume sequence: %d\n", resp.VolumeSequence)
	return nil
}

// volumeTagArg checks --tag-name against the SDK's tag grammar, the only copy
// this client has. A refusal names the value the user typed. On delete the tag
// is a URL path segment, so a slash in it would come back from the API as a 404.
func volumeTagArg(ref client.VolumeRef, tag string) error {
	parsed, err := client.ParseVolumeRef(volumeRefScheme + ref.Namespace + "/" + ref.Volume + ":" + tag)
	if err != nil || parsed.Tag != tag {
		return cmd.NewErrUsagef(
			"--tag-name %q must begin with a letter, digit, or underscore, hold only those plus "+
				"dots and hyphens, and be at most 128 characters", tag)
	}
	return nil
}
