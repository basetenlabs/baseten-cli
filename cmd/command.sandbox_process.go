package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/sandboxapi"
)

var commandSandboxProcess = Command{
	Name:    "process",
	Summary: "Run and inspect processes in a sandbox (PRE-RELEASE)",
	Description: sandboxPreRelease +
		"Processes are commands running, or that ran, in a sandbox. 'process start' runs one in " +
		"the background; the other commands find one by --pid or by the name it was started " +
		"with, --process-name.",
	Children: []Command{
		{
			Name:      "start",
			Summary:   "Start a background process in a sandbox (PRE-RELEASE)",
			ArgsUsage: "-- COMMAND [ARGS...]",
			MaxArgs:   -1,
			Description: sandboxPreRelease +
				"Starts a command in a sandbox and returns right away, leaving it running. The " +
				"argument rules of 'sandbox exec' apply. Follow it with 'sandbox process logs " +
				"--tail' and wait for it with 'sandbox process wait'.",
			Flags: SandboxProcessStartFlags{},
			Output: &CommandOutput[sandboxapi.ProcessResponse]{
				TextDescription: "The started process's PID and how to follow its output.",
				JSONDescription: "The process record as of the start.",
				Examples: []CommandExample{
					{
						Description: "Start a web server in the background.",
						CommandLines: []string{
							"baseten sandbox process start --name my-sandbox",
							"--process-name web --wait-for-port 8000",
							"-- python -m http.server 8000",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Start a process and print its PID.",
					Command:     "baseten sandbox process start --name my-sandbox --jq '.pid' -- sleep 60",
				},
			},
		},
		{
			Name:    "list",
			Summary: "List processes in a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Lists every process the sandbox knows about, running and exited.",
			Flags: SandboxProcessListFlags{},
			Output: &CommandOutput[SandboxProcessList]{
				TextDescription: "Table with columns: PID, NAME, STATUS, EXIT, STARTED, COMMAND. " +
					"Prints \"No processes found.\" to stderr when the list is empty.",
				Examples: []CommandExample{{
					Description: "List processes in a sandbox.",
					Command:     "baseten sandbox process list --name my-sandbox",
				}},
				JQExample: CommandExample{
					Description: "Print every running process's PID.",
					CommandLines: []string{
						"baseten sandbox process list --name my-sandbox",
						"--jq '.items[] | select(.status == \"running\") | .pid'",
					},
				},
			},
		},
		{
			Name:    "describe",
			Summary: "Describe a process in a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Retrieves one process's record, including its captured standard output and " +
				"standard error as separate fields.",
			Flags: SandboxProcessDescribeFlags{},
			Output: &CommandOutput[sandboxapi.ProcessResponse]{
				TextDescription: "One field per line describing the process, without its output. Empty " +
					"fields are left out.",
				Examples: []CommandExample{{
					Description: "Describe a process by name.",
					Command:     "baseten sandbox process describe --name my-sandbox --process-name web",
				}},
				JQExample: CommandExample{
					Description: "Print a process's standard error.",
					CommandLines: []string{
						"baseten sandbox process describe --name my-sandbox",
						"--pid 42 --jq '.stderr'",
					},
				},
			},
		},
		{
			Name:    "logs",
			Summary: "Print a process's output (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Prints one process's output so far, standard output and standard error interleaved. " +
				"With --tail, prints its output from the start and then follows it until the process " +
				"exits, with each line marked by its stream in JSON.\n\n" +
				"For standard output and standard error as separate fields, use 'sandbox process " +
				"describe --output json'.",
			Flags: SandboxProcessLogsFlags{},
			Output: &CommandOutput[SandboxProcessLogLine]{
				TextDescription: "The process's output lines. With --tail, standard error lines go to " +
					"stderr.",
				JSONDescription: "One record per line. stream is stdout or stderr with --tail, and left " +
					"out otherwise, since the output so far does not say which stream a line came from.",
				JSONArrayStreamed: true,
				Examples: []CommandExample{
					{
						Description: "Print a process's output so far.",
						Command:     "baseten sandbox process logs --name my-sandbox --pid 42",
					},
					{
						Description: "Follow a process's output until it exits.",
						Command:     "baseten sandbox process logs --name my-sandbox --process-name web --tail",
					},
				},
				JQExample: CommandExample{
					Description: "Print only a process's standard error as it arrives.",
					CommandLines: []string{
						"baseten sandbox process logs --name my-sandbox --pid 42",
						"--tail --jq 'select(.stream == \"stderr\") | .text'",
					},
				},
			},
		},
		{
			Name:    "wait",
			Summary: "Wait for a process in a sandbox to exit (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Waits for a process to exit and prints its record. The process's exit code becomes " +
				"the CLI's exit code. Waits with no limit unless --timeout is set; neither a timeout " +
				"nor Ctrl+C stops the process.",
			Flags: SandboxProcessWaitFlags{},
			Output: &CommandOutput[sandboxapi.ProcessResponse]{
				TextDescription: "One field per line describing the exited process, without its output. " +
					"Empty fields are left out.",
				Examples: []CommandExample{{
					Description: "Wait for a process to exit, for up to an hour.",
					Command:     "baseten sandbox process wait --name my-sandbox --pid 42 --timeout 1h",
				}},
				JQExample: CommandExample{
					Description: "Wait for a process and print its output.",
					Command:     "baseten sandbox process wait --name my-sandbox --pid 42 --jq '.logs'",
				},
			},
		},
		{
			Name:    "stop",
			Summary: "Stop a process in a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Requests that a process stop gracefully. Use 'sandbox process kill' to force it.",
			Flags: SandboxProcessRefCommandFlags{},
			Output: &CommandOutput[struct{}]{
				TextDescription: "A confirmation line on stderr.",
				Examples: []CommandExample{{
					Description: "Stop a process by name.",
					Command:     "baseten sandbox process stop --name my-sandbox --process-name web",
				}},
				JSONOutputUnimportant: true,
			},
		},
		{
			Name:    "kill",
			Summary: "Kill a process in a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Requests that a process be killed at once. Use 'sandbox process stop' to stop it " +
				"gracefully.",
			Flags: SandboxProcessRefCommandFlags{},
			Output: &CommandOutput[struct{}]{
				TextDescription: "A confirmation line on stderr.",
				Examples: []CommandExample{{
					Description: "Kill a process by PID.",
					Command:     "baseten sandbox process kill --name my-sandbox --pid 42",
				}},
				JSONOutputUnimportant: true,
			},
		},
	},
}

// SandboxProcessList is the JSON shape of 'baseten sandbox process list'.
type SandboxProcessList struct {
	Items []sandboxapi.ProcessResponse `json:"items"`
}

// SandboxProcessLogLine is one line of 'baseten sandbox process logs'. The
// API streams process output as plain text, so it has no type of its own.
type SandboxProcessLogLine struct {
	Stream string `json:"stream,omitempty"`
	Text   string `json:"text"`
}

// SandboxProcessRefFlags select one process in a sandbox.
type SandboxProcessRefFlags struct {
	PID         string `flag:"pid" desc:"PID of the process." oneof:"process-ref"`
	ProcessName string `flag:"process-name" desc:"Name the process was started with." oneof:"process-ref"`
}

// SandboxProcessStartFlags configures 'baseten sandbox process start'.
type SandboxProcessStartFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxProcessOptionFlags

	KeepAlive        bool     `flag:"keep-alive" desc:"Keep the sandbox from going to sleep while the process runs."`
	RestartOnFailure bool     `flag:"restart-on-failure" desc:"Restart the process when it exits with a failure."`
	MaxRestarts      int      `flag:"max-restarts" desc:"Most restarts with --restart-on-failure."`
	WaitForPort      []string `flag:"wait-for-port" desc:"Return only once the process listens on this port. Repeatable."`
}

// SandboxProcessListFlags configures 'baseten sandbox process list'.
type SandboxProcessListFlags struct {
	CommandFlags
	SandboxRefFlags
}

// SandboxProcessDescribeFlags configures 'baseten sandbox process describe'.
type SandboxProcessDescribeFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxProcessRefFlags
}

// SandboxProcessLogsFlags configures 'baseten sandbox process logs'.
type SandboxProcessLogsFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxProcessRefFlags

	Tail bool `flag:"tail" desc:"Print the process's output from the start, then follow it until the process exits or you interrupt with Ctrl-C. For machine-readable streaming, prefer --output jsonl over --output json."`
}

// SandboxProcessWaitFlags configures 'baseten sandbox process wait'.
type SandboxProcessWaitFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxProcessRefFlags

	Timeout time.Duration `flag:"timeout" desc:"Fail if the process has not exited after this long, such as 30m. The process keeps running."`
}

// SandboxProcessRefCommandFlags configures the commands that only select a
// process: 'baseten sandbox process stop' and 'kill'.
type SandboxProcessRefCommandFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxProcessRefFlags
}
