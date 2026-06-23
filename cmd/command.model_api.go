package cmd

import "github.com/basetenlabs/baseten-go/client/managementapi"

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
			Summary: "List Model APIs the workspace has added",
			Description: "List the Model APIs the workspace has added.\n\n" +
				"Pass --all to browse the full visible catalog instead of just the added ones.",
			Flags: ModelAPIListFlags{},
			Output: &CommandOutput[ModelAPIList]{
				TextDescription: "Table with columns: NAME, CONTEXT, $/1M IN, $/1M OUT, ADDED. " +
					"When no Model APIs match, prints \"No Model APIs found.\" to stderr.",
				Examples: []CommandExample{
					{
						Description: "List the Model APIs the workspace has added.",
						Command:     "baseten model-api list",
					},
					{
						Description: "Browse the full visible catalog.",
						Command:     "baseten model-api list --all",
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

	Model string `flag:"model" desc:"Name of the Model API to fetch." required:"true"`
}

// ModelAPIListFlags configures `baseten model-api list`.
type ModelAPIListFlags struct {
	CommandFlags

	All bool `flag:"all" desc:"Browse the full visible catalog instead of only the Model APIs the workspace has added."`
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
