package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// commandLoops groups the `baseten loops` subcommands.
var commandLoops = Command{
	Name:    "loops",
	Summary: "Manage Loops runs and checkpoints",
	Children: []Command{
		commandLoopsCheckpoint,
		commandLoopsRun,
		{
			Name:    "usage",
			Summary: "Report Loops GPU capacity",
			Description: "Report Loops GPU capacity, one row per trainer deployment plus a row " +
				"for each standalone sampler.\n\n" +
				"A summary above the table totals the GPUs in use and scaled to zero. Rows " +
				"holding no live GPUs (scaled to zero, or in a terminal state) are counted in " +
				"the summary but hidden unless you pass --all.",
			Flags: LoopsUsageFlags{},
			Output: &CommandOutput[LoopsUsageResult]{
				TextDescription: "A summary line of trainer and sampler GPU totals, then a table with " +
					"columns: RUN, OWNER (with --org), BASE MODEL, TRAINER GPU, TRAINER STATUS, " +
					"SAMPLER GPU, SAMPLER STATUS, CREATED.",
				JSONDescription: "An object with the GPU summary and one entry per row, each carrying the " +
					"trainer and sampler allocation and status.",
				Examples: []CommandExample{
					{
						Description: "Report your own Loops GPU usage.",
						Command:     "baseten loops usage",
					},
					{
						Description: "Report GPU usage across the organization, including idle allocations.",
						Command:     "baseten loops usage --org --all",
					},
					{
						Description: "Report GPU usage for one owner.",
						Command:     "baseten loops usage --org --user someone@example.com",
					},
				},
				JQExample: CommandExample{
					Description: "Print the total trainer GPUs in use.",
					Command:     "baseten loops usage --jq '.summary.trainer_in_use'",
				},
			},
		},
	},
}

