package cmd

import (
	"time"

	"github.com/basetenlabs/baseten-go/client/managementapi"
)

var commandModelAPI = Command{
	Name:    "model-api",
	Summary: "Manage Model APIs",
	Description: "List and inspect Baseten Model APIs.\n\n" +
		"Authentication is via 'baseten auth login' or the BASETEN_API_KEY environment variable.",
	Children: []Command{
		{
			Name:        "describe",
			Summary:     "Describe a Model API",
			Description: "Describe a single Model API by name.",
			Flags:       ModelAPIDescribeFlags{},
			Output: &CommandOutput[managementapi.ModelAPI]{
				TextDescription: "Field-per-line summary of the Model API.",
				Examples: []CommandExample{
					{
						Description: "Describe a Model API by name.",
						Command:     "baseten model-api describe --model <name>",
					},
				},
				JQExample: CommandExample{
					Description: "Print the Model API's invoke URL.",
					Command:     "baseten model-api describe --model <name> --jq '.invoke_url'",
				},
			},
		},
		{
			Name:    "list",
			Summary: "List Model APIs",
			Description: "List the Model APIs in the full visible catalog.\n\n" +
				"Pass --added-only to restrict to just the Model APIs the workspace has added.",
			Flags: ModelAPIListFlags{},
			Output: &CommandOutput[ModelAPIList]{
				TextDescription: "Table with columns: NAME, CONTEXT, $/1M IN, $/1M OUT, ADDED. " +
					"When no Model APIs match, prints \"No Model APIs found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List the full visible catalog of Model APIs.",
						Command:     "baseten model-api list",
					},
					{
						Description: "List only the Model APIs the workspace has added.",
						Command:     "baseten model-api list --added-only",
					},
				},
				JQExample: CommandExample{
					Description: "Print just the Model API names.",
					Command:     "baseten model-api list --jq '.items[].name'",
				},
			},
		},
		{
			Name:    "predict",
			Summary: "Run an inference request against a Model API",
			Description: "POST an inference request to a Model API and write the response to " +
				"stdout.\n\n" +
				"The request is sent to --url, which defaults to the OpenAI chat-completions " +
				"endpoint on the shared inference host. Override it for other shapes (e.g. " +
				"/v1/messages, /v1/embeddings) or different hosts.\n\n" +
				"--content is the simple path: it builds an OpenAI chat-completions body with a " +
				"single user message and --model as the model, and prints just the assistant's " +
				"reply. It is only valid for OpenAI chat URLs and requires --model.\n\n" +
				"--data and --file send a request body verbatim, so any format the endpoint " +
				"accepts works (OpenAI, Anthropic, embeddings, custom). The response is written " +
				"as-is: JSON is pretty-printed, streams and binary bodies are passed through.",
			Flags: ModelAPIPredictFlags{},
			Output: &CommandOutput[JSONUndefined]{
				TextDescription: "With --content, the assistant message text. With --data/--file, the " +
					"response body as-is (pretty-printed JSON, or a raw stream/binary body).",
				JSONDescription: "Under --output json, --content emits the full chat-completions " +
					"response. For --data/--file, a streamed response becomes one JSON record per " +
					"chunk under --output jsonl, and a binary body is base64-encoded under a 'body' key.",
				Examples: []CommandExample{
					{
						Description: "Send a single user message.",
						Command:     `baseten model-api predict --model <name> --content "hello"`,
					},
					{
						Description: "Send a full OpenAI-shaped body and stream it as JSONL.",
						Command:     `baseten model-api predict --model <name> --data '{"model":"<name>","messages":[{"role":"user","content":"hi"}],"stream":true}' --output jsonl`,
					},
				},
				JQExample: CommandExample{
					Description: "Extract the assistant's message content.",
					Command:     `baseten model-api predict --model <name> --content "hi" --jq '.choices[0].message.content'`,
				},
			},
		},
		{
			Name:    "usage",
			Summary: "Show Model APIs token usage in time buckets",
			Description: "Show the workspace's Model APIs token usage as contiguous time buckets, " +
				"oldest first, broken down by the dimensions passed to --group-by.\n\n" +
				"Buckets with no usage are included, so the series has no gaps. Usage is retained " +
				"for 92 days; buckets older than that come back empty. Every bucket in the window " +
				"is fetched, paging as needed, until --limit buckets are collected.\n\n" +
				"Usage is attributed to a user when the request was authenticated with a personal " +
				"API key or an OAuth credential. Usage from workspace or other non-user-scoped " +
				"credentials has no user.\n\n" +
				"For machine-readable streaming, prefer --output jsonl over --output json.",
			Flags: ModelAPIUsageFlags{},
			Output: &CommandOutput[managementapi.ModelApisUsageBucket]{
				JSONArrayStreamed: true,
				TextDescription: "Table with a time column, one column per --group-by dimension, " +
					"then REQUESTS, INPUT, CACHED, and OUTPUT token counts, followed by an ALL " +
					"totals row. A bucket with no usage renders as a single \"(no usage)\" row. " +
					"When no bucket in the window has any usage, prints \"No usage in the selected " +
					"window.\" to stderr instead of a table.",
				JSONDescription: "One record per time bucket: its start_time, end_time, and the " +
					"per-dimension usage totals in results.",
				Examples: []CommandExample{
					{
						Description: "Show daily usage per model over the last 7 days.",
						Command:     "baseten model-api usage",
					},
					{
						Description: "Show which users drove usage over the last 3 days.",
						Command:     "baseten model-api usage --since 3d --group-by user",
					},
					{
						Description: "Break hourly usage down by user and model for one model.",
						Command:     "baseten model-api usage --since 12h --bucket-width 1h --group-by user --group-by model --model <name>",
					},
				},
				JQExample: CommandExample{
					Description: "Stream each bucket's per-user output tokens as a JSONL stream.",
					Command:     "baseten model-api usage --group-by user --output jsonl --jq '.results[] | {user_id, output_tokens}'",
				},
			},
		},
	},
}

