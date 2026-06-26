package main

// This file defines the versioned JSON schema emitted for docs.baseten.co. The
// schema is the contract; bump SchemaVersion on any breaking change and
// coordinate with the consumer.

// SchemaVersion is the major version of the emitted JSON. Increment when an
// existing field is removed or its semantics change. Adding a new optional
// field does not require a bump.
const SchemaVersion = "1"

// Schema is the top-level document emitted by the walker.
type Schema struct {
	SchemaVersion string `json:"schema_version"`
	CLIVersion    string `json:"cli_version"`
	// GeneratedAt is the UTC RFC3339 timestamp this Schema was emitted at.
	GeneratedAt    string       `json:"generated_at"`
	StandardErrors []ErrorEntry `json:"standard_errors"`
	Root           Command      `json:"root"`
}

// Command is one node in the command tree. Non-leaf commands (with Children)
// have empty Flags/Examples/Output fields.
type Command struct {
	Name               string       `json:"name"`
	Path               []string     `json:"path"`
	Summary            string       `json:"summary"`
	Description        string       `json:"description"`
	IsLeaf             bool         `json:"is_leaf"`
	ArgsUsage          string       `json:"args_usage"`
	ExactArgs          int          `json:"exact_args"`
	MaxArgs            int          `json:"max_args"`
	DisableFlagParsing bool         `json:"disable_flag_parsing"`
	Flags              []Flag       `json:"flags"`
	Examples           []Example    `json:"examples"`
	JQExample          *Example     `json:"jq_example"`
	TextDescription    string       `json:"text_description"`
	JSONDescription    string       `json:"json_description"`
	JSONOutputType     string       `json:"json_output_type"`
	JSONArrayStreamed  bool         `json:"json_array_streamed"`
	Errors             []ErrorEntry `json:"errors"`
	Children           []Command    `json:"children"`
}

// Flag is one CLI flag on a leaf command.
type Flag struct {
	Name        string   `json:"name"`
	Short       string   `json:"short"`
	Description string   `json:"description"`
	Default     string   `json:"default"`
	Enum        []string `json:"enum"`
	Required    bool     `json:"required"`
	Oneof       string   `json:"oneof"`
	Type        string   `json:"type"`
	FieldName   string   `json:"field_name"`
	Group       string   `json:"group"`
	// GroupPri is the rendering priority for the flag's group (lower renders
	// earlier), copied verbatim from the source field's group-pri tag. A value
	// of 0 means the field set no group-pri; the consumer applies the framework
	// default (DefaultFlagGroupPri = 100). The walker does not resolve it.
	GroupPri int `json:"group_pri"`
}

// Example is one documented invocation of a command.
type Example struct {
	Description string `json:"description"`
	Command     string `json:"command"`
}

// ErrorEntry is one typed error a command may surface. Standard errors at the
// top level are inherited by every leaf; per-command Errors lists additions.
type ErrorEntry struct {
	Name    string `json:"name"`
	Code    int    `json:"code"`
	Meaning string `json:"meaning"`
}
