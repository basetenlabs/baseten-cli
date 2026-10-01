package cmd

import "github.com/basetenlabs/baseten-go/sandbox"

// sandboxInteractionCommands are the commands that work inside one sandbox,
// kept apart from the management ones above them in the family.
var sandboxInteractionCommands = []Command{
	{
		Name:      "exec",
		Summary:   "Run a command in a sandbox (PRE-RELEASE)",
		ArgsUsage: "NAME -- COMMAND [ARGS...]",
		MaxArgs:   -1,
		Description: sandboxPreRelease +
			"Runs a command in a deployed sandbox, streaming its output as it arrives. The " +
			"command's exit code becomes the CLI's exit code.\n\n" +
			"Put the command after a literal --, and every sandbox flag before it, so flags " +
			"inside the command are passed through to it. One quoted argument is the whole " +
			"command string, passed to the sandbox's shell as-is; several arguments are " +
			"re-quoted so their boundaries survive.",
		Flags: SandboxExecFlags{},
		Output: &CommandOutput[sandbox.ProcessInfo]{
			TextDescription: "The command's output, streamed, with the exit code passed " +
				"through as the CLI's exit code.",
			JSONDescription: "With --output json, the final process record once the command " +
				"exits, and no streamed output.",
			Examples: []CommandExample{
				{
					Description: "Run a command in a sandbox.",
					Command:     "baseten sandbox exec my-sandbox -- echo hello",
				},
				{
					Description: "Run a command with extra environment variables.",
					CommandLines: []string{
						"baseten sandbox exec my-sandbox",
						"--env DEBUG=1 -- python script.py",
					},
				},
			},
			JQExample: CommandExample{
				Description: "Run a command and print its exit code.",
				Command:     "baseten sandbox exec my-sandbox --jq '.exit_code' -- true",
			},
		},
	},
	{
		Name:      "connect",
		Summary:   "Open an interactive terminal to a sandbox (PRE-RELEASE)",
		ArgsUsage: "NAME",
		ExactArgs: 1,
		Description: sandboxPreRelease +
			"Opens a raw-mode terminal to a deployed sandbox over its execution API, like SSH: " +
			"full ANSI output, resize handling, interactive applications. Press Ctrl+D to " +
			"disconnect. Requires an interactive terminal.",
		Flags: SandboxTeamFlags{},
		Output: &CommandOutput[struct{}]{
			TextDescription: "The sandbox's terminal, until Ctrl+D or the sandbox closes the session.",
			Examples: []CommandExample{{
				Description: "Open a terminal to a sandbox.",
				Command:     "baseten sandbox connect my-sandbox",
			}},
			JSONOutputUnimportant: true,
		},
	},
}
