package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	publiccmd "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("volume sync start", commandVolumeSyncStart)
	Register("volume sync describe", commandVolumeSyncDescribe)
	Register("volume sync list", commandVolumeSyncList)
	Register("volume sync cancel", commandVolumeSyncCancel)
}

const volumeSyncPollInterval = 2 * time.Second

type volumeSyncAWSAssumeRole struct {
	RoleARN string `json:"role_arn"`
	Region  string `json:"region"`
}

type volumeSyncSourceRequest struct {
	Type           string                   `json:"type"`
	URI            string                   `json:"uri"`
	Include        []string                 `json:"include"`
	Exclude        []string                 `json:"exclude"`
	AuthSecretName string                   `json:"auth_secret_name,omitempty"`
	AWSAssumeRole  *volumeSyncAWSAssumeRole `json:"aws_assume_role,omitempty"`
}

type volumeSyncCreateRequest struct {
	Source      volumeSyncSourceRequest         `json:"source"`
	Destination publiccmd.VolumeSyncDestination `json:"destination"`
}

type volumeSyncPage struct {
	Items      []publiccmd.VolumeSync `json:"items"`
	Pagination struct {
		Cursor  *string `json:"cursor"`
		HasMore bool    `json:"has_more"`
	} `json:"pagination"`
}

func commandVolumeSyncStart(ctx *CommandContext, flags *publiccmd.VolumeSyncStartFlags) error {
	source, err := volumeSyncSourceFromFlags(flags)
	if err != nil {
		return err
	}
	destination, err := volumeSyncDestination(flags.Destination)
	if err != nil {
		return err
	}

	var sync publiccmd.VolumeSync
	err = volumeSyncRequest(ctx, http.MethodPost, "/v1/volumes/syncs", nil,
		volumeSyncCreateRequest{
			Source:      source,
			Destination: publiccmd.VolumeSyncDestination{Ref: destination},
		}, &sync)
	if err != nil {
		return fmt.Errorf("starting volume sync: %w", err)
	}

	if flags.Wait {
		sync, err = waitVolumeSync(ctx, sync)
		if err != nil {
			return err
		}
	}
	outputVolumeSync(ctx, sync)
	if flags.Wait {
		return volumeSyncTerminalError(ctx, sync)
	}
	return nil
}

func commandVolumeSyncDescribe(ctx *CommandContext, flags *publiccmd.VolumeSyncIDFlags) error {
	sync, err := getVolumeSync(ctx, flags.VolumeSyncID)
	if err != nil {
		return fmt.Errorf("describing volume sync %s: %w", flags.VolumeSyncID, err)
	}
	outputVolumeSync(ctx, sync)
	return nil
}

func commandVolumeSyncCancel(ctx *CommandContext, flags *publiccmd.VolumeSyncIDFlags) error {
	var sync publiccmd.VolumeSync
	path := "/v1/volumes/syncs/" + url.PathEscape(flags.VolumeSyncID) + "/cancel"
	if err := volumeSyncRequest(ctx, http.MethodPost, path, nil, nil, &sync); err != nil {
		return fmt.Errorf("canceling volume sync %s: %w", flags.VolumeSyncID, err)
	}
	outputVolumeSync(ctx, sync)
	return nil
}

