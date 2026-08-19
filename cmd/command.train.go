package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// commandTrain groups the `baseten train` subcommands.
var commandTrain = Command{
	Name:    "train",
	Summary: "Manage training projects and jobs",
	Children: []Command{
		commandTrainCapacity,
		commandTrainCheckpoint,
		commandTrainJob,
		commandTrainProject,
	},
}

// commandTrainCapacity groups the `baseten train capacity` subcommands.
var commandTrainCapacity = Command{
	Name:    "capacity",
	Summary: "Manage training GPU capacity",
	Children: []Command{
		{
			Name:    "describe",
			Summary: "Describe training GPU capacity",
			Description: "Describe the organization's training GPU limits and current usage, per GPU type.\n\n" +
				"Per-team limits are listed below the organization totals when any are set.",
			Flags: TrainCapacityDescribeFlags{},
			Output: &CommandOutput[managementapi.GetTrainingGpuCapacityResponse]{
				TextDescription: "An organization table with columns: GPU TYPE, IN USE, LIMIT, BASELINE, then a " +
					"per-team table with a leading TEAM column when teams have limits set.",
				Examples: []CommandExample{
					{
						Description: "Describe training GPU capacity.",
						Command:     "baseten train capacity describe",
					},
				},
				JQExample: CommandExample{
					Description: "Print the organization's H100 limit.",
					Command:     "baseten train capacity describe --jq '.gpu_capacities[] | select(.gpu_type == \"H100\") | .limit'",
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a team's training GPU limit",
			Description: "Update the maximum concurrent GPUs of one type that a team may use.\n\n" +
				"The limit is per GPU type, so raising H100 leaves other types alone.",
			Flags: TrainCapacityUpdateFlags{},
			Output: &CommandOutput[managementapi.PatchTeamTrainingGpuCapacityResponse]{
				TextDescription: "On success, prints the team, GPU type, and new limit to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Raise a team's H100 limit.",
						Command:     "baseten train capacity update --team research --gpu-type H100 --max-gpus 32",
					},
				},
				JQExample: CommandExample{
					Description: "Print the applied limit.",
					Command:     "baseten train capacity update --team research --gpu-type H100 --max-gpus 32 --jq '.team_gpu_capacity.limit'",
				},
			},
		},
	},
}

// commandTrainCheckpoint groups the `baseten train checkpoint` subcommands.
var commandTrainCheckpoint = Command{
	Name:    "checkpoint",
	Summary: "Inspect training job checkpoints",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List a training job's checkpoints",
			Description: "List the checkpoints a training job has synced, newest first.\n\n" +
				"Checkpoints only appear for jobs that enabled checkpointing.",
			Flags: TrainCheckpointListFlags{},
			Output: &CommandOutput[managementapi.GetTrainingJobCheckpointsResponse]{
				TextDescription: "Table with columns: ID, TYPE, BASE MODEL, SIZE, SYNC, CREATED. " +
					"When the job has no checkpoints, prints \"No checkpoints found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List a job's checkpoints.",
						Command:     "baseten train checkpoint list --job-id p7qr9qv",
					},
					{
						Description: "List a job's checkpoints oldest first.",
						Command:     "baseten train checkpoint list --job-id p7qr9qv --direction asc",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the checkpoint IDs.",
					Command:     "baseten train checkpoint list --job-id p7qr9qv --jq '.checkpoints[].checkpoint_id'",
				},
			},
		},
		{
			Name:    "files",
			Summary: "List presigned URLs for a job's checkpoint files",
			Description: "List presigned download URLs for the files of a training job's checkpoints.\n\n" +
				"The URLs are short-lived.\n\n" +
				"For machine-readable streaming, prefer --output jsonl over --output json.",
			Flags: TrainCheckpointFilesFlags{},
			Output: &CommandOutput[managementapi.CheckpointFile]{
				JSONArrayStreamed: true,
				TextDescription: "Table with columns: NODE, NAME, SIZE, MODIFIED, URL. " +
					"When there are no files, prints \"No checkpoint files found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List every checkpoint file of a job.",
						Command:     "baseten train checkpoint files --job-id p7qr9qv",
					},
					{
						Description: "Save the URLs to a file for a download script.",
						Command:     "baseten train checkpoint files --job-id p7qr9qv --output json > checkpoint-urls.json",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the download URLs.",
					Command:     "baseten train checkpoint files --job-id p7qr9qv --output jsonl --jq '.url'",
				},
			},
		},
	},
}

