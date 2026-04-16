package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ErrWithCode is an error that carries a specific process exit code.
type ErrWithCode struct {
	Err  error
	Code int
}

func (e *ErrWithCode) Error() string { return e.Err.Error() }
func (e *ErrWithCode) Unwrap() error { return e.Err }

// ErrUsage is an error that signals Cobra should display usage alongside the
// error message.
type ErrUsage struct {
	Err error
}

func (e *ErrUsage) Error() string { return e.Err.Error() }
func (e *ErrUsage) Unwrap() error { return e.Err }

// GetAPIKey returns the Baseten API key from the BASETEN_API_KEY environment
// variable or an ErrUsage if not set.
func GetAPIKey() (string, error) {
	key := os.Getenv("BASETEN_API_KEY")
	if key == "" {
		return "", &ErrUsage{Err: fmt.Errorf("BASETEN_API_KEY environment variable is required")}
	}
	return key, nil
}

// runner is a registered run function for a command.
type runner struct {
	// func(*CommandContext, *T) error stored as any
	fn       any
	flagType reflect.Type
}

var runners = map[string]runner{}

// Register associates a run function with a command path (e.g. "api management").
// Panics if the path is already registered.
func Register[T any](path string, fn func(*CommandContext, *T) error) {
	if _, ok := runners[path]; ok {
		panic(fmt.Sprintf("runner already registered for %q", path))
	}
	runners[path] = runner{
		fn:       fn,
		flagType: reflect.TypeOf((*T)(nil)).Elem(),
	}
}

// ExecuteOptions configures command execution.
type ExecuteOptions struct {
	Args         []string
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	ExitWithCode func(int)
}

func (o *ExecuteOptions) applyDefaults() {
	if o.Args == nil {
		o.Args = os.Args[1:]
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.ExitWithCode == nil {
		o.ExitWithCode = os.Exit
	}
}

// Execute builds the Cobra command tree and runs it.
func Execute(ctx context.Context, options ExecuteOptions) error {
	options.applyDefaults()
	root := buildCommand(cmd.Root, "", &options)
	root.SetOut(options.Stdout)
	root.SetErr(options.Stderr)
	root.SetArgs(options.Args)
	root.ResetCommands()
	for _, child := range cmd.Root.Children {
		root.AddCommand(buildCommand(child, "", &options))
	}
	return root.ExecuteContext(ctx)
}

func buildCommand(def cmd.Command, parentPath string, options *ExecuteOptions) *cobra.Command {
	path := def.Name
	if parentPath != "" {
		path = parentPath + " " + def.Name
	}

	use := def.Name
	if def.ArgsUsage != "" {
		use += " " + def.ArgsUsage
	}

	c := &cobra.Command{
		Use:   use,
		Short: def.Summary,
		Long:  def.Description,
	}

	if def.DisableFlagParsing {
		c.DisableFlagParsing = true
		c.Args = cobra.ArbitraryArgs
	} else if def.ExactArgs > 0 {
		c.Args = cobra.ExactArgs(def.ExactArgs)
	} else {
		switch def.MaxArgs {
		case -1:
			c.Args = cobra.ArbitraryArgs
		case 0:
			c.Args = cobra.NoArgs
		default:
			c.Args = cobra.MaximumNArgs(def.MaxArgs)
		}
	}

	if len(def.Children) > 0 {
		if def.Flags != nil {
			panic(fmt.Sprintf("command %q has children and must not have Flags", path))
		}
		c.Args = cobra.ArbitraryArgs
		c.RunE = func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				return c.Help()
			}
			return fmt.Errorf("unknown command %q for %q", args[0], c.CommandPath())
		}
		for _, child := range def.Children {
			c.AddCommand(buildCommand(child, path, options))
		}

		// Hoist shared flags: if all children share an embedded struct,
		// add those flags to the parent so they appear in its help output.
		// These are regular flags (not persistent) because each child binds
		// its own copy — persistent flags would be shadowed and unused.
		if shared := sharedEmbeddedTypes(def.Children); len(shared) > 0 {
			for _, t := range shared {
				bindFlags(c.Flags(), reflect.New(t).Elem(), cmd.LoadFlagsFromType(t))
			}
		}
	} else if def.Flags == nil {
		panic(fmt.Sprintf("command %q has no children and must have Flags", path))
	} else {
		flagMetas := def.LoadFlags()
		// Create a fresh instance so repeated Build() calls don't share state.
		flagsPtr := reflect.New(reflect.TypeOf(def.Flags))
		flagsVal := flagsPtr.Elem()
		bindFlags(c.Flags(), flagsVal, flagMetas)

		r := runners[path]
		c.RunE = func(_ *cobra.Command, args []string) error {
			ctx := &CommandContext{
				Context:      c.Context(),
				Command:      c,
				Args:         args,
				Stdin:        options.Stdin,
				Stdout:       options.Stdout,
				Stderr:       options.Stderr,
				ExitWithCode: options.ExitWithCode,
			}
			if f := flagsVal.FieldByName("CommandFlags"); f.IsValid() {
				cmdFlags := f.Interface().(cmd.CommandFlags)
				ctx.JSON = cmdFlags.Output == "json" || cmdFlags.Output == "jsonl"
				ctx.JSONCompact = cmdFlags.Output == "jsonl"
				ctx.JSONLines = cmdFlags.Output == "jsonl"
				ctx.verbose = cmdFlags.Verbose
				if cmdFlags.Output == "none" {
					ctx.Stdout = io.Discard
				}
			}
			results := reflect.ValueOf(r.fn).Call([]reflect.Value{
				reflect.ValueOf(ctx),
				flagsPtr,
			})
			if results[0].IsNil() {
				return nil
			}
			err := results[0].Interface().(error)
			if e, ok := err.(*ErrUsage); ok {
				return e.Err
			}
			code := 1
			if e, ok := err.(*ErrWithCode); ok {
				code = e.Code
			}
			fmt.Fprintln(options.Stderr, err)
			c.SilenceErrors = true
			c.SilenceUsage = true
			ctx.ExitWithCode(code)
			return nil
		}
	}

	return c
}

