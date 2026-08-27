package cmd

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("train capacity describe", commandTrainCapacityDescribe)
	Register("train capacity update", commandTrainCapacityUpdate)
	Register("train checkpoint list", commandTrainCheckpointList)
	Register("train checkpoint files", commandTrainCheckpointFiles)
	Register("train checkpoint deploy", commandTrainCheckpointDeploy)
	Register("train init", commandTrainInit)
	Register("train job list", commandTrainJobList)
	Register("train job describe", commandTrainJobDescribe)
	Register("train job logs", commandTrainJobLogs)
	Register("train job metrics", commandTrainJobMetrics)
	Register("train job stop", commandTrainJobStop)
	Register("train job recreate", commandTrainJobRecreate)
	Register("train job update", commandTrainJobUpdate)
	Register("train job download", commandTrainJobDownload)
	Register("train job session describe", commandTrainJobSessionDescribe)
	Register("train project list", commandTrainProjectList)
	Register("train project cache describe", commandTrainProjectCacheDescribe)
	Register("train push", commandTrainPush)
	Register("train workstation create", commandTrainWorkstationCreate)
}

// trainJobStatusPrefix is the prefix every training job status carries. The CLI
// spells statuses without it, so 'running' means TRAINING_JOB_RUNNING, and both
// spellings are accepted on input. The mapping is mechanical rather than a fixed
// table so a status added to the backend works without a CLI release, and so an
// unrecognized one still renders readably.
const trainJobStatusPrefix = "TRAINING_JOB_"

// trainJobStatusToAPI turns a CLI status into its API spelling. A value that
// already carries the prefix passes through, so both forms work.
func trainJobStatusToAPI(status string) string {
	upper := strings.ToUpper(strings.ReplaceAll(status, "-", "_"))
	if strings.HasPrefix(upper, trainJobStatusPrefix) {
		return upper
	}
	return trainJobStatusPrefix + upper
}

// trainJobStatusFromAPI turns an API status into its CLI spelling. A value
// without the expected prefix is lowercased as-is rather than dropped.
func trainJobStatusFromAPI(status string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(status, trainJobStatusPrefix), "_", "-"))
}

// trainJobRef is a training job and the project that owns it. Every job
// endpoint is nested under a project, but users only supply a job ID.
type trainJobRef struct {
	ProjectID   string
	ProjectName string
	JobID       string
}

// resolveTrainJob finds the project owning a job. The search endpoint is the
// only lookup that takes a bare job ID, and it returns the job with its project
// attached.
func resolveTrainJob(ctx *CommandContext, api *managementapi.Client, jobID string) (*trainJobRef, error) {
	resp, err := api.PostTrainingJobsSearch(ctx, managementapi.SearchTrainingJobsRequest{JobId: &jobID})
	if err != nil {
		return nil, fmt.Errorf("search training job %s: %w", jobID, err)
	}
	if len(resp.TrainingJobs) == 0 {
		return nil, fmt.Errorf("no training job found with ID %s", jobID)
	}
	job := resp.TrainingJobs[0]
	return &trainJobRef{
		ProjectID:   job.TrainingProject.Id,
		ProjectName: job.TrainingProject.Name,
		JobID:       job.Id,
	}, nil
}

// resolveTrainProject translates a --project value (project name or ID) into a
// project ID. An exact ID match wins over a name match, matching ResolveTeam.
func resolveTrainProject(ctx *CommandContext, api *managementapi.Client, input string) (string, error) {
	resp, err := api.GetTrainingProjects(ctx)
	if err != nil {
		return "", fmt.Errorf("list training projects: %w", err)
	}
	found := ""
	for _, project := range resp.TrainingProjects {
		if project.Id == input {
			return project.Id, nil
		}
		if project.Name == input {
			if found != "" {
				return "", cmd.NewErrUsagef("multiple training projects named %q; pass the project ID instead", input)
			}
			found = project.Id
		}
	}
	if found == "" {
		return "", fmt.Errorf("no training project found named %q", input)
	}
	return found, nil
}

