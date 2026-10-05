package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/basetenlabs/baseten-go/client/sandboxapi"
)

// sandboxPreRelease leads every sandbox command's description, paired with
// the " (PRE-RELEASE)" summary suffix, following volumePreRelease.
const sandboxPreRelease = "PRE-RELEASE: Sandbox commands are not GA yet. " +
	"Their arguments, flags, and output may change.\n\n"

var commandSandbox = Command{
	Name:    "sandbox",
	Summary: "Manage sandboxes (PRE-RELEASE)",
	Description: sandboxPreRelease +
		"Sandboxes run arbitrary commands in isolated environments. Create one, run commands in " +
		"it with 'sandbox exec', and delete it when done.",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List sandboxes (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Lists the team's sandboxes, up to --limit. Filter by free-text --query or repeatable " +
				"--status.",
			Flags: SandboxListFlags{},
			Output: &CommandOutput[SandboxList]{
				TextDescription: "Table with columns: NAME, STATUS, REGION, CREATED. " +
					"Prints \"No sandboxes found.\" to stderr when the list is empty, and a note to " +
					"stderr when --limit left some out.",
				Examples: []CommandExample{
					{
						Description: "List sandboxes.",
						Command:     "baseten sandbox list",
					},
					{
						Description: "List only deployed sandboxes.",
						Command:     "baseten sandbox list --status deployed",
					},
				},
				JQExample: CommandExample{
					Description: "Print every sandbox's URL.",
					Command:     "baseten sandbox list --jq '.items[].url'",
				},
			},
		},
		{
			Name:    "describe",
			Summary: "Describe a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Retrieves one sandbox's record: status, URL, image, memory, region, labels, and " +
				"environment variables. Secret environment variable values are masked unless " +
				"--show-secrets is passed.",
			Flags: SandboxDescribeFlags{},
			Output: &CommandOutput[managementapi.Sandbox]{
				TextDescription: "One field per line describing the sandbox. Empty fields are left out.",
				Examples: []CommandExample{{
					Description: "Describe a sandbox.",
					Command:     "baseten sandbox describe --name my-sandbox",
				}},
				JQExample: CommandExample{
					Description: "Print the sandbox's URL.",
					Command:     "baseten sandbox describe --name my-sandbox --jq '.url'",
				},
			},
		},
		{
			Name:    "create",
			Summary: "Create a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Creates a sandbox and prints its record. Only the flags passed go into the request; " +
				"the server applies its defaults to the rest.\n\n" +
				"Environment variables from --env are secret, so their values are masked when read " +
				"back; use --plain-env for values that are not.",
			Flags: SandboxCreateFlags{},
			Output: &CommandOutput[managementapi.Sandbox]{
				TextDescription: "One field per line describing the created sandbox. Empty fields are left out.",
				Examples: []CommandExample{
					{
						Description: "Create a sandbox with a generated name and server defaults.",
						Command:     "baseten sandbox create",
					},
					{
						Description: "Create a named sandbox, or get it back if it already exists.",
						Command:     "baseten sandbox create --name my-sandbox --if-not-exists",
					},
					{
						Description: "Create one in a specific region with more memory.",
						CommandLines: []string{
							"baseten sandbox create --name my-sandbox",
							"--region us-was-1 --memory 8192",
						},
					},
					{
						Description: "Create one with environment variables and labels.",
						CommandLines: []string{
							"baseten sandbox create --name worker",
							"--env API_TOKEN=secret --plain-env WORKERS=4",
							"--label team=cli",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Create a sandbox and print its generated name.",
					Command:     "baseten sandbox create --jq '.name'",
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Updates a sandbox's image, environment variables, or labels. Omitted flags leave " +
				"their fields unchanged. Any --env or --plain-env replaces all of the sandbox's " +
				"environment variables, and any --label replaces all of its labels.",
			Flags: SandboxUpdateFlags{},
			Output: &CommandOutput[managementapi.Sandbox]{
				TextDescription: "One field per line describing the updated sandbox. Empty fields are left out.",
				Examples: []CommandExample{{
					Description: "Replace a sandbox's labels.",
					Command:     "baseten sandbox update --name my-sandbox --label env=dev",
				}},
				JQExample: CommandExample{
					Description: "Replace a sandbox's labels and print them.",
					CommandLines: []string{
						"baseten sandbox update --name my-sandbox",
						"--label env=dev --jq '.labels'",
					},
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Deletes a sandbox and everything in it. This cannot be undone. Deletion continues " +
				"after this command returns.",
			Flags: SandboxDeleteFlags{},
			Output: &CommandOutput[managementapi.Sandbox]{
				TextDescription: "A confirmation line on stderr.",
				JSONDescription: "The sandbox's record as deletion starts.",
				Examples: []CommandExample{{
					Description: "Delete a sandbox without the confirmation prompt.",
					Command:     "baseten sandbox delete --name my-sandbox --yes",
				}},
				JQExample: CommandExample{
					Description: "Delete a sandbox and print its status.",
					Command:     "baseten sandbox delete --name my-sandbox --yes --jq '.status'",
				},
			},
		},
		{
			Name:      "exec",
			Summary:   "Run a command in a sandbox (PRE-RELEASE)",
			ArgsUsage: "-- COMMAND [ARGS...]",
			MaxArgs:   -1,
			Description: sandboxPreRelease +
				"Runs a command in a sandbox and waits for it to exit. This is basically 'sandbox " +
				"process start' followed by 'sandbox process wait', except that the command's output " +
				"streams as it arrives and --stdin is available. The command's exit code becomes the " +
				"CLI's exit code.\n\n" +
				"Put the command after a literal --, and every flag before it. One argument is the " +
				"whole command line, passed to the sandbox's shell as is, so its quoting, variables, " +
				"pipes, and redirects are interpreted there. Several arguments are quoted so each " +
				"stays one argument.",
			Flags: SandboxExecFlags{},
			Output: &CommandOutput[sandboxapi.ProcessResponse]{
				TextDescription: "The command's standard output on stdout and standard error on stderr, " +
					"as they arrive.",
				JSONDescription: "The process record once the command exits, with its captured output, " +
					"and nothing streamed.",
				Examples: []CommandExample{
					{
						Description: "Run a command.",
						Command:     "baseten sandbox exec --name my-sandbox -- ls -la /tmp",
					},
					{
						Description: "Run a shell pipeline as one argument.",
						Command:     "baseten sandbox exec --name my-sandbox -- 'ps aux | grep python'",
					},
					{
						Description: "Pipe a local file into a command.",
						Command:     "baseten sandbox exec --name my-sandbox --stdin -- wc -l < data.csv",
					},
					{
						Description: "Run a command in a directory with an extra environment variable.",
						CommandLines: []string{
							"baseten sandbox exec --name my-sandbox",
							"--working-dir /app --env DEBUG=1 -- python main.py",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Run a command and print its exit code.",
					Command:     "baseten sandbox exec --name my-sandbox --jq '.exitCode' -- false",
				},
			},
		},
		{
			Name:    "connect",
			Summary: "Open an interactive terminal to a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Opens an interactive shell in a sandbox, like SSH: full terminal output, resize " +
				"handling, and interactive programs. Each connect is a new shell. Exit the shell or " +
				"press Ctrl+D to disconnect.\n\n" +
				"Requires an interactive terminal for both input and output; fails at once, before " +
				"connecting, when either is redirected.",
			Flags: SandboxConnectFlags{},
			Output: &CommandOutput[struct{}]{
				TextDescription: "The sandbox's terminal, until the shell exits.",
				Examples: []CommandExample{{
					Description: "Open a terminal to a sandbox.",
					Command:     "baseten sandbox connect --name my-sandbox",
				}},
				JSONOutputUnimportant: true,
			},
		},
		commandSandboxProcess,
		commandSandboxImage,
	},
}

// SandboxList is the JSON shape of 'baseten sandbox list'.
type SandboxList struct {
	Items []managementapi.Sandbox `json:"items"`
}

// SandboxRefFlags selects one sandbox.
type SandboxRefFlags struct {
	Name string `flag:"name" desc:"Name of the sandbox." required:"true"`
	Team string `flag:"team" desc:"Team name or ID the sandbox belongs to. Defaults to your only team; required if you belong to more than one. Run 'baseten org team list' to see teams."`
}

// SandboxListFlags configures 'baseten sandbox list'.
type SandboxListFlags struct {
	CommandFlags

	Team   string   `flag:"team" desc:"Team name or ID to list sandboxes of. Defaults to your only team; required if you belong to more than one. Run 'baseten org team list' to see teams."`
	Query  string   `flag:"query" desc:"Free-text search over sandbox names and labels."`
	Status []string `flag:"status" desc:"Only sandboxes with one of these statuses. Repeatable." enum:"archived,archiving,building,deactivated,deactivating,deleting,deployed,deploying,failed,terminated,unarchiving,uploading"`
	Limit  int      `flag:"limit" desc:"Most sandboxes to list. 0 lists all." default:"1000"`
}

// SandboxDescribeFlags configures 'baseten sandbox describe'.
type SandboxDescribeFlags struct {
	CommandFlags
	SandboxRefFlags

	ShowSecrets bool `flag:"show-secrets" desc:"Reveal secret environment variable values. Requires the workspace administrator role; other callers still see masked values."`
}

// SandboxEnvFlags set a sandbox's environment variables.
type SandboxEnvFlags struct {
	Env      []string `flag:"env" desc:"Secret environment variable as KEY=VALUE, masked when read back. Repeatable."`
	PlainEnv []string `flag:"plain-env" desc:"Environment variable that is not secret, as KEY=VALUE, readable when read back. Repeatable."`
}

// SandboxCreateFlags configures 'baseten sandbox create'. Unset flags are left
// out of the request, so the server applies its defaults.
type SandboxCreateFlags struct {
	CommandFlags
	SandboxEnvFlags

	Name        string   `flag:"name" desc:"Unique name of the sandbox. The server generates one when omitted."`
	Team        string   `flag:"team" desc:"Team name or ID to create the sandbox in. Defaults to your only team; required if you belong to more than one. Run 'baseten org team list' to see teams."`
	IfNotExists bool     `flag:"if-not-exists" desc:"Return the existing sandbox with this name instead of failing, or recreate it if it is failed, terminated, or being deleted."`
	Region      string   `flag:"region" desc:"Region to create the sandbox in."`
	Memory      int      `flag:"memory" desc:"Memory in MB, which also sets the CPU allocation."`
	Image       string   `flag:"image" desc:"Image to create the sandbox from, including its tag, such as my-image:latest."`
	Label       []string `flag:"label" desc:"Label as KEY=VALUE. Repeatable."`
}

// SandboxUpdateFlags configures 'baseten sandbox update'. Unset flags leave
// their fields unchanged.
type SandboxUpdateFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxEnvFlags

	Image string   `flag:"image" desc:"Image to use, including its tag, such as my-image:latest."`
	Label []string `flag:"label" desc:"Label as KEY=VALUE, replacing all of the sandbox's labels. Repeatable."`
}

// SandboxDeleteFlags configures 'baseten sandbox delete'.
type SandboxDeleteFlags struct {
	CommandFlags
	SandboxRefFlags

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// SandboxProcessOptionFlags configure a process that 'baseten sandbox exec'
// or 'baseten sandbox process start' runs.
type SandboxProcessOptionFlags struct {
	Env         []string      `flag:"env" desc:"Environment variable for the command as KEY=VALUE, on top of the sandbox's own. Repeatable."`
	WorkingDir  string        `flag:"working-dir" desc:"Directory to run the command in."`
	Timeout     time.Duration `flag:"timeout" desc:"Stop the command if it runs longer than this, such as 10m."`
	ProcessName string        `flag:"process-name" desc:"Name for the process, to find it later with --process-name."`
}

// SandboxExecFlags configures 'baseten sandbox exec'.
type SandboxExecFlags struct {
	CommandFlags
	SandboxRefFlags
	SandboxProcessOptionFlags

	Stdin bool `flag:"stdin" desc:"Send this command's standard input to the sandbox command, closing it at end of input."`
}

// SandboxConnectFlags configures 'baseten sandbox connect'.
type SandboxConnectFlags struct {
	CommandFlags
	SandboxRefFlags
}
