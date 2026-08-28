package cmd

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("loops run create", commandLoopsRunCreate)
	Register("loops run list", commandLoopsRunList)
	Register("loops run describe", commandLoopsRunDescribe)
	Register("loops run deactivate", commandLoopsRunDeactivate)
	Register("loops run logs", commandLoopsRunLogs)
	Register("loops usage", commandLoopsUsage)
	Register("loops checkpoint list", commandLoopsCheckpointList)
	Register("loops checkpoint files", commandLoopsCheckpointFiles)
	Register("loops checkpoint deploy", commandLoopsCheckpointDeploy)
}

// loopsTrainerLiveStatuses are the trainer deployment statuses where the trainer
// is alive: coming up, or running. A live trainer both holds GPUs and may still
// produce logs. SCALED_TO_ZERO holds no GPUs and is tracked separately in the
// usage summary; STOPPED and FAILED are terminal and hold none either.
var loopsTrainerLiveStatuses = []managementapi.Name{
	managementapi.Name_CREATED,
	managementapi.Name_DEPLOYING,
	managementapi.Name_RUNNING,
}

// loopsSamplerLiveStatuses are the sampler statuses where the sampler is alive:
// serving, or on the way there. A sampler keeps reporting the instance type it
// last ran on after it dies, so capacity has to key off status rather than the
// presence of an instance type.
var loopsSamplerLiveStatuses = []managementapi.DeploymentStatus{
	managementapi.DeploymentStatus_ACTIVE,
	managementapi.DeploymentStatus_BUILDING,
	managementapi.DeploymentStatus_DEPLOYING,
	managementapi.DeploymentStatus_LOADING_MODEL,
	managementapi.DeploymentStatus_UPDATING,
	managementapi.DeploymentStatus_WAKING_UP,
}

