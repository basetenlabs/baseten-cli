package cmd

import (
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// commandGridCloudSession is 'baseten grid cloud-session': a coding sandbox
// running a harness. A session's name is its sandbox name; no separate registry.
var commandGridCloudSession = Command{
	Name:    "cloud-session",
	Summary: "Manage cloud coding sessions (PRE-RELEASE)",
	Description: gridPrereleaseNotice +
		"Sandboxes that run a coding harness, created from a session image already imported into the sandbox image store " +
		"('baseten sandbox image push'); cloud-session never builds or imports images. " +
		"Create runs the harness setup inside the session and starts the harness in a tmux session. " +
		"Connect reattaches to that tmux session, and dispatch runs a headless prompt in it. " +
		"A session left idle hibernates with its sandbox and resumes on the next connection, with the harness still running.",
	Children: []Command{
		{
			Name:    "create",
			Summary: "Create a cloud session (PRE-RELEASE)",
			Description: gridPrereleaseNotice +
				"Create a sandbox from a session image, wait for it to deploy, run 'grid setup' inside with the " +
				"session credential, and start the harness in a tmux session. Idempotent: an existing running session is " +
				"set up again and returned.\n\n" +
				"Point at a repository with --repo owner/name and the image is resolved from it: from the session-image " +
				"map written by the build/import step, by the backend's image service in production. --image names an " +
				"imported image directly instead. " +
				"The session credential is a dedicated sandbox token this CLI picks up from its environment; " +
				"it goes to the sandbox's network proxy, which injects it on the harness and management " +
				"traffic, while the sandbox itself holds only a placeholder credential. " +
				"--env injects local environment variables the same way, by name. " +
				"The session's name defaults to a generated readable name. " +
				"Run in a terminal, create attaches to the session when it is ready; run otherwise, it prints " +
				"the command to attach. Reconnect later with 'baseten grid cloud-session connect --name <name>'.",
			Flags: CloudSessionCreateFlags{},
			Output: &CommandOutput[managementapi.Sandbox]{
				TextDescription: "One field per line, like 'sandbox describe', followed by the connect command.",
				Examples: []CommandExample{
					{
						Description: "Create a session from an imported image with a generated name.",
						Command:     "baseten grid cloud-session create --image my-session-image:latest",
					},
					{
						Description: "Create a named session and inject a local secret by name.",
						CommandLines: []string{
							"baseten grid cloud-session create --image my-session-image:latest",
							"--name my-session --env GITHUB_TOKEN",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print the session's state.",
					Command:     "baseten grid cloud-session create --image my-session-image:latest --jq '.status'",
				},
			},
		},
		{
			Name:    "connect",
			Summary: "Attach to a cloud session's harness (PRE-RELEASE)",
			Description: gridPrereleaseNotice +
				"Open an interactive terminal to the session's sandbox and attach to its tmux session, landing directly in " +
				"the harness. While attached, the sandbox stays awake; after the terminal detaches (exit the shell or " +
				"Ctrl+D), it sleeps again when idle. Detaching from tmux (Ctrl+B D) or a dropped connection leaves the " +
				"harness running; connect again to reattach. A sleeping sandbox drops the connection; typing a key " +
				"wakes it and reconnects.\n\n" +
				"Requires an interactive terminal for both input and output; fails at once, before connecting, when either " +
				"is redirected.",
			Flags: CloudSessionConnectFlags{},
			Output: &CommandOutput[struct{}]{
				TextDescription: "The harness's terminal, until you detach.",
				Examples: []CommandExample{{
					Description: "Attach to a session's harness.",
					Command:     "baseten grid cloud-session connect --name my-session",
				}},
				JSONOutputUnimportant: true,
			},
		},
		{
			Name:    "dispatch",
			Summary: "Run a headless prompt in a cloud session (PRE-RELEASE)",
			Description: gridPrereleaseNotice +
				"Run the session's harness headless with a prompt ('claude -p' for Claude Code) and stream its output. " +
				"The harness's exit code becomes the CLI's exit code. A dispatch runs as a sandbox process and does not " +
				"survive the connection ending, unlike the harness in the tmux session. --permission-mode defaults to " +
				"auto; pass manual or plan for read-only runs.",
			Flags: CloudSessionDispatchFlags{},
			Output: &CommandOutput[struct{}]{
				TextDescription: "The harness's standard output on stdout and standard error on stderr, as they arrive.",
				Examples: []CommandExample{
					{
						Description: "Run a prompt headless in a session.",
						Command:     "baseten grid cloud-session dispatch --name my-session --prompt \"Fix the failing test in go/limiter\"",
					},
				},
				JSONOutputUnimportant: true,
			},
		},
	},
}

// CloudSessionConnectFlags are the flags for 'baseten grid cloud-session connect'.
type CloudSessionConnectFlags struct {
	CommandFlags
	SandboxRefFlags
	Harness string `flag:"harness" desc:"Harness the session runs, used to start it when its tmux session is gone." enum:"claude-code,codex,opencode,pi" default:"claude-code"`
}

// CloudSessionCreateFlags are the flags for 'baseten grid cloud-session create'.
type CloudSessionCreateFlags struct {
	CommandFlags
	Repo                string   `flag:"repo" desc:"Repository the session targets, as owner/name; the session image is resolved from it. Requires exactly one of --repo and --image."`
	Image               string   `flag:"image" desc:"Image from the sandbox image store, as shown by 'sandbox image list', bypassing --repo resolution."`
	Name                string   `flag:"name" desc:"Name of the session's sandbox. Defaults to a generated readable name."`
	Team                string   `flag:"team" desc:"Team name or ID the session belongs to. Defaults to your only team; required if you belong to more than one. Run 'baseten org team list' to see teams."`
	Memory              int      `flag:"memory" desc:"Sandbox memory in MB, 1024 to 262144." default:"8192"`
	Harness             string   `flag:"harness" desc:"Harness the session runs." enum:"claude-code,codex,opencode,pi" default:"claude-code"`
	HarnessURL          string   `flag:"harness-url" desc:"Harness API base URL. Default https://coding.baseten.co." default:"https://coding.baseten.co"`
	PrimaryRouteID      string   `flag:"primary-route-id" desc:"Primary route ID for the harness. Defaults to GLM 5.3 Fast for claude-code, else the team's default for the harness."`
	Env                 []string `flag:"env" desc:"Local environment variable to inject into the session as a secret, by name; its value is read from this process. Repeatable."`
	PortConfig          bool     `flag:"port-config" desc:"Copy the local harness configuration into the session: ~/.claude/skills verbatim, and ~/CLAUDE.md and ~/AGENTS.md merged after the sandbox instructions."`
	RunlayerServer      []string `flag:"runlayer-server" desc:"Runlayer server ID to expose in the session as an MCP server through the agent account. Repeatable. Defaults to the launcher-configured servers when omitted."`
	RunlayerClientIDEnv string   `flag:"runlayer-client-id-env" desc:"Local environment variable holding the Runlayer agent account client ID." default:"RUNLAYER_GRID_AGENT_ID"`
	RunlayerSecretEnv   string   `flag:"runlayer-secret-env" desc:"Local environment variable holding the Runlayer agent account client secret." default:"RUNLAYER_GRID_AGENT_SECRET"`
}

// CloudSessionDispatchFlags are the flags for 'baseten grid cloud-session dispatch'.
type CloudSessionDispatchFlags struct {
	CommandFlags
	SandboxProcessOptionFlags
	SandboxRefFlags
	Harness        string `flag:"harness" desc:"Harness to run the prompt in." enum:"claude-code,codex,opencode,pi" default:"claude-code"`
	Prompt         string `flag:"prompt" desc:"Prompt to run the harness with, headless." required:"true"`
	PermissionMode string `flag:"permission-mode" desc:"Claude Code permission mode for the headless run; defaults to auto. Only --harness claude-code supports this." enum:"acceptEdits,auto,bypassPermissions,manual,dontAsk,plan"`
}
