package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

var commandSandboxImage = Command{
	Name:    "image",
	Summary: "Manage sandbox images (PRE-RELEASE)",
	Description: sandboxPreRelease +
		"Images are what sandboxes are created from. Push one from a directory with a " +
		"Dockerfile, or import one from a registry, then create sandboxes with --image " +
		"<name>:latest.",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List sandbox images (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Lists the team's images, up to --limit.",
			Flags: SandboxImageListFlags{},
			Output: &CommandOutput[SandboxImageList]{
				TextDescription: "Table with columns: NAME, STATUS, TAGS, SIZE, CREATED. " +
					"Prints \"No sandbox images found.\" to stderr when the list is empty, and a note " +
					"to stderr when --limit left some out.",
				Examples: []CommandExample{{
					Description: "List images.",
					Command:     "baseten sandbox image list",
				}},
				JQExample: CommandExample{
					Description: "Print every built image's name.",
					Command:     "baseten sandbox image list --jq '.items[] | select(.status == \"BUILT\") | .name'",
				},
			},
		},
		{
			Name:    "describe",
			Summary: "Describe a sandbox image (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Retrieves one image's record: status, tag count, and size.",
			Flags: SandboxImageRefCommandFlags{},
			Output: &CommandOutput[managementapi.Image]{
				TextDescription: "One field per line describing the image. Empty fields are left out.",
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
				"Pushes a new version of an image, from exactly one source: --dir builds the " +
				"directory, which must have a Dockerfile at its root, and --registry-image imports " +
				"an image from a registry.\n\n" +
				"Paths in --dir are left out as a .dockerignore at its root says. Without one, these " +
				"are left out, as if the .dockerignore were:\n\n" +
				"  **/.blaxel\n" +
				"  **/.env.build\n" +
				"  **/.docker\n" +
				"  **/.git\n" +
				"  **/dist\n" +
				"  **/.venv\n" +
				"  **/venv\n" +
				"  **/node_modules\n" +
				"  **/.env\n" +
				"  .env*\n" +
				"  **/.next\n" +
				"  **/__pycache__\n\n" +
				"A .dockerignore replaces these defaults, so copy any you still want into it.\n\n" +
				"By default the command returns once the push is accepted and the build runs on; " +
				"--wait waits until the image is built.",
			Flags: SandboxImagePushFlags{},
			Output: &CommandOutput[managementapi.Image]{
				TextDescription: "One field per line describing the image. Empty fields are left out.",
				Examples: []CommandExample{
					{
						Description: "Build an image from the current directory and wait for it.",
						Command:     "baseten sandbox image push --name my-image --dir . --wait",
					},
					{
						Description: "Import an image from a private registry.",
						CommandLines: []string{
							"baseten sandbox image push --name my-image",
							"--registry-image registry.example.com/app:v1",
							"--docker-config ~/.docker/config.json",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Push from a directory and print the image's status.",
					Command:     "baseten sandbox image push --name my-image --dir . --jq '.status'",
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a sandbox image (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Deletes an image and every version of it. This cannot be undone.",
			Flags: SandboxImageDeleteFlags{},
			Output: &CommandOutput[managementapi.Image]{
				TextDescription: "A confirmation line on stderr.",
				JSONDescription: "The image's record as deletion starts.",
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
		{
			Name:    "logs",
			Summary: "Print a sandbox image's build logs (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Prints an image's build logs, oldest first. At most the newest 11,000 lines in the " +
				"time range are printed. Lines may take a short time to appear after they are " +
				"written.",
			Flags: SandboxImageLogsFlags{},
			Output: &CommandOutput[managementapi.ImageBuildLog]{
				TextDescription:   "One line per log entry: timestamp and message.",
				JSONArrayStreamed: true,
				Examples: []CommandExample{{
					Description: "Print an image's build logs.",
					Command:     "baseten sandbox image logs --name my-image",
				}},
				JQExample: CommandExample{
					Description: "Print only the messages.",
					Command:     "baseten sandbox image logs --name my-image --jq '.message'",
				},
			},
		},
		{
			Name:    "list-tags",
			Summary: "List a sandbox image's tags (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Lists every tag of an image, one per pushed version. Create a sandbox from a " +
				"specific version with --image <name>:<tag>.",
			Flags: SandboxImageRefCommandFlags{},
			Output: &CommandOutput[SandboxImageTagList]{
				TextDescription: "Table with columns: TAG, SIZE, CREATED. Prints \"No tags found.\" to " +
					"stderr when the list is empty.",
				Examples: []CommandExample{{
					Description: "List an image's tags.",
					Command:     "baseten sandbox image list-tags --name my-image",
				}},
				JQExample: CommandExample{
					Description: "Print every tag.",
					Command:     "baseten sandbox image list-tags --name my-image --jq '.items[].name'",
				},
			},
		},
		{
			Name:    "list-library",
			Summary: "List starter sandbox images (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Lists the platform's starter images, which any sandbox can be created from without " +
				"pushing anything. Pass an entry's image as --image to 'sandbox create'.",
			Flags: SandboxImageListLibraryFlags{},
			Output: &CommandOutput[SandboxImageLibraryList]{
				TextDescription: "Table with columns: NAME, IMAGE, MEMORY, CATEGORIES. Prints \"No " +
					"starter images found.\" to stderr when the list is empty.",
				Examples: []CommandExample{{
					Description: "List the starter images.",
					Command:     "baseten sandbox image list-library",
				}},
				JQExample: CommandExample{
					Description: "Print every starter image.",
					Command:     "baseten sandbox image list-library --jq '.items[].image'",
				},
			},
		},
	},
}

// SandboxImageList is the JSON shape of 'baseten sandbox image list'.
type SandboxImageList struct {
	Items []managementapi.Image `json:"items"`
}

// SandboxImageTagList is the JSON shape of 'baseten sandbox image list-tags'.
type SandboxImageTagList struct {
	Items []managementapi.ImageTag `json:"items"`
}

// SandboxImageLibraryList is the JSON shape of 'baseten sandbox image
// list-library'.
type SandboxImageLibraryList struct {
	Items []managementapi.SandboxLibraryImage `json:"items"`
}

// SandboxImageRefFlags selects one image.
type SandboxImageRefFlags struct {
	Name string `flag:"name" desc:"Name of the image." required:"true"`
	Team string `flag:"team" desc:"Team name or ID the image belongs to. Defaults to your only team; required if you belong to more than one. Run 'baseten org team list' to see teams."`
}

// SandboxImageListFlags configures 'baseten sandbox image list'.
type SandboxImageListFlags struct {
	CommandFlags

	Team  string `flag:"team" desc:"Team name or ID to list images of. Defaults to your only team; required if you belong to more than one. Run 'baseten org team list' to see teams."`
	Limit int    `flag:"limit" desc:"Most images to list. 0 lists all." default:"1000"`
}

// SandboxImageRefCommandFlags configures the commands that only select an
// image: 'baseten sandbox image describe' and 'list-tags'.
type SandboxImageRefCommandFlags struct {
	CommandFlags
	SandboxImageRefFlags
}

// SandboxImagePushFlags configures 'baseten sandbox image push'.
type SandboxImagePushFlags struct {
	CommandFlags
	SandboxImageRefFlags

	Dir           string `flag:"dir" desc:"Directory to build the image from, with a Dockerfile at its root." oneof:"image-source"`
	RegistryImage string `flag:"registry-image" desc:"Registry image reference to import, such as registry.example.com/app:v1." oneof:"image-source"`
	DockerConfig  string `flag:"docker-config" desc:"Path to a Docker config.json with credentials for pulling --registry-image from a private registry."`
	Wait          bool   `flag:"wait" desc:"Wait until the image is built. Exits non-zero if the build fails. Does not stop the build if interrupted."`
}

// SandboxImageDeleteFlags configures 'baseten sandbox image delete'.
type SandboxImageDeleteFlags struct {
	CommandFlags
	SandboxImageRefFlags

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// SandboxImageLogsFlags configures 'baseten sandbox image logs'.
type SandboxImageLogsFlags struct {
	CommandFlags
	SandboxImageRefFlags

	Start time.Time `flag:"start" desc:"Earliest log time to print, such as 2026-10-05T15:04:05Z."`
	End   time.Time `flag:"end" desc:"Latest log time to print, such as 2026-10-05T15:04:05Z."`
}

// SandboxImageListLibraryFlags configures 'baseten sandbox image
// list-library'.
type SandboxImageListLibraryFlags struct {
	CommandFlags
}
