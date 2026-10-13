package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/sandbox"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

func init() {
	Register("sandbox image list", commandSandboxImageList)
	Register("sandbox image describe", commandSandboxImageDescribe)
	Register("sandbox image push", commandSandboxImagePush)
	Register("sandbox image delete", commandSandboxImageDelete)
	Register("sandbox image logs", commandSandboxImageLogs)
	Register("sandbox image list-tags", commandSandboxImageListTags)
	Register("sandbox image list-library", commandSandboxImageListLibrary)
}

func commandSandboxImageList(ctx *CommandContext, flags *cmd.SandboxImageListFlags) error {
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	params := managementapi.ListImagesParams{TeamId: teamID}
	items, truncated, err := sandboxCollectPages(flags.Limit, func(cursor *string, pageLimit int) ([]managementapi.SandboxImageSummary, managementapi.SandboxApiPagination, error) {
		params.Cursor, params.Limit = cursor, &pageLimit
		page, err := client.API().ListImages(ctx, params)
		if err != nil {
			return nil, managementapi.SandboxApiPagination{}, err
		}
		return page.Items, page.Pagination, nil
	})
	if err != nil {
		return fmt.Errorf("listing sandbox images: %w", err)
	}
	if truncated {
		ctx.Logf("Reached the --limit of %d; more exist. Increase --limit or use --limit 0 for no limit.\n", flags.Limit)
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.SandboxImageList{Items: items})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No sandbox images found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		tags, size := "-", "-"
		if item.TagCount != nil {
			tags = strconv.FormatInt(*item.TagCount, 10)
		}
		if item.Size != nil {
			size = formatBytes(*item.Size)
		}
		rows = append(rows, []string{item.Name, string(item.Status), tags, size, sandboxFormatTime(item.CreatedAt)})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"NAME", "STATUS", "TAGS", "SIZE", "CREATED"},
		Rows:                rows,
		RightAlignedColumns: []int{2, 3},
	})
	return nil
}

func commandSandboxImageDescribe(ctx *CommandContext, flags *cmd.SandboxImageRefCommandFlags) error {
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	image, err := client.API().GetImage(ctx, flags.Name, managementapi.GetImageParams{TeamId: teamID})
	if err != nil {
		return fmt.Errorf("describing sandbox image %s: %w", flags.Name, err)
	}
	sandboxOutputImage(ctx, image)
	return nil
}

func commandSandboxImagePush(ctx *CommandContext, flags *cmd.SandboxImagePushFlags) error {
	options := sandbox.ImagePushOptions{Name: flags.Name, NoWait: true}
	switch {
	case flags.Dir != "":
		if flags.DockerConfig != "" {
			return cmd.NewErrUsagef("--docker-config only applies with --registry-image")
		}
		options.SourceDirectory = flags.Dir
		// Docker's own .dockerignore reader and matcher, so what is left out
		// of the upload is what Docker would leave out of the build.
		options.IgnoreFileProcessor = func(_ context.Context, opts sandbox.ImageIgnoreFileProcessorOptions) (sandbox.ImageIgnoreFileFunc, error) {
			patterns, err := ignorefile.ReadAll(bytes.NewReader(opts.Contents))
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", opts.Path, err)
			}
			matcher, err := patternmatcher.New(patterns)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", opts.Path, err)
			}
			return func(_ context.Context, opts sandbox.ImageIgnoreFileOptions) (bool, error) {
				ignored, err := matcher.MatchesOrParentMatches(opts.RelPath)
				if err != nil || !ignored || !opts.Entry.IsDir() {
					return ignored, err
				}
				// An exception pattern under an ignored directory may bring
				// back some of its contents, so they are matched one by one.
				// Docker decides this the same way, by the pattern's text.
				for _, pattern := range matcher.Patterns() {
					if pattern.Exclusion() && strings.HasPrefix(filepath.ToSlash(pattern.String())+"/", opts.RelPath+"/") {
						return false, nil
					}
				}
				return true, nil
			}, nil
		}
	case flags.RegistryImage != "":
		options.SourceRegistry = &sandbox.ImageRegistrySource{Image: flags.RegistryImage}
		if flags.DockerConfig != "" {
			dockerConfig, err := os.ReadFile(flags.DockerConfig)
			if err != nil {
				return cmd.NewErrUsagef("reading --docker-config: %v", err)
			}
			options.SourceRegistry.DockerConfig = string(dockerConfig)
		}
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	if _, err := client.Images().Push(ctx, options); err != nil {
		if ctx.Err() != nil {
			return cmd.NewErrInterrupted(fmt.Errorf("stopped before the push of sandbox image %s finished, so nothing was built", flags.Name))
		}
		return fmt.Errorf("pushing sandbox image %s: %w", flags.Name, err)
	}
	if flags.Wait {
		ctx.Logf("Pushed sandbox image %s. Waiting for it to build; press Ctrl+C to stop waiting, and the build continues.\n", flags.Name)
		_, err := client.Images().WaitBuilt(ctx, sandbox.ImageWaitBuiltOptions{Name: flags.Name, Timeout: -1})
		var buildErr *sandbox.ImageBuildError
		switch {
		case ctx.Err() != nil:
			return cmd.NewErrInterrupted(fmt.Errorf("stopped waiting; sandbox image %s is still building. Check it with: baseten sandbox image describe --name %s", flags.Name, flags.Name))
		case errors.As(err, &buildErr):
			return fmt.Errorf("sandbox image %s failed to build. See why with: baseten sandbox image logs --name %s", flags.Name, flags.Name)
		case err != nil:
			return fmt.Errorf("waiting for sandbox image %s: %w", flags.Name, err)
		}
	}
	image, err := client.API().GetImage(ctx, flags.Name, managementapi.GetImageParams{TeamId: teamID})
	if err != nil {
		return fmt.Errorf("describing sandbox image %s: %w", flags.Name, err)
	}
	sandboxOutputImage(ctx, image)
	return nil
}