// VerifyRunners panics if any command with Flags is missing a registered
// runner, or if a runner's flag type doesn't match.
func VerifyRunners() {
	for _, child := range cmd.Root.Children {
		verifyRunners(child, "")
	}
}

func verifyRunners(def cmd.Command, parentPath string) {
	path := def.Name
	if parentPath != "" {
		path = parentPath + " " + def.Name
	}

	if len(def.Children) > 0 {
		for _, child := range def.Children {
			verifyRunners(child, path)
		}
	} else {
		r, ok := runners[path]
		if !ok {
			panic(fmt.Sprintf("no runner registered for command %q", path))
		}
		expected := reflect.TypeOf(def.Flags)
		if r.flagType != expected {
			panic(fmt.Sprintf("runner for %q has flag type %v, expected %v", path, r.flagType, expected))
		}
	}
}

// bindFlags adds cobra flags for each CommandFlag, binding to the struct fields.
func bindFlags(flags *pflag.FlagSet, val reflect.Value, metas []cmd.CommandFlag) {
	for _, meta := range metas {
		desc := meta.Desc
		if len(meta.Enum) > 0 {
			desc += " {" + strings.Join(meta.Enum, ",") + "}"
		}

		ptr := val.FieldByName(meta.FieldName).Addr().Interface()
		switch ptr := ptr.(type) {
		case *string:
			if len(meta.Enum) > 0 {
				*ptr = meta.Default
				flags.VarPF(&enumValue{value: ptr, allowed: meta.Enum}, meta.Name, meta.Short, desc)
			} else {
				flags.StringVarP(ptr, meta.Name, meta.Short, meta.Default, desc)
			}
		case *int:
			v := 0
			if meta.Default != "" {
				fmt.Sscanf(meta.Default, "%d", &v)
			}
			flags.IntVarP(ptr, meta.Name, meta.Short, v, desc)
		case *bool:
			flags.BoolVarP(ptr, meta.Name, meta.Short, meta.Default == "true", desc)
		case *[]string:
			flags.StringArrayVarP(ptr, meta.Name, meta.Short, nil, desc)
		default:
			panic(fmt.Sprintf("unsupported flag type %T for flag %q", ptr, meta.Name))
		}

		if meta.Required {
			cobra.MarkFlagRequired(flags, meta.Name)
		}
	}
}

// enumValue implements pflag.Value for enum-constrained string flags.
type enumValue struct {
	value   *string
	allowed []string
}

func (e *enumValue) String() string { return *e.value }
func (e *enumValue) Type() string   { return "string" }

func (e *enumValue) Set(v string) error {
	for _, a := range e.allowed {
		if v == a {
			*e.value = v
			return nil
		}
	}
	return fmt.Errorf("must be one of: %s", strings.Join(e.allowed, ", "))
}

// sharedEmbeddedTypes returns embedded struct types that appear in all
// children's Flags structs.
func sharedEmbeddedTypes(children []cmd.Command) []reflect.Type {
	if len(children) == 0 {
		return nil
	}

	counts := map[reflect.Type]int{}
	flaggedChildren := 0
	for _, child := range children {
		if child.Flags == nil {
			continue
		}
		flaggedChildren++
		t := reflect.TypeOf(child.Flags)
		for i := range t.NumField() {
			if t.Field(i).Anonymous {
				counts[t.Field(i).Type]++
			}
		}
	}

	if flaggedChildren == 0 {
		return nil
	}

	var shared []reflect.Type
	for t, count := range counts {
		if count == flaggedChildren {
			shared = append(shared, t)
		}
	}
	return shared
}