func commandTrainCapacityDescribe(ctx *CommandContext, flags *cmd.TrainCapacityDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	resp, err := cl.API().GetTrainingCapacity(ctx)
	if err != nil {
		return fmt.Errorf("describe training capacity: %w", err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	// Team limits are enforced whether or not the org has its own capacity, so
	// the two tables stand alone: neither absence hides the other.
	hasTeamCapacities := resp.TeamGpuCapacities != nil && len(*resp.TeamGpuCapacities) > 0
	if len(resp.GpuCapacities) == 0 && !hasTeamCapacities {
		ctx.LogLine("No training GPU capacity configured.")
		return nil
	}
	if len(resp.GpuCapacities) > 0 {
		rows := make([][]string, 0, len(resp.GpuCapacities))
		for _, capacity := range resp.GpuCapacities {
			rows = append(rows, []string{
				capacity.GpuType,
				fmt.Sprint(capacity.UsageCount),
				fmt.Sprint(capacity.Limit),
				fmt.Sprint(capacity.Baseline),
			})
		}
		ctx.OutputTable(TableOutput{
			Headers: []string{"GPU TYPE", "IN USE", "LIMIT", "BASELINE"},
			Rows:    rows,
		})
	}

	if !hasTeamCapacities {
		return nil
	}
	teamRows := make([][]string, 0, len(*resp.TeamGpuCapacities))
	for _, capacity := range *resp.TeamGpuCapacities {
		teamRows = append(teamRows, []string{
			capacity.TeamName,
			capacity.GpuType,
			fmt.Sprint(capacity.UsageCount),
			fmt.Sprint(capacity.Limit),
			fmt.Sprint(capacity.Baseline),
		})
	}
	if len(resp.GpuCapacities) > 0 {
		ctx.Outputf("\n")
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"TEAM", "GPU TYPE", "IN USE", "LIMIT", "BASELINE"},
		Rows:    teamRows,
	})
	return nil
}

func commandTrainCapacityUpdate(ctx *CommandContext, flags *cmd.TrainCapacityUpdateFlags) error {
	if flags.MaxGPUs < 0 {
		return cmd.NewErrUsagef("--max-gpus must be zero or a positive number")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}
	resp, err := cl.API().PatchTrainingCapacity(ctx, managementapi.PatchTeamTrainingGpuCapacityRequest{
		TeamId:  teamID,
		GpuType: flags.GPUType,
		MaxGpus: flags.MaxGPUs,
	})
	if err != nil {
		return fmt.Errorf("update training capacity for team %s: %w", flags.Team, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Set %s limit to %d GPUs for team %s.\n",
		resp.TeamGpuCapacity.GpuType, resp.TeamGpuCapacity.Limit, resp.TeamGpuCapacity.TeamName)
	return nil
}

func commandTrainCheckpointList(ctx *CommandContext, flags *cmd.TrainCheckpointListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := resolveTrainJob(ctx, cl.API(), flags.JobID)
	if err != nil {
		return err
	}
	resp, err := cl.API().GetTrainingProjectsJobsCheckpoints(ctx, ref.ProjectID, ref.JobID)
	if err != nil {
		return fmt.Errorf("list checkpoints for training job %s: %w", flags.JobID, err)
	}

	checkpoints := resp.Checkpoints
	slices.SortStableFunc(checkpoints, func(a, b managementapi.TrainingJobCheckpoint) int {
		if flags.Direction == "asc" {
			return a.CreatedAt.Compare(b.CreatedAt)
		}
		return b.CreatedAt.Compare(a.CreatedAt)
	})

	if ctx.JSON {
		ctx.OutputJSON(managementapi.GetTrainingJobCheckpointsResponse{
			Checkpoints: checkpoints,
			TrainingJob: resp.TrainingJob,
		})
		return nil
	}
	if len(checkpoints) == 0 {
		ctx.LogLine("No checkpoints found.")
		return nil
	}
	rows := make([][]string, 0, len(checkpoints))
	for _, checkpoint := range checkpoints {
		baseModel := ""
		if checkpoint.BaseModel != nil {
			baseModel = *checkpoint.BaseModel
		}
		syncStatus := ""
		if checkpoint.SyncStatus != nil {
			syncStatus = *checkpoint.SyncStatus
		}
		rows = append(rows, []string{
			checkpoint.CheckpointId,
			checkpoint.CheckpointType,
			baseModel,
			formatBytes(int64(checkpoint.SizeBytes)),
			syncStatus,
			checkpoint.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "TYPE", "BASE MODEL", "SIZE", "SYNC", "CREATED"},
		Rows:    rows,
	})
	return nil
}

func commandTrainCheckpointFiles(ctx *CommandContext, flags *cmd.TrainCheckpointFilesFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := resolveTrainJob(ctx, cl.API(), flags.JobID)
	if err != nil {
		return err
	}
	var jw *JSONArrayWriter
	if ctx.JSON {
		jw = ctx.NewJSONArrayWriter()
		defer jw.Close()
	}
	var rows [][]string

	// The URLs are short-lived, so emit each record as its page arrives rather
	// than buffering the whole listing. An empty page also ends the walk, so a
	// server that keeps handing back a token cannot spin here forever.
	params := managementapi.GetV1TrainingProjectsTrainingProjectIdJobsTrainingJobIdCheckpointFilesParams{}
	for {
		resp, err := cl.API().GetTrainingProjectsJobsCheckpointFiles(ctx, ref.ProjectID, ref.JobID, params)
		if err != nil {
			return fmt.Errorf("list checkpoint files for training job %s: %w", flags.JobID, err)
		}
		for _, file := range resp.PresignedUrls {
			if ctx.JSON {
				jw.Write(file)
			} else {
				rows = append(rows, []string{
					fmt.Sprint(file.NodeRank),
					file.RelativeFileName,
					formatBytes(int64(file.SizeBytes)),
					file.LastModified,
					file.Url,
				})
			}
		}
		if resp.NextPageToken == nil || len(resp.PresignedUrls) == 0 {
			break
		}
		params.PageToken = resp.NextPageToken
	}

	if ctx.JSON {
		return nil
	}
	if len(rows) == 0 {
		ctx.LogLine("No checkpoint files found.")
		return nil
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"NODE", "NAME", "SIZE", "MODIFIED", "URL"},
		Rows:    rows,
	})
	return nil
}

