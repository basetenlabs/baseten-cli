package cmd

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Flag group defaults and pflag annotation keys.
const (
	// DefaultFlagGroup is the group name applied to a flag with no `group:` tag.
	DefaultFlagGroup = "command"
	// DefaultFlagGroupPri is the rendering priority for a group whose flags
	// declare no `group-pri:` tag.
	DefaultFlagGroupPri = 100
	// FlagAnnotationGroup names the pflag.Flag annotation key that carries the
	// declarative `group` tag onto the bound flag, where the help renderer reads it.
	FlagAnnotationGroup = "baseten/group"
	// FlagAnnotationGroupPri names the pflag.Flag annotation key that carries the
	// declarative `group-pri` tag onto the bound flag, where the help renderer reads it.
	FlagAnnotationGroupPri = "baseten/group-pri"
)

// Root is the top-level baseten command.
var Root = Command{
	Name:    "baseten",
	Summary: "Baseten CLI",
	Description: "Command-line interface for managing Baseten resources.\n\n" +
		"Authentication is via 'baseten auth login' or the BASETEN_API_KEY environment variable.\n\n" +
		"The CLI never prompts interactively when stdin is not a terminal; commands that need " +
		"input must be supplied via flags or stdin redirection or they fail fast.",
	Children: []Command{
		commandAPI,
		commandAuth,
		commandLoops,
		commandModel,
		commandModelAPI,
		commandOrg,
		commandSSH,
		commandTrain,
		commandTruss,
		commandVersion,
		commandWhoami,
	},
}

// CommandFlags are shared flags that every command must embed, either directly
// or via another struct that embeds it.
type CommandFlags struct {
	Verbose bool   `flag:"verbose" short:"v" desc:"Enable verbose logging" group:"common" group-pri:"500"`
	Output  string `flag:"output" short:"o" desc:"Output format" default:"text" enum:"text,json,jsonl,none" group:"common"`
	JQ      string `flag:"jq" short:"q" desc:"Filter JSON output with a jq expression; implies --output json (or jsonl for streamed commands)" group:"common"`
	Profile string `flag:"profile" desc:"Use a specific stored profile for this command, overriding BASETEN_PROFILE and the current profile" group:"common"`
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
	// Output declares the leaf command's stdout shape, text-mode behavior, and
	// examples. Required on every leaf (a command with no Children); must be
	// nil on commands with Children. Typically a *[CommandOutput][T].
	Output CommandOutputSpec
	// Errors lists command-declared typed errors. Each entry is built via
	// [ErrorDescOf] and documents one extra exit code surfaced by this leaf
	// beyond the standard set. Rendered in --help-output.
	Errors []ErrorDesc
	// Hidden keeps the command out of help listings and completion. The command
	// is still fully runnable.
	Hidden bool
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
	for field := range t.Fields() {
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
	Name     string
	Short    string
	Desc     string
	Default  string
	Enum     []string
	Required bool
	Hidden   bool   // omitted from help output; still parsed and settable
	Oneof    string // group name: exactly one flag in the group must be set
	// Nullable allows an [OptionalFlag] to be passed as 'null'.
	Nullable  bool
	Type      reflect.Type
	FieldName string // Go struct field name
	// Group is the help-output flag-section bucket. Empty in raw metadata; the
	// loader fills in [DefaultFlagGroup] when no `group:` tag is set.
	Group string
	// GroupPri is the rendering priority declared via `group-pri:`. 0 means
	// "unset on this field". The whole group resolves to [DefaultFlagGroupPri]
	// when no field in the group declares one. Lower values render earlier.
	GroupPri int
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
		Hidden:    field.Tag.Get("hidden") == "true",
		Oneof:     field.Tag.Get("oneof"),
		Nullable:  field.Tag.Get("nullable") == "true",
		Type:      field.Type,
		FieldName: field.Name,
	}
	if enum := field.Tag.Get("enum"); enum != "" {
		f.Enum = strings.Split(enum, ",")
	}
	f.Group = field.Tag.Get("group")
	if f.Group == "" {
		f.Group = DefaultFlagGroup
	}
	if pri := field.Tag.Get("group-pri"); pri != "" {
		n, err := strconv.Atoi(pri)
		if err != nil {
			panic(fmt.Sprintf("flag %q has invalid group-pri %q: %v", name, pri, err))
		}
		f.GroupPri = n
	}
	return f, true
}

// NullFlagValue is the literal a caller passes to a `nullable:"true"` flag to
// send an explicit null, clearing a setting rather than leaving it unchanged.
const NullFlagValue = "null"

// OptionalFlag is a flag that can be left unpassed, keeping `--min-replica 0`
// distinguishable from an omitted flag. The settings endpoints patch only the
// fields the request body carries, so that distinction decides whether a
// setting is changed at all.
//
// Adding `nullable:"true"` lets the flag also be passed as [NullFlagValue].
// Optionality and nullability are separate concerns: an OptionalFlag without
// the tag can be omitted but not nulled.
//
// The state is behind methods rather than exported fields so binding cannot
// leave it inconsistent, and so a value type carries no pointer to patch.
type OptionalFlag[T any] struct {
	set   bool
	null  bool
	value T
}

// SetValue records that the flag was passed with a value. Called by flag binding.
func (o *OptionalFlag[T]) SetValue(value T) {
	o.set, o.null, o.value = true, false, value
}

// SetNull records that the flag was passed as null. Called by flag binding.
func (o *OptionalFlag[T]) SetNull() {
	var zero T
	o.set, o.null, o.value = true, true, zero
}

// IsSet reports whether the flag was passed at all, as a value or as null.
func (o OptionalFlag[T]) IsSet() bool { return o.set }

// IsNull reports whether the flag was passed as null.
func (o OptionalFlag[T]) IsNull() bool { return o.null }

// Pointer returns the value, or nil when the flag was omitted or passed as
// null. Both cases marshal away, so a caller that has already rejected null can
// use this directly as a request field.
func (o OptionalFlag[T]) Pointer() *T {
	if !o.set || o.null {
		return nil
	}
	value := o.value
	return &value
}