func commandSandboxImageDelete(ctx *CommandContext, flags *cmd.SandboxImageDeleteFlags) error {
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf("Delete sandbox image %s and every version of it? This cannot be undone.", flags.Name)); err != nil {
			return err
		}
	}
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	image, err := client.API().DeleteImage(ctx, flags.Name, managementapi.DeleteImageParams{TeamId: teamID})
	if err != nil {
		return fmt.Errorf("deleting sandbox image %s: %w", flags.Name, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(image)
		return nil
	}
	ctx.Logf("Deleting sandbox image %s.\n", flags.Name)
	return nil
}

func commandSandboxImageLogs(ctx *CommandContext, flags *cmd.SandboxImageLogsFlags) error {
	client, _, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	lines, err := client.Images().Logs(ctx, sandbox.ImageLogsOptions{Name: flags.Name, StartTime: flags.Start, EndTime: flags.End})
	if err != nil {
		return fmt.Errorf("getting build logs of sandbox image %s: %w", flags.Name, err)
	}
	if ctx.JSON {
		writer := ctx.NewJSONArrayWriter()
		defer writer.Close()
		for _, line := range lines {
			writer.Write(managementapi.SandboxImageBuildLog{Timestamp: line.Timestamp, Severity: line.Severity, Message: line.Text})
		}
		return nil
	}
	if len(lines) == 0 {
		ctx.LogLine("No build logs found.")
		return nil
	}
	for _, line := range lines {
		ctx.Outputf("%s %s\n", line.Timestamp.UTC().Format(time.RFC3339), strings.TrimSuffix(line.Text, "\n"))
	}
	return nil
}

func commandSandboxImageListTags(ctx *CommandContext, flags *cmd.SandboxImageRefCommandFlags) error {
	client, teamID, err := ctx.NewSandboxClient(flags.Team)
	if err != nil {
		return err
	}
	params := managementapi.ListImageTagsParams{TeamId: teamID}
	items, _, err := sandboxCollectPages(0, func(cursor *string, pageLimit int) ([]managementapi.SandboxImageTag, managementapi.SandboxApiPagination, error) {
		params.Cursor, params.Limit = cursor, &pageLimit
		page, err := client.API().ListImageTags(ctx, flags.Name, params)
		if err != nil {
			return nil, managementapi.SandboxApiPagination{}, err
		}
		return page.Items, page.Pagination, nil
	})
	if err != nil {
		return fmt.Errorf("listing tags of sandbox image %s: %w", flags.Name, err)
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.SandboxImageTagList{Items: items})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No tags found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		size := "-"
		if item.Size != nil {
			size = formatBytes(*item.Size)
		}
		rows = append(rows, []string{item.Name, size, sandboxFormatTime(item.CreatedAt)})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"TAG", "SIZE", "CREATED"},
		Rows:                rows,
		RightAlignedColumns: []int{1},
	})
	return nil
}

func commandSandboxImageListLibrary(ctx *CommandContext, _ *cmd.SandboxImageListLibraryFlags) error {
	client, _, err := ctx.NewSandboxClient("")
	if err != nil {
		return err
	}
	response, err := client.API().ListSandboxLibraryImages(ctx)
	if err != nil {
		return fmt.Errorf("listing starter sandbox images: %w", err)
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.SandboxImageLibraryList{Items: response.Items})
		return nil
	}
	if len(response.Items) == 0 {
		ctx.LogLine("No starter images found.")
		return nil
	}
	rows := make([][]string, 0, len(response.Items))
	for _, item := range response.Items {
		name, memory, categories := item.Name, "-", ""
		if item.DisplayName != nil && *item.DisplayName != "" {
			name = *item.DisplayName
		}
		if item.Memory != nil {
			memory = strconv.Itoa(*item.Memory) + " MB"
		}
		if item.Categories != nil {
			categories = strings.Join(*item.Categories, ", ")
		}
		rows = append(rows, []string{name, item.Image, memory, categories})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"NAME", "IMAGE", "MEMORY", "CATEGORIES"},
		Rows:                rows,
		RightAlignedColumns: []int{2},
	})
	return nil
}

func sandboxOutputImage(ctx *CommandContext, image *managementapi.SandboxImage) {
	if ctx.JSON {
		ctx.OutputJSON(image)
		return
	}
	ctx.Outputf("Name:           %s\n", image.Name)
	ctx.Outputf("Status:         %s\n", image.Status)
	if image.TagCount != nil {
		ctx.Outputf("Tags:           %d\n", *image.TagCount)
	}
	if image.Size != nil {
		ctx.Outputf("Size:           %s\n", formatBytes(*image.Size))
	}
	sandboxOutputField(ctx, "Created:        ", sandboxFormatTime(image.CreatedAt))
	sandboxOutputField(ctx, "Last deployed:  ", sandboxFormatTime(image.LastDeployedAt))
}