// commandTrainCheckpointDeploy delegates to the truss CLI, which is the only
// thing that can evaluate the Python config the deploy is described by.
func commandTrainCheckpointDeploy(ctx *CommandContext, flags *cmd.TrainCheckpointDeployFlags) error {
	// Given neither, the job would default to the most recently created one in
	// any project the caller's teams can reach, as likely to be a colleague's
	// as their own.
	if flags.JobID == "" && flags.Config == "" {
		return cmd.NewErrUsagef("--job-id is required unless --config names the checkpoints to deploy")
	}

	args := []string{"train", "deploy_checkpoints"}
	args = trussArg(args, "job-id", flags.JobID)
	args = trussArg(args, "config", flags.Config)
	args = trussArg(args, "truss-config-output-dir", flags.ConfigOutDir)
	args = trussBoolArg(args, "dry-run", flags.DryRun)

	return trussRun(ctx, trussInvocation{
		Flags:       flags.TrussFlags,
		Args:        args,
		ForwardAuth: !flags.TrussNoForwardAuth,
		JSONResult:  true,
	})
}

// commandTrainInit delegates to the truss CLI, which owns the project template
// and the example downloads. It calls no API, so no credential is forwarded and
// being logged out is not an error.
func commandTrainInit(ctx *CommandContext, flags *cmd.TrainInitFlags) error {
	args := []string{"train", "init"}
	if flags.ListExamples {
		args = append(args, "--list-examples")
	}
	args = trussArg(args, "target-directory", flags.Dir)
	args = trussArg(args, "examples", strings.Join(flags.Example, ","))

	return trussRun(ctx, trussInvocation{
		Flags:      flags.TrussFlags,
		Args:       args,
		JSONResult: true,
	})
}

