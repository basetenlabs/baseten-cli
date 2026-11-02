package cmd

import (
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

const gridMachineUserDescription = "A machine user is a team-owned identity, such as a bot or a scheduled job, with its own Grid budget and API keys. " +
	"Managing machine users requires team admin. Set a machine user's spend limit with " +
	"'baseten grid user-settings update --team <team> --user-id <machine-user-id>'; without one it uses the team's per-member default.\n\n"

const gridMachineUserKeyNameDescription = "Key names are lowercase letters, numbers, and hyphens, unique among the machine user's keys."

var commandGridMachineUser = Command{
	Name:        "machine-user",
	Summary:     "Manage machine users (PRE-RELEASE)",
	Description: gridPrereleaseNotice + gridMachineUserDescription + "Their API keys are managed with 'baseten grid machine-user api-key'.",
	Children: []Command{
		{
			Name:        "list",
			Summary:     "List machine users (PRE-RELEASE)",
			Description: gridPrereleaseNotice + "Lists machine users across the teams you administer; use --team to select one.",
			Flags:       GridMachineUserListFlags{},
			Output: &CommandOutput[GridMachineUserList]{
				TextDescription: "Table with ID, NAME, TEAM, and CREATED columns. Lists all pages; empty results print a message to stderr.",
				Examples: []CommandExample{{
					Description: "List machine users in a team.",
					Command:     "baseten grid machine-user list --team <team>",
				}},
				JQExample: CommandExample{
					Description: "Print machine user IDs.",
					Command:     "baseten grid machine-user list --jq '.items[].id'",
				},
			},
		},
		{
			Name:    "create",
			Summary: "Create a machine user and its first API key (PRE-RELEASE)",
			Description: gridPrereleaseNotice + gridMachineUserDescription +
				"Creates the machine user with one API key, printed exactly once; pass --no-api-key to create it without one. " +
				"--api-key-name defaults to a name derived from the machine user's. " + gridMachineUserKeyNameDescription + " " + gridTeamDescription,
			Flags: GridMachineUserCreateFlags{},
			Output: &CommandOutput[managementapi.CreatedGridMachineUser]{
				TextDescription: "Prints the raw API key value on stdout (one line), and the machine user's ID plus " +
					"\"Save this key now. It will not be shown again.\" on stderr. With --no-api-key, only the stderr line.",
				Examples: []CommandExample{{
					Description: "Create a machine user for a review bot.",
					Command:     "baseten grid machine-user create --team <team> --name review-bot",
				}},
				JQExample: CommandExample{
					Description: "Print the new machine user's ID and its one-time key.",
					Command:     "baseten grid machine-user create --name review-bot --jq '{id, api_key}'",
				},
			},
		},
		{
			Name:        "describe",
			Summary:     "Show a machine user (PRE-RELEASE)",
			Description: gridPrereleaseNotice + "Shows a machine user's name, team, and creator. Requires team admin.",
			Flags:       GridMachineUserDescribeFlags{},
			Output: &CommandOutput[managementapi.GridMachineUser]{
				TextDescription: "ID, name, team, creator, and creation time.",
				Examples: []CommandExample{{
					Description: "Show a machine user.",
					Command:     "baseten grid machine-user describe --id <id>",
				}},
				JQExample: CommandExample{
					Description: "Print the machine user's team ID.",
					Command:     "baseten grid machine-user describe --id <id> --jq '.team_id'",
				},
			},
		},
		{
			Name:        "update",
			Summary:     "Rename a machine user (PRE-RELEASE)",
			Description: gridPrereleaseNotice + "Requires team admin. Names are unique within the team.",
			Flags:       GridMachineUserUpdateFlags{},
			Output: &CommandOutput[managementapi.GridMachineUser]{
				TextDescription: "The updated machine user, in the same format as describe.",
				Examples: []CommandExample{{
					Description: "Rename a machine user.",
					Command:     "baseten grid machine-user update --id <id> --name night-shift",
				}},
				JQExample: CommandExample{
					Description: "Print the new name.",
					Command:     "baseten grid machine-user update --id <id> --name night-shift --jq '.name'",
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a machine user and revoke its API keys (PRE-RELEASE)",
			Description: gridPrereleaseNotice + "Requires team admin. Its API keys stop working; its usage history remains. " +
				"Pass --yes to skip confirmation.",
			Flags: GridMachineUserDeleteFlags{},
			Output: &CommandOutput[managementapi.GridMachineUserTombstone]{
				TextDescription: "Confirmation on stderr; JSON contains the deleted machine user's ID.",
				Examples: []CommandExample{{
					Description: "Delete a machine user.",
					Command:     "baseten grid machine-user delete --id <id> --yes",
				}},
				JQExample: CommandExample{
					Description: "Print the deleted ID.",
					Command:     "baseten grid machine-user delete --id <id> --yes --jq '.id'",
				},
			},
		},
		{
			Name:    "api-key",
			Summary: "Manage a machine user's API keys (PRE-RELEASE)",
			Children: []Command{
				{
					Name:        "list",
					Summary:     "List a machine user's API keys (PRE-RELEASE)",
					Description: gridPrereleaseNotice + "Lists key metadata; key values are never returned. Requires team admin.",
					Flags:       GridMachineUserAPIKeyListFlags{},
					Output: &CommandOutput[managementapi.APIKeys]{
						TextDescription: "Table with NAME, KEY (prefix + ****), CREATED, and LAST USED columns. " +
							"When the machine user has no keys, prints \"No API keys found.\" to stderr.",
						Examples: []CommandExample{{
							Description: "List a machine user's keys.",
							Command:     "baseten grid machine-user api-key list --machine-user-id <id>",
						}},
						JQExample: CommandExample{
							Description: "Print the key prefixes.",
							Command:     "baseten grid machine-user api-key list --machine-user-id <id> --jq '.keys[].prefix'",
						},
					},
				},
				{
					Name:    "create",
					Summary: "Create an API key for a machine user (PRE-RELEASE)",
					Description: gridPrereleaseNotice + "The key value is printed to stdout exactly once and cannot be retrieved later. " +
						"--name defaults to a name derived from the machine user's. " + gridMachineUserKeyNameDescription + " Requires team admin.",
					Flags: GridMachineUserAPIKeyCreateFlags{},
					Output: &CommandOutput[managementapi.APIKey]{
						TextDescription: "Prints the raw API key value on stdout (one line). Also prints " +
							"\"Save this key now. It will not be shown again.\" to stderr.",
						Examples: []CommandExample{{
							Description: "Create a named key for a machine user.",
							Command:     "baseten grid machine-user api-key create --machine-user-id <id> --name ci",
						}},
						JQExample: CommandExample{
							Description: "Print just the raw key value.",
							Command:     "baseten grid machine-user api-key create --machine-user-id <id> --jq '.api_key'",
						},
					},
				},
				{
					Name:    "revoke",
					Summary: "Revoke a machine user's API key (PRE-RELEASE)",
					Description: gridPrereleaseNotice + "Revokes one key; the machine user and its other keys are unchanged. " +
						"Exactly one of --name or --prefix is required. Requires team admin.",
					Flags: GridMachineUserAPIKeyRevokeFlags{},
					Output: &CommandOutput[managementapi.APIKeyTombstone]{
						TextDescription: "Prints \"Revoked API key <prefix>\" to stderr on success; no stdout output.",
						Examples: []CommandExample{{
							Description: "Revoke a key by name.",
							Command:     "baseten grid machine-user api-key revoke --machine-user-id <id> --name ci",
						}},
						JQExample: CommandExample{
							Description: "Print the revoked key's prefix.",
							CommandLines: []string{
								"baseten grid machine-user api-key revoke --machine-user-id <id>",
								"--prefix <prefix> --jq '.prefix'",
							},
						},
					},
				},
			},
		},
	},
}

type GridMachineUserList struct {
	Items []managementapi.GridMachineUser `json:"items"`
}

type GridMachineUserIDFlags struct {
	ID string `flag:"id" desc:"Machine user ID." required:"true"`
}

type GridMachineUserListFlags struct {
	CommandFlags
	GridTeamListFlags
}

type GridMachineUserCreateFlags struct {
	CommandFlags
	GridTeamFlags
	Name       string `flag:"name" desc:"Name of the machine user, unique within the team." required:"true"`
	APIKeyName string `flag:"api-key-name" desc:"Name of the first API key. Cannot be combined with --no-api-key."`
	NoAPIKey   bool   `flag:"no-api-key" desc:"Create the machine user without an API key."`
}

type GridMachineUserDescribeFlags struct {
	CommandFlags
	GridMachineUserIDFlags
}

type GridMachineUserUpdateFlags struct {
	CommandFlags
	GridMachineUserIDFlags
	Name string `flag:"name" desc:"New name, unique within the team." required:"true"`
}

type GridMachineUserDeleteFlags struct {
	CommandFlags
	GridMachineUserIDFlags
	Yes bool `flag:"yes" desc:"Skip confirmation; required when stdin is not a terminal."`
}

type GridMachineUserKeyOwnerFlags struct {
	MachineUserID string `flag:"machine-user-id" desc:"Machine user ID." required:"true"`
}

type GridMachineUserAPIKeyListFlags struct {
	CommandFlags
	GridMachineUserKeyOwnerFlags
}

type GridMachineUserAPIKeyCreateFlags struct {
	CommandFlags
	GridMachineUserKeyOwnerFlags
	Name string `flag:"name" desc:"Name of the API key."`
}

type GridMachineUserAPIKeyRevokeFlags struct {
	CommandFlags
	GridMachineUserKeyOwnerFlags
	Name   string `flag:"name" desc:"Name of the API key to revoke." oneof:"identifier"`
	Prefix string `flag:"prefix" desc:"Prefix of the API key to revoke (as shown in list)." oneof:"identifier"`
}