func commandLoopsRunCreate(ctx *CommandContext, flags *cmd.LoopsRunCreateFlags) error {
	if ctx.Command.Flags().Changed("replicas") && flags.Replicas < 1 {
		return cmd.NewErrUsagef("--replicas must be a positive number")
	}
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	teamID, err := ResolveTeam(ctx, cl.API(), flags.Team)
	if err != nil {
		return err
	}

	// A run belongs to a session, so creating one takes two calls. The session is
	// otherwise invisible: no CLI command surfaces it, and the SDK makes its own.
	ctx.VerboseLogf("Creating Loops session...\n")
	var session *managementapi.CreateLoopsSessionResponse
	if teamID != "" {
		session, err = cl.API().PostTeamsLoopsSessions(ctx, teamID)
	} else {
		session, err = cl.API().PostLoopsSessions(ctx)
	}
	if err != nil {
		return fmt.Errorf("create Loops session: %w", err)
	}

	req := managementapi.CreateLoopsRunRequest{
		SessionId: session.Session.Id,
		BaseModel: flags.BaseModel,
	}
	if flags.Name != "" {
		req.Name = &flags.Name
	}
	if flags.Replicas > 0 {
		req.Replicas = &flags.Replicas
	}

	ctx.Logf("Provisioning Loops run and sampler for %s...\n", flags.BaseModel)
	var created *managementapi.CreateLoopsRunResponse
	if teamID != "" {
		created, err = cl.API().PostTeamsLoopsRuns(ctx, teamID, req)
	} else {
		created, err = cl.API().PostLoopsRuns(ctx, req)
	}
	if err != nil {
		return fmt.Errorf("create Loops run for base model %s: %w", flags.BaseModel, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(created.Run)
		return nil
	}
	// Readiness is the SDK's concern: its clients block on construction. The CLI
	// only reports that both halves were provisioned.
	ctx.Logf("Created Loops run %s for %s. The trainer and sampler finish coming up in the background.\n",
		created.Run.Id, created.Run.BaseModel)
	return nil
}

func commandLoopsRunList(ctx *CommandContext, flags *cmd.LoopsRunListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	params := managementapi.GetV1LoopsRunsParams{}
	if flags.BaseModel != "" {
		params.BaseModel = &flags.BaseModel
	}
	if flags.Org {
		scope := "org"
		params.Scope = &scope
	}
	resp, err := cl.API().GetLoopsRuns(ctx, params)
	if err != nil {
		return fmt.Errorf("list Loops runs: %w", err)
	}

	runs := resp.Runs
	if !flags.All {
		runs = slices.DeleteFunc(runs, func(run managementapi.LoopsRun) bool {
			return run.Status.Name == managementapi.LoopsRunStatusName_INACTIVE
		})
	}
	slices.SortStableFunc(runs, func(a, b managementapi.LoopsRun) int {
		if flags.Direction == "asc" {
			return a.CreatedAt.Compare(b.CreatedAt)
		}
		return b.CreatedAt.Compare(a.CreatedAt)
	})

	if ctx.JSON {
		ctx.OutputJSON(managementapi.ListLoopsRunsResponse{Runs: runs})
		return nil
	}
	if len(runs) == 0 {
		if flags.All {
			ctx.LogLine("No Loops runs found.")
		} else {
			ctx.LogLine("No active Loops runs found. Pass --all to include inactive runs.")
		}
		return nil
	}
	headers := []string{"ID", "BASE MODEL", "STATUS", "CREATED"}
	if flags.Org {
		headers = slices.Insert(headers, 1, "OWNER")
	}
	rows := make([][]string, 0, len(runs))
	for _, run := range runs {
		row := []string{run.Id}
		if flags.Org {
			owner := ""
			if run.User.Email != nil {
				owner = *run.User.Email
			}
			row = append(row, owner)
		}
		rows = append(rows, append(row,
			run.BaseModel,
			string(run.Status.Name),
			run.CreatedAt.UTC().Format(time.RFC3339),
		))
	}
	ctx.OutputTable(TableOutput{Headers: headers, Rows: rows})
	return nil
}

func commandLoopsRunDescribe(ctx *CommandContext, flags *cmd.LoopsRunDescribeFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	resp, err := cl.API().GetLoopsRunsRunId(ctx, flags.RunID)
	if err != nil {
		return fmt.Errorf("describe Loops run %s: %w", flags.RunID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp.Run)
		return nil
	}
	run := resp.Run
	ctx.Outputf("ID:          %s\n", run.Id)
	ctx.Outputf("Name:        %s\n", run.Name)
	ctx.Outputf("Base Model:  %s\n", run.BaseModel)
	ctx.Outputf("Status:      %s\n", run.Status.Name)
	if run.User.Email != nil {
		ctx.Outputf("Owner:       %s\n", *run.User.Email)
	}
	ctx.Outputf("Session:     %s\n", run.SessionId)
	if run.DeploymentId != nil {
		ctx.Outputf("Deployment:  %s\n", *run.DeploymentId)
	}
	ctx.Outputf("Base URL:    %s\n", hyperlink(ctx.Stdout, run.BaseUrl))
	ctx.Outputf("Created:     %s\n", run.CreatedAt.UTC().Format(time.RFC3339))
	if run.Sampler != nil {
		ctx.Outputf("\nSampler\n")
		ctx.Outputf("  ID:         %s\n", run.Sampler.Id)
		ctx.Outputf("  Status:     %s\n", run.Sampler.Status.Name)
		ctx.Outputf("  Model:      %s\n", run.Sampler.ModelId)
		ctx.Outputf("  Deployment: %s\n", run.Sampler.DeploymentId)
		ctx.Outputf("  Base URL:   %s\n", hyperlink(ctx.Stdout, run.Sampler.BaseUrl))
	}
	return nil
}

func commandLoopsRunDeactivate(ctx *CommandContext, flags *cmd.LoopsRunDeactivateFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	if !flags.Yes {
		if err := ctx.ConfirmYesNo(fmt.Sprintf(
			"Deactivate Loops run %s? This tears down its trainer and sampler.", flags.RunID)); err != nil {
			return err
		}
	}
	resp, err := cl.API().PostLoopsRunsDeactivate(ctx, flags.RunID)
	if err != nil {
		return fmt.Errorf("deactivate Loops run %s: %w", flags.RunID, err)
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}
	ctx.Logf("Deactivated Loops run %s. Saved checkpoints remain accessible.\n", resp.Id)
	return nil
}

func commandLoopsRunLogs(ctx *CommandContext, flags *cmd.LoopsRunLogsFlags) error {
	// The filters Loops does not offer stay zero-valued, which the shared logs
	// flow treats as absent.
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
	// One run resolves both log sources: the run's own trainer deployment, and the
	// paired sampler's inference deployment plus the model that owns it.
	run, err := cl.API().GetLoopsRunsRunId(ctx, flags.RunID)
	if err != nil {
		return fmt.Errorf("describe Loops run %s: %w", flags.RunID, err)
	}

	if flags.Sampler {
		sampler := run.Run.Sampler
		if sampler == nil {
			return fmt.Errorf("Loops run %s has no paired sampler to fetch logs from; omit --sampler for the run's trainer logs", flags.RunID)
		}
		fetchLogs := func(q logQuery) (*managementapi.GetLogsResponse, error) {
			return cl.API().GetModelsDeploymentsLogs(ctx, sampler.ModelId, sampler.DeploymentId, deploymentLogParams(q))
		}
		fetchStatus := func() (*tailStatus, error) {
			dep, err := cl.API().GetModelsDeploymentsDeploymentId(ctx, sampler.ModelId, sampler.DeploymentId)
			if err != nil {
				return nil, err
			}
			return deploymentTailStatus(dep, false), nil
		}
		return runLogsCommand(ctx, &logFlags, fetchLogs, fetchStatus)
	}

	if run.Run.DeploymentId == nil {
		return fmt.Errorf("Loops run %s has no deployment yet, so it has no trainer logs", flags.RunID)
	}
	deploymentID := *run.Run.DeploymentId
	fetchLogs := func(q logQuery) (*managementapi.GetLogsResponse, error) {
		direction := managementapi.SortOrder_desc
		return cl.API().GetLoopsDeploymentsLogs(ctx, deploymentID,
			managementapi.GetV1LoopsDeploymentsDeploymentIdLogsParams{
				StartEpochMillis: q.StartEpochMillis,
				EndEpochMillis:   q.EndEpochMillis,
				Limit:            q.Limit,
				Direction:        &direction,
				MinLevel:         q.MinLevel,
			})
	}
	fetchStatus := func() (*tailStatus, error) {
		// The per-deployment endpoint carries the latest status directly, which is
		// cheaper than paging the deployment list.
		deployment, err := cl.API().GetLoopsDeploymentsDeploymentId(ctx, deploymentID)
		if err != nil {
			return nil, err
		}
		name := deployment.Deployment.Status.Name
		return &tailStatus{
			Label: string(name),
			// A trainer only produces logs while it is coming up or running;
			// anything else, including unknown statuses, ends the tail.
			Runnable: slices.Contains(loopsTrainerLiveStatuses, name),
		}, nil
	}
	return runLogsCommand(ctx, &logFlags, fetchLogs, fetchStatus)
}

func commandLoopsUsage(ctx *CommandContext, flags *cmd.LoopsUsageFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	// --user filters org-wide data client-side, so it only means anything against
	// the org-wide listing.
	orgWide := flags.Org || flags.User != ""

	deploymentParams := managementapi.GetV1LoopsDeploymentsParams{}
	samplerParams := managementapi.GetV1LoopsSamplersParams{}
	if orgWide {
		scope := "org"
		deploymentParams.Scope = &scope
		samplerParams.Scope = &scope
	}
	deploymentsResp, err := cl.API().GetLoopsDeployments(ctx, deploymentParams)
	if err != nil {
		return fmt.Errorf("list Loops deployments: %w", err)
	}
	samplersResp, err := cl.API().GetLoopsSamplers(ctx, samplerParams)
	if err != nil {
		return fmt.Errorf("list Loops samplers: %w", err)
	}

	// Samplers paired to a trainer already appear as that trainer's row. Match
	// them by id across every deployment before the owner filter, so a
	// filtered-out trainer's sampler is not then counted as standalone.
	paired := map[string]struct{}{}
	for _, deployment := range deploymentsResp.Deployments {
		if deployment.Sampler != nil {
			paired[deployment.Sampler.Id] = struct{}{}
		}
	}

	rows := make([]cmd.LoopsUsageRow, 0, len(deploymentsResp.Deployments)+len(samplersResp.Samplers))
	for _, deployment := range deploymentsResp.Deployments {
		row := cmd.LoopsUsageRow{
			BaseModel:           deployment.BaseModel,
			CreatedAt:           deployment.CreatedAt.UTC().Format(time.RFC3339),
			TrainerStatus:       string(deployment.Status.Name),
			TrainerInstanceType: deployment.InstanceType.Name,
			TrainerNodeCount:    1,
		}
		// An idle trainer has no active run but still exposes the most recent one
		// as a usable handle.
		if deployment.ActiveRunId != nil {
			row.RunID = *deployment.ActiveRunId
		} else if deployment.LatestRunId != nil {
			row.RunID = *deployment.LatestRunId
		}
		if deployment.User.Email != nil {
			row.Owner = *deployment.User.Email
		}
		if deployment.NodeCount != nil && *deployment.NodeCount > 1 {
			row.TrainerNodeCount = *deployment.NodeCount
		}
		row.TrainerGPUs = deployment.InstanceType.GpuCount * row.TrainerNodeCount
		if deployment.Sampler != nil {
			loopsApplySamplerToRow(&row, *deployment.Sampler)
		}
		if flags.User != "" && row.Owner != flags.User {
			continue
		}
		rows = append(rows, row)
	}
	for _, sampler := range samplersResp.Samplers {
		if _, dup := paired[sampler.Id]; dup {
			continue
		}
		row := cmd.LoopsUsageRow{
			BaseModel: sampler.BaseModel,
			CreatedAt: sampler.CreatedAt.UTC().Format(time.RFC3339),
		}
		if sampler.User.Email != nil {
			row.Owner = *sampler.User.Email
		}
		loopsApplySamplerToRow(&row, sampler)
		if flags.User != "" && row.Owner != flags.User {
			continue
		}
		rows = append(rows, row)
	}
	// Timestamps are RFC 3339 in UTC, so they sort lexicographically.
	slices.SortStableFunc(rows, func(a, b cmd.LoopsUsageRow) int {
		return strings.Compare(b.CreatedAt, a.CreatedAt)
	})

	// The summary totals every row, including the ones the table hides, so it
	// reports true capacity rather than what happens to be on screen.
	summary := loopsUsageSummaryOf(rows)
	shown := rows
	if !flags.All {
		shown = slices.DeleteFunc(slices.Clone(rows), func(row cmd.LoopsUsageRow) bool {
			return !loopsRowHoldsLiveGPUs(row)
		})
	}

	if ctx.JSON {
		ctx.OutputJSON(cmd.LoopsUsageResult{Summary: summary, Rows: shown})
		return nil
	}
	ctx.Outputf("Trainer GPUs: %d in use, %d scaled to zero. Sampler GPUs: %d in use, %d scaled to zero.\n",
		summary.TrainerInUse, summary.TrainerScaledToZero, summary.SamplerInUse, summary.SamplerScaledToZero)
	if len(rows) == 0 {
		ctx.LogLine("No Loops trainers or samplers found.")
		return nil
	}
	if hidden := len(rows) - len(shown); hidden > 0 {
		ctx.Logf("%d allocation(s) holding no live GPUs hidden; pass --all to show them.\n", hidden)
	}
	if len(shown) == 0 {
		return nil
	}
	headers := []string{"RUN", "BASE MODEL", "TRAINER GPU", "TRAINER STATUS", "SAMPLER GPU", "SAMPLER STATUS", "CREATED"}
	if orgWide {
		headers = slices.Insert(headers, 1, "OWNER")
	}
	tableRows := make([][]string, 0, len(shown))
	for _, row := range shown {
		out := []string{row.RunID}
		if orgWide {
			out = append(out, row.Owner)
		}
		tableRows = append(tableRows, append(out,
			row.BaseModel,
			loopsFormatGPUCell(row.TrainerInstanceType, row.TrainerNodeCount, row.TrainerGPUs),
			row.TrainerStatus,
			loopsFormatGPUCell(row.SamplerInstanceType, row.SamplerNodeCount, row.SamplerGPUs),
			row.SamplerStatus,
			row.CreatedAt,
		))
	}
	ctx.OutputTable(TableOutput{Headers: headers, Rows: tableRows})
	return nil
}

func commandLoopsCheckpointList(ctx *CommandContext, flags *cmd.LoopsCheckpointListFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	params := managementapi.GetV1LoopsCheckpointsParams{}
	if flags.RunID != "" {
		params.RunId = &flags.RunID
	} else {
		params.BaseModel = &flags.BaseModel
	}
	resp, err := cl.API().GetLoopsCheckpoints(ctx, params)
	if err != nil {
		return fmt.Errorf("list Loops checkpoints: %w", err)
	}

	// The server already returns newest-first, so only asc needs a re-sort.
	checkpoints := resp.Checkpoints
	if flags.Direction == "asc" {
		slices.Reverse(checkpoints)
	}

	if ctx.JSON {
		ctx.OutputJSON(managementapi.ListLoopsCheckpointsResponse{Checkpoints: checkpoints})
		return nil
	}
	if len(checkpoints) == 0 {
		ctx.LogLine("No Loops checkpoints found.")
		return nil
	}
	rows := make([][]string, 0, len(checkpoints))
	for _, checkpoint := range checkpoints {
		rows = append(rows, []string{
			checkpoint.Id,
			checkpoint.CheckpointId,
			checkpoint.RunId,
			string(checkpoint.Target),
			checkpoint.CheckpointType,
			formatBytes(int64(checkpoint.SizeBytes)),
			checkpoint.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"ID", "NAME", "RUN", "TARGET", "TYPE", "SIZE", "CREATED"},
		Rows:    rows,
	})
	return nil
}

func commandLoopsCheckpointFiles(ctx *CommandContext, flags *cmd.LoopsCheckpointFilesFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	var jw *JSONArrayWriter
	if ctx.JSON {
		jw = ctx.NewJSONArrayWriter()
		defer jw.Close()
	}
	var rows [][]string

	// The server pages this at 1000 files by default, so walk every page. The
	// URLs are short-lived, so emit each record as its page arrives rather than
	// buffering the whole listing. An empty page also ends the walk, so a server
	// that keeps handing back a token cannot spin here forever.
	params := managementapi.GetV1LoopsCheckpointsCheckpointIdFilesParams{}
	for {
		resp, err := cl.API().GetLoopsCheckpointsFiles(ctx, flags.CheckpointID, params)
		if err != nil {
			return fmt.Errorf("list files for Loops checkpoint %s: %w", flags.CheckpointID, err)
		}
		for _, file := range resp.PresignedUrls {
			if ctx.JSON {
				jw.Write(file)
			} else {
				rows = append(rows, []string{
					file.RelativeFileName,
					formatBytes(int64(file.SizeBytes)),
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
		ctx.LogLine("No files found for this Loops checkpoint.")
		return nil
	}
	ctx.OutputTable(TableOutput{Headers: []string{"NAME", "SIZE", "URL"}, Rows: rows})
	return nil
}

// commandLoopsCheckpointDeploy delegates to the truss CLI: it is the only
// command in this surface backed by GraphQL rather than REST, and its config
// input is a Python file only truss can evaluate.
func commandLoopsCheckpointDeploy(ctx *CommandContext, flags *cmd.LoopsCheckpointDeployFlags) error {
	args := []string{"loops", "checkpoints", "deploy"}
	args = trussArg(args, "run-id", flags.RunID)
	args = trussArg(args, "checkpoints", strings.Join(flags.Checkpoint, ","))
	args = trussArg(args, "checkpoint-ids", strings.Join(flags.CheckpointID, ","))
	args = trussArg(args, "config", flags.Config)
	args = trussBoolArg(args, "dry-run", flags.DryRun)

	return trussRun(ctx, trussInvocation{
		Flags:       flags.TrussFlags,
		Args:        args,
		ForwardAuth: !flags.TrussNoForwardAuth,
		JSONResult:  true,
	})
}

// loopsApplySamplerToRow fills in a usage row's sampler half.
func loopsApplySamplerToRow(row *cmd.LoopsUsageRow, sampler managementapi.LoopsSampler) {
	row.SamplerStatus = string(sampler.Status.Name)
	row.SamplerNodeCount = 1
	if sampler.NodeCount != nil && *sampler.NodeCount > 1 {
		row.SamplerNodeCount = *sampler.NodeCount
	}
	if sampler.InstanceType != nil {
		row.SamplerInstanceType = sampler.InstanceType.Name
		row.SamplerGPUs = sampler.InstanceType.GpuCount * row.SamplerNodeCount
	}
}

// loopsUsageSummaryOf totals GPU capacity across rows, splitting each half into
// in-use versus scaled to zero. Terminal allocations hold no GPUs at all and land
// in neither bucket, even though they still report the instance type they last
// ran on.
func loopsUsageSummaryOf(rows []cmd.LoopsUsageRow) cmd.LoopsUsageSummary {
	var summary cmd.LoopsUsageSummary
	for _, row := range rows {
		switch {
		case slices.Contains(loopsTrainerLiveStatuses, managementapi.Name(row.TrainerStatus)):
			summary.TrainerInUse += row.TrainerGPUs
		case row.TrainerStatus == string(managementapi.Name_SCALED_TO_ZERO):
			summary.TrainerScaledToZero += row.TrainerGPUs
		}
		switch {
		case slices.Contains(loopsSamplerLiveStatuses, managementapi.DeploymentStatus(row.SamplerStatus)):
			summary.SamplerInUse += row.SamplerGPUs
		case row.SamplerStatus == string(managementapi.DeploymentStatus_SCALED_TO_ZERO):
			summary.SamplerScaledToZero += row.SamplerGPUs
		}
	}
	return summary
}

// loopsRowHoldsLiveGPUs reports whether either half of a usage row is holding
// GPUs right now. Idle (scaled to zero) and terminal allocations return false so
// the default view can hide them while the summary still counts them.
func loopsRowHoldsLiveGPUs(row cmd.LoopsUsageRow) bool {
	return slices.Contains(loopsTrainerLiveStatuses, managementapi.Name(row.TrainerStatus)) ||
		slices.Contains(loopsSamplerLiveStatuses, managementapi.DeploymentStatus(row.SamplerStatus))
}

// loopsFormatGPUCell renders an allocation as "<instance-type> (<gpus> GPUs)",
// appending the node count when the allocation spans more than one node. An
// allocation with no GPUs renders as just its instance type, and an absent one as
// the empty cell.
func loopsFormatGPUCell(instanceType string, nodeCount, gpus int) string {
	if instanceType == "" {
		return ""
	}
	if gpus == 0 {
		return instanceType
	}
	if nodeCount > 1 {
		return fmt.Sprintf("%s (%d GPUs across %d nodes)", instanceType, gpus, nodeCount)
	}
	return fmt.Sprintf("%s (%d GPUs)", instanceType, gpus)
}
