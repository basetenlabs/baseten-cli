package cmd

import "github.com/basetenlabs/baseten-cli/internal/sandboxclient"

// sandboxInteractionCommands are the commands that work inside one sandbox,
// kept apart from the management ones above them in the family.
var sandboxInteractionCommands = []Command{
	processSubcommands,
	{
		Name:      "exec",
		Summary:   "Run a command in a sandbox (PRE-RELEASE)",
		ArgsUsage: "-- COMMAND [ARGS...]",
		MaxArgs:   -1,
		Description: sandboxPreRelease +
			"Alias for 'sandbox process exec'.\n\n" +
			"Runs a command in a deployed sandbox, streaming its output as it arrives. The " +
			"command's exit code becomes the CLI's exit code.\n\n" +
			"Put the command after a literal --, and every sandbox flag before it, so flags " +
			"inside the command are passed through to it. One quoted argument is the whole " +
			"command string, passed to the sandbox's shell as-is; several arguments are " +
			"re-quoted so their boundaries survive.",
		Flags: SandboxExecFlags{},
		Output: &CommandOutput[sandboxclient.ProcessInfo]{
			TextDescription: "The command's output, streamed, with the exit code passed " +
				"through as the CLI's exit code.",
			JSONDescription: "With --output json, the final process record once the command " +
				"exits, and no streamed output.",
			Examples: []CommandExample{
				{
					Description: "Run a command in a sandbox.",
					Command:     "baseten sandbox exec --name my-sandbox -- echo hello",
				},
				{
					Description: "Run a command with extra environment variables.",
					CommandLines: []string{
						"baseten sandbox exec --name my-sandbox",
						"--env DEBUG=1 -- python script.py",
					},
				},
			},
			JQExample: CommandExample{
				Description: "Run a command and print its exit code.",
				Command:     "baseten sandbox exec --name my-sandbox --jq '.exit_code' -- true",
			},
		},
	},
	{
		Name:    "connect",
		Summary: "Open an interactive terminal to a sandbox (PRE-RELEASE)",
		Description: sandboxPreRelease +
			"Opens a raw-mode terminal to a deployed sandbox over its execution API, like SSH: " +
			"full ANSI output, resize handling, interactive applications. Press Ctrl+D to " +
			"disconnect. Requires an interactive terminal.",
		Flags: SandboxNameFlags{},
		Output: &CommandOutput[struct{}]{
			TextDescription: "The sandbox's terminal, until Ctrl+D or the sandbox closes the session.",
			Examples: []CommandExample{{
				Description: "Open a terminal to a sandbox.",
				Command:     "baseten sandbox connect --name my-sandbox",
			}},
			JSONOutputUnimportant: true,
		},
	},
}

// processSubcommands is the process family under baseten sandbox process,
// mirroring the SDK's process surface.
var processSubcommands = Command{
	Name:    "process",
	Summary: "Run and inspect processes in a sandbox (PRE-RELEASE)",
	Description: sandboxPreRelease +
		"Processes are commands running in a sandbox. 'process exec' runs one and streams its " +
		"output; 'process start' launches one in the background; 'process list' and " +
		"'process logs' inspect what ran.",
	Children: []Command{
		{
			Name:      "exec",
			Summary:   "Run a command in a sandbox (PRE-RELEASE)",
			ArgsUsage: "-- COMMAND [ARGS...]",
			MaxArgs:   -1,
			Description: sandboxPreRelease +
				"Runs a command in a deployed sandbox, streaming its output as it arrives. The " +
				"command's exit code becomes the CLI's exit code.\n\n" +
				"Put the command after a literal --, and every sandbox flag before it, so flags " +
				"inside the command are passed through to it. One quoted argument is the whole " +
				"command string, passed to the sandbox's shell as-is; several arguments are " +
				"re-quoted so their boundaries survive.",
			Flags: SandboxExecFlags{},
			Output: &CommandOutput[sandboxclient.ProcessInfo]{
				TextDescription: "The command's output, streamed, with the exit code passed " +
					"through as the CLI's exit code.",
				JSONDescription: "With --output json, the final process record once the command " +
					"exits, and no streamed output.",
				Examples: []CommandExample{
					{
						Description: "Run a command in a sandbox.",
						Command:     "baseten sandbox process exec --name my-sandbox -- echo hello",
					},
				},
				JQExample: CommandExample{
					Description: "Run a command and print its exit code.",
					CommandLines: []string{
						"baseten sandbox process exec --name my-sandbox",
						"--jq '.exit_code' -- true",
					},
				},
			},
		},
		{
			Name:      "start",
			Summary:   "Start a background process in a sandbox (PRE-RELEASE)",
			ArgsUsage: "-- COMMAND [ARGS...]",
			MaxArgs:   -1,
			Description: sandboxPreRelease +
				"Starts a command in a deployed sandbox and returns immediately with the " +
				"process's pid; the command keeps running in the background. The argument " +
				"rules of 'process exec' apply.\n\n" +
				"Inspect it with 'sandbox process list' and 'sandbox process logs'.",
			Flags: SandboxExecFlags{},
			Output: &CommandOutput[sandboxclient.ProcessInfo]{
				TextDescription: "The started process's pid and how to follow its output.",
				JSONDescription: "With --output json, the process record as of the start.",
				Examples: []CommandExample{{
					Description: "Start a web server in the background.",
					Command:     "baseten sandbox process start --name my-sandbox -- python -m http.server",
				}},
				JQExample: CommandExample{
					Description: "Start a process and print its pid.",
					Command:     "baseten sandbox process start --name my-sandbox --jq '.pid' -- sleep 60",
				},
			},
		},
		{
			Name:    "list",
			Summary: "List processes in a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Lists every process the sandbox knows about, running and exited.",
			Flags: SandboxNameFlags{},
			Output: &CommandOutput[SandboxProcessList]{
				TextDescription: "Table with columns: PID, NAME, STATUS, EXIT, STARTED. " +
					"Prints \"No processes found.\" to stderr when the list is empty.",
				Examples: []CommandExample{{
					Description: "List processes in a sandbox.",
					Command:     "baseten sandbox process list --name my-sandbox",
				}},
				JQExample: CommandExample{
					Description: "Print every running process's pid.",
					CommandLines: []string{
						"baseten sandbox process list --name my-sandbox",
						"--jq '.items[] | select(.status==\"running\") | .pid'",
					},
				},
			},
		},
		{
			Name:    "logs",
			Summary: "Print a process's logs (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Prints one process's captured output: standard output and standard error, " +
				"interleaved.",
			Flags: SandboxProcessLogsFlags{},
			Output: &CommandOutput[sandboxclient.ProcessLogs]{
				TextDescription: "The process's interleaved output.",
				JSONDescription: "With --output json, stdout, stderr, and the interleaved logs " +
					"as separate fields.",
				Examples: []CommandExample{{
					Description: "Print a process's logs.",
					Command:     "baseten sandbox process logs --name my-sandbox --pid 123",
				}},
				JQExample: CommandExample{
					Description: "Print only a process's standard error.",
					CommandLines: []string{
						"baseten sandbox process logs --name my-sandbox",
						"--pid 123 --jq '.stderr'",
					},
				},
			},
		},
	},
}

// SandboxProcessList is the JSON shape of 'baseten sandbox process list'.
type SandboxProcessList struct {
	Items []sandboxclient.ProcessInfo `json:"items"`
}

// SandboxProcessLogsFlags configures 'baseten sandbox process logs'.
type SandboxProcessLogsFlags struct {
	SandboxNameFlags
	PID string `flag:"pid" desc:"Pid or name of the process." required:"true"`
}