// commandTrainJob groups the `baseten train job` subcommands.
var commandTrainJob = Command{
	Name:    "job",
	Summary: "Manage training jobs",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List training jobs",
			Description: "List training jobs, newest first.\n\n" +
				"Lists every job in projects your teams can access. Pass --project to narrow to " +
				"one project, or --status to narrow to particular job states.",
			Flags: TrainJobListFlags{},
			Output: &CommandOutput[managementapi.SearchTrainingJobsResponse]{
				TextDescription: "Table with columns: ID, PROJECT, NAME, STATUS, INSTANCE TYPE, NODES, CREATED. " +
					"When no jobs match, prints \"No training jobs found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List every training job you can see.",
						Command:     "baseten train job list",
					},
					{
						Description: "List the running jobs of one project.",
						Command:     "baseten train job list --project my-project --status running",
					},
					{
						Description: "List jobs oldest first.",
						Command:     "baseten train job list --direction asc",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the job IDs.",
					Command:     "baseten train job list --jq '.training_jobs[].id'",
				},
			},
		},
		{
			Name:        "describe",
			Summary:     "Describe a training job",
			Description: "Describe a training job by ID, including its project, compute, and checkpoint sync state.",
			Flags:       TrainJobDescribeFlags{},
			Output: &CommandOutput[managementapi.GetTrainingJobResponse]{
				TextDescription: "A field-per-line summary of the job and its project.",
				Examples: []CommandExample{
					{
						Description: "Describe a job.",
						Command:     "baseten train job describe --job-id p7qr9qv",
					},
				},
				JQExample: CommandExample{
					Description: "Print the job's current status.",
					Command:     "baseten train job describe --job-id p7qr9qv --jq '.training_job.current_status'",
				},
			},
		},
		{
			Name:    "logs",
			Summary: "Fetch logs for a training job",
			Description: "Fetch logs for a training job.\n\n" +
				"Without --tail, returns the logs in a time window; with --tail, streams new logs " +
				"until the job stops producing them or you interrupt with Ctrl-C.",
			Flags: TrainJobLogsFlags{},
			Output: &CommandOutput[managementapi.GetLogsResponse]{
				TextDescription: "One log line per row, timestamped. With --output jsonl, one JSON log record per line.",
				Examples: []CommandExample{
					{
						Description: "Fetch the last 30 minutes of logs.",
						Command:     "baseten train job logs --job-id p7qr9qv",
					},
					{
						Description: "Stream logs as they arrive.",
						Command:     "baseten train job logs --job-id p7qr9qv --tail",
					},
					{
						Description: "Fetch a day of error logs.",
						Command:     "baseten train job logs --job-id p7qr9qv --since 1d --min-level error",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the log messages.",
					Command:     "baseten train job logs --job-id p7qr9qv --jq '.message'",
				},
			},
		},
		{
			Name:    "metrics",
			Summary: "Report resource metrics for a training job",
			Description: "Report CPU, memory, GPU, and storage metrics for a training job.\n\n" +
				"Each series is reported as its most recent sample. Pass --start and --end, or " +
				"--since, to select the window the samples come from.",
			Flags: TrainJobMetricsFlags{},
			Output: &CommandOutput[managementapi.GetTrainingJobMetricsResponse]{
				TextDescription: "Table with columns: METRIC, NODE, VALUE, MEASURED.",
				Examples: []CommandExample{
					{
						Description: "Report a job's latest metrics.",
						Command:     "baseten train job metrics --job-id p7qr9qv",
					},
					{
						Description: "Report metrics sampled over the last hour.",
						Command:     "baseten train job metrics --job-id p7qr9qv --since 1h",
					},
				},
				JQExample: CommandExample{
					Description: "Print the GPU utilization series.",
					Command:     "baseten train job metrics --job-id p7qr9qv --jq '.gpu_utilization'",
				},
			},
		},
		{
			Name:    "stop",
			Summary: "Stop a training job",
			Description: "Stop a running or queued training job.\n\n" +
				"Stopping is not reversible: use 'baseten train job recreate' to run the same " +
				"configuration again. Checkpoints already synced remain accessible.",
			Flags: TrainJobStopFlags{},
			Output: &CommandOutput[managementapi.StopTrainingJobResponse]{
				TextDescription: "On success, prints the stopped job's ID to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Stop a job, confirming interactively.",
						Command:     "baseten train job stop --job-id p7qr9qv",
					},
					{
						Description: "Stop a job without confirmation, for scripts.",
						Command:     "baseten train job stop --job-id p7qr9qv --yes",
					},
				},
				JQExample: CommandExample{
					Description: "Print the stopped job's status.",
					Command:     "baseten train job stop --job-id p7qr9qv --yes --jq '.training_job.current_status'",
				},
			},
		},
		{
			Name:    "recreate",
			Summary: "Recreate a training job",
			Description: "Create a new training job from an existing job's configuration.\n\n" +
				"The original job is left as it is. The new job gets a new ID and starts from the " +
				"beginning.",
			Flags: TrainJobRecreateFlags{},
			Output: &CommandOutput[managementapi.RecreateTrainingJobResponse]{
				TextDescription: "On success, prints the new job's ID to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Rerun a job's configuration.",
						Command:     "baseten train job recreate --job-id p7qr9qv",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the new job's ID.",
					Command:     "baseten train job recreate --job-id p7qr9qv --jq '.training_job.id'",
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a training job's queue priority",
			Description: "Update a queued training job's priority. Higher values are dequeued first.\n\n" +
				"Only jobs in the PENDING state can have their priority changed.",
			Flags: TrainJobUpdateFlags{},
			Output: &CommandOutput[managementapi.UpdateTrainingJobResponse]{
				TextDescription: "On success, prints the job's ID and new priority to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Raise a queued job's priority.",
						Command:     "baseten train job update --job-id p7qr9qv --priority 10",
					},
				},
				JQExample: CommandExample{
					Description: "Print the job's new priority.",
					Command:     "baseten train job update --job-id p7qr9qv --priority 10 --jq '.training_job.priority'",
				},
			},
		},
		{
			Name:    "download",
			Summary: "Download a training job's artifacts",
			Description: "Download the code archive that was uploaded with a training job as an " +
				"uncompressed tar.\n\n" +
				"Exactly one of --out-file or --out-dir is required. --out-file writes the " +
				"raw tar bytes; --out-dir extracts the tar into the directory. Use " +
				"--overwrite to replace an existing file or write into a non-empty directory.\n\n" +
				"This is the job's input code, not its checkpoints; use " +
				"'baseten train checkpoint files' for those.",
			Flags: TrainJobDownloadFlags{},
			Output: &CommandOutput[TrainJobDownloadResult]{
				TextDescription: "Writes the artifact to disk; prints progress and the final destination " +
					"path to stderr; no stdout output.",
				JSONDescription: "On success, stdout is a JSON object naming the job, with either " +
					"out_file or out_dir set to the path written.",
				Examples: []CommandExample{
					{
						Description: "Extract a job's code into a directory.",
						Command:     "baseten train job download --job-id p7qr9qv --out-dir ./job-code",
					},
					{
						Description: "Save the archive as a tar file instead of extracting it.",
						Command:     "baseten train job download --job-id p7qr9qv --out-file job.tar",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the destination path.",
					Command:     "baseten train job download --job-id p7qr9qv --out-file job.tar --jq '.out_file'",
				},
			},
		},
		commandTrainJobSession,
	},
}