func commandTrainJobList(ctx *CommandContext, flags *cmd.TrainJobListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	req := managementapi.SearchTrainingJobsRequest{}
	if flags.Project != "" {
		projectID, err := resolveTrainProject(ctx, cl.API(), flags.Project)
		if err != nil {
			return err
		}
		req.ProjectId = &projectID
	}
	if len(flags.Status) > 0 {
		statuses := make([]string, 0, len(flags.Status))
		for _, status := range flags.Status {
			statuses = append(statuses, trainJobStatusToAPI(status))
		}
		req.Statuses = &statuses
	}
	order := []managementapi.OrderBy{{Field: "created_at", Order: flags.Direction}}
	req.OrderBy = &order

	resp, err := cl.API().PostTrainingJobsSearch(ctx, req)
	if err != nil {
		return fmt.Errorf("list training jobs: %w", err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	if len(resp.TrainingJobs) == 0 {
		ctx.LogLine("No training jobs found.")
		return nil
	}
	rows := make([][]string, 0, len(resp.TrainingJobs))
	for _, job := range resp.TrainingJobs {
		name := ""
		if job.Name != nil {
			name = *job.Name
		}
		nodes := ""
		if job.NodeCount != nil {
			nodes = fmt.Sprint(*job.NodeCount)
		}
		rows = append(rows, []string{
			job.Id,
			job.TrainingProject.Name,
			name,
			trainJobStatusFromAPI(job.CurrentStatus),
			job.InstanceType.Name,
			nodes,
			job.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "PROJECT", "NAME", "STATUS", "INSTANCE TYPE", "NODES", "CREATED"},
		Rows:    rows,
	})
	return nil
}

func commandTrainJobDescribe(ctx *CommandContext, flags *cmd.TrainJobDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := resolveTrainJob(ctx, cl.API(), flags.JobID)
	if err != nil {
		return err
	}
	resp, err := cl.API().GetTrainingProjectsJobsTrainingJobId(ctx, ref.ProjectID, ref.JobID)
	if err != nil {
		return fmt.Errorf("describe training job %s: %w", flags.JobID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	job := resp.TrainingJob
	ctx.Outputf("ID:             %s\n", job.Id)
	if job.Name != nil {
		ctx.Outputf("Name:           %s\n", *job.Name)
	}
	ctx.Outputf("Project:        %s (%s)\n", resp.TrainingProject.Name, resp.TrainingProject.Id)
	ctx.Outputf("Status:         %s\n", trainJobStatusFromAPI(job.CurrentStatus))
	if job.ErrorMessage != nil && *job.ErrorMessage != "" {
		ctx.Outputf("Error:          %s\n", *job.ErrorMessage)
	}
	ctx.Outputf("Instance Type:  %s\n", job.InstanceType.Name)
	if job.NodeCount != nil {
		ctx.Outputf("Nodes:          %d\n", *job.NodeCount)
	}
	if job.AvailabilityModel != nil {
		ctx.Outputf("Availability:   %s\n", *job.AvailabilityModel)
	}
	if job.Priority != nil {
		ctx.Outputf("Priority:       %d\n", *job.Priority)
	}
	if job.CheckpointSyncStatus != nil {
		ctx.Outputf("Checkpoints:    %s\n", *job.CheckpointSyncStatus)
	}
	if job.User != nil && job.User.Email != nil {
		ctx.Outputf("Owner:          %s\n", *job.User.Email)
	}
	ctx.Outputf("Created:        %s\n", job.CreatedAt.UTC().Format(time.RFC3339))
	ctx.Outputf("Updated:        %s\n", job.UpdatedAt.UTC().Format(time.RFC3339))
	return nil
}

func commandTrainJobLogs(ctx *CommandContext, flags *cmd.TrainJobLogsFlags) error {
	// The filters training logs do not offer stay zero-valued, which the shared
	// logs flow treats as absent.
	logFlags := cmd.LogFlags{
		Tail:     flags.Tail,
		Start:    flags.Start,
		End:      flags.End,
		Since:    flags.Since,
		Limit:    flags.Limit,
		PageSize: flags.PageSize,
		MinLevel: flags.MinLevel,
	}
	if err := validateLogFlags(ctx, &logFlags); err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := resolveTrainJob(ctx, cl.API(), flags.JobID)
	if err != nil {
		return err
	}

	fetchLogs := func(q logQuery) (*managementapi.GetLogsResponse, error) {
		direction := managementapi.SortOrder_desc
		return cl.API().GetTrainingProjectsJobsLogs(ctx, ref.ProjectID, ref.JobID,
			managementapi.GetV1TrainingProjectsTrainingProjectIdJobsTrainingJobIdLogsParams{
				StartEpochMillis: q.StartEpochMillis,
				EndEpochMillis:   q.EndEpochMillis,
				Limit:            q.Limit,
				Direction:        &direction,
				MinLevel:         q.MinLevel,
			})
	}
	fetchStatus := func() (*tailStatus, error) {
		resp, err := cl.API().GetTrainingProjectsJobsTrainingJobId(ctx, ref.ProjectID, ref.JobID)
		if err != nil {
			return nil, err
		}
		status := resp.TrainingJob.CurrentStatus
		return &tailStatus{
			Label: trainJobStatusFromAPI(status),
			// A job only produces logs while it is coming up or running. Every
			// other status, including ones this build does not know, is treated
			// as terminal so the tail cannot hang on a finished job.
			Runnable: slices.Contains(trainJobRunnableStatuses, status),
		}, nil
	}
	return runLogsCommand(ctx, &logFlags, fetchLogs, fetchStatus)
}

// trainJobRunnableStatuses are the statuses where a training job may still
// produce logs: waiting for capacity, starting up, or running.
var trainJobRunnableStatuses = []string{
	"TRAINING_JOB_PENDING",
	"TRAINING_JOB_CREATED",
	"TRAINING_JOB_DEPLOYING",
	"TRAINING_JOB_QUEUED",
	"TRAINING_JOB_RUNNING",
}

func commandTrainJobMetrics(ctx *CommandContext, flags *cmd.TrainJobMetricsFlags) error {
	hasStart := !flags.Start.IsZero()
	hasEnd := !flags.End.IsZero()
	// Use Changed rather than the zero value so explicit --since 0 fails the
	// positive-duration check below instead of being silently dropped.
	hasSince := ctx.Command.Flags().Changed("since")
	if hasSince && (hasStart || hasEnd) {
		return cmd.NewErrUsagef("--since cannot be combined with --start or --end")
	}
	if hasSince && flags.Since <= 0 {
		return cmd.NewErrUsagef("--since must be a positive duration")
	}
	if hasStart && hasEnd && !flags.End.After(flags.Start) {
		return cmd.NewErrUsagef("--end must be after --start")
	}

	params := managementapi.GetV1TrainingProjectsTrainingProjectIdJobsTrainingJobIdMetricsParams{}
	if hasSince {
		now := ctx.Now()
		start := int(now.Add(-flags.Since).UnixMilli())
		end := int(now.UnixMilli())
		params.StartEpochMillis = &start
		params.EndEpochMillis = &end
	} else {
		if hasStart {
			start := int(flags.Start.UnixMilli())
			params.StartEpochMillis = &start
		}
		if hasEnd {
			end := int(flags.End.UnixMilli())
			params.EndEpochMillis = &end
		}
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := resolveTrainJob(ctx, cl.API(), flags.JobID)
	if err != nil {
		return err
	}
	resp, err := cl.API().GetTrainingProjectsJobsMetrics(ctx, ref.ProjectID, ref.JobID, params)
	if err != nil {
		return fmt.Errorf("fetch metrics for training job %s: %w", flags.JobID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	var rows [][]string
	// Per-node metrics carry the same series as the top-level fields, broken out
	// by node, so a multi-node job reports each node rather than one blended
	// figure. Single-node jobs still report one row per series.
	for _, node := range resp.PerNodeMetrics {
		rows = append(rows, trainMetricRows(node.NodeId, node.Metrics)...)
	}
	if len(resp.PerNodeMetrics) == 0 {
		rows = append(rows, trainMetricRows("", managementapi.TrainingJobMetrics{
			CpuUsage:            resp.CpuUsage,
			CpuMemoryUsageBytes: resp.CpuMemoryUsageBytes,
			GpuUtilization:      resp.GpuUtilization,
			GpuMemoryUsageBytes: resp.GpuMemoryUsageBytes,
			EphemeralStorage:    resp.EphemeralStorage,
		})...)
	}
	if resp.Cache != nil {
		rows = append(rows, trainStorageRows("", "cache", *resp.Cache)...)
	}
	if len(rows) == 0 {
		ctx.LogLine("No metrics reported for this training job.")
		return nil
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"METRIC", "NODE", "VALUE", "MEASURED"},
		Rows:    rows,
	})
	return nil
}

// trainMetricRows renders one row per series, each holding that series' most
// recent sample.
func trainMetricRows(nodeID string, metrics managementapi.TrainingJobMetrics) [][]string {
	var rows [][]string
	if row, ok := trainMetricRow("cpu usage", nodeID, metrics.CpuUsage, func(v float32) string {
		return fmt.Sprintf("%.2f cores", v)
	}); ok {
		rows = append(rows, row)
	}
	if row, ok := trainMetricRow("cpu memory", nodeID, metrics.CpuMemoryUsageBytes, func(v float32) string {
		return formatBytes(int64(v))
	}); ok {
		rows = append(rows, row)
	}
	for _, gpu := range slices.Sorted(maps.Keys(metrics.GpuUtilization)) {
		if row, ok := trainMetricRow(fmt.Sprintf("gpu %s utilization", gpu), nodeID, metrics.GpuUtilization[gpu], func(v float32) string {
			return fmt.Sprintf("%.1f%%", v*100)
		}); ok {
			rows = append(rows, row)
		}
	}
	for _, gpu := range slices.Sorted(maps.Keys(metrics.GpuMemoryUsageBytes)) {
		if row, ok := trainMetricRow(fmt.Sprintf("gpu %s memory", gpu), nodeID, metrics.GpuMemoryUsageBytes[gpu], func(v float32) string {
			return formatBytes(int64(v))
		}); ok {
			rows = append(rows, row)
		}
	}
	return append(rows, trainStorageRows(nodeID, "ephemeral storage", metrics.EphemeralStorage)...)
}

// trainStorageRows renders the usage and utilization series of one storage
// volume.
func trainStorageRows(nodeID, label string, storage managementapi.StorageMetrics) [][]string {
	var rows [][]string
	if row, ok := trainMetricRow(label+" usage", nodeID, storage.UsageBytes, func(v float32) string {
		return formatBytes(int64(v))
	}); ok {
		rows = append(rows, row)
	}
	if row, ok := trainMetricRow(label+" utilization", nodeID, storage.Utilization, func(v float32) string {
		return fmt.Sprintf("%.1f%%", v*100)
	}); ok {
		rows = append(rows, row)
	}
	return rows
}

// trainMetricRow renders a series' most recent sample, reporting false when the
// series is empty so the caller can omit the row entirely.
func trainMetricRow(label, nodeID string, series []managementapi.TrainingJobMetric, format func(float32) string) ([]string, bool) {
	if len(series) == 0 {
		return nil, false
	}
	latest := series[0]
	for _, sample := range series[1:] {
		if sample.Timestamp.After(latest.Timestamp) {
			latest = sample
		}
	}
	return []string{label, nodeID, format(latest.Value), latest.Timestamp.UTC().Format(time.RFC3339)}, true
}

func commandTrainJobStop(ctx *CommandContext, flags *cmd.TrainJobStopFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := resolveTrainJob(ctx, cl.API(), flags.JobID)
	if err != nil {
		return err
	}
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf(
			"Stop training job %s in project %s? This cannot be undone.", ref.JobID, ref.ProjectName)); err != nil {
			return err
		}
	}
	resp, err := cl.API().PostTrainingProjectsJobsStop(ctx, ref.ProjectID, ref.JobID,
		managementapi.StopTrainingJobRequest{})
	if err != nil {
		return fmt.Errorf("stop training job %s: %w", flags.JobID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Stopped training job %s. Synced checkpoints remain accessible.\n", resp.TrainingJob.Id)
	return nil
}

func commandTrainJobRecreate(ctx *CommandContext, flags *cmd.TrainJobRecreateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := resolveTrainJob(ctx, cl.API(), flags.JobID)
	if err != nil {
		return err
	}
	resp, err := cl.API().PostTrainingProjectsJobsRecreate(ctx, ref.ProjectID, ref.JobID)
	if err != nil {
		return fmt.Errorf("recreate training job %s: %w", flags.JobID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Created training job %s from %s.\n", resp.TrainingJob.Id, flags.JobID)
	return nil
}

func commandTrainJobUpdate(ctx *CommandContext, flags *cmd.TrainJobUpdateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := resolveTrainJob(ctx, cl.API(), flags.JobID)
	if err != nil {
		return err
	}
	resp, err := cl.API().PatchTrainingProjectsJobs(ctx, ref.ProjectID, ref.JobID,
		managementapi.UpdateTrainingJobRequest{Priority: flags.Priority})
	if err != nil {
		return fmt.Errorf("update training job %s: %w", flags.JobID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Set training job %s priority to %d.\n", resp.TrainingJob.Id, flags.Priority)
	return nil
}

func commandTrainJobDownload(ctx *CommandContext, flags *cmd.TrainJobDownloadFlags) error {
	if err := checkDownloadOutTarget(flags.OutFile, flags.OutDir, flags.Overwrite); err != nil {
		return err
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := resolveTrainJob(ctx, cl.API(), flags.JobID)
	if err != nil {
		return err
	}
	resp, err := cl.API().GetTrainingProjectsJobsDownload(ctx, ref.ProjectID, ref.JobID)
	if err != nil {
		return fmt.Errorf("download training job %s: %w", flags.JobID, err)
	}
	if len(resp.ArtifactPresignedUrls) == 0 {
		return fmt.Errorf("training job %s has no artifacts to download", flags.JobID)
	}
	// The API can report several artifacts per job, but server-side callers and
	// truss alike download the first and ignore the rest, so say so rather than
	// inventing a fan-out no other client has.
	if len(resp.ArtifactPresignedUrls) > 1 {
		ctx.Logf("Training job %s has %d artifacts; downloading the first.\n",
			ref.JobID, len(resp.ArtifactPresignedUrls))
	}

	ctx.Logf("Downloading artifact...\n")
	if err := downloadTarArchive(ctx, resp.ArtifactPresignedUrls[0], "artifact", flags.OutFile, flags.OutDir); err != nil {
		return err
	}
	if ctx.JSON {
		ctx.OutputJSON(cmd.TrainJobDownloadResult{
			JobID:   ref.JobID,
			OutFile: flags.OutFile,
			OutDir:  flags.OutDir,
		})
	}
	return nil
}

func commandTrainJobSessionDescribe(ctx *CommandContext, flags *cmd.TrainJobSessionDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := resolveTrainJob(ctx, cl.API(), flags.JobID)
	if err != nil {
		return err
	}
	resp, err := cl.API().GetTrainingProjectsJobsAuthCodes(ctx, ref.ProjectID, ref.JobID)
	if err != nil {
		return fmt.Errorf("describe interactive sessions for training job %s: %w", flags.JobID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	if len(resp.AuthCodes) == 0 {
		ctx.LogLine("No interactive sessions found.")
		return nil
	}
	rows := make([][]string, 0, len(resp.AuthCodes))
	for _, code := range resp.AuthCodes {
		expires := ""
		if code.ExpiresAt != nil {
			expires = code.ExpiresAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, []string{
			code.ReplicaId,
			code.SessionId,
			hyperlink(ctx.Stdout, code.AuthUrl),
			expires,
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"REPLICA", "SESSION", "AUTH URL", "EXPIRES"},
		Rows:    rows,
	})
	return nil
}

func commandTrainProjectList(ctx *CommandContext, flags *cmd.TrainProjectListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	resp, err := cl.API().GetTrainingProjects(ctx)
	if err != nil {
		return fmt.Errorf("list training projects: %w", err)
	}

	projects := resp.TrainingProjects
	slices.SortStableFunc(projects, func(a, b managementapi.TrainingProject) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})

	if ctx.JSON {
		ctx.OutputJSON(managementapi.ListTrainingProjectsResponse{TrainingProjects: projects})
		return nil
	}
	if len(projects) == 0 {
		ctx.LogLine("No training projects found.")
		return nil
	}
	rows := make([][]string, 0, len(projects))
	for _, project := range projects {
		team := ""
		if project.TeamName != nil {
			team = *project.TeamName
		}
		latestJob, latestStatus := "", ""
		if project.LatestJob != nil {
			latestJob = project.LatestJob.Id
			latestStatus = trainJobStatusFromAPI(project.LatestJob.CurrentStatus)
		}
		rows = append(rows, []string{
			project.Id,
			project.Name,
			team,
			latestJob,
			latestStatus,
			project.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "TEAM", "LATEST JOB", "LATEST STATUS", "CREATED"},
		Rows:    rows,
	})
	return nil
}

func commandTrainProjectCacheDescribe(ctx *CommandContext, flags *cmd.TrainProjectCacheDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	projectID, err := resolveTrainProject(ctx, cl.API(), flags.Project)
	if err != nil {
		return err
	}
	resp, err := cl.API().GetTrainingProjectsCacheSummary(ctx, projectID)
	if err != nil {
		return fmt.Errorf("describe cache for training project %s: %w", flags.Project, err)
	}

	files := resp.FileSummaries
	slices.SortStableFunc(files, func(a, b managementapi.FileSummary) int {
		if flags.Sort == "path" {
			return strings.Compare(a.Path, b.Path)
		}
		return b.SizeBytes - a.SizeBytes
	})

	if ctx.JSON {
		ctx.OutputJSON(managementapi.GetCacheSummaryResponse{
			FileSummaries: files,
			ProjectId:     resp.ProjectId,
			Timestamp:     resp.Timestamp,
		})
		return nil
	}
	if len(files) == 0 {
		ctx.LogLine("Cache is empty.")
		return nil
	}
	total := 0
	rows := make([][]string, 0, len(files))
	for _, file := range files {
		total += file.SizeBytes
		rows = append(rows, []string{
			file.Path,
			file.FileType,
			formatBytes(int64(file.SizeBytes)),
			file.Permissions,
			file.Modified,
		})
	}
	ctx.Outputf("Total: %s across %d files\n\n", formatBytes(int64(total)), len(files))
	ctx.OutputTable(TableOutput{
		Headers: []string{"PATH", "TYPE", "SIZE", "PERMISSIONS", "MODIFIED"},
		Rows:    rows,
	})
	return nil
}

// commandTrainPush delegates to the truss CLI, which is the only thing that can
// evaluate the Python config the job is defined by, and which archives and
// uploads the config's directory as the job's code.
//
// truss's --tail is deliberately not offered: the forwarded credential cannot be
// refreshed by the child process, so a long tail would outlive it. The command's
// output points at `baseten train job logs --tail` instead.
func commandTrainPush(ctx *CommandContext, flags *cmd.TrainPushFlags) error {
	// A sub-minute timeout truncates to zero, which trussIntArg drops as "not
	// given": truss would silently apply its default instead of the request.
	if d := flags.InteractiveTimeout; d != 0 && int(d.Minutes()) == 0 {
		return cmd.NewErrUsagef("--interactive-timeout must be at least 1m")
	}

	args := []string{"train", "push", flags.Config}
	args = trussArg(args, "job-name", flags.JobName)
	args = trussArg(args, "team", flags.Team)
	args = trussArg(args, "accelerator", flags.Accelerator)
	args = trussIntArg(args, "node-count", flags.NodeCount)
	args = trussArg(args, "entrypoint", flags.Entrypoint)
	args = trussIntArg(args, "priority", flags.Priority)
	args = trussBoolArg(args, "spot", flags.Spot)
	// Interactive triggers are hyphenated in this CLI and underscored downstream,
	// like every other multi-word value.
	args = trussArg(args, "interactive", strings.ReplaceAll(flags.Interactive, "-", "_"))
	args = trussIntArg(args, "interactive-timeout-minutes", int(flags.InteractiveTimeout.Minutes()))

	return trussRun(ctx, trussInvocation{
		Flags:       flags.TrussFlags,
		Args:        args,
		ForwardAuth: !flags.TrussNoForwardAuth,
		JSONResult:  true,
	})
}

// commandTrainWorkstationCreate delegates to the truss CLI, which builds the
// workstation's job spec and ships the SLURM setup scripts a multi-node
// workstation needs.
func commandTrainWorkstationCreate(ctx *CommandContext, flags *cmd.TrainWorkstationCreateFlags) error {
	args := []string{"train", "workstation"}
	args = trussArg(args, "accelerator", flags.Accelerator)
	args = trussIntArg(args, "gpu-count", flags.GPUCount)
	args = trussIntArg(args, "node-count", flags.NodeCount)
	args = trussArg(args, "image", flags.Image)
	// The workstation's project is named rather than identified: it is created
	// when no project of that name exists yet.
	args = trussArg(args, "project-id", flags.Project)
	args = trussArg(args, "team", flags.Team)
	args = trussArg(args, "orchestrator", flags.Orchestrator)
	args = trussBoolArg(args, "enable-checkpointing", flags.EnableCheckpointing)
	args = trussArg(args, "checkpoint-path", flags.CheckpointPath)
	args = trussIntArg(args, "checkpoint-volume-size", flags.CheckpointVolumeSize)
	args = trussArg(args, "checkpoint-from-job", flags.CheckpointFromJob)

	return trussRun(ctx, trussInvocation{
		Flags:       flags.TrussFlags,
		Args:        args,
		ForwardAuth: !flags.TrussNoForwardAuth,
		JSONResult:  true,
	})
}