// commandLoopsRun groups the `baseten loops run` subcommands.
var commandLoopsRun = Command{
	Name:    "run",
	Summary: "Manage Loops runs",
	Children: []Command{
		{
			Name:    "create",
			Summary: "Create a Loops run and its paired sampler",
			Description: "Create a Loops session, a run for the given base model, and the run's " +
				"paired sampler.\n\n" +
				"Returns as soon as the resources are provisioned. Both halves finish coming up " +
				"in the background; poll with 'baseten loops run describe' or watch " +
				"'baseten loops run logs'.",
			Flags: LoopsRunCreateFlags{},
			Output: &CommandOutput[managementapi.LoopsRun]{
				TextDescription: "On success, prints the new run's ID and base model to stderr; no stdout output.",
				JSONDescription: "The created run, including its paired sampler.",
				Examples: []CommandExample{
					{
						Description: "Create a run for a base model.",
						Command:     "baseten loops run create --base-model Qwen/Qwen3-8B",
					},
					{
						Description: "Create a named run with four data-parallel trainer replicas.",
						Command:     "baseten loops run create --base-model Qwen/Qwen3-8B --name experiment-1 --replicas 4",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the new run's ID.",
					Command:     "baseten loops run create --base-model Qwen/Qwen3-8B --jq '.id'",
				},
			},
		},
		{
			Name:    "list",
			Summary: "List Loops runs",
			Description: "List Loops runs, newest first.\n\n" +
				"Lists your own runs; pass --org to list every run in the organization, which " +
				"adds an Owner column. Inactive runs are hidden unless you pass --all.",
			Flags: LoopsRunListFlags{},
			Output: &CommandOutput[managementapi.ListLoopsRunsResponse]{
				TextDescription: "Table with columns: ID, OWNER (with --org), BASE MODEL, STATUS, CREATED. " +
					"When no runs match, prints \"No Loops runs found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List your active runs.",
						Command:     "baseten loops run list",
					},
					{
						Description: "List every run in the organization, including inactive ones.",
						Command:     "baseten loops run list --org --all",
					},
					{
						Description: "List runs for one base model, oldest first.",
						Command:     "baseten loops run list --base-model Qwen/Qwen3-8B --direction asc",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the run IDs.",
					Command:     "baseten loops run list --jq '.runs[].id'",
				},
			},
		},
		{
			Name:        "describe",
			Summary:     "Describe a Loops run",
			Description: "Describe a Loops run by ID, including its paired sampler.",
			Flags:       LoopsRunDescribeFlags{},
			Output: &CommandOutput[managementapi.LoopsRun]{
				TextDescription: "Field-per-line summary of the run, followed by its sampler when one exists.",
				Examples: []CommandExample{
					{
						Description: "Describe a run by ID.",
						Command:     "baseten loops run describe --run-id <run-id>",
					},
				},
				JQExample: CommandExample{
					Description: "Print the run's sampler base URL.",
					Command:     "baseten loops run describe --run-id <run-id> --jq '.sampler.base_url'",
				},
			},
		},
		{
			Name:    "deactivate",
			Summary: "Deactivate a Loops run",
			Description: "Deactivate a Loops run, tearing down both the run and its paired " +
				"sampler. Saved checkpoints remain accessible.\n\n" +
				"Prompts for yes/no confirmation. Pass --yes to skip the prompt. When " +
				"stdin is not a terminal, --yes is required.",
			Flags: LoopsRunDeactivateFlags{},
			Output: &CommandOutput[managementapi.DeactivateLoopsRunResponse]{
				TextDescription: "On success, prints \"Deactivated Loops run <id>\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Deactivate a run without the confirmation prompt.",
						Command:     "baseten loops run deactivate --run-id <run-id> --yes",
					},
				},
				JQExample: CommandExample{
					Description: "Print the deactivated run's base model.",
					Command:     "baseten loops run deactivate --run-id <run-id> --yes --jq '.base_model'",
				},
			},
		},
		{
			Name:    "logs",
			Summary: "Stream or tail logs for a Loops run",
			Description: "Fetch logs for a Loops run.\n\n" +
				"By default returns the run's trainer logs; pass --sampler for the paired " +
				"sampler's logs instead, which are a separate stream.\n\n" +
				"By default returns up to --limit lines from the last 30 minutes, newest first. " +
				"Use --start/--end or --since to scope the window (max 7 days). Use --tail to " +
				"stream live logs until the run stops producing them or you interrupt with Ctrl-C.\n\n" +
				"For machine-readable streaming, prefer --output jsonl over --output json.",
			Flags: LoopsRunLogsFlags{},
			Output: &CommandOutput[managementapi.Log]{
				JSONArrayStreamed: true,
				TextDescription:   "One line per log record: \"[YYYY-MM-DD HH:MM:SS]: (replica) message\".",
				Examples: []CommandExample{
					{
						Description: "Print a run's trainer logs over the last hour.",
						Command:     "baseten loops run logs --run-id <run-id> --since 1h",
					},
					{
						Description: "Tail the paired sampler's logs.",
						Command:     "baseten loops run logs --run-id <run-id> --sampler --tail",
					},
				},
				JQExample: CommandExample{
					Description: "Stream just the log messages as a JSONL stream.",
					Command:     "baseten loops run logs --run-id <run-id> --output jsonl --jq '.message'",
				},
			},
		},
	},
}

