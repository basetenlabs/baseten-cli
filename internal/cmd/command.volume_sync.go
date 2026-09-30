package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("volume sync start", commandVolumeSyncStart)
	Register("volume sync describe", commandVolumeSyncDescribe)
	Register("volume sync list", commandVolumeSyncList)
	Register("volume sync cancel", commandVolumeSyncCancel)
}

const volumeSyncPollInterval = 2 * time.Second

func commandVolumeSyncStart(ctx *CommandContext, flags *cmd.VolumeSyncStartFlags) error {
	source, err := volumeSyncSourceFromFlags(flags)
	if err != nil {
		return err
	}
	destination, err := volumeSyncDestination(flags.Destination)
	if err != nil {
		return err
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()
	sync, err := api.PostVolumesSyncs(ctx, managementapi.CreateVolumeSyncRequest{
		Source:      source,
		Destination: managementapi.VolumeSyncDestination{Ref: destination},
	})
	if err != nil {
		return fmt.Errorf("starting volume sync: %w", err)
	}

	if flags.Wait {
		waited, err := waitVolumeSync(ctx, api, *sync)
		if err != nil {
			return err
		}
		sync = &waited
	}
	outputVolumeSync(ctx, *sync)
	if flags.Wait {
		return volumeSyncTerminalError(ctx, *sync)
	}
	return nil
}

func commandVolumeSyncDescribe(ctx *CommandContext, flags *cmd.VolumeSyncIDFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	sync, err := getVolumeSync(ctx, cl.API(), flags.SyncID)
	if err != nil {
		return fmt.Errorf("describing volume sync %s: %w", flags.SyncID, err)
	}
	outputVolumeSync(ctx, sync)
	return nil
}

func commandVolumeSyncCancel(ctx *CommandContext, flags *cmd.VolumeSyncIDFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	sync, err := cl.API().PostVolumesSyncsCancel(ctx, flags.SyncID)
	if err != nil {
		return fmt.Errorf("canceling volume sync %s: %w", flags.SyncID, err)
	}
	outputVolumeSync(ctx, *sync)
	return nil
}

func commandVolumeSyncList(ctx *CommandContext, flags *cmd.VolumeSyncListFlags) error {
	params := managementapi.GetV1VolumesSyncsParams{}
	if flags.Destination != "" {
		destination, err := volumeSyncDestination(flags.Destination)
		if err != nil {
			return err
		}
		params.Ref = &destination
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	var items []managementapi.VolumeSync
	for {
		page, err := cl.API().GetVolumesSyncs(ctx, params)
		if err != nil {
			return fmt.Errorf("listing volume syncs: %w", err)
		}
		items = append(items, page.Items...)
		if !page.Pagination.HasMore || page.Pagination.Cursor == nil {
			break
		}
		params.Cursor = page.Pagination.Cursor
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.VolumeSyncList{Items: items})
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
			size = formatBytes(int64(*sync.TotalSizeBytes))
		}
		completed := "-"
		if sync.CompletedAt != nil {
			completed = sync.CompletedAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{
			sync.SyncId,
			string(sync.Status),
			volumeSyncSourceURI(sync.Source),
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

func volumeSyncSourceFromFlags(flags *cmd.VolumeSyncStartFlags) (managementapi.CreateVolumeSyncRequest_Source, error) {
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
		return managementapi.CreateVolumeSyncRequest_Source{}, cmd.NewErrUsagef(
			"unsupported source %q: want hf://, s3://, gs://, azure://, r2://, cw://, or bt://", uri)
	}

	arn := strings.TrimSpace(flags.AuthAWSAssumeRoleARN)
	region := strings.TrimSpace(flags.AuthAWSAssumeRoleRegion)
	secret := strings.TrimSpace(flags.AuthSecretName)
	awsOIDCRoleARN := strings.TrimSpace(flags.AuthAWSOIDCRoleARN)
	awsOIDCRegion := strings.TrimSpace(flags.AuthAWSOIDCRegion)
	gcpOIDCServiceAccount := strings.TrimSpace(flags.AuthGCPOIDCServiceAccount)
	gcpOIDCWorkloadIdentityProvider := strings.TrimSpace(flags.AuthGCPOIDCWorkloadIdentityProvider)
	if (arn == "") != (region == "") {
		return managementapi.CreateVolumeSyncRequest_Source{}, cmd.NewErrUsagef(
			"--auth-aws-assume-role-arn and --auth-aws-assume-role-region must be provided together")
	}
	if (awsOIDCRoleARN == "") != (awsOIDCRegion == "") {
		return managementapi.CreateVolumeSyncRequest_Source{}, cmd.NewErrUsagef(
			"--auth-aws-oidc-role-arn and --auth-aws-oidc-region must be provided together")
	}
	if (gcpOIDCServiceAccount == "") != (gcpOIDCWorkloadIdentityProvider == "") {
		return managementapi.CreateVolumeSyncRequest_Source{}, cmd.NewErrUsagef(
			"--auth-gcp-oidc-service-account and --auth-gcp-oidc-workload-identity-provider must be provided together")
	}
	authMethodCount := 0
	for _, configured := range []bool{
		secret != "", arn != "", awsOIDCRoleARN != "", gcpOIDCServiceAccount != "",
	} {
		if configured {
			authMethodCount++
		}
	}
	if authMethodCount > 1 {
		return managementapi.CreateVolumeSyncRequest_Source{}, cmd.NewErrUsagef(
			"authentication methods are mutually exclusive")
	}
	if arn != "" && sourceType != "S3" {
		return managementapi.CreateVolumeSyncRequest_Source{}, cmd.NewErrUsagef(
			"--auth-aws-assume-role-* is supported only for an s3:// source")
	}
	if awsOIDCRoleARN != "" && sourceType != "S3" {
		return managementapi.CreateVolumeSyncRequest_Source{}, cmd.NewErrUsagef(
			"--auth-aws-oidc-* is supported only for an s3:// source")
	}
	if gcpOIDCServiceAccount != "" && sourceType != "GCS" {
		return managementapi.CreateVolumeSyncRequest_Source{}, cmd.NewErrUsagef(
			"--auth-gcp-oidc-* is supported only for a gs:// source")
	}
	if authMethodCount > 0 && sourceType == "BASETEN_TRAINING" {
		return managementapi.CreateVolumeSyncRequest_Source{}, cmd.NewErrUsagef(
			"a bt:// source does not accept authentication flags")
	}

	include := append([]string{}, flags.Include...)
	exclude := append([]string{}, flags.Exclude...)
	var authSecretName *string
	if secret != "" {
		authSecretName = &secret
	}

	var source managementapi.CreateVolumeSyncRequest_Source
	var err error
	switch sourceType {
	case "HUGGING_FACE":
		err = source.FromVolumeSyncSourceHuggingFace(managementapi.VolumeSyncSourceHuggingFace{
			Uri: uri, Include: &include, Exclude: &exclude, AuthSecretName: authSecretName,
		})
	case "S3":
		var assumeRole *managementapi.VolumeSyncAuthenticationAWSAssumeRole
		if arn != "" {
			assumeRole = &managementapi.VolumeSyncAuthenticationAWSAssumeRole{RoleArn: arn, Region: region}
		}
		var awsOIDC *managementapi.VolumeSyncAuthenticationAWSOIDC
		if awsOIDCRoleARN != "" {
			awsOIDC = &managementapi.VolumeSyncAuthenticationAWSOIDC{
				RoleArn: awsOIDCRoleARN,
				Region:  awsOIDCRegion,
			}
		}
		err = source.FromVolumeSyncSourceS3(managementapi.VolumeSyncSourceS3{
			Uri: uri, Include: &include, Exclude: &exclude,
			AuthSecretName: authSecretName, AwsAssumeRole: assumeRole, AwsOidc: awsOIDC,
		})
	case "GCS":
		var gcpOIDC *managementapi.VolumeSyncAuthenticationGCPOIDC
		if gcpOIDCServiceAccount != "" {
			gcpOIDC = &managementapi.VolumeSyncAuthenticationGCPOIDC{
				ServiceAccount:           gcpOIDCServiceAccount,
				WorkloadIdentityProvider: gcpOIDCWorkloadIdentityProvider,
			}
		}
		err = source.FromVolumeSyncSourceGCS(managementapi.VolumeSyncSourceGCS{
			Uri: uri, Include: &include, Exclude: &exclude, AuthSecretName: authSecretName,
			GcpOidc: gcpOIDC,
		})
	case "AZURE":
		err = source.FromVolumeSyncSourceAzure(managementapi.VolumeSyncSourceAzure{
			Uri: uri, Include: &include, Exclude: &exclude, AuthSecretName: authSecretName,
		})
	case "R2":
		err = source.FromVolumeSyncSourceR2(managementapi.VolumeSyncSourceR2{
			Uri: uri, Include: &include, Exclude: &exclude, AuthSecretName: authSecretName,
		})
	case "COREWEAVE":
		err = source.FromVolumeSyncSourceCoreWeave(managementapi.VolumeSyncSourceCoreWeave{
			Uri: uri, Include: &include, Exclude: &exclude, AuthSecretName: authSecretName,
		})
	case "BASETEN_TRAINING":
		err = source.FromVolumeSyncSourceBasetenTraining(managementapi.VolumeSyncSourceBasetenTraining{
			Uri: uri, Include: &include, Exclude: &exclude,
		})
	}
	if err != nil {
		return managementapi.CreateVolumeSyncRequest_Source{}, fmt.Errorf("encoding volume sync source: %w", err)
	}
	return source, nil
}

func volumeSyncDestination(raw string) (string, error) {
	ref, err := volumeParseRef(raw)
	if err != nil {
		return "", err
	}
	if ref.Volume == "" {
		return "", cmd.NewErrUsagef("destination %s names a namespace; want bdn:<namespace>/<volume>", ref)
	}
	if ref.Path != "" {
		return "", cmd.NewErrUsagef("destination %s carries a path; want a volume or tag ref", ref)
	}
	if ref.Digest != "" {
		return "", cmd.NewErrUsagef("destination %s carries an immutable digest; want a volume or tag ref", ref)
	}
	return ref.String(), nil
}

func getVolumeSync(
	ctx *CommandContext, api *managementapi.Client, syncID string,
) (managementapi.VolumeSync, error) {
	sync, err := api.GetVolumesSyncsVolumeSyncId(ctx, syncID)
	if err != nil {
		return managementapi.VolumeSync{}, err
	}
	return *sync, nil
}

func waitVolumeSync(
	ctx *CommandContext, api *managementapi.Client, sync managementapi.VolumeSync,
) (managementapi.VolumeSync, error) {
	var lastStatus managementapi.VolumeSyncStatus
	for {
		if sync.Status != lastStatus {
			ctx.Logf("Status: %s\n", sync.Status)
			lastStatus = sync.Status
		}
		switch sync.Status {
		case managementapi.VolumeSyncStatus_READY,
			managementapi.VolumeSyncStatus_FAILED,
			managementapi.VolumeSyncStatus_CANCELED:
			return sync, nil
		case managementapi.VolumeSyncStatus_PENDING, managementapi.VolumeSyncStatus_SYNCING:
			// Keep polling.
		default:
			return managementapi.VolumeSync{}, fmt.Errorf(
				"volume sync %s returned unknown status %q", sync.SyncId, sync.Status)
		}
		if err := ctx.Sleep(volumeSyncPollInterval); err != nil {
			return managementapi.VolumeSync{}, err
		}
		syncID := sync.SyncId
		var err error
		sync, err = getVolumeSync(ctx, api, syncID)
		if err != nil {
			return managementapi.VolumeSync{}, fmt.Errorf("waiting for volume sync %s: %w", syncID, err)
		}
	}
}

func volumeSyncTerminalError(ctx *CommandContext, sync managementapi.VolumeSync) error {
	switch sync.Status {
	case managementapi.VolumeSyncStatus_FAILED:
		ctx.SuppressJSONError()
		if sync.Error != nil && sync.Error.Message != "" {
			return fmt.Errorf("volume sync %s failed: %s", sync.SyncId, sync.Error.Message)
		}
		return fmt.Errorf("volume sync %s failed", sync.SyncId)
	case managementapi.VolumeSyncStatus_CANCELED:
		ctx.SuppressJSONError()
		return fmt.Errorf("volume sync %s was canceled", sync.SyncId)
	default:
		return nil
	}
}

func outputVolumeSync(ctx *CommandContext, sync managementapi.VolumeSync) {
	if ctx.JSON {
		ctx.OutputJSON(sync)
		return
	}
	ctx.Outputf("ID:          %s\n", sync.SyncId)
	ctx.Outputf("Status:      %s\n", sync.Status)
	ctx.Outputf("Source:      %s\n", volumeSyncSourceURI(sync.Source))
	ctx.Outputf("Destination: %s\n", sync.Destination.Ref)
	ctx.Outputf("Created:     %s\n", sync.CreatedAt.UTC().Format(time.RFC3339))
	if sync.CompletedAt != nil {
		ctx.Outputf("Completed:   %s\n", sync.CompletedAt.UTC().Format(time.RFC3339))
	}
	if sync.VolumeVersionId != nil {
		ctx.Outputf("Version ID:  %s\n", *sync.VolumeVersionId)
	}
	if sync.VersionRef != nil {
		ctx.Outputf("Version ref: %s\n", *sync.VersionRef)
	}
	if sync.ContentDigest != nil {
		ctx.Outputf("Digest:      %s\n", *sync.ContentDigest)
	}
	if sync.TotalSizeBytes != nil {
		ctx.Outputf("Size:        %s\n", formatBytes(int64(*sync.TotalSizeBytes)))
	}
	if sync.Error != nil {
		ctx.Outputf("Error code:  %s\n", sync.Error.Code)
		ctx.Outputf("Error:       %s\n", sync.Error.Message)
	}
}

func volumeSyncSourceURI(source managementapi.VolumeSync_Source) string {
	value, err := source.ValueByDiscriminator()
	if err != nil {
		return "<unrecognized source>"
	}
	switch source := value.(type) {
	case managementapi.VolumeSyncSourceHuggingFace:
		return source.Uri
	case managementapi.VolumeSyncSourceS3:
		return source.Uri
	case managementapi.VolumeSyncSourceGCS:
		return source.Uri
	case managementapi.VolumeSyncSourceAzure:
		return source.Uri
	case managementapi.VolumeSyncSourceR2:
		return source.Uri
	case managementapi.VolumeSyncSourceCoreWeave:
		return source.Uri
	case managementapi.VolumeSyncSourceBasetenTraining:
		return source.Uri
	default:
		return "<unrecognized source>"
	}
}