func commandVolumeSyncList(ctx *CommandContext, flags *publiccmd.VolumeSyncListFlags) error {
	query := url.Values{"limit": {"100"}}
	if flags.Destination != "" {
		destination, err := volumeSyncDestination(flags.Destination)
		if err != nil {
			return err
		}
		query.Set("ref", destination)
	}

	var items []publiccmd.VolumeSync
	for {
		var page volumeSyncPage
		if err := volumeSyncRequest(ctx, http.MethodGet, "/v1/volumes/syncs", query, nil, &page); err != nil {
			return fmt.Errorf("listing volume syncs: %w", err)
		}
		items = append(items, page.Items...)
		if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
			break
		}
		query.Set("cursor", *page.Pagination.Cursor)
	}

	if ctx.JSON {
		ctx.OutputJSON(publiccmd.VolumeSyncList{Items: items})
		return nil
	}
	if len(items) == 0 {
		ctx.LogLine("No volume syncs found.")
		return nil
	}
	rows := make([][]string, 0, len(items))
	for _, sync := range items {
		size := "-"
		if sync.TotalSizeBytes != nil {
			size = formatBytes(*sync.TotalSizeBytes)
		}
		completed := "-"
		if sync.CompletedAt != nil {
			completed = sync.CompletedAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{
			sync.SyncID,
			sync.Status,
			sync.Source.URI,
			sync.Destination.Ref,
			size,
			sync.CreatedAt.UTC().Format(time.RFC3339),
			completed,
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"ID", "STATUS", "SOURCE", "DESTINATION", "SIZE", "CREATED", "COMPLETED"},
		Rows:                rows,
		RightAlignedColumns: []int{4},
	})
	return nil
}

func volumeSyncSourceFromFlags(flags *publiccmd.VolumeSyncStartFlags) (volumeSyncSourceRequest, error) {
	uri := strings.TrimSpace(flags.Source)
	sourceType := ""
	switch {
	case strings.HasPrefix(uri, "hf://"):
		sourceType = "HUGGING_FACE"
	case strings.HasPrefix(uri, "s3://"):
		sourceType = "S3"
	case strings.HasPrefix(uri, "gs://"):
		sourceType = "GCS"
	case strings.HasPrefix(uri, "azure://"):
		sourceType = "AZURE"
	case strings.HasPrefix(uri, "r2://"):
		sourceType = "R2"
	case strings.HasPrefix(uri, "cw://"):
		sourceType = "COREWEAVE"
	case strings.HasPrefix(uri, "bt://"):
		sourceType = "BASETEN_TRAINING"
	default:
		return volumeSyncSourceRequest{}, publiccmd.NewErrUsagef(
			"unsupported source %q: want hf://, s3://, gs://, azure://, r2://, cw://, or bt://", uri)
	}

	arn := strings.TrimSpace(flags.AuthAWSAssumeRoleARN)
	region := strings.TrimSpace(flags.AuthAWSAssumeRoleRegion)
	secret := strings.TrimSpace(flags.AuthSecretName)
	if (arn == "") != (region == "") {
		return volumeSyncSourceRequest{}, publiccmd.NewErrUsagef(
			"--auth-aws-assume-role-arn and --auth-aws-assume-role-region must be provided together")
	}
	if secret != "" && arn != "" {
		return volumeSyncSourceRequest{}, publiccmd.NewErrUsagef(
			"--auth-secret-name and --auth-aws-assume-role-* are mutually exclusive")
	}
	if arn != "" && sourceType != "S3" {
		return volumeSyncSourceRequest{}, publiccmd.NewErrUsagef(
			"--auth-aws-assume-role-* is supported only for an s3:// source")
	}
	if secret != "" && sourceType == "BASETEN_TRAINING" {
		return volumeSyncSourceRequest{}, publiccmd.NewErrUsagef(
			"a bt:// source does not accept authentication flags")
	}

	source := volumeSyncSourceRequest{
		Type:           sourceType,
		URI:            uri,
		Include:        append([]string{}, flags.Include...),
		Exclude:        append([]string{}, flags.Exclude...),
		AuthSecretName: secret,
	}
	if arn != "" {
		source.AWSAssumeRole = &volumeSyncAWSAssumeRole{RoleARN: arn, Region: region}
	}
	return source, nil
}

func volumeSyncDestination(raw string) (string, error) {
	ref, err := volumeParseRef(raw)
	if err != nil {
		return "", err
	}
	if ref.Volume == "" {
		return "", publiccmd.NewErrUsagef("destination %s names a namespace; want bdn:<namespace>/<volume>", ref)
	}
	if ref.Path != "" {
		return "", publiccmd.NewErrUsagef("destination %s carries a path; want a volume or tag ref", ref)
	}
	if ref.Digest != "" {
		return "", publiccmd.NewErrUsagef("destination %s carries an immutable digest; want a volume or tag ref", ref)
	}
	return ref.String(), nil
}