// commandTrainJobSession groups the `baseten train job session` subcommands.
// Only `describe` exists: truss's `update_session` targets a route the backend
// does not serve, and the route that does exist can only adjust a session that
// is already running.
var commandTrainJobSession = Command{
	Name:    "session",
	Summary: "Inspect a training job's interactive sessions",
	Children: []Command{
		{
			Name:    "describe",
			Summary: "Describe a training job's interactive sessions",
			Description: "Describe the interactive sessions of a training job, one per replica.\n\n" +
				"Only replicas with a live session are listed, so a job whose session has not " +
				"started yet reports none.",
			Flags: TrainJobSessionDescribeFlags{},
			Output: &CommandOutput[managementapi.GetAuthCodesResponse]{
				TextDescription: "Table with columns: REPLICA, SESSION, AUTH URL, EXPIRES. " +
					"When no session is live, prints \"No interactive sessions found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "Show a job's interactive sessions.",
						Command:     "baseten train job session describe --job-id p7qr9qv",
					},
				},
				JQExample: CommandExample{
					Description: "Print the first replica's auth URL.",
					Command:     "baseten train job session describe --job-id p7qr9qv --jq '.auth_codes[0].auth_url'",
				},
			},
		},
	},
}

// commandTrainProject groups the `baseten train project` subcommands.
var commandTrainProject = Command{
	Name:    "project",
	Summary: "Manage training projects",
	Children: []Command{
		{
			Name:        "list",
			Summary:     "List training projects",
			Description: "List the training projects your teams can access, newest first.",
			Flags:       TrainProjectListFlags{},
			Output: &CommandOutput[managementapi.ListTrainingProjectsResponse]{
				TextDescription: "Table with columns: ID, NAME, TEAM, LATEST JOB, LATEST STATUS, CREATED. " +
					"When there are no projects, prints \"No training projects found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List your training projects.",
						Command:     "baseten train project list",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the project names.",
					Command:     "baseten train project list --jq '.training_projects[].name'",
				},
			},
		},
		commandTrainProjectCache,
	},
}

