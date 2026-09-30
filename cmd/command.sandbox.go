package cmd

import "github.com/basetenlabs/baseten-go/sandbox"

// sandboxPreRelease leads every sandbox command's description, paired with
// the " (PRE-RELEASE)" summary suffix, following volumePreRelease.
const sandboxPreRelease = "PRE-RELEASE: Sandbox commands are not GA yet. " +
	"Their arguments, flags, and output may change.\n\n"

var commandSandbox = Command{
	Name:    "sandbox",
	Summary: "Manage sandboxes (PRE-RELEASE)",
	Description: sandboxPreRelease +
		"Sandboxes run arbitrary commands in isolated environments. Create one, wait for it to " +
		"deploy, run commands in it with 'sandbox exec', and delete it when done.\n\n" +
		"During development, the sandbox control plane may live on a different domain than the " +
		"management API; set BASETEN_SANDBOXES_API_URL_OVERRIDE to route it there.",
	Children: []Command{
		imageSubcommands,
		{
			Name:    "list",
			Summary: "List sandboxes (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Lists every sandbox in the team, following every server page. Filter by free-text " +
				"--query or repeatable --status.",
			Flags: SandboxListFlags{},
			Output: &CommandOutput[SandboxList]{
				TextDescription: "Table with columns: NAME, STATUS, STATE, REGION, CREATED. " +
					"Prints \"No sandboxes found.\" to stderr when the list is empty.",
				Examples: []CommandExample{
					{
						Description: "List sandboxes in the default team.",
						Command:     "baseten sandbox list",
					},
					{
						Description: "List only deployed sandboxes.",
						Command:     "baseten sandbox list --status DEPLOYED",
					},
				},
				JQExample: CommandExample{
					Description: "Print every sandbox's URL.",
					Command:     "baseten sandbox list --jq '.items[].url'",
				},
			},
		},
		{
			Name:      "describe",
			Summary:   "Describe a sandbox (PRE-RELEASE)",
			ArgsUsage: "NAME",
			ExactArgs: 1,
			Description: sandboxPreRelease +
				"Retrieves one sandbox's current record: status, execution URL, image, memory, " +
				"region, and labels.",
			Flags: SandboxTeamFlags{},
			Output: &CommandOutput[sandbox.SandboxInfo]{
				TextDescription: "One field per line describing the sandbox.",
				Examples: []CommandExample{{
					Description: "Describe a sandbox.",
					Command:     "baseten sandbox describe my-sandbox",
				}},
				JQExample: CommandExample{
					Description: "Print the sandbox's execution URL.",
					Command:     "baseten sandbox describe my-sandbox --jq '.url'",
				},
			},
		},
		{
			Name:      "create",
			Summary:   "Create a sandbox (PRE-RELEASE)",
			ArgsUsage: "[NAME]",
			MaxArgs:   1,
			Description: sandboxPreRelease +
				"Creates a sandbox. Only the flags passed go into the request; the server applies " +
				"its defaults to the rest (built-in image, 4096 MB, closest region).\n\n" +
				"By default the command waits until the sandbox is DEPLOYED and then prints its " +
				"execution URL. Pass --no-wait to return as soon as the server accepts the create; " +
				"check status later with 'baseten sandbox describe'.",
			Flags: SandboxCreateFlags{},
			Output: &CommandOutput[sandbox.SandboxInfo]{
				TextDescription: "One field per line describing the sandbox. With --no-wait, the " +
					"record as of creation, usually still DEPLOYING and without its URL.",
				Examples: []CommandExample{
					{
						Description: "Create a sandbox with server defaults and wait for it.",
						Command:     "baseten sandbox create my-sandbox",
					},
					{
						Description: "Create one in a specific region with extra memory.",
						CommandLines: []string{
							"baseten sandbox create",
							"--region us-was-1 --memory-mb 8192",
						},
					},
					{
						Description: "Create one with environment variables and labels.",
						CommandLines: []string{
							"baseten sandbox create worker",
							"--env WORKERS=4 --label team=cli",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Create one and print its name.",
					Command:     "baseten sandbox create --jq '.name'",
				},
			},
		},
		{
			Name:      "update",
			Summary:   "Update a sandbox (PRE-RELEASE)",
			ArgsUsage: "NAME",
			ExactArgs: 1,
			Description: sandboxPreRelease +
				"Updates a sandbox's display name, labels, environment variables, or image. " +
				"Omitted flags leave their fields unchanged; a supplied --label or --env set " +
				"replaces all previous labels or environment variables. Name, memory, and network " +
				"cannot change after creation.",
			Flags: SandboxUpdateFlags{},
			Output: &CommandOutput[sandbox.SandboxInfo]{
				TextDescription: "One field per line describing the updated sandbox.",
				Examples: []CommandExample{{
					Description: "Rename a sandbox's display name.",
					Command:     "baseten sandbox update my-sandbox --display-name \"My sandbox\"",
				}},
				JQExample: CommandExample{
					Description: "Update labels and print them.",
					Command:     "baseten sandbox update my-sandbox --label env=dev --jq '.labels'",
				},
			},
		},
		{
			Name:      "start",
			Summary:   "Start a stopped sandbox (PRE-RELEASE)",
			ArgsUsage: "NAME",
			ExactArgs: 1,
			Description: sandboxPreRelease +
				"Enables a disabled sandbox so it accepts connections again. The sandbox keeps its " +
				"filesystem and processes.",
			Flags: SandboxTeamFlags{},
			Output: &CommandOutput[sandbox.SandboxInfo]{
				TextDescription: "One field per line describing the sandbox.",
				Examples: []CommandExample{{
					Description: "Start a sandbox.",
					Command:     "baseten sandbox start my-sandbox",
				}},
				JQExample: CommandExample{
					Description: "Start a sandbox and print its status.",
					Command:     "baseten sandbox start my-sandbox --jq '.status'",
				},
			},
		},
		{
			Name:      "stop",
			Summary:   "Stop a sandbox (PRE-RELEASE)",
			ArgsUsage: "NAME",
			ExactArgs: 1,
			Description: sandboxPreRelease +
				"Disables a sandbox so it accepts no connections, without deleting it. Start it " +
				"again with 'baseten sandbox start'.",
			Flags: SandboxTeamFlags{},
			Output: &CommandOutput[sandbox.SandboxInfo]{
				TextDescription: "One field per line describing the sandbox.",
				Examples: []CommandExample{{
					Description: "Stop a sandbox.",
					Command:     "baseten sandbox stop my-sandbox",
				}},
				JQExample: CommandExample{
					Description: "Stop a sandbox and print its enabled state.",
					Command:     "baseten sandbox stop my-sandbox --jq '.enabled'",
				},
			},
		},
		{
			Name:      "delete",
			Summary:   "Delete a sandbox (PRE-RELEASE)",
			ArgsUsage: "NAME",
			ExactArgs: 1,
			Description: sandboxPreRelease +
				"Deletes a sandbox and everything in it. This cannot be undone. Deletion continues " +
				"after this command returns.",
			Flags: SandboxDeleteFlags{},
			Output: &CommandOutput[sandbox.SandboxInfo]{
				TextDescription: "One field per line describing the sandbox as deletion starts.",
				Examples: []CommandExample{{
					Description: "Delete a sandbox without the confirmation prompt.",
					Command:     "baseten sandbox delete my-sandbox --yes",
				}},
				JQExample: CommandExample{
					Description: "Delete a sandbox and print its status.",
					Command:     "baseten sandbox delete my-sandbox --yes --jq '.status'",
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
		{
			Name:      "exec",
			Summary:   "Run a command in a sandbox (PRE-RELEASE)",
			ArgsUsage: "NAME -- COMMAND [ARGS...]",
			MaxArgs:   -1,
			Description: sandboxPreRelease +
				"Runs a command in a deployed sandbox, streaming its output as it arrives. The " +
				"command's exit code becomes the CLI's exit code.\n\n" +
				"Put the command after a literal --, and every sandbox flag before it, so flags " +
				"inside the command are passed through to it.",
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
	},
}

// SandboxList is the JSON shape of 'baseten sandbox list'.
type SandboxList struct {
	Items []sandbox.SandboxInfo `json:"items"`
}

// SandboxTeamFlags carries the sandbox commands' shared team selection.
type SandboxTeamFlags struct {
	CommandFlags
	Team string `flag:"team" desc:"Team ID to act in. Defaults to the caller's only accessible team. Run 'baseten org team list' to see teams."`
}

// SandboxListFlags filters 'baseten sandbox list'.
type SandboxListFlags struct {
	SandboxTeamFlags
	Query  string   `flag:"query" desc:"Free-text search over sandbox names and labels."`
	Status []string `flag:"status" desc:"Only sandboxes with one of these statuses, for example DEPLOYED. May be repeated."`
}

// SandboxCreateFlags configures 'baseten sandbox create'. Every field is
// optional; unset fields fall back to the server's defaults.
type SandboxCreateFlags struct {
	SandboxTeamFlags
	Region      string   `flag:"region" desc:"Region to run in. Defaults to the closest region."`
	MemoryMB    int      `flag:"memory-mb" desc:"Memory in megabytes, which also sets the CPU allocation. Defaults to 4096."`
	Image       string   `flag:"image" desc:"Image reference including its tag. Defaults to the built-in sandbox image."`
	DisplayName string   `flag:"display-name" desc:"Human-readable name for display in the UI."`
	Env         []string `flag:"env" desc:"Environment variable as KEY=VALUE. May be repeated."`
	Label       []string `flag:"label" desc:"Label as KEY=VALUE. May be repeated."`
	NoWait      bool     `flag:"no-wait" desc:"Return as soon as the server accepts the create, without waiting for the sandbox to deploy."`
}

// SandboxUpdateFlags configures 'baseten sandbox update'. Omitted flags leave
// their fields unchanged.
type SandboxUpdateFlags struct {
	SandboxTeamFlags
	DisplayName string   `flag:"display-name" desc:"Human-readable name for display in the UI. Omitted leaves it unchanged."`
	Image       string   `flag:"image" desc:"Image reference including its tag. Omitted leaves it unchanged."`
	Env         []string `flag:"env" desc:"Environment variable as KEY=VALUE. May be repeated. A supplied set replaces all previous environment variables."`
	Label       []string `flag:"label" desc:"Label as KEY=VALUE. May be repeated. A supplied set replaces all previous labels."`
}

// SandboxDeleteFlags configures 'baseten sandbox delete'.
type SandboxDeleteFlags struct {
	SandboxTeamFlags
	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// SandboxExecFlags configures 'baseten sandbox exec'.
type SandboxExecFlags struct {
	SandboxTeamFlags
	Env []string `flag:"env" desc:"Environment variable as KEY=VALUE for the command, on top of the sandbox's own. May be repeated."`
}

// imageSubcommands is the image family under baseten sandbox image. Images
// are the sources sandboxes are created from; push builds one from a
// directory or imports one from a registry.
var imageSubcommands = Command{
	Name:    "image",
	Summary: "Manage sandbox images (PRE-RELEASE)",
	Description: sandboxPreRelease +
		"Images are the sources sandboxes are created from: push one from a directory or " +
		"import one from a registry, then create sandboxes with image <name>:<tag>.\n\n" +
		"A FAILED build carries no reason yet: the API's build-log endpoint is not in the " +
		"Baseten spec, so only the status is readable.",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List sandbox images (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Lists every image repository in the team, following every server page.",
			Flags: SandboxTeamFlags{},
			Output: &CommandOutput[SandboxImageList]{
				TextDescription: "Table with columns: NAME, STATUS, TAGS, SIZE, CREATED. " +
					"Prints \"No sandbox images found.\" to stderr when the list is empty.",
				Examples: []CommandExample{{
					Description: "List images.",
					Command:     "baseten sandbox image list",
				}},
				JQExample: CommandExample{
					Description: "Print every built image's name.",
					Command:     "baseten sandbox image list --jq '.items[].name'",
				},
			},
		},
		{
			Name:      "describe",
			Summary:   "Describe a sandbox image (PRE-RELEASE)",
			ArgsUsage: "NAME",
			ExactArgs: 1,
			Description: sandboxPreRelease +
				"Retrieves one image repository's current record: status, version count, size.",
			Flags: SandboxTeamFlags{},
			Output: &CommandOutput[sandbox.ImageInfo]{
				TextDescription: "One field per line describing the image.",
				Examples: []CommandExample{{
					Description: "Describe an image.",
					Command:     "baseten sandbox image describe my-image",
				}},
				JQExample: CommandExample{
					Description: "Print the image's status.",
					Command:     "baseten sandbox image describe my-image --jq '.status'",
				},
			},
		},
		{
			Name:      "push",
			Summary:   "Push a sandbox image (PRE-RELEASE)",
			ArgsUsage: "NAME",
			ExactArgs: 1,
			Description: sandboxPreRelease +
				"Pushes one image version. Exactly one source: --dir zips the directory and " +
				"uploads it (a Dockerfile must sit at its root), or --image imports a registry " +
				"image.\n\n" +
				"By default the command waits until the image is BUILT. Pass --no-wait to return " +
				"as soon as the push is accepted.",
			Flags: SandboxImagePushFlags{},
			Output: &CommandOutput[sandbox.ImageInfo]{
				TextDescription: "One field per line describing the image. With --no-wait, the " +
					"record as of the push, usually still UPLOADING.",
				Examples: []CommandExample{
					{
						Description: "Build an image from the current directory and wait for it.",
						Command:     "baseten sandbox image push my-image --dir .",
					},
					{
						Description: "Import an image from a registry.",
						Command:     "baseten sandbox image push my-image --image registry.example/app:v1",
					},
				},
				JQExample: CommandExample{
					Description: "Push from a directory and print the resulting status.",
					Command:     "baseten sandbox image push my-image --dir . --jq '.status'",
				},
			},
		},
		{
			Name:      "delete",
			Summary:   "Delete a sandbox image (PRE-RELEASE)",
			ArgsUsage: "NAME",
			ExactArgs: 1,
			Description: sandboxPreRelease +
				"Deletes an image repository and every version in it. This cannot be undone.",
			Flags: SandboxDeleteFlags{},
			Output: &CommandOutput[sandbox.ImageInfo]{
				TextDescription: "One field per line describing the image as deletion starts.",
				Examples: []CommandExample{{
					Description: "Delete an image without the confirmation prompt.",
					Command:     "baseten sandbox image delete my-image --yes",
				}},
				JQExample: CommandExample{
					Description: "Delete an image and print its status.",
					Command:     "baseten sandbox image delete my-image --yes --jq '.status'",
				},
			},
		},
	},
}

// SandboxImageList is the JSON shape of 'baseten sandbox image list'.
type SandboxImageList struct {
	Items []sandbox.ImageInfo `json:"items"`
}

// SandboxImagePushFlags configures 'baseten sandbox image push'. Exactly one
// of Dir and Image is given.
type SandboxImagePushFlags struct {
	SandboxTeamFlags
	Dir    string `flag:"dir" desc:"Directory to zip and upload as the image source. It must hold a Dockerfile at its root."`
	Image  string `flag:"image" desc:"Registry image reference including a registry hostname, imported instead of building from a directory."`
	NoWait bool   `flag:"no-wait" desc:"Return as soon as the push is accepted, without waiting for the build."`
}
