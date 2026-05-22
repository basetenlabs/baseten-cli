package cmd

import "reflect"

// CommandOutputSpec is the type-erased view of a leaf command's [CommandOutput].
// Every leaf [Command] must declare an Output value implementing this interface
// (concretely a *[CommandOutput][JSONT] for some T).
type CommandOutputSpec interface {
	// JSONOutputType returns the Go type of the stdout payload under
	// --output json, used for JSON schema generation and documentation.
	JSONOutputType() reflect.Type
	// Text describes the human-readable (--output text) output. Free-form
	// prose; surfaced via --help-output.
	Text() string
	// JSON describes the --output json shape beyond what the JSON schema
	// already conveys (special cases, status semantics, etc.). May be empty.
	JSON() string
	// ExampleList returns the declared usage examples. Every leaf must
	// declare at least one.
	ExampleList() []CommandExample
	// JQ returns the required example invoking --jq.
	JQ() CommandExample
	// JSONArrayStreamedBool reports whether the command streams JSONT records:
	// --output json wraps them in a JSON array, --output jsonl emits one
	// record per line, and --jq applies per record.
	JSONArrayStreamedBool() bool
}

// CommandOutput declaratively documents a leaf command's output. JSONT is the
// Go type produced on stdout under --output json (and --output jsonl, one
// JSONT per line). JSONT is also rendered as a JSON schema in --help-output.
type CommandOutput[JSONT any] struct {
	// TextDescription describes the --output text format. Free-form prose.
	TextDescription string
	// JSONDescription is optional prose describing the --output json shape
	// beyond what the JSON schema already conveys (e.g. how status fields
	// behave, what --dry-run emits). Free-form.
	JSONDescription string
	// Examples documents how to invoke the command. At least one required.
	Examples []CommandExample
	// JQExample is a required example that uses --jq. Surfaced separately so
	// every command guarantees a JQ-using example for agents to copy.
	// Optional on commands with DisableFlagParsing, since --jq is not honored
	// when the framework does not parse flags.
	JQExample CommandExample
	// JSONArrayStreamed indicates the command streams JSONT records: under
	// --output json the records are wrapped in a JSON array, under
	// --output jsonl one record per line, and --jq applies per record.
	JSONArrayStreamed bool
}

// JSONAny is the JSONT for leaf commands whose stdout JSON is valid JSON but
// whose shape is user- or runtime-determined. --help-output renders it as
// "any JSON object" rather than a concrete schema.
type JSONAny = map[string]any

// JSONUndefined is the JSONT for leaf commands whose stdout is not guaranteed
// to be JSON at all (e.g. raw HTTP passthrough, binary, streamed model output).
// --help-output renders it as "output shape is undefined".
type JSONUndefined struct{}

// CommandExample documents one usage of a command.
type CommandExample struct {
	// Description is a one-line "what this does" preceding the command.
	Description string
	// Command is a literal shell line beginning with "baseten ...".
	Command string
}

func (*CommandOutput[JSONT]) JSONOutputType() reflect.Type    { return reflect.TypeFor[JSONT]() }
func (o *CommandOutput[JSONT]) Text() string                  { return o.TextDescription }
func (o *CommandOutput[JSONT]) JSON() string                  { return o.JSONDescription }
func (o *CommandOutput[JSONT]) ExampleList() []CommandExample { return o.Examples }
func (o *CommandOutput[JSONT]) JQ() CommandExample            { return o.JQExample }
func (o *CommandOutput[JSONT]) JSONArrayStreamedBool() bool   { return o.JSONArrayStreamed }
