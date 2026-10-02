package cmd

import "github.com/basetenlabs/baseten-cli/internal/sandboxclient"

// sandboxPreRelease leads every sandbox command's description, paired with
// the " (PRE-RELEASE)" summary suffix, following volumePreRelease.
const sandboxPreRelease = "PRE-RELEASE: Sandbox commands are not GA yet. " +
	"Their arguments, flags, and output may change.\n\n"

var commandSandbox = Command{
	Name:    "sandbox",
	Summary: "Manage sandboxes (PRE-RELEASE)",
	Description: sandboxPreRelease +
		"Sandboxes run arbitrary commands in isolated environments. Create one, wait for it to " +
		"deploy, run commands in it with 'sandbox process exec', and delete it when done.\n\n" +
		"During development, the sandbox control plane may live on a different domain than the " +
		"management API; set BASETEN_SANDBOXES_API_URL_OVERRIDE to route it there.",
	Children: append([]Command{
		imageLibrarySubcommands,
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
			Name:    "describe",
			Summary: "Describe a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Retrieves one sandbox's current record: status, execution URL, image, memory, " +
				"region, and labels.",
			Flags: SandboxDescribeFlags{},
			Output: &CommandOutput[sandboxclient.SandboxInfo]{
				TextDescription: "One field per line describing the sandbox.",
				Examples: []CommandExample{{
					Description: "Describe a sandbox.",
					Command:     "baseten sandbox describe --name my-sandbox",
				}},
				JQExample: CommandExample{
					Description: "Print the sandbox's execution URL.",
					Command:     "baseten sandbox describe --name my-sandbox --jq '.url'",
				},
			},
		},
		{
			Name:    "create",
			Summary: "Create a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Creates a sandbox. Only the flags passed go into the request; the server applies " +
				"its defaults to the rest (built-in image, 4096 MB, closest region).\n\n" +
				"By default the command returns as soon as the server accepts the create; " +
				"--wait blocks until the sandbox is DEPLOYED and then prints its execution URL.",
			Flags: SandboxCreateFlags{},
			Output: &CommandOutput[sandboxclient.SandboxInfo]{
				TextDescription: "One field per line describing the sandbox. Without --wait, the " +
					"record as of creation, usually still DEPLOYING and without its URL.",
				Examples: []CommandExample{
					{
						Description: "Create a sandbox with server defaults.",
						Command:     "baseten sandbox create --name my-sandbox",
					},
					{
						Description: "Create and wait for the sandbox to deploy.",
						Command:     "baseten sandbox create --name my-sandbox --wait",
					},
					{
						Description: "Create, or get the existing live sandbox back on a name conflict.",
						Command:     "baseten sandbox create --name my-sandbox --or-get-existing",
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
							"baseten sandbox create --name worker",
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
			Name:    "update",
			Summary: "Update a sandbox (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Updates a sandbox's labels, environment variables, or image. " +
				"Omitted flags leave their fields unchanged; a supplied --label or --env set " +
				"replaces all previous labels or environment variables. Name, memory, and network " +
				"cannot change after creation.",
			Flags: SandboxUpdateFlags{},
			Output: &CommandOutput[sandboxclient.SandboxInfo]{
				TextDescription: "One field per line describing the updated sandbox.",
				Examples: []CommandExample{{
					Description: "Replace a sandbox's labels.",
					Command:     "baseten sandbox update --name my-sandbox --label env=dev",
				}},
				JQExample: CommandExample{
					Description: "Update labels and print them.",
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
			Output: &CommandOutput[sandboxclient.SandboxInfo]{
				TextDescription: "One field per line describing the sandbox as deletion starts.",
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
		imageSubcommands,
	}, sandboxInteractionCommands...),
}

// SandboxList is the JSON shape of 'baseten sandbox list'.
type SandboxList struct {
	Items []sandboxclient.SandboxInfo `json:"items"`
}

// SandboxTeamFlags carries the sandbox commands' shared team selection.
type SandboxTeamFlags struct {
	CommandFlags
	Team string `flag:"team" desc:"Team name or ID to act in. Defaults to the caller's only accessible team. Run 'baseten org team list' to see teams."`
}

// SandboxNameFlags selects one sandbox by name.
type SandboxNameFlags struct {
	SandboxTeamFlags
	Name string `flag:"name" desc:"Name of the sandbox." required:"true"`
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
	Name        string   `flag:"name" desc:"Unique name of the sandbox. The server generates one when omitted."`
	IfNotExists bool     `flag:"or-get-existing" desc:"Get the existing live sandbox with this name back instead of a conflict, or recreate one that is failed, terminated, or being deleted. The API's create_if_not_exists form; requires --name."`
	Region      string   `flag:"region" desc:"Region to run in. Defaults to the closest region."`
	MemoryMB    int      `flag:"memory-mb" desc:"Memory in megabytes, which also sets the CPU allocation. Defaults to 4096."`
	Image       string   `flag:"image" desc:"Image reference including its tag. Defaults to the built-in sandbox image."`
	Env         []string `flag:"env" desc:"Environment variable as KEY=VALUE. May be repeated."`
	Label       []string `flag:"label" desc:"Label as KEY=VALUE. May be repeated."`
	Wait        bool     `flag:"wait" desc:"Wait until the sandbox is DEPLOYED, then print its execution URL."`
}

// SandboxUpdateFlags configures 'baseten sandbox update'. Omitted flags leave
// their fields unchanged.
type SandboxUpdateFlags struct {
	SandboxNameFlags
	Image string   `flag:"image" desc:"Image reference including its tag. Omitted leaves it unchanged."`
	Env   []string `flag:"env" desc:"Environment variable as KEY=VALUE. May be repeated. A supplied set replaces all previous environment variables."`
	Label []string `flag:"label" desc:"Label as KEY=VALUE. May be repeated. A supplied set replaces all previous labels."`
}

// SandboxDeleteFlags configures 'baseten sandbox delete' and 'baseten sandbox
// image delete'.
type SandboxDeleteFlags struct {
	SandboxNameFlags
	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// SandboxExecFlags configures 'baseten sandbox exec' and 'baseten sandbox
// process exec'.
type SandboxExecFlags struct {
	SandboxNameFlags
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
		"import one from a registry, then create sandboxes with image <name>:<tag>. A build " +
		"that fails reports its status with the last build-log lines when the log service " +
		"answers.",
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
			Name:    "describe",
			Summary: "Describe a sandbox image (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Retrieves one image repository's current record: status, version count, size.",
			Flags: SandboxImageNameFlags{},
			Output: &CommandOutput[sandboxclient.ImageInfo]{
				TextDescription: "One field per line describing the image.",
				Examples: []CommandExample{{
					Description: "Describe an image.",
					Command:     "baseten sandbox image describe --name my-image",
				}},
				JQExample: CommandExample{
					Description: "Print the image's status.",
					Command:     "baseten sandbox image describe --name my-image --jq '.status'",
				},
			},
		},
		{
			Name:    "push",
			Summary: "Push a sandbox image (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Pushes one image version. Exactly one source: --dir zips the directory and " +
				"uploads it (a Dockerfile must sit at its root), or --image imports a registry " +
				"image.\n\n" +
				"By default the command returns as soon as the push is accepted; --wait blocks " +
				"until the image is BUILT.",
			Flags: SandboxImagePushFlags{},
			Output: &CommandOutput[sandboxclient.ImageInfo]{
				TextDescription: "One field per line describing the image. Without --wait, the " +
					"record as of the push, usually still UPLOADING.",
				Examples: []CommandExample{
					{
						Description: "Build an image from the current directory and wait for it.",
						Command:     "baseten sandbox image push --name my-image --dir . --wait",
					},
					{
						Description: "Import an image from a registry.",
						CommandLines: []string{
							"baseten sandbox image push --name my-image",
							"--image registry.example/app:v1",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Push from a directory and print the resulting status.",
					Command:     "baseten sandbox image push --name my-image --dir . --jq '.status'",
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a sandbox image (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Deletes an image repository and every version in it. This cannot be undone.",
			Flags: SandboxDeleteFlags{},
			Output: &CommandOutput[sandboxclient.ImageInfo]{
				TextDescription: "One field per line describing the image as deletion starts.",
				Examples: []CommandExample{{
					Description: "Delete an image without the confirmation prompt.",
					Command:     "baseten sandbox image delete --name my-image --yes",
				}},
				JQExample: CommandExample{
					Description: "Delete an image and print its status.",
					Command:     "baseten sandbox image delete --name my-image --yes --jq '.status'",
				},
			},
		},
	},
}

// SandboxImageList is the JSON shape of 'baseten sandbox image list'.
type SandboxImageList struct {
	Items []sandboxclient.ImageInfo `json:"items"`
}

// SandboxImageNameFlags selects one sandbox image by name.
type SandboxImageNameFlags struct {
	SandboxTeamFlags
	Name string `flag:"name" desc:"Name of the image." required:"true"`
}

// SandboxImagePushFlags configures 'baseten sandbox image push'. Exactly one
// of Dir and Image is given.
type SandboxImagePushFlags struct {
	SandboxImageNameFlags
	Dir   string `flag:"dir" desc:"Directory to zip and upload as the image source. It must hold a Dockerfile at its root."`
	Image string `flag:"image" desc:"Registry image reference including a registry hostname, imported instead of building from a directory."`
	Wait  bool   `flag:"wait" desc:"Wait until the image is BUILT."`
}

// SandboxDescribeFlags configures 'baseten sandbox describe'.
type SandboxDescribeFlags struct {
	SandboxNameFlags
	ShowSecrets bool `flag:"show-secrets" desc:"Reveal environment variable values. Requires the workspace administrator role; other callers still see masked values."`
}

// imageLibrarySubcommands is the starter-image library under baseten sandbox
// image-library. The library is workspace-global, so no team selection applies.
var imageLibrarySubcommands = Command{
	Name:    "image-library",
	Summary: "Browse starter sandbox images (PRE-RELEASE)",
	Description: sandboxPreRelease +
		"Lists the platform's starter images, available to any sandbox without building or " +
		"pushing. Pass an entry's image reference as image on 'sandbox create'.\n\n" +
		"Hidden and coming-soon entries are left out, matching what the console's create form shows.",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List starter sandbox images (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Lists every visible starter image: its reference, description, default memory, and " +
				"categories.",
			Flags: SandboxLibraryListFlags{},
			Output: &CommandOutput[SandboxLibraryImageList]{
				TextDescription: "Table with columns: NAME, IMAGE, DEFAULT MB, CATEGORIES. " +
					"Prints \"No starter images found.\" to stderr when the library is empty.",
				Examples: []CommandExample{{
					Description: "List the starter library.",
					Command:     "baseten sandbox image-library list",
				}},
				JQExample: CommandExample{
					Description: "Print every starter image's reference.",
					Command:     "baseten sandbox image-library list --jq '.items[].image'",
				},
			},
		},
	},
}

// SandboxLibraryImageList is the JSON shape of 'baseten sandbox image-library
// list'.
type SandboxLibraryImageList struct {
	Items []SandboxLibraryImage `json:"items"`
}

// SandboxLibraryImage is one starter image from the library.
type SandboxLibraryImage struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name,omitempty"`
	Image       string   `json:"image"`
	Description string   `json:"description,omitempty"`
	MemoryMB    *int     `json:"memory_mb,omitempty"`
	Categories  []string `json:"categories"`
}

// SandboxLibraryListFlags are the standard flags only; the starter-image
// library is workspace-global, so no team selection applies.
type SandboxLibraryListFlags struct {
	CommandFlags
}
