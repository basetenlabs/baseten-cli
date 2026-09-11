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
			Name:    "costs",
			Summary: "Show daily Model APIs costs",
			Description: "Show the workspace's Model APIs costs in USD as daily UTC buckets, " +
				"oldest first, grouped by model unless --group-by is set.\n\n" +
				"Defaults to the last 7 UTC calendar days, including today. --start is inclusive " +
				"and --end is exclusive; both accept YYYY-MM-DD dates in UTC. Use --since alone " +
				"to select whole days (e.g. 7d), including today. " +
				"The range cannot exceed 90 days. History starts on 2026-08-05 at 20:45 UTC, " +
				"so that first day is partial.\n\n" +
				"Costs preserve fractional cents and may differ from finalized invoice amounts. " +
				"Empty days are included. Every bucket is fetched, paging as needed, until " +
				"--limit buckets are collected. For streaming, use --output jsonl.",
			Flags: ModelAPICostsFlags{},
			Output: &CommandOutput[ModelAPICostBucket]{
				JSONArrayStreamed: true,
				TextDescription: "Table with DATE, the requested grouping dimensions, and SUBTOTAL (USD), " +
					"followed by an ALL totals row when there are multiple results. Empty days show " +
					"(no usage). If every day is empty, prints a message to stderr instead of a table.",
				JSONDescription: "One record per UTC day: date and results. Each subtotal is an exact " +
					"decimal string in USD; unavailable attribution remains null.",
				Examples: []CommandExample{
					{Description: "Show daily costs per model for the last 7 UTC days.", Command: "baseten model-api costs"},
					{Description: "Break costs down by user and model.", Command: "baseten model-api costs --since 7d --group-by user --group-by model"},
					{Description: "Show costs for an explicit UTC date range.", Command: "baseten model-api costs --start 2026-09-01 --end 2026-09-08"},
				},
				JQExample: CommandExample{
					Description: "Stream each day's per-model dollar costs.",
					Command:     "baseten model-api costs --output jsonl --jq '.results[] | {model, subtotal}'",
				},
			},
		},
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
				TextDescription: "Table with columns: NAME, CONTEXT, $/1M IN, $/1M OUT, RELEASED. " +
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
						CommandLines: []string{
							`baseten model-api predict --model <name>`,
							`--data '{"model":"<name>","messages":[{"role":"user","content":"hi"}],"stream":true}' --output jsonl`,
						},
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

// ModelAPICostsFlags configures `baseten model-api costs`.
type ModelAPICostsFlags struct {
	CommandFlags

	Start string        `flag:"start" desc:"Inclusive UTC date, YYYY-MM-DD. Defaults to 7 days before --end."`
	End   string        `flag:"end" desc:"Exclusive UTC date, YYYY-MM-DD. Defaults to tomorrow UTC to include today."`
	Since time.Duration `flag:"since" desc:"Number of whole UTC days, including today (e.g. 7d). Mutually exclusive with --start and --end."`

	GroupBy        []string `flag:"group-by" desc:"Dimension to break costs down by: api-key, user, model, service-tier. May be repeated. Defaults to model."`
	APIKeyPrefixes []string `flag:"api-key-prefix" desc:"Only return costs for these API key prefixes. May be repeated."`
	UserIDs        []string `flag:"user-id" desc:"Only return costs attributed to these user IDs. May be repeated."`
	Models         []string `flag:"model" desc:"Only return costs for these models. May be repeated."`
	ServiceTiers   []string `flag:"service-tier" desc:"Only return costs for these service tiers. May be repeated."`
	Limit          int      `flag:"limit" desc:"Maximum number of daily buckets, paging as needed. 0 for no limit."`
}

// ModelAPICostBucket is one UTC day's Model API costs.
type ModelAPICostBucket struct {
	Date    string               `json:"date" jsonschema:"format=date"`
	Results []ModelAPICostResult `json:"results"`
}

// ModelAPICostResult is a cost subtotal for one combination of dimensions.
type ModelAPICostResult struct {
	APIKeyPrefixes []string `json:"api_key_prefixes" jsonschema:"nullable"`
	UserID         *string  `json:"user_id" jsonschema:"nullable"`
	Model          *string  `json:"model" jsonschema:"nullable"`
	ServiceTier    *string  `json:"service_tier" jsonschema:"nullable"`
	Subtotal       string   `json:"subtotal"`
}

// ModelAPIPredictFlags configures `baseten model-api predict`.
type ModelAPIPredictFlags struct {
	CommandFlags

	URL   string `flag:"url" desc:"Endpoint to POST the request to. Defaults to https://inference.baseten.co/v1/chat/completions."`
	Model string `flag:"model" desc:"Name of the Model API. Required with --content, where it sets the request's model." `

	Content string `flag:"content" desc:"Single user message; builds an OpenAI chat-completions request and prints the assistant's reply. Only valid for OpenAI chat URLs and requires --model." oneof:"predict-input"`
	Data    string `flag:"data" desc:"Inline request body, sent verbatim. The accepted shapes are documented at https://docs.baseten.co/inference/model-apis/overview." oneof:"predict-input"`
	File    string `flag:"file" desc:"Path to a file containing the request body, sent verbatim. Use '-' for stdin." oneof:"predict-input"`
}
