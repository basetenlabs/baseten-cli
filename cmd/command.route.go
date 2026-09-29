package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

const routePrereleaseNotice = "PRE-RELEASE: route commands are not GA yet. " +
	"Their arguments, flags, and output may change.\n\n"

const routeTargetJSONDescription = "target.type is BASETEN_MODEL_API, ANTHROPIC, OPENAI, or XAI. " +
	"Every target includes model. External targets also include secret_name."

var commandRoute = Command{
	Name:    "route",
	Summary: "Manage routes (PRE-RELEASE)",
	Description: routePrereleaseNotice +
		"Manage routes. Baseten derives each route's name from its target. " +
		"A route's name, team, and target cannot be changed after creation.",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List routes (PRE-RELEASE)",
			Flags:   RouteListFlags{},
			Description: routePrereleaseNotice +
				"List all routes you can invoke, newest first.",
			Output: &CommandOutput[RouteList]{
				JSONDescription: "All matching routes in items. " + routeTargetJSONDescription,
				TextDescription: "Table with columns: ID, NAME, DISPLAY NAME, TEAM, TARGET, CREATED. " +
					"When no routes match, prints \"No routes found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List all visible routes.",
						Command:     "baseten route list",
					},
					{
						Description: "Filter by team.",
						Command:     "baseten route list --team Engineering",
					},
				},
				JQExample: CommandExample{
					Description: "Print route names.",
					Command:     "baseten route list --jq '.items[].name'",
				},
			},
		},
		{
			Name:    "describe",
			Summary: "Describe a route (PRE-RELEASE)",
			Flags:   RouteDescribeFlags{},
			Description: routePrereleaseNotice +
				"Describe a route by ID or exact name. Pass exactly one of --id or --name.",
			Output: &CommandOutput[managementapi.Route]{
				JSONDescription: routeTargetJSONDescription,
				TextDescription: "Field-per-line summary of the route, including its target, invoke URL, and team name. " +
					"Unrecognized target types are shown as <unrecognized type>.",
				Examples: []CommandExample{
					{
						Description: "Describe a route by name.",
						Command:     "baseten route describe --name acme/assistant",
					},
				},
				JQExample: CommandExample{
					Description: "Print the invoke URL.",
					Command:     "baseten route describe --id <id> --jq '.invoke_url'",
				},
			},
		},
		{
			Name:    "create",
			Summary: "Create a route (PRE-RELEASE)",
			Flags:   RouteCreateFlags{},
			Description: routePrereleaseNotice +
				"Create a route with --target-type and --target-model. Baseten derives the route's name " +
				"from its target. Find Model API names with 'baseten model-api list'.\n\n" +
				"Omit --team to use the organization's default team. External targets require " +
				"--target-secret, the name of an existing secret in the route's team. Store credentials " +
				"in a secret with 'baseten org secret set --name <secret>'; pass the same --team value " +
				"if the route uses a specific team.\n\n" +
				"Set optional metadata with --display-name and --description.",
			Output: &CommandOutput[managementapi.Route]{
				JSONDescription: routeTargetJSONDescription,
				TextDescription: "On success, prints \"Created route <name> (<id>)\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Create a Model API route.",
						CommandLines: []string{
							"baseten route create",
							"--target-type baseten-model-api --target-model <model>",
						},
					},
					{
						Description: "Create an external provider route.",
						CommandLines: []string{
							"baseten route create",
							"--target-type anthropic --target-model <model>",
							"--target-secret anthropic-key",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print the created route ID.",
					CommandLines: []string{
						"baseten route create",
						"--target-type baseten-model-api --target-model <model> --jq '.id'",
					},
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a route (PRE-RELEASE)",
			Flags:   RouteUpdateFlags{},
			Description: routePrereleaseNotice +
				"Update a route by ID or exact name. Pass exactly one of --id or --name.\n\n" +
				"Pass --display-name or --description; omitted fields are unchanged. " +
				"Pass --description '' to clear the description. Name, team, and target cannot be changed; " +
				"to change a target, create a new route.",
			Output: &CommandOutput[managementapi.Route]{
				JSONDescription: routeTargetJSONDescription,
				TextDescription: "On success, prints \"Updated route <name> (<id>)\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Change a route's description.",
						CommandLines: []string{
							"baseten route update --name acme/assistant",
							"--description 'Team assistant'",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Change the display name and print it.",
					Command:     "baseten route update --id <id> --display-name Assistant --jq '.display_name'",
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a route (PRE-RELEASE)",
			Flags:   RouteDeleteFlags{},
			Description: routePrereleaseNotice +
				"Delete a route by ID or exact name. Pass exactly one of --id or --name.\n\n" +
				"Prompts for confirmation. Pass --yes to skip the prompt. " +
				"When stdin is not a terminal, --yes is required.",
			Output: &CommandOutput[managementapi.RouteTombstone]{
				TextDescription: "On success, prints \"Deleted route <name> (<id>)\" to stderr; no stdout output.",
				Examples: []CommandExample{
					{
						Description: "Delete a route without prompting.",
						Command:     "baseten route delete --name acme/assistant --yes",
					},
				},
				JQExample: CommandExample{
					Description: "Print the deleted route's ID.",
					Command:     "baseten route delete --id <id> --yes --jq '.id'",
				},
			},
		},
		{
			Name:        "api-key",
			Summary:     "Manage your routes API keys (PRE-RELEASE)",
			Description: routePrereleaseNotice + "Manage the routes API keys you created, such as with harness setup, across all your machines and teams.",
			Children: []Command{
				{
					Name:        "list",
					Summary:     "List your routes API keys (PRE-RELEASE)",
					Description: routePrereleaseNotice + "List the routes API keys you created, across all your machines and teams. Key values are never shown.",
					Flags:       RouteAPIKeyListFlags{},
					Output: &CommandOutput[managementapi.APIKeys]{
						TextDescription: "Table with NAME, PREFIX, TEAM, CREATED, and LAST USED columns.",
						Examples:        []CommandExample{{Description: "List your routes API keys.", Command: "baseten route api-key list"}},
						JQExample:       CommandExample{Description: "Print key prefixes.", Command: "baseten route api-key list --jq '.keys[].prefix'"},
					},
				},
				{
					Name:    "delete",
					Summary: "Delete your routes API keys (PRE-RELEASE)",
					Description: routePrereleaseNotice + "Delete one routes API key by prefix, or all of them with --all. Harnesses using a deleted key lose access " +
						"until you rerun harness setup on that machine, which creates a new key.",
					Flags: RouteAPIKeyDeleteFlags{},
					Output: &CommandOutput[RouteAPIKeyDeleteResult]{
						TextDescription: "Each deleted prefix, on stderr.",
						JSONDescription: "deleted lists the deleted prefixes and failed the ones that could not be deleted. The command fails if any deletion failed.",
						Examples: []CommandExample{
							{Description: "Delete a lost machine's key.", Command: "baseten route api-key delete --prefix <prefix>"},
							{Description: "Delete all your routes API keys without prompting.", Command: "baseten route api-key delete --all --yes"},
						},
						JQExample: CommandExample{Description: "Print deleted prefixes.", Command: "baseten route api-key delete --prefix <prefix> --yes --jq '.deleted[]'"},
					},
				},
			},
		},
	},
}

// RouteAPIKeyListFlags are the flags for `baseten route api-key list`.
type RouteAPIKeyListFlags struct{ CommandFlags }

// RouteAPIKeyDeleteFlags are the flags for `baseten route api-key delete`.
type RouteAPIKeyDeleteFlags struct {
	CommandFlags
	Prefix string `flag:"prefix" desc:"Prefix of the key to delete, as shown by route api-key list." oneof:"key"`
	All    bool   `flag:"all" desc:"Delete all your routes API keys, on every machine and team." oneof:"key"`
	Yes    bool   `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}

// RouteAPIKeyDeleteResult is the JSON output of `baseten route api-key delete`.
type RouteAPIKeyDeleteResult struct {
	Deleted []string `json:"deleted"`
	Failed  []string `json:"failed"`
}

type RouteRefFlags struct {
	ID   string `flag:"id" desc:"Stable route ID." oneof:"route-ref"`
	Name string `flag:"name" desc:"Exact route name, including its organization-owned prefix." oneof:"route-ref"`
}

type RouteTargetFlags struct {
	TargetType   string `flag:"target-type" desc:"Target type." enum:"baseten-model-api,anthropic,openai,xai" required:"true"`
	TargetModel  string `flag:"target-model" desc:"Model API name or external provider model name." required:"true"`
	TargetSecret string `flag:"target-secret" desc:"Name of an existing secret in the route's team. Required for external targets."`
}

// RouteList contains the routes aggregated across all pages.
type RouteList struct {
	Items []managementapi.Route `json:"items"`
}

type RouteListFlags struct {
	CommandFlags
	Team string `flag:"team" desc:"Team name or ID to scope the listing to. Defaults to all routes you can invoke."`
}

type RouteDescribeFlags struct {
	CommandFlags
	RouteRefFlags
}

type RouteCreateFlags struct {
	CommandFlags
	RouteTargetFlags
	Team        string               `flag:"team" desc:"Team name or ID the route belongs to. Defaults to the organization's default team. Run 'baseten org team list' to see teams."`
	DisplayName OptionalFlag[string] `flag:"display-name" desc:"Display name (1 to 255 characters). Defaults to the route name."`
	Description OptionalFlag[string] `flag:"description" desc:"Optional route description (up to 1000 characters)."`
}

type RouteUpdateFlags struct {
	CommandFlags
	RouteRefFlags
	DisplayName OptionalFlag[string] `flag:"display-name" desc:"New display name (1 to 255 characters). Omit to keep it unchanged."`
	Description OptionalFlag[string] `flag:"description" desc:"New description (up to 1000 characters). Pass an empty string to clear it."`
}

type RouteDeleteFlags struct {
	CommandFlags
	RouteRefFlags
	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}
