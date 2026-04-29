package cmd

import (
	"fmt"
	"reflect"
	"strings"
)

// Root is the top-level baseten command.
var Root = Command{
	Name:    "baseten",
	Summary: "Baseten CLI",
	Description: "Command-line interface for managing Baseten resources.\n\n" +
		"Authentication is via 'baseten auth login' or the BASETEN_API_KEY environment variable.",
	Children: []Command{
		commandAPI,
		commandAuth,
		commandTruss,
	},
}

// CommandFlags are shared flags that every command must embed, either directly
// or via another struct that embeds it.
type CommandFlags struct {
	Verbose bool   `flag:"verbose" short:"v" desc:"Enable verbose logging"`
	Output  string `flag:"output" short:"o" desc:"Output format" default:"text" enum:"text,json,jsonl,none"`
}

// Command defines a CLI command declaratively. The tree structure is built
// via Children. Flags is a struct value whose fields use struct tags
// to define CLI flags.
type Command struct {
	Name        string
	Summary     string
	Description string
	Flags       any // nil, or struct value with flag tags
	Children    []Command
	// ArgsUsage is appended to the command name in help output (e.g. "[path]").
	ArgsUsage string
	// ExactArgs requires exactly this many positional arguments. Mutually
	// exclusive with MaxArgs.
	ExactArgs int
	// MaxArgs is the maximum number of positional arguments. 0 means no args
	// (the default), -1 means unlimited. Mutually exclusive with ExactArgs.
	MaxArgs int
	// DisableFlagParsing disables all flag parsing; everything after the command
	// name is passed as raw args. MaxArgs is ignored and assumed -1.
	DisableFlagParsing bool
}

// LoadFlags parses the Flags struct tags and returns the flag metadata. Returns
// nil if Flags is nil.
func (c Command) LoadFlags() []CommandFlag {
	if c.Flags == nil {
		return nil
	}
	t := reflect.TypeOf(c.Flags)
	if !c.DisableFlagParsing {
		if _, ok := t.FieldByName("CommandFlags"); !ok {
			panic(fmt.Sprintf("flags for %q must embed CommandFlags", c.Name))
		}
	}
	return LoadFlagsFromType(t)
}

// LoadFlagsFromType parses flag metadata from a struct type's tags.
func LoadFlagsFromType(t reflect.Type) []CommandFlag {
	var flags []CommandFlag
	for i := range t.NumField() {
		field := t.Field(i)
		if field.Anonymous {
			flags = append(flags, LoadFlagsFromType(field.Type)...)
			continue
		}
		if f, ok := commandFlagFromField(field); ok {
			flags = append(flags, f)
		}
	}
	return flags
}

// CommandFlag describes a single CLI flag parsed from struct tags.
type CommandFlag struct {
	Name      string
	Short     string
	Desc      string
	Default   string
	Enum      []string
	Required  bool
	Type      reflect.Type
	FieldName string // Go struct field name
}

// InferenceClientFlags are the flags needed to target an inference endpoint.
// Embedded by any command that needs to create an inference client.
type InferenceClientFlags struct {
	ModelID     string `flag:"model-id" desc:"Model ID to target"`
	ChainID     string `flag:"chain-id" desc:"Chain ID to target"`
	Environment string `flag:"environment" desc:"Environment name (e.g. production)"`
}

func commandFlagFromField(field reflect.StructField) (CommandFlag, bool) {
	name := field.Tag.Get("flag")
	if name == "" {
		return CommandFlag{}, false
	}
	f := CommandFlag{
		Name:      name,
		Short:     field.Tag.Get("short"),
		Desc:      field.Tag.Get("desc"),
		Default:   field.Tag.Get("default"),
		Required:  field.Tag.Get("required") == "true",
		Type:      field.Type,
		FieldName: field.Name,
	}
	if enum := field.Tag.Get("enum"); enum != "" {
		f.Enum = strings.Split(enum, ",")
	}
	return f, true
}