// commandLoopsCheckpoint groups the `baseten loops checkpoint` subcommands.
var commandLoopsCheckpoint = Command{
	Name:    "checkpoint",
	Summary: "Work with Loops checkpoints",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List Loops checkpoints",
			Description: "List checkpoints for a Loops run, newest first.\n\n" +
				"Identify the run with --run-id, or pass --base-model to list checkpoints " +
				"across all of your runs of that base model.",
			Flags: LoopsCheckpointListFlags{},
			Output: &CommandOutput[managementapi.ListLoopsCheckpointsResponse]{
				TextDescription: "Table with columns: ID, NAME, RUN, TARGET, TYPE, SIZE, CREATED. When no " +
					"checkpoints match, prints \"No Loops checkpoints found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List a run's checkpoints.",
						Command:     "baseten loops checkpoint list --run-id <run-id>",
					},
					{
						Description: "List every checkpoint for a base model, oldest first.",
						Command:     "baseten loops checkpoint list --base-model Qwen/Qwen3-8B --direction asc",
					},
				},
				JQExample: CommandExample{
					Description: "Print the deployable checkpoint names.",
					Command:     "baseten loops checkpoint list --run-id <run-id> --jq '.checkpoints[] | select(.target != \"trainer\") | .checkpoint_id'",
				},
			},
		},
		{
			Name:    "files",
			Summary: "List a Loops checkpoint's files",
			Description: "List presigned download URLs for the files under a Loops checkpoint.\n\n" +
				"The URLs are short-lived. Use 'baseten loops checkpoint list' to find " +
				"checkpoint IDs.\n\n" +
				"For machine-readable streaming, prefer --output jsonl over --output json.",
			Flags: LoopsCheckpointFilesFlags{},
			Output: &CommandOutput[managementapi.CheckpointFile]{
				JSONArrayStreamed: true,
				TextDescription:   "Table with columns: NAME, SIZE, URL.",
				Examples: []CommandExample{
					{
						Description: "List a checkpoint's files.",
						Command:     "baseten loops checkpoint files --checkpoint-id <checkpoint-id>",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the download URLs.",
					Command:     "baseten loops checkpoint files --checkpoint-id <checkpoint-id> --output jsonl --jq '.url'",
				},
			},
		},
	},
}

// LoopsRunRefFlags identifies a Loops run. Embedded by commands that act on a
// specific run.
type LoopsRunRefFlags struct {
	RunID string `flag:"run-id" desc:"ID of the Loops run." required:"true"`
}

// LoopsRunCreateFlags configures `baseten loops run create`.
type LoopsRunCreateFlags struct {
	CommandFlags

	BaseModel string `flag:"base-model" desc:"HuggingFace ID of the base model to fine-tune (for example 'Qwen/Qwen3-8B')." required:"true"`
	Name      string `flag:"name" desc:"Display name for the run. Defaults to the base model."`
	Replicas  int    `flag:"replicas" desc:"Number of data-parallel trainer replicas. The trainer deployment runs this many copies of the model's preset node group, so --replicas 4 on a 4-node preset provisions 16 nodes. Defaults to 1."`
	Team      string `flag:"team" desc:"Team name or ID that owns the run's infrastructure. Defaults to the organization's default team."`
}

// LoopsRunListFlags configures `baseten loops run list`.
type LoopsRunListFlags struct {
	CommandFlags

	BaseModel string `flag:"base-model" desc:"Only list runs of this base model."`
	Org       bool   `flag:"org" desc:"List every run in the organization, with its owner, instead of only your own."`
	All       bool   `flag:"all" desc:"Include inactive runs."`
	Direction string `flag:"direction" desc:"Sort order by creation time: 'desc' (newest first) or 'asc' (oldest first)." enum:"asc,desc" default:"desc"`
}

// LoopsRunDescribeFlags configures `baseten loops run describe`.
type LoopsRunDescribeFlags struct {
	CommandFlags
	LoopsRunRefFlags
}

