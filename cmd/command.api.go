package cmd

var commandAPI = Command{
	Name:    "api",
	Summary: "Make raw API requests",
	Description: "Make raw HTTP requests to Baseten management or inference APIs.\n\n" +
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
	JQ       string   `flag:"jq" short:"q" desc:"Filter JSON response with a jq expression"`
}

type APIManagementFlags struct {
	APIFlags
}

type APIInferenceFlags struct {
	APIFlags
	InferenceClientFlags
}
