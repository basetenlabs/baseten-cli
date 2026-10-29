package cmd

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("sandbox volume list", commandSandboxVolumeList)
	Register("sandbox volume describe", commandSandboxVolumeDescribe)
	Register("sandbox volume create", commandSandboxVolumeCreate)
	Register("sandbox volume update", commandSandboxVolumeUpdate)
	Register("sandbox volume delete", commandSandboxVolumeDelete)
}

func commandSandboxVolumeList(ctx *CommandContext, flags *cmd.SandboxVolumeListFlags) error {
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	params := managementapi.ListSandboxVolumesParams{TeamId: teamID}
	if flags.Query != "" {
		params.Q = &flags.Query
	}
	if len(flags.Status) > 0 {
		statuses := make([]managementapi.SandboxVolumeStatus, 0, len(flags.Status))
		for _, status := range flags.Status {
			statuses = append(statuses, managementapi.SandboxVolumeStatus(enumToAPIValue(status)))
		}
		params.Status = &statuses
	}
	items, truncated, err := sandboxCollectPages(flags.Limit, func(cursor *string, pageLimit int) ([]managementapi.SandboxVolume, managementapi.SandboxApiPagination, error) {
		params.Cursor, params.Limit = cursor, &pageLimit
		page, err := client.API().ListSandboxVolumes(ctx, params)
		if err != nil {
			return nil, managementapi.SandboxApiPagination{}, err
		}
		return page.Items, page.Pagination, nil
	})
	if err != nil {
		return fmt.Errorf("listing sandbox volumes: %w", err)
	}
	if truncated {
		ctx.Logf("Reached the --limit of %d; more exist. Increase --limit or use --limit 0 for no limit.\n", flags.Limit)
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.SandboxVolumeList{Items: normalizeJSONSlice(items)})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No sandbox volumes found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		attachedTo := deref(item.AttachedTo)
		if attachedTo == "" {
			attachedTo = "-"
		}
		rows = append(rows, []string{
			deref(item.Name),
			string(item.Status),
			sandboxVolumeFormatSize(item.Size),
			item.Region,
			attachedTo,
			sandboxFormatTime(item.CreatedAt),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"NAME", "STATUS", "SIZE", "REGION", "ATTACHED TO", "CREATED"},
		Rows:                rows,
		RightAlignedColumns: []int{2},
	})
	return nil
}

func commandSandboxVolumeDescribe(ctx *CommandContext, flags *cmd.SandboxVolumeRefCommandFlags) error {
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	volume, err := client.API().GetSandboxVolume(ctx, flags.Name, managementapi.GetSandboxVolumeParams{TeamId: teamID})
	if err != nil {
		return fmt.Errorf("describing sandbox volume %s: %w", flags.Name, err)
	}
	sandboxVolumeOutputRecord(ctx, volume)
	return nil
}

