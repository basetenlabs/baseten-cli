package cmd

import "time"

const routePrereleaseNotice = "Pre-release. These commands and their API may change.\n\n"

var commandRoute = Command{
	Name:        "route",
	Summary:     "Manage Routes (pre-release)",
	Description: routePrereleaseNotice + "Manage Routes through the unstable management API. Route slugs are globally unique and must use an organization-owned prefix. Slugs and team ownership are immutable. In a terminal, missing required inputs are prompted; supplied flags skip their prompts.",
	Children: []Command{
		{
			Name: "list", Summary: "List Routes (pre-release)", Flags: RouteListFlags{},
			Description: routePrereleaseNotice + "List Routes you can invoke, newest first. Returns one page by default; use --cursor to continue or --all to fetch all remaining pages. Cursor filters must match the original request.",
			Output: &CommandOutput[RouteList]{
				TextDescription: "Table with ID, SLUG, DISPLAY NAME, TEAM ID, and TARGET. A continuation cursor or empty-list message is written to stderr.",
				Examples:        []CommandExample{{Description: "List all visible Routes.", Command: "baseten route list --all"}, {Description: "Filter by team and exact slug.", Command: "baseten route list --team Engineering --name acme/assistant"}},
				JQExample:       CommandExample{Description: "Print Route slugs.", Command: "baseten route list --all --jq '.items[].name'"},
			},
		},
		{
			Name: "describe", Summary: "Describe a Route (pre-release)", Flags: RouteDescribeFlags{},
			Description: routePrereleaseNotice + "Describe a Route by exactly one of --id or --name (an exact match). Omit both in a terminal to choose from your visible Routes.",
			Output: &CommandOutput[Route]{
				TextDescription: "Field-per-line Route summary, including the full target, invoke URL, and team name when accessible.",
				Examples:        []CommandExample{{Description: "Describe a Route by slug.", Command: "baseten route describe --name acme/assistant"}},
				JQExample:       CommandExample{Description: "Print the invoke URL.", Command: "baseten route describe --id <id> --jq '.invoke_url'"},
			},
		},
		{
			Name: "create", Summary: "Create a Route (pre-release)", Flags: RouteCreateFlags{},
			Description: routePrereleaseNotice + "Create a Route with a team, slug, and exactly one target head: --target-model-api or --target-provider. Provider targets require a model and a credential secret belonging to the owning team. Missing inputs are prompted in a terminal, including a team dropdown and optional display name and description.",
			Output: &CommandOutput[Route]{
				TextDescription: "Field-per-line summary of the created Route.",
				Examples: []CommandExample{
					{Description: "Create a Model API Route.", CommandLines: []string{"baseten route create --team Engineering --name acme/assistant", "--target-model-api moonshotai/glm-5.3"}},
					{Description: "Create an external provider Route.", CommandLines: []string{"baseten route create --team Engineering --name acme/assistant", "--target-provider anthropic --target-provider-model claude-opus-5", "--target-provider-secret anthropic-key"}},
				},
				JQExample: CommandExample{Description: "Print the created Route ID.", CommandLines: []string{"baseten route create --team Engineering --name acme/assistant", "--target-model-api moonshotai/glm-5.3 --jq '.id'"}},
			},
		},
		{
			Name: "update", Summary: "Update a Route (pre-release)", Flags: RouteUpdateFlags{},
			Description: routePrereleaseNotice + "Select exactly one of --id or --name. Set --display-name, --description, a complete target, or a combination. A target replaces the entire previous target; omitted fields are not merged. Slug and team cannot be changed. In a terminal, omit the selector to pick a Route; omit changes to choose what to update.",
			Output: &CommandOutput[Route]{
				TextDescription: "Field-per-line summary of the updated Route.",
				Examples:        []CommandExample{{Description: "Replace a Route's target.", Command: "baseten route update --name acme/assistant --target-model-api moonshotai/glm-5.3"}},
				JQExample:       CommandExample{Description: "Change the display label and print it.", Command: "baseten route update --id <id> --display-name Assistant --jq '.display_name'"},
			},
		},
		{
			Name: "delete", Summary: "Delete a Route (pre-release)", Flags: RouteDeleteFlags{},
			Description: routePrereleaseNotice + "Delete a Route by exactly one of --id or --name. Prompts for confirmation unless --yes is passed. Omit the selector in a terminal to pick a Route. Noninteractive calls require a selector and --yes.",
			Output: &CommandOutput[RouteTombstone]{
				TextDescription: "Confirmation on stderr; no stdout in text mode.",
				Examples:        []CommandExample{{Description: "Delete a Route without prompting.", Command: "baseten route delete --name acme/assistant --yes"}},
				JQExample:       CommandExample{Description: "Print the deleted Route's ID.", Command: "baseten route delete --id <id> --yes --jq '.id'"},
			},
		},
	},
}

// Route types mirror the backend management contract at c10f76e76799201c2036d4acedd021c1a29acb29.
// Replace these with SDK types when the generated management client includes Routes.
type Route struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	TeamID      string      `json:"team_id"`
	DisplayName string      `json:"display_name"`
	Description string      `json:"description"`
	Target      RouteTarget `json:"target"`
	InvokeURL   string      `json:"invoke_url"`
	CreatedAt   time.Time   `json:"created_at"`
}

// RouteTarget is discriminated by Type: BASETEN_MODEL_API uses ModelAPI; external
// provider types use Model, SecretName, and provider-specific configuration.
type RouteTarget struct {
	Type         string             `json:"type" jsonschema:"enum=BASETEN_MODEL_API,enum=ANTHROPIC,enum=OPENAI,enum=XAI,enum=VERTEX,enum=OPENAI_COMPATIBLE"`
	ModelAPI     string             `json:"model_api,omitempty"`
	Model        string             `json:"model,omitempty"`
	SecretName   string             `json:"secret_name,omitempty"`
	BaseURL      *string            `json:"base_url,omitempty"`
	VertexConfig *RouteVertexConfig `json:"vertex_config,omitempty"`
}

type RouteVertexConfig struct {
	ProjectID string `json:"project_id"`
	Location  string `json:"location"`
}

type RouteList struct {
	Items      []Route         `json:"items"`
	Pagination RoutePagination `json:"pagination"`
}

type RoutePagination struct {
	HasMore bool    `json:"has_more"`
	Cursor  *string `json:"cursor" jsonschema:"nullable"`
}

type RouteTombstone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type RouteRefFlags struct {
	ID   string `flag:"id" desc:"Stable Route ID."`
	Name string `flag:"name" desc:"Exact Route slug, including its organization-owned prefix."`
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
	Team   string `flag:"team" desc:"Filter by team name or ID."`
	Name   string `flag:"name" desc:"Filter by exact Route slug."`
	Limit  int    `flag:"limit" desc:"Items per request (1 to 1000)." default:"100"`
	Cursor string `flag:"cursor" desc:"Opaque continuation cursor from a previous page."`
	All    bool   `flag:"all" desc:"Fetch all remaining pages, starting at --cursor if supplied."`
}

type RouteDescribeFlags struct {
	CommandFlags
	RouteRefFlags
}

type RouteCreateFlags struct {
	CommandFlags
	RouteTargetFlags
	Name        string               `flag:"name" desc:"Globally unique Route slug with an organization-owned prefix."`
	Team        string               `flag:"team" desc:"Owning team name or ID (immutable)."`
	DisplayName OptionalFlag[string] `flag:"display-name" desc:"Display label (1 to 255 characters). Defaults to the Route slug."`
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
	Yes bool `flag:"yes" desc:"Skip confirmation. Required when stdin is not a terminal."`
}