// commandTrainProjectCache groups the `baseten train project cache` subcommands.
var commandTrainProjectCache = Command{
	Name:    "cache",
	Summary: "Inspect a training project's cache",
	Children: []Command{
		{
			Name:    "describe",
			Summary: "Describe a training project's cache contents",
			Description: "Describe the files in a training project's shared cache, largest first.\n\n" +
				"The cache is shared by every job in the project and persists between runs.",
			Flags: TrainProjectCacheDescribeFlags{},
			Output: &CommandOutput[managementapi.GetCacheSummaryResponse]{
				TextDescription: "A total size line, then a table with columns: PATH, TYPE, SIZE, PERMISSIONS, MODIFIED. " +
					"When the cache is empty, prints \"Cache is empty.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "Describe a project's cache.",
						Command:     "baseten train project cache describe --project my-project",
					},
					{
						Description: "Sort the cache listing by path.",
						Command:     "baseten train project cache describe --project my-project --sort path",
					},
				},
				JQExample: CommandExample{
					Description: "Print the cached file paths.",
					Command:     "baseten train project cache describe --project my-project --jq '.file_summaries[].path'",
				},
			},
		},
	},
}

// TrainJobRefFlags identifies a training job. The project is resolved from the
// job ID, so callers never pass one: every job endpoint is nested under a
// project, but the search endpoint returns the owning project with the job.
type TrainJobRefFlags struct {
	JobID string `flag:"job-id" desc:"ID of the training job." required:"true"`
}

// TrainCapacityDescribeFlags configures `baseten train capacity describe`.
type TrainCapacityDescribeFlags struct {
	CommandFlags
}

// TrainCapacityUpdateFlags configures `baseten train capacity update`.
type TrainCapacityUpdateFlags struct {
	CommandFlags

	Team    string `flag:"team" desc:"Team name or ID whose limit changes." required:"true"`
	GPUType string `flag:"gpu-type" desc:"GPU type the limit applies to (for example H100)." required:"true"`
	MaxGPUs int    `flag:"max-gpus" desc:"Maximum concurrent GPUs of this type the team may use." required:"true"`
}

// TrainCheckpointListFlags configures `baseten train checkpoint list`.
type TrainCheckpointListFlags struct {
	CommandFlags
	TrainJobRefFlags

	Direction string `flag:"direction" desc:"Sort order by creation time: 'desc' (newest first) or 'asc' (oldest first)." enum:"asc,desc" default:"desc"`
}

// TrainCheckpointFilesFlags configures `baseten train checkpoint files`.
type TrainCheckpointFilesFlags struct {
	CommandFlags
	TrainJobRefFlags
}

// TrainJobListFlags configures `baseten train job list`.
type TrainJobListFlags struct {
	CommandFlags

	Project   string   `flag:"project" desc:"Only list jobs in this training project, by name or ID."`
	Status    []string `flag:"status" desc:"Only list jobs in these states: pending, created, deploying, deploy-failed, running, completed, failed, stopped, preempted. Repeatable."`
	Direction string   `flag:"direction" desc:"Sort order by creation time: 'desc' (newest first) or 'asc' (oldest first)." enum:"asc,desc" default:"desc"`
}

// TrainJobDescribeFlags configures `baseten train job describe`.
type TrainJobDescribeFlags struct {
	CommandFlags
	TrainJobRefFlags
}

// TrainJobLogsFlags configures `baseten train job logs`.
type TrainJobLogsFlags struct {
	CommandFlags
	TrainJobRefFlags
	TrainLogFlags
}

