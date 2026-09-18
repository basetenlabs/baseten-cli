package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

const routePrereleaseNotice = "PRE-RELEASE: route commands are not GA yet. Their arguments, flags, and output may change.\n\n"

const routeTargetJSONDescription = "target.type is BASETEN_MODEL_API, ANTHROPIC, OPENAI, XAI, VERTEX, or " +
	"OPENAI_COMPATIBLE. Every target includes model. External targets also include " +
	"secret_name; VERTEX also includes vertex_config.project_id and vertex_config.location, and " +
	"OPENAI_COMPATIBLE includes base_url."

var commandRoute = Command{
	Name:    "route",
	Summary: "Manage routes (PRE-RELEASE)",
	Description: routePrereleaseNotice +
		"Manage routes with globally unique names using an organization-owned prefix. Names " +
		"and team ownership are immutable. Supply required inputs as flags.",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List routes (PRE-RELEASE)",
			Flags:   RouteListFlags{},
			Description: routePrereleaseNotice +
				"List routes you can invoke, newest first. Fetches all pages automatically.",
			Output: &CommandOutput[RouteList]{
				JSONDescription: "All matching routes in items. " + routeTargetJSONDescription,
				TextDescription: "Table with ID, NAME, DISPLAY NAME, TEAM, TARGET, and CREATED. An empty-list message is written to stderr.",
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
				"Describe a route by exactly one of --id or --name (an exact match).",
			Output: &CommandOutput[managementapi.Route]{
				JSONDescription: routeTargetJSONDescription,
				TextDescription: "Field-per-line route summary, including the full target, invoke URL, and team name.",
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
				"Create a route with a name, --target-type, and --target-model. Omit --team to use your " +
				"organization's default team. External targets " +
				"require --target-secret naming a credential secret belonging to the owning team. " +
				"Find Model API names with 'baseten model-api list'. " +
				"Set optional metadata with --display-name and --description. Create credentials " +
				"separately with 'baseten org secret set --team <team> --name <secret>'.",
			Output: &CommandOutput[managementapi.Route]{
				JSONDescription: routeTargetJSONDescription,
				TextDescription: "Creation confirmation on stderr; no stdout in text mode.",
				Examples: []CommandExample{
					{
						Description: "Create a Model API route.",
						CommandLines: []string{
							"baseten route create --name acme/assistant",
							"--target-type baseten-model-api --target-model <model>",
						},
					},
					{
						Description: "Create an external provider route.",
						CommandLines: []string{
							"baseten route create --name acme/assistant",
							"--target-type anthropic --target-model <model>",
							"--target-secret anthropic-key",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print the created route ID.",
					CommandLines: []string{
						"baseten route create --name acme/assistant",
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
				"Select exactly one of --id or --name. Set --display-name, --description, a complete target, or " +
				"a combination. A target replaces the entire previous target; omitted fields are not merged. " +
				"Name and team cannot be changed. Pass --description '' to clear the description.",
			Output: &CommandOutput[managementapi.Route]{
				JSONDescription: routeTargetJSONDescription,
				TextDescription: "Update confirmation on stderr; no stdout in text mode.",
				Examples: []CommandExample{
					{
						Description: "Replace a route's target.",
						CommandLines: []string{
							"baseten route update --name acme/assistant",
							"--target-type baseten-model-api --target-model <model>",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Change the display label and print it.",
					Command:     "baseten route update --id <id> --display-name Assistant --jq '.display_name'",
				},
			},
		},
		{
			Name:    "delete",
			Summary: "Delete a route (PRE-RELEASE)",
			Flags:   RouteDeleteFlags{},
			Description: routePrereleaseNotice +
				"Delete a route by exactly one of --id or --name. Prompts for confirmation unless --yes is " +
				"passed. When stdin is not a terminal, --yes is required.",
			Output: &CommandOutput[managementapi.RouteTombstone]{
				TextDescription: "Confirmation on stderr; no stdout in text mode.",
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
	},
}

type RouteRefFlags struct {
	ID   string `flag:"id" desc:"Stable route ID." oneof:"route-ref"`
	Name string `flag:"name" desc:"Exact route name, including its organization-owned prefix." oneof:"route-ref"`
}

type RouteTargetFlags struct {
	TargetType           string `flag:"target-type" desc:"Target type. Supply --target-model and any required type-specific flags." enum:"baseten-model-api,anthropic,openai,xai,vertex,openai-compatible"`
	TargetModel          string `flag:"target-model" desc:"Model API name or model name sent to the external provider."`
	TargetSecret         string `flag:"target-secret" desc:"Name of an existing credential secret in the route's team. Required for external targets."`
	TargetBaseURL        string `flag:"target-base-url" desc:"HTTPS base URL. Required for and only valid with openai-compatible."`
	TargetVertexProject  string `flag:"target-vertex-project" desc:"Google Cloud project ID or number. Required for and only valid with vertex."`
	TargetVertexLocation string `flag:"target-vertex-location" desc:"Google Cloud location, such as global. Required for and only valid with vertex."`
}

// RouteList contains the routes aggregated across all pages.
type RouteList struct {
	Items []managementapi.Route `json:"items"`
}

type RouteListFlags struct {
	CommandFlags
	Team string `flag:"team" desc:"Filter by team name or ID."`
}

type RouteDescribeFlags struct {
	CommandFlags
	RouteRefFlags
}

type RouteCreateFlags struct {
	CommandFlags
	RouteTargetFlags
	Name        string               `flag:"name" desc:"Globally unique route name with an organization-owned prefix." required:"true"`
	Team        string               `flag:"team" desc:"Owning team name or ID (immutable). Defaults to your organization's default team."`
	DisplayName OptionalFlag[string] `flag:"display-name" desc:"Display label (1 to 255 characters). Defaults to the route name."`
	Description OptionalFlag[string] `flag:"description" desc:"Optional route description (up to 1000 characters)."`
}

type RouteUpdateFlags struct {
	CommandFlags
	RouteRefFlags
	RouteTargetFlags
	DisplayName OptionalFlag[string] `flag:"display-name" desc:"New display label (1 to 255 characters). Omit to keep it unchanged."`
	Description OptionalFlag[string] `flag:"description" desc:"New description (up to 1000 characters). Pass an empty string to clear it."`
}

type RouteDeleteFlags struct {
	CommandFlags
	RouteRefFlags
	Yes bool `flag:"yes" desc:"Skip the interactive confirmation prompt. Required when stdin is not a terminal."`
}