func getVolumeSync(ctx *CommandContext, syncID string) (publiccmd.VolumeSync, error) {
	var sync publiccmd.VolumeSync
	path := "/v1/volumes/syncs/" + url.PathEscape(syncID)
	err := volumeSyncRequest(ctx, http.MethodGet, path, nil, nil, &sync)
	return sync, err
}

func waitVolumeSync(ctx *CommandContext, sync publiccmd.VolumeSync) (publiccmd.VolumeSync, error) {
	lastStatus := ""
	for {
		if sync.Status != lastStatus {
			ctx.Logf("Status: %s\n", sync.Status)
			lastStatus = sync.Status
		}
		switch sync.Status {
		case "READY", "FAILED", "CANCELED":
			return sync, nil
		case "PENDING", "SYNCING":
			// Keep polling.
		default:
			return publiccmd.VolumeSync{}, fmt.Errorf(
				"volume sync %s returned unknown status %q", sync.SyncID, sync.Status)
		}
		if err := ctx.Sleep(volumeSyncPollInterval); err != nil {
			return publiccmd.VolumeSync{}, err
		}
		syncID := sync.SyncID
		var err error
		sync, err = getVolumeSync(ctx, syncID)
		if err != nil {
			return publiccmd.VolumeSync{}, fmt.Errorf("waiting for volume sync %s: %w", syncID, err)
		}
	}
}

func volumeSyncTerminalError(ctx *CommandContext, sync publiccmd.VolumeSync) error {
	switch sync.Status {
	case "FAILED":
		ctx.SuppressJSONError()
		if sync.Error != nil && sync.Error.Message != "" {
			return fmt.Errorf("volume sync %s failed: %s", sync.SyncID, sync.Error.Message)
		}
		return fmt.Errorf("volume sync %s failed", sync.SyncID)
	case "CANCELED":
		ctx.SuppressJSONError()
		return fmt.Errorf("volume sync %s was canceled", sync.SyncID)
	default:
		return nil
	}
}

func outputVolumeSync(ctx *CommandContext, sync publiccmd.VolumeSync) {
	if ctx.JSON {
		ctx.OutputJSON(sync)
		return
	}
	ctx.Outputf("ID:          %s\n", sync.SyncID)
	ctx.Outputf("Status:      %s\n", sync.Status)
	ctx.Outputf("Source:      %s\n", sync.Source.URI)
	ctx.Outputf("Destination: %s\n", sync.Destination.Ref)
	ctx.Outputf("Created:     %s\n", sync.CreatedAt.UTC().Format(time.RFC3339))
	if sync.CompletedAt != nil {
		ctx.Outputf("Completed:   %s\n", sync.CompletedAt.UTC().Format(time.RFC3339))
	}
	if sync.VolumeVersionID != nil {
		ctx.Outputf("Version ID:  %s\n", *sync.VolumeVersionID)
	}
	if sync.VersionRef != nil {
		ctx.Outputf("Version ref: %s\n", *sync.VersionRef)
	}
	if sync.ContentDigest != nil {
		ctx.Outputf("Digest:      %s\n", *sync.ContentDigest)
	}
	if sync.TotalSizeBytes != nil {
		ctx.Outputf("Size:        %s\n", formatBytes(*sync.TotalSizeBytes))
	}
	if sync.Error != nil {
		ctx.Outputf("Error code:  %s\n", sync.Error.Code)
		ctx.Outputf("Error:       %s\n", sync.Error.Message)
	}
}

// volumeSyncRequest uses the management client's configured base URL, auth
// transport, and headers while the unstable endpoints are not yet in the
// released generated client.
func volumeSyncRequest(
	ctx *CommandContext,
	method, path string,
	query url.Values,
	body any,
	out any,
) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()
	requestURL := strings.TrimRight(api.BaseURL, "/") + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, values := range api.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := api.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &managementapi.ResponseError{StatusCode: resp.StatusCode, Body: string(responseBody)}
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}
