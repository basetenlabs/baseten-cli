package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

const routePrereleaseNotice = "PRE-RELEASE: Route commands are not GA yet. Their arguments, flags, and output may change.\n\n"

const routeTargetJSONDescription = "target.type is BASETEN_MODEL_API, ANTHROPIC, OPENAI, XAI, VERTEX, or " +
	"OPENAI_COMPATIBLE. A Model API target includes model_api. Provider targets include model and " +
	"secret_name; VERTEX also includes vertex_config.project_id and vertex_config.location, and " +
	"OPENAI_COMPATIBLE includes base_url."

var commandRoute = Command{
	Name:    "route",
	Summary: "Manage Routes (PRE-RELEASE)",
	Description: routePrereleaseNotice +
		"Manage Routes. Route names are globally unique and must use an organization-owned prefix. Names " +
		"and team ownership are immutable. Supply required inputs as flags.",
	Children: []Command{
		{
			Name:    "list",
			Summary: "List Routes (PRE-RELEASE)",
			Flags:   RouteListFlags{},
			Description: routePrereleaseNotice +
				"List Routes you can invoke, newest first. Fetches all pages automatically.",
			Output: &CommandOutput[managementapi.RoutesResponse]{
				JSONDescription: "All matching Routes in items, with pagination from the final page. " + routeTargetJSONDescription,
				TextDescription: "Table with ID, NAME, DISPLAY NAME, TEAM ID, and TARGET. An empty-list message is written to stderr.",
				Examples: []CommandExample{
					{
						Description: "List all visible Routes.",
						Command:     "baseten route list",
					},
					{
						Description: "Filter by team and exact name.",
						Command:     "baseten route list --team Engineering --name acme/assistant",
					},
				},
				JQExample: CommandExample{
					Description: "Print Route names.",
					Command:     "baseten route list --jq '.items[].name'",
				},
			},
		},
		{
			Name:    "describe",
			Summary: "Describe a Route (PRE-RELEASE)",
			Flags:   RouteDescribeFlags{},
			Description: routePrereleaseNotice +
				"Describe a Route by exactly one of --id or --name (an exact match).",
			Output: &CommandOutput[managementapi.Route]{
				JSONDescription: routeTargetJSONDescription,
				TextDescription: "Field-per-line Route summary, including the full target, invoke URL, and team name when accessible.",
				Examples: []CommandExample{
					{
						Description: "Describe a Route by name.",
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
			Summary: "Create a Route (PRE-RELEASE)",
			Flags:   RouteCreateFlags{},
			Description: routePrereleaseNotice +
				"Create a Route with a team, name, and exactly one target: --target-model-api or " +
				"--target-provider. Provider targets require a model and a credential secret belonging to the " +
				"owning team. Set optional metadata with --display-name and --description. Create provider " +
				"credentials separately with 'baseten org secret set --team <team> --name <secret>'.",
			Output: &CommandOutput[managementapi.Route]{
				JSONDescription: routeTargetJSONDescription,
				TextDescription: "Creation confirmation on stderr; no stdout in text mode.",
				Examples: []CommandExample{
					{
						Description: "Create a Model API Route.",
						CommandLines: []string{
							"baseten route create --team Engineering --name acme/assistant",
							"--target-model-api moonshotai/glm-5.3",
						},
					},
					{
						Description: "Create an external provider Route.",
						CommandLines: []string{
							"baseten route create --team Engineering --name acme/assistant",
							"--target-provider anthropic --target-provider-model claude-opus-5",
							"--target-provider-secret anthropic-key",
						},
					},
				},
				JQExample: CommandExample{
					Description: "Print the created Route ID.",
					CommandLines: []string{
						"baseten route create --team Engineering --name acme/assistant",
						"--target-model-api moonshotai/glm-5.3 --jq '.id'",
					},
				},
			},
		},
		{
			Name:    "update",
			Summary: "Update a Route (PRE-RELEASE)",
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
						Description: "Replace a Route's target.",
						Command:     "baseten route update --name acme/assistant --target-model-api moonshotai/glm-5.3",
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
			Summary: "Delete a Route (PRE-RELEASE)",
			Flags:   RouteDeleteFlags{},
			Description: routePrereleaseNotice +
				"Delete a Route by exactly one of --id or --name. Requires --yes to confirm deletion.",
			Output: &CommandOutput[managementapi.RouteTombstone]{
				TextDescription: "Confirmation on stderr; no stdout in text mode.",
				Examples: []CommandExample{
					{
						Description: "Delete a Route without prompting.",
						Command:     "baseten route delete --name acme/assistant --yes",
					},
				},
				JQExample: CommandExample{
					Description: "Print the deleted Route's ID.",
					Command:     "baseten route delete --id <id> --yes --jq '.id'",
				},
			},
		},
	},
}

type RouteRefFlags struct {
	ID   string `flag:"id" desc:"Stable Route ID." oneof:"route-ref"`
	Name string `flag:"name" desc:"Exact Route name, including its organization-owned prefix." oneof:"route-ref"`
}

type RouteTargetFlags struct {
	TargetModelAPI               string `flag:"target-model-api" desc:"Target Model API name. Mutually exclusive with provider target flags."`
	TargetProvider               string `flag:"target-provider" desc:"External provider; requires --target-provider-model and --target-provider-secret." enum:"anthropic,openai,xai,vertex,openai-compatible"`
	TargetProviderModel          string `flag:"target-provider-model" desc:"Model name sent to the external provider."`
	TargetProviderSecret         string `flag:"target-provider-secret" desc:"Name of an existing credential secret in the Route's team, never the secret value."`
	TargetProviderBaseURL        string `flag:"target-provider-base-url" desc:"HTTPS base URL. Required for and only valid with openai-compatible."`
	TargetProviderVertexProject  string `flag:"target-provider-vertex-project" desc:"Google Cloud project ID or number. Required for and only valid with vertex."`
	TargetProviderVertexLocation string `flag:"target-provider-vertex-location" desc:"Google Cloud location, such as global. Required for and only valid with vertex."`
}

type RouteListFlags struct {
	CommandFlags
	Team string `flag:"team" desc:"Filter by team name or ID."`
	Name string `flag:"name" desc:"Filter by exact Route name."`
}

type RouteDescribeFlags struct {
	CommandFlags
	RouteRefFlags
}

type RouteCreateFlags struct {
	CommandFlags
	RouteTargetFlags
	Name        string               `flag:"name" desc:"Globally unique Route name with an organization-owned prefix." required:"true"`
	Team        string               `flag:"team" desc:"Owning team name or ID (immutable)." required:"true"`
	DisplayName OptionalFlag[string] `flag:"display-name" desc:"Display label (1 to 255 characters). Defaults to the Route name."`
	Description OptionalFlag[string] `flag:"description" desc:"Optional Route description (up to 1000 characters)."`
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
	Yes bool `flag:"yes" desc:"Confirm deletion (required)."`
}
