package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

var commandGridConnection = Command{
	Name: "connection", Summary: "Manage provider connections (PRE-RELEASE)",
	Children: []Command{
		{
			Name: "list", Summary: "List a team's provider connections",
			Description: gridPrereleaseNotice + gridTeamDescription,
			Flags:       GridConnectionListFlags{},
			Output: &CommandOutput[GridConnectionList]{
				TextDescription: "Table with ID, TEAM, PROVIDER, SECRET ID, and SECRET NAME columns. Lists all pages; secret values are never returned.",
				Examples:        []CommandExample{{Description: "List your only team's connections.", Command: "baseten grid connection list"}},
				JQExample:       CommandExample{Description: "Print connection IDs.", Command: "baseten grid connection list --jq '.items[].id'"},
			},
		},
		{
			Name: "create", Summary: "Create a provider connection using an existing team secret",
			Description: gridPrereleaseNotice + "The secret must belong to the connection's team. Requires team admin. " + gridTeamDescription,
			Flags:       GridConnectionCreateFlags{},
			Output: &CommandOutput[managementapi.GridConnection]{
				TextDescription: "The created connection's ID, team, provider, and secret metadata.",
				Examples:        []CommandExample{{Description: "Connect Anthropic using a team secret.", Command: "baseten grid connection create --provider anthropic --secret-id <id>"}},
				JQExample:       CommandExample{Description: "Print the new connection ID.", CommandLines: []string{"baseten grid connection create --provider openai --secret-id <id>", "--jq '.id'"}},
			},
		},
		{
			Name: "update", Summary: "Point a connection at another team secret",
			Description: gridPrereleaseNotice + "Requires team admin. The provider must match the existing connection; provider and owning team cannot change.",
			Flags:       GridConnectionUpdateFlags{},
			Output: &CommandOutput[managementapi.GridConnection]{
				TextDescription: "The updated connection's ID, team, provider, and secret metadata.",
				Examples:        []CommandExample{{Description: "Change the secret used by a connection.", CommandLines: []string{"baseten grid connection update --id <id>", "--provider anthropic --secret-id <secret-id>"}}},
				JQExample:       CommandExample{Description: "Print the updated secret metadata.", CommandLines: []string{"baseten grid connection update --id <id>", "--provider anthropic --secret-id <secret-id> --jq '.config'"}},
			},
		},
		{
			Name: "delete", Summary: "Delete a provider connection",
			Description: gridPrereleaseNotice + "Requires team admin. Leaves the referenced secret in place. Pass --yes to skip confirmation.",
			Flags:       GridConnectionDeleteFlags{},
			Output: &CommandOutput[managementapi.GridConnectionTombstone]{
				TextDescription: "Confirmation on stderr; JSON contains the deleted connection ID.",
				Examples:        []CommandExample{{Description: "Delete a connection.", Command: "baseten grid connection delete --id <id> --yes"}},
				JQExample:       CommandExample{Description: "Print the deleted ID.", Command: "baseten grid connection delete --id <id> --yes --jq '.id'"},
			},
		},
	},
}

type GridConnectionList struct {
	Items []managementapi.GridConnection `json:"items"`
}
type GridConnectionListFlags struct {
	CommandFlags
	GridTeamFlags
}
type GridConnectionConfigFlags struct {
	Provider string `flag:"provider" desc:"Upstream provider." enum:"anthropic,openai,xai" required:"true"`
	SecretID string `flag:"secret-id" desc:"ID of an existing secret in the connection's team." required:"true"`
}
type GridConnectionCreateFlags struct {
	CommandFlags
	GridTeamFlags
	GridConnectionConfigFlags
}
type GridConnectionUpdateFlags struct {
	CommandFlags
	GridConnectionConfigFlags
	ID string `flag:"id" desc:"Connection ID." required:"true"`
}
type GridConnectionDeleteFlags struct {
	CommandFlags
	ID  string `flag:"id" desc:"Connection ID." required:"true"`
	Yes bool   `flag:"yes" desc:"Skip confirmation; required when stdin is not a terminal."`
}