// LoopsRunDeactivateFlags configures `baseten loops run deactivate`.
type LoopsRunDeactivateFlags struct {
	CommandFlags
	LoopsRunRefFlags

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// LoopsRunLogsFlags configures `baseten loops run logs`.
type LoopsRunLogsFlags struct {
	CommandFlags
	LoopsRunRefFlags
	LoopsLogFlags

	Sampler bool `flag:"sampler" desc:"Fetch the paired sampler's logs instead of the run's trainer logs."`
}

// LoopsLogFlags is the log-query flag set for `baseten loops run logs`. It is a
// subset of [LogFlags]: the Loops trainer logs endpoint supports only the window,
// limit, and severity filters, so the message and replica filters that model
// deployment logs accept are not offered here.
type LoopsLogFlags struct {
	Tail bool `flag:"tail" desc:"Stream new logs as they arrive until the run stops producing them or you interrupt with Ctrl-C. Cannot be combined with the time-range or filter flags. For machine-readable streaming, prefer --output jsonl over --output json."`

	Start time.Time     `flag:"start" desc:"Start of the log time range. Accepts ISO 8601 (e.g. '2026-05-14', '2026-05-14T12:00:00', '2026-05-14T12:00:00Z'). Values without a timezone designator are interpreted in the local timezone. Default is 30 minutes before the end. Window must be at most 7 days."`
	End   time.Time     `flag:"end" desc:"End of the log time range. Accepts ISO 8601; values without a timezone designator are interpreted in the local timezone. Default is now. Window must be at most 7 days."`
	Since time.Duration `flag:"since" desc:"Shortcut for fetching logs from a relative time ago until now. Accepts a Go duration (e.g. '30m', '1h30m') or '<N>d' (e.g. '3d'). Maximum '7d'. Mutually exclusive with --start and --end."`

	Limit int `flag:"limit" desc:"Maximum number of log lines to return, paging backward from the end of the window. Use 0 for no limit (every log line in the window). Not applicable with --tail." default:"5000"`

	// PageSize is the per-request fetch size while paging. Hidden; exists so
	// tests can force multiple pages without generating a full page of logs.
	PageSize int `flag:"page-size" hidden:"true" desc:"Log lines fetched per backend request while paging." default:"1000"`

	MinLevel string `flag:"min-level" desc:"Only return logs at or above this severity level." enum:"debug,info,warning,error"`
}

// LoopsUsageFlags configures `baseten loops usage`.
type LoopsUsageFlags struct {
	CommandFlags

	Org  bool   `flag:"org" desc:"Report usage across the organization, with each allocation's owner, instead of only your own."`
	User string `flag:"user" desc:"Only report allocations owned by this email. Implies --org."`
	All  bool   `flag:"all" desc:"Include allocations holding no live GPUs (scaled to zero, or in a terminal state)."`
}

// LoopsUsageResult is the JSON output of `baseten loops usage`: the GPU summary
// plus one entry per displayed row. The rows are a client-side join of trainer
// deployments and standalone samplers, so this has no generated equivalent.
type LoopsUsageResult struct {
	Summary LoopsUsageSummary `json:"summary"`
	Rows    []LoopsUsageRow   `json:"rows"`
}

// LoopsUsageSummary totals GPU capacity across every allocation, including rows
// hidden from the table.
type LoopsUsageSummary struct {
	TrainerInUse        int `json:"trainer_in_use"`
	TrainerScaledToZero int `json:"trainer_scaled_to_zero"`
	SamplerInUse        int `json:"sampler_in_use"`
	SamplerScaledToZero int `json:"sampler_scaled_to_zero"`
}

// LoopsUsageRow is one row of `baseten loops usage`: a trainer deployment with
// its paired sampler, or a standalone sampler with the trainer fields empty.
type LoopsUsageRow struct {
	RunID     string `json:"run_id,omitempty"`
	Owner     string `json:"owner,omitempty"`
	BaseModel string `json:"base_model"`
	CreatedAt string `json:"created_at"`

	TrainerStatus       string `json:"trainer_status,omitempty"`
	TrainerInstanceType string `json:"trainer_instance_type,omitempty"`
	TrainerNodeCount    int    `json:"trainer_node_count,omitempty"`
	TrainerGPUs         int    `json:"trainer_gpus"`

	SamplerStatus       string `json:"sampler_status,omitempty"`
	SamplerInstanceType string `json:"sampler_instance_type,omitempty"`
	SamplerNodeCount    int    `json:"sampler_node_count,omitempty"`
	SamplerGPUs         int    `json:"sampler_gpus"`
}

// LoopsCheckpointListFlags configures `baseten loops checkpoint list`.
type LoopsCheckpointListFlags struct {
	CommandFlags

	RunID     string `flag:"run-id" desc:"List checkpoints saved by this run." oneof:"loops-checkpoint-scope"`
	BaseModel string `flag:"base-model" desc:"List checkpoints across your runs of this base model." oneof:"loops-checkpoint-scope"`
	Direction string `flag:"direction" desc:"Sort order by creation time: 'desc' (newest first) or 'asc' (oldest first)." enum:"asc,desc" default:"desc"`
}

// LoopsCheckpointFilesFlags configures `baseten loops checkpoint files`.
type LoopsCheckpointFilesFlags struct {
	CommandFlags

	CheckpointID string `flag:"checkpoint-id" desc:"ID of the Loops checkpoint." required:"true"`
}
