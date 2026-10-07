package cmd

var commandAPI = Command{
	Name:    "api",
	Summary: "Make raw API requests",
	Description: "Make raw HTTP requests to Baseten management, inference, or sandbox APIs.\n\n" +
		"The HTTP method defaults to GET, or POST when --field, --raw-field, or --input is provided. " +
		"JSON responses are pretty-printed by default; non-JSON responses are streamed raw. " +
		"Use --jq to filter JSON responses.",
	Children: []Command{
		{
			Name:    "management",
			Summary: "Make management API requests",
			Description: "Make raw HTTP requests to the Baseten management API (api.baseten.co).\n\n" +
				"Paths are relative to /v1/, so 'baseten api management models' requests /v1/models.",
			ArgsUsage: "<api-path>",
			ExactArgs: 1,
			Flags:     APIManagementFlags{},
			Output: &CommandOutput[JSONUndefined]{
				TextDescription: "The HTTP response body, passed through verbatim. JSON responses are " +
					"pretty-printed; non-JSON responses are streamed raw to stdout.",
				JSONDescription: "Shape depends on the requested endpoint. See the management API " +
					"OpenAPI spec at https://api.baseten.co/v1/spec.",
				Examples: []CommandExample{
					{
						Description: "GET a management resource.",
						Command:     "baseten api management models",
					},
					{
						Description: "POST a management resource with fields.",
						Command:     "baseten api management models --field name=my-model",
					},
				},
				JQExample: CommandExample{
					Description: "List model IDs from /v1/models.",
					Command:     "baseten api management models --jq '.models[].id'",
				},
			},
		},
		{
			Name:    "inference",
			Summary: "Make inference API requests",
			Description: "Make raw HTTP requests to a Baseten inference endpoint.\n\n" +
				"Requires either --model-id or --chain-id to identify the target. " +
				"Use --environment to target a specific environment (e.g. production).",
			ArgsUsage: "<api-path>",
			ExactArgs: 1,
			Flags:     APIInferenceFlags{},
			Output: &CommandOutput[JSONUndefined]{
				TextDescription: "The inference endpoint's response body, passed through verbatim. JSON " +
					"responses are pretty-printed; non-JSON responses are streamed raw.",
				JSONDescription: "Shape depends on the model and endpoint. See the inference API " +
					"OpenAPI spec at https://api.baseten.co/inference-spec.",
				Examples: []CommandExample{
					{
						Description: "POST a predict body to a model.",
						Command:     `baseten api inference production/predict --model-id <model-id> --field prompt=hello`,
					},
				},
				JQExample: CommandExample{
					Description: "Filter a JSON predict response.",
					Command:     `baseten api inference production/predict --model-id <model-id> --field prompt=hello --jq '.result'`,
				},
			},
		},
		{
			Name:    "sandbox",
			Summary: "Make sandbox API requests (PRE-RELEASE)",
			Description: sandboxPreRelease +
				"Make raw HTTP requests to a sandbox's own API, which runs processes and manages files " +
				"inside the sandbox.\n\n" +
				"Paths are relative to --sandbox-url, the sandbox's URL as shown by 'baseten sandbox describe', " +
				"so 'baseten api sandbox process --sandbox-url <url>' requests <url>/process.",
			ArgsUsage: "<api-path>",
			ExactArgs: 1,
			Flags:     APISandboxFlags{},
			Output: &CommandOutput[JSONUndefined]{
				TextDescription: "The sandbox API's response body, passed through verbatim. JSON " +
					"responses are pretty-printed; non-JSON responses are streamed raw.",
				JSONDescription: "Shape depends on the requested endpoint.",
				Examples: []CommandExample{
					{
						Description: "List processes in a sandbox, looking up its URL by name.",
						Command:     `baseten api sandbox process --sandbox-url "$(baseten sandbox describe --name my-sandbox --jq .url)"`,
					},
				},
				JQExample: CommandExample{
					Description: "List the names of the files in a sandbox's /tmp directory.",
					Command:     `baseten api sandbox filesystem/tmp --sandbox-url <url> --jq '.files[].name'`,
				},
			},
		},
	},
}

// APIFlags are shared flags for raw API commands.
type APIFlags struct {
	CommandFlags
	Method   string   `flag:"method" short:"X" desc:"HTTP method, defaults to GET or POST if fields are provided"`
	Field    []string `flag:"field" short:"F" desc:"Add a string field (key=value), parsed as JSON value"`
	RawField []string `flag:"raw-field" short:"f" desc:"Add a raw string field (key=value)"`
	Header   []string `flag:"header" short:"H" desc:"Add a request header (key:value)"`
	Input    string   `flag:"input" desc:"Read request body from file (use - for stdin)"`
}

type APIManagementFlags struct {
	APIFlags
}

type APIInferenceFlags struct {
	APIFlags
	InferenceClientFlags
}

type APISandboxFlags struct {
	APIFlags
	SandboxURL string `flag:"sandbox-url" desc:"Sandbox URL to target, as shown by 'baseten sandbox describe'" required:"true"`
}