// TrainLogFlags is the log-query flag set for `baseten train job logs`. Like
// Loops, the training logs endpoint supports only the window, limit, and
// severity filters, so the message and replica filters that model deployment
// logs accept are not offered here.
type TrainLogFlags struct {
	Tail bool `flag:"tail" desc:"Stream new logs as they arrive until the job stops producing them or you interrupt with Ctrl-C. Cannot be combined with the time-range or filter flags. For machine-readable streaming, prefer --output jsonl over --output json."`

	Start time.Time     `flag:"start" desc:"Start of the log time range. Accepts ISO 8601 (e.g. '2026-05-14', '2026-05-14T12:00:00', '2026-05-14T12:00:00Z'). Values without a timezone designator are interpreted in the local timezone. Default is 30 minutes before the end. Window must be at most 7 days."`
	End   time.Time     `flag:"end" desc:"End of the log time range. Accepts ISO 8601; values without a timezone designator are interpreted in the local timezone. Default is now. Window must be at most 7 days."`
	Since time.Duration `flag:"since" desc:"Shortcut for fetching logs from a relative time ago until now. Accepts a Go duration (e.g. '30m', '1h30m') or '<N>d' (e.g. '3d'). Maximum '7d'. Mutually exclusive with --start and --end."`

	Limit int `flag:"limit" desc:"Maximum number of log lines to return, paging backward from the end of the window. Use 0 for no limit (every log line in the window). Not applicable with --tail." default:"5000"`

	// PageSize is the per-request fetch size while paging. Hidden; exists so
	// tests can force multiple pages without generating a full page of logs.
	PageSize int `flag:"page-size" hidden:"true" desc:"Log lines fetched per backend request while paging." default:"1000"`

	MinLevel string `flag:"min-level" desc:"Only return logs at or above this severity level." enum:"debug,info,warning,error"`
}

// TrainJobMetricsFlags configures `baseten train job metrics`.
type TrainJobMetricsFlags struct {
	CommandFlags
	TrainJobRefFlags

	Start time.Time     `flag:"start" desc:"Start of the sample window. Accepts ISO 8601; values without a timezone designator are interpreted in the local timezone."`
	End   time.Time     `flag:"end" desc:"End of the sample window. Accepts ISO 8601; values without a timezone designator are interpreted in the local timezone. Default is now."`
	Since time.Duration `flag:"since" desc:"Shortcut for sampling from a relative time ago until now. Accepts a Go duration (e.g. '30m', '1h30m') or '<N>d' (e.g. '3d'). Mutually exclusive with --start and --end."`
}

// TrainJobStopFlags configures `baseten train job stop`.
type TrainJobStopFlags struct {
	CommandFlags
	TrainJobRefFlags

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// TrainJobRecreateFlags configures `baseten train job recreate`.
type TrainJobRecreateFlags struct {
	CommandFlags
	TrainJobRefFlags
}

// TrainJobUpdateFlags configures `baseten train job update`.
type TrainJobUpdateFlags struct {
	CommandFlags
	TrainJobRefFlags

	Priority int `flag:"priority" desc:"New queue priority. Higher values are dequeued first." required:"true"`
}

// TrainJobDownloadFlags configures `baseten train job download`.
type TrainJobDownloadFlags struct {
	CommandFlags
	TrainJobRefFlags

	OutFile   string `flag:"out-file" desc:"Save the artifact as an uncompressed tar file at this path." oneof:"download-out"`
	OutDir    string `flag:"out-dir" desc:"Extract the artifact tar into this directory." oneof:"download-out"`
	Overwrite bool   `flag:"overwrite" desc:"Allow overwriting an existing file or non-empty directory."`
}

// TrainJobDownloadResult is the JSON output of `baseten train job download`.
// The API returns presigned URLs rather than files, so the useful result is
// what landed on disk, which has no generated equivalent. Exactly one of
// OutFile or OutDir is set, matching whichever flag the caller passed.
type TrainJobDownloadResult struct {
	JobID   string `json:"job_id"`
	OutFile string `json:"out_file,omitempty"`
	OutDir  string `json:"out_dir,omitempty"`
}

// TrainJobSessionDescribeFlags configures `baseten train job session describe`.
type TrainJobSessionDescribeFlags struct {
	CommandFlags
	TrainJobRefFlags
}

// TrainProjectListFlags configures `baseten train project list`.
type TrainProjectListFlags struct {
	CommandFlags
}

// TrainProjectCacheDescribeFlags configures `baseten train project cache describe`.
type TrainProjectCacheDescribeFlags struct {
	CommandFlags

	Project string `flag:"project" desc:"Training project to describe, by name or ID." required:"true"`
	Sort    string `flag:"sort" desc:"Sort the listing by 'size' (largest first) or 'path' (alphabetical)." enum:"size,path" default:"size"`
}
