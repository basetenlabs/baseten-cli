package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

var commandSandboxVolume = Command{
	Name:    "volume",
	Summary: "Manage sandbox volumes (PRE-RELEASE)",
	Description: sandboxPreRelease +
		"Sandbox volumes are persistent disks that keep their data beyond the sandboxes they are " +
		"mounted in. Create one, then mount it with 'sandbox create --volume NAME:MOUNT_PATH'. " +
		"Only sandboxes in the volume's region can mount it, one at a time.",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List sandbox volumes (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Lists the team's sandbox volumes, newest first, up to --limit. Filter by --query or " +
				"repeatable --status. Labels are not listed; see them with 'sandbox volume describe'.",
			Flags: SandboxVolumeListFlags{},
			Output: &CommandOutput[SandboxVolumeList]{
				TextDescription: "Table with columns: NAME, STATUS, SIZE, REGION, ATTACHED TO, CREATED. " +
					"Prints \"No sandbox volumes found.\" to stderr when the list is empty, and a note " +
					"to stderr when --limit left some out.",
				Examples: []CommandExample{
					{
						Description: "List sandbox volumes.",
						Command:     "baseten sandbox volume list",
					},
					{
						Description: "List only failed sandbox volumes.",
						Command:     "baseten sandbox volume list --status failed",
					},
				},
				JQExample: CommandExample{
					Description: "Print the name of every sandbox volume not mounted anywhere.",
					Command:     "baseten sandbox volume list --jq '.items[] | select(.attached_to == null) | .name'",
				},
			},
		},
		{
			Name:    "describe",
			Summary: "Describe a sandbox volume (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Retrieves one sandbox volume's record: status, size, region, the sandbox it is " +
				"mounted in, and labels.",
			Flags: SandboxVolumeRefCommandFlags{},
			Output: &CommandOutput[managementapi.SandboxVolume]{
				TextDescription: "One field per line describing the sandbox volume. Empty fields are left out.",
				Examples: []CommandExample{{
					Description: "Describe a sandbox volume.",
					Command:     "baseten sandbox volume describe --name my-volume",
				}},
				JQExample: CommandExample{
					Description: "Print the sandbox the volume is mounted in.",
					Command:     "baseten sandbox volume describe --name my-volume --jq '.attached_to'",
				},
			},
		},
		{
			Name:    "create",
			Summary: "Create a sandbox volume (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Creates a sandbox volume and prints its record. The volume becomes ready " +
				"asynchronously, so its status may still be DEPLOYING; check it with 'sandbox volume " +
				"describe' before mounting it.",
			Flags: SandboxVolumeCreateFlags{},
			Output: &CommandOutput[managementapi.SandboxVolume]{
				TextDescription: "One field per line describing the created sandbox volume. Empty fields are left out.",
				Examples: []CommandExample{
					{
						Description: "Create a 10 GiB sandbox volume.",
						Command:     "baseten sandbox volume create --name my-volume --size 10Gi",
					},
					{
						Description: "Create one in a specific region with labels.",
						CommandLines: []string{
							"baseten sandbox volume create --name my-volume --size 10Gi",
							"--region us-was-1 --label team=cli",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Create a sandbox volume and print its status.",
					Command:     "baseten sandbox volume create --name my-volume --size 10Gi --jq '.status'",
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a sandbox volume (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Grows a sandbox volume or replaces its labels. Omitted flags leave their fields " +
				"unchanged. A volume can grow but never shrink. Any --label replaces all of its " +
				"labels, and --clear-labels removes them all.",
			Flags: SandboxVolumeUpdateFlags{},
			Output: &CommandOutput[managementapi.SandboxVolume]{
				TextDescription: "One field per line describing the updated sandbox volume. Empty fields are left out.",
				Examples: []CommandExample{
					{
						Description: "Grow a sandbox volume to 20 GiB.",
						Command:     "baseten sandbox volume update --name my-volume --size 20Gi",
					},
					{
						Description: "Replace a sandbox volume's labels.",
						Command:     "baseten sandbox volume update --name my-volume --label env=dev",
					},
				},
				JQExample: CommandExample{
					Description: "Grow a sandbox volume and print its new size.",
					Command:     "baseten sandbox volume update --name my-volume --size 20Gi --jq '.size'",
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a sandbox volume (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Deletes a sandbox volume and all its data. This cannot be undone. Fails while a " +
				"sandbox mounts the volume. Deletion continues after this command returns.",
			Flags: SandboxVolumeDeleteFlags{},
			Output: &CommandOutput[managementapi.SandboxVolume]{
				TextDescription: "A confirmation line on stderr.",
				JSONDescription: "The sandbox volume's record as deletion starts.",
				Examples: []CommandExample{{
					Description: "Delete a sandbox volume without the confirmation prompt.",
					Command:     "baseten sandbox volume delete --name my-volume --yes",
				}},
				JQExample: CommandExample{
					Description: "Delete a sandbox volume and print its status.",
					Command:     "baseten sandbox volume delete --name my-volume --yes --jq '.status'",
				},
			},
		},
	},
}

// SandboxVolumeList is the JSON shape of 'baseten sandbox volume list'.
type SandboxVolumeList struct {
	Items []managementapi.SandboxVolume `json:"items"`
}

// SandboxVolumeRefFlags selects one sandbox volume.
type SandboxVolumeRefFlags struct {
	Name string `flag:"name" desc:"Name of the sandbox volume." required:"true"`
	Team string `flag:"team" desc:"Team name or ID the sandbox volume belongs to. Defaults to your only team; required if you belong to more than one. Run 'baseten org team list' to see teams."`
}

// SandboxVolumeListFlags configures 'baseten sandbox volume list'.
type SandboxVolumeListFlags struct {
	CommandFlags

	Team   string   `flag:"team" desc:"Team name or ID to list sandbox volumes of. Defaults to your only team; required if you belong to more than one. Run 'baseten org team list' to see teams."`
	Query  string   `flag:"query" desc:"Search over sandbox volume names."`
	Status []string `flag:"status" desc:"Only sandbox volumes with one of these statuses. Repeatable." enum:"deleting,deployed,deploying,failed"`
	Limit  int      `flag:"limit" desc:"Most sandbox volumes to list. 0 lists all." default:"1000"`
}

// SandboxVolumeRefCommandFlags configures the commands that only select a
// sandbox volume: 'baseten sandbox volume describe'.
type SandboxVolumeRefCommandFlags struct {
	CommandFlags
	SandboxVolumeRefFlags
}

// SandboxVolumeCreateFlags configures 'baseten sandbox volume create'. Unset
// flags are left out of the request, so the server applies its defaults.
type SandboxVolumeCreateFlags struct {
	CommandFlags

	Name   string   `flag:"name" desc:"Unique name of the sandbox volume. The server generates one when omitted."`
	Team   string   `flag:"team" desc:"Team name or ID to create the sandbox volume in. Defaults to your only team; required if you belong to more than one. Run 'baseten org team list' to see teams."`
	Size   string   `flag:"size" desc:"Capacity, as a size (10Gi) or Mi (10240). It can grow later but never shrink." required:"true"`
	Region string   `flag:"region" desc:"Region of the sandbox volume. Only sandboxes in the same region can mount it. Defaults to the default sandbox region."`
	Label  []string `flag:"label" desc:"Label as KEY=VALUE. Repeatable."`
}

// SandboxVolumeUpdateFlags configures 'baseten sandbox volume update'. Unset
// flags leave their fields unchanged.
type SandboxVolumeUpdateFlags struct {
	CommandFlags
	SandboxVolumeRefFlags

	Size        string   `flag:"size" desc:"New capacity, as a size (20Gi) or Mi (20480), no less than the current capacity."`
	Label       []string `flag:"label" desc:"Label as KEY=VALUE, replacing all of the sandbox volume's labels. Repeatable. Cannot be combined with --clear-labels."`
	ClearLabels bool     `flag:"clear-labels" desc:"Remove all of the sandbox volume's labels. Cannot be combined with --label."`
}

// SandboxVolumeDeleteFlags configures 'baseten sandbox volume delete'.
type SandboxVolumeDeleteFlags struct {
	CommandFlags
	SandboxVolumeRefFlags

	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}