func commandSandboxVolumeCreate(ctx *CommandContext, flags *cmd.SandboxVolumeCreateFlags) error {
	labels, err := sandboxParseKeyValues("label", flags.Label)
	if err != nil {
		return err
	}
	request := managementapi.CreateSandboxVolumeRequest{}
	if size, mi, isMi := sandboxVolumeParseSize(flags.Size); isMi {
		err = request.Size.FromCreateSandboxVolumeRequestSize1(mi)
	} else {
		err = request.Size.FromCreateSandboxVolumeRequestSize0(size)
	}
	if err != nil {
		return fmt.Errorf("encoding --size: %w", err)
	}
	if flags.Name != "" {
		request.Name = &flags.Name
	}
	if flags.Region != "" {
		request.Region = &flags.Region
	}
	if labels != nil {
		volumeLabels := managementapi.SandboxMetadataLabels(labels)
		request.Labels = &volumeLabels
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	volume, err := client.API().CreateSandboxVolume(ctx, managementapi.CreateSandboxVolumeParams{TeamId: teamID}, request)
	if err != nil {
		return fmt.Errorf("creating sandbox volume: %w", err)
	}
	sandboxVolumeOutputRecord(ctx, volume)
	return nil
}

func commandSandboxVolumeUpdate(ctx *CommandContext, flags *cmd.SandboxVolumeUpdateFlags) error {
	if flags.ClearLabels && len(flags.Label) > 0 {
		return cmd.NewErrUsagef("--label and --clear-labels cannot be combined")
	}
	labels, err := sandboxParseKeyValues("label", flags.Label)
	if err != nil {
		return err
	}
	request := managementapi.UpdateSandboxVolumeRequest{}
	if ctx.Command.Flags().Changed("size") {
		request.Size = &managementapi.UpdateSandboxVolumeRequest_Size{}
		if size, mi, isMi := sandboxVolumeParseSize(flags.Size); isMi {
			err = request.Size.FromUpdateSandboxVolumeRequestSize1(mi)
		} else {
			err = request.Size.FromUpdateSandboxVolumeRequestSize0(size)
		}
		if err != nil {
			return fmt.Errorf("encoding --size: %w", err)
		}
	}
	// An empty map is sent, not left out, so the server removes every label.
	if flags.ClearLabels {
		labels = map[string]string{}
	}
	if labels != nil {
		volumeLabels := managementapi.SandboxMetadataLabels(labels)
		request.Labels = &volumeLabels
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	volume, err := client.API().UpdateSandboxVolume(ctx, flags.Name, managementapi.UpdateSandboxVolumeParams{TeamId: teamID}, request)
	if err != nil {
		return fmt.Errorf("updating sandbox volume %s: %w", flags.Name, err)
	}
	sandboxVolumeOutputRecord(ctx, volume)
	return nil
}

func commandSandboxVolumeDelete(ctx *CommandContext, flags *cmd.SandboxVolumeDeleteFlags) error {
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete sandbox volume %s and all its data? This cannot be undone.", flags.Name)); err != nil {
			return err
		}
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	volume, err := client.API().DeleteSandboxVolume(ctx, flags.Name, managementapi.DeleteSandboxVolumeParams{TeamId: teamID})
	if err != nil {
		return fmt.Errorf("deleting sandbox volume %s: %w", flags.Name, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(volume)
		return nil
	}
	ctx.Logf("Deleting sandbox volume %s.\n", flags.Name)
	return nil
}

// sandboxVolumeParseSize reads a --size: a bare integer is a number of Mi,
// sent as a number, and anything else a size string such as 10Gi.
func sandboxVolumeParseSize(value string) (size string, mi int, isMi bool) {
	if mi, err := strconv.Atoi(value); err == nil {
		return "", mi, true
	}
	return value, 0, false
}

// sandboxVolumeFormatSize shows a volume's size in whichever form the server
// returned it.
func sandboxVolumeFormatSize(size managementapi.SandboxVolume_Size) string {
	if value, err := size.AsSandboxVolumeSize0(); err == nil {
		return value
	}
	if mi, err := size.AsSandboxVolumeSize1(); err == nil {
		return strconv.Itoa(mi) + "Mi"
	}
	return "-"
}

func sandboxVolumeOutputRecord(ctx *CommandContext, volume *managementapi.SandboxVolume) {
	if ctx.JSON {
		ctx.OutputJSON(volume)
		return
	}
	ctx.Outputf("Name:         %s\n", deref(volume.Name))
	ctx.Outputf("Status:       %s\n", volume.Status)
	ctx.Outputf("Size:         %s\n", sandboxVolumeFormatSize(volume.Size))
	sandboxOutputField(ctx, "Region:       ", volume.Region)
	sandboxOutputField(ctx, "Attached to:  ", deref(volume.AttachedTo))
	if volume.Labels != nil && len(*volume.Labels) > 0 {
		labels := *volume.Labels
		pairs := make([]string, 0, len(labels))
		// Sorted, so the same labels always print the same way.
		for _, key := range slices.Sorted(maps.Keys(labels)) {
			pairs = append(pairs, key+"="+labels[key])
		}
		ctx.Outputf("Labels:       %s\n", strings.Join(pairs, ", "))
	}
	sandboxOutputField(ctx, "Created:      ", sandboxFormatTime(volume.CreatedAt))
	sandboxOutputField(ctx, "Created by:   ", deref(volume.CreatedBy))
	sandboxOutputField(ctx, "Updated:      ", sandboxFormatTime(volume.UpdatedAt))
	sandboxOutputField(ctx, "Updated by:   ", deref(volume.UpdatedBy))
}