// ModelAPIList is the JSON output of `baseten model-api list`: the Model APIs
// aggregated across all pages.
type ModelAPIList struct {
	Items []managementapi.ModelAPI `json:"items"`
}

// ModelAPIDescribeFlags configures `baseten model-api describe`.
type ModelAPIDescribeFlags struct {
	CommandFlags

	Model string `flag:"model" desc:"Name of the Model API to describe." required:"true"`
}

// ModelAPIListFlags configures `baseten model-api list`.
type ModelAPIListFlags struct {
	CommandFlags

	AddedOnly bool `flag:"added-only" desc:"Restrict to the Model APIs the workspace has added instead of the full visible catalog."`
}

// ModelAPIUsageFlags configures `baseten model-api usage`.
type ModelAPIUsageFlags struct {
	CommandFlags

	Start time.Time     `flag:"start" desc:"Start of the range, inclusive, snapped down to its bucket start. ISO 8601, local when no timezone is given."`
	End   time.Time     `flag:"end" desc:"End of the range, exclusive. ISO 8601, local when no timezone is given. Defaults to now."`
	Since time.Duration `flag:"since" desc:"Window from a relative time ago until now (e.g. '30m', '3d'). Mutually exclusive with --start and --end."`

	BucketWidth string   `flag:"bucket-width" desc:"Width of each time bucket. Also sets the default window: 7d for 1d, 24h for 1h, 60m for 1m." enum:"1m,1h,1d" default:"1d"`
	GroupBy     []string `flag:"group-by" desc:"Dimension to break usage down by. May be repeated. One of: api-key, user, model. Defaults to model."`

	APIKeyPrefixes []string `flag:"api-key-prefix" desc:"Only return usage for these API key prefixes. May be repeated."`
	UserIDs        []string `flag:"user-id" desc:"Only return usage attributed to these user IDs. May be repeated."`
	Models         []string `flag:"model" desc:"Only return usage for these models. May be repeated."`

	Limit int `flag:"limit" desc:"Maximum number of time buckets, paging as needed. Rows per bucket depend on --group-by. 0 for no limit."`

	// PageSize is the per-request fetch size while paging. Hidden; exists so
	// tests can force multiple pages without a full page of buckets. Zero uses
	// the backend's maximum for the bucket width.
	PageSize int `flag:"page-size" hidden:"true" desc:"Time buckets fetched per backend request while paging."`
}

// ModelAPIPredictFlags configures `baseten model-api predict`.
type ModelAPIPredictFlags struct {
	CommandFlags

	URL   string `flag:"url" desc:"Endpoint to POST the request to. Defaults to https://inference.baseten.co/v1/chat/completions."`
	Model string `flag:"model" desc:"Name of the Model API. Required with --content, where it sets the request's model." `

	Content string `flag:"content" desc:"Single user message; builds an OpenAI chat-completions request and prints the assistant's reply. Only valid for OpenAI chat URLs and requires --model." oneof:"predict-input"`
	Data    string `flag:"data" desc:"Inline request body, sent verbatim." oneof:"predict-input"`
	File    string `flag:"file" desc:"Path to a file containing the request body, sent verbatim. Use '-' for stdin." oneof:"predict-input"`
}
