package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/help"
	"github.com/basetenlabs/baseten-go/client"
	"github.com/itchyny/gojq"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func init() {
	client.SetClientName("baseten-cli")
}

// GetAPIKey returns the Baseten API key from the BASETEN_API_KEY environment
// variable or an ErrUsage if not set.
func GetAPIKey() (string, error) {
	key := os.Getenv("BASETEN_API_KEY")
	if key == "" {
		return "", cmd.NewErrUsagef("BASETEN_API_KEY environment variable is required")
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
	root.Version = Version
	root.SetVersionTemplate("{{.Version}}\n")
	root.ResetCommands()
	for _, child := range cmd.Root.Children {
		root.AddCommand(buildCommand(child, "", &options))
	}
	return help.Execute(ctx, root, help.Options{
		Args:    options.Args,
		Version: Version,
		Signals: []os.Signal{os.Interrupt, syscall.SIGTERM},
		Tree:    cmd.Root,
	})
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
	c.InitDefaultHelpFlag()
	if f := c.Flags().Lookup("help"); f != nil {
		f.Usage += " (use --help-output for output details)"
		if f.Annotations == nil {
			f.Annotations = map[string][]string{}
		}
		f.Annotations[cmd.FlagAnnotationGroup] = []string{"common"}
		f.Annotations[cmd.FlagAnnotationGroupPri] = []string{"500"}
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
		if def.Output != nil {
			panic(fmt.Sprintf("command %q has children and must not have Output", path))
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
		if def.Output == nil {
			panic(fmt.Sprintf("command %q has no children and must have Output", path))
		}
		validateOutput(path, def)
		flagMetas := def.LoadFlags()
		// Create a fresh instance so repeated Build() calls don't share state.
		flagsPtr := reflect.New(reflect.TypeOf(def.Flags))
		flagsVal := flagsPtr.Elem()
		bindFlags(c.Flags(), flagsVal, flagMetas)
		applyOneofGroups(c, flagMetas)

		r := runners[path]
		streamed := def.Output != nil && def.Output.JSONArrayStreamedBool()
		c.RunE = func(_ *cobra.Command, args []string) error {
			var cmdFlags cmd.CommandFlags
			if f := flagsVal.FieldByName("CommandFlags"); f.IsValid() {
				cmdFlags = f.Interface().(cmd.CommandFlags)
			}
			remote, err := NewRemote(cmdFlags.RemoteURL)
			if err != nil {
				return err
			}
			ctx := &CommandContext{
				Context:      c.Context(),
				Command:      c,
				Args:         args,
				Stdin:        options.Stdin,
				Stdout:       options.Stdout,
				Stderr:       options.Stderr,
				ExitWithCode: options.ExitWithCode,
				Remote:       remote,
			}

			// Resolve --jq and --output, populating ctx.
			var runErr error
			if cmdFlags.JQ != "" {
				outputChanged := c.Flags().Changed("output")
				if outputChanged && (cmdFlags.Output == "text" || cmdFlags.Output == "none") {
					runErr = cmd.NewErrUsagef("--jq cannot be used with --output %s", cmdFlags.Output)
				} else {
					if !outputChanged {
						if streamed {
							cmdFlags.Output = "jsonl"
						} else {
							cmdFlags.Output = "json"
						}
					}
					q, parseErr := gojq.Parse(cmdFlags.JQ)
					if parseErr != nil {
						runErr = fmt.Errorf("invalid jq expression: %w", parseErr)
					} else {
						ctx.JQQuery = q
					}
				}
			}
			ctx.JSON = cmdFlags.Output == "json" || cmdFlags.Output == "jsonl"
			ctx.JSONCompact = cmdFlags.Output == "jsonl"
			ctx.JSONLines = cmdFlags.Output == "jsonl"
			ctx.verbose = cmdFlags.Verbose
			if cmdFlags.Output == "none" {
				ctx.Stdout = io.Discard
			}

			// Run the leaf. Runner-returned errors win over jqErr.
			if runErr == nil {
				results := reflect.ValueOf(r.fn).Call([]reflect.Value{
					reflect.ValueOf(ctx),
					flagsPtr,
				})
				if !results[0].IsNil() {
					runErr = results[0].Interface().(error)
				}
				if runErr == nil && ctx.jqErr != nil {
					runErr = ctx.jqErr
				}
			}
			if runErr == nil {
				return nil
			}

			// Render the error and set exit code.
			ce := normalizeError(runErr)
			c.SilenceErrors = true
			c.SilenceUsage = true
			fmt.Fprintln(options.Stderr, ce)
			if ce.ExitCode() == cmd.ExitUsage {
				_ = c.Usage()
			}
			ctx.ExitWithCode(int(ce.ExitCode()))
			return nil
		}
	}

	return c
}

// validateOutput panics if a leaf command's Output is malformed: missing
// examples, or a JQExample whose Command doesn't actually invoke --jq.
// Leaves with DisableFlagParsing are exempt from the JQExample requirement
// since --jq is not honored when the framework doesn't parse flags.
func validateOutput(path string, def cmd.Command) {
	if len(def.Output.ExampleList()) == 0 {
		panic(fmt.Sprintf("command %q Output requires at least one example", path))
	}
	if def.DisableFlagParsing {
		return
	}
	if def.Output.JQ().Command == "" {
		panic(fmt.Sprintf("command %q Output requires a JQExample", path))
	}
	if !strings.Contains(def.Output.JQ().Command, "--jq") {
		panic(fmt.Sprintf("command %q JQExample.Command must invoke --jq, got %q", path, def.Output.JQ().Command))
	}
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
		case *time.Time:
			flags.VarP(&friendlyTimeValue{value: ptr}, meta.Name, meta.Short, desc)
		case *time.Duration:
			flags.VarP(&friendlyDurationValue{value: ptr}, meta.Name, meta.Short, desc)
		default:
			panic(fmt.Sprintf("unsupported flag type %T for flag %q", ptr, meta.Name))
		}

		if meta.Required {
			cobra.MarkFlagRequired(flags, meta.Name)
		}
		f := flags.Lookup(meta.Name)
		if f != nil {
			if f.Annotations == nil {
				f.Annotations = map[string][]string{}
			}
			f.Annotations[cmd.FlagAnnotationGroup] = []string{meta.Group}
			if meta.GroupPri != 0 {
				f.Annotations[cmd.FlagAnnotationGroupPri] = []string{strconv.Itoa(meta.GroupPri)}
			}
		}
	}
}

// friendlyTimeValue implements pflag.Value for a time.Time accepting a few
// common ISO 8601 forms. Values without a timezone designator are parsed in
// the local timezone (matches ISO 8601's "no tz = local" rule and other
// CLI conventions like journalctl and docker logs); values with `Z` or an
// offset are parsed as given.
type friendlyTimeValue struct{ value *time.Time }

func (v *friendlyTimeValue) String() string {
	if v.value == nil || v.value.IsZero() {
		return ""
	}
	return v.value.Format(time.RFC3339)
}

func (v *friendlyTimeValue) Type() string { return "time" }

func (v *friendlyTimeValue) Set(s string) error {
	// ParseInLocation only applies the location when the layout has no
	// zone info, so RFC 3339 inputs (which require a designator) still
	// honor their explicit zone.
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			*v.value = t
			return nil
		}
	}
	return fmt.Errorf("invalid time %q: expected ISO 8601 (e.g. 2026-05-14, 2026-05-14T12:00:00, 2026-05-14T12:00:00Z)", s)
}

// friendlyDurationValue implements pflag.Value for a time.Duration that also
// accepts "<N>d" forms (Go's time.ParseDuration rejects 'd').
type friendlyDurationValue struct{ value *time.Duration }

func (v *friendlyDurationValue) String() string {
	if v.value == nil {
		return ""
	}
	return v.value.String()
}

func (v *friendlyDurationValue) Type() string { return "duration" }

func (v *friendlyDurationValue) Set(s string) error {
	if d, err := time.ParseDuration(s); err == nil {
		*v.value = d
		return nil
	}
	if strings.HasSuffix(s, "d") && len(s) > 1 {
		if days, err := strconv.ParseInt(s[:len(s)-1], 10, 64); err == nil {
			*v.value = time.Duration(days) * 24 * time.Hour
			return nil
		}
	}
	return fmt.Errorf("invalid duration %q: expected a Go duration (e.g. 30m, 1h30m) or <N>d (e.g. 3d)", s)
}

// applyOneofGroups wires `oneof:"<group>"`-tagged flags as mutually exclusive
// and one-required via Cobra. Empty group names are ignored.
func applyOneofGroups(c *cobra.Command, metas []cmd.CommandFlag) {
	groups := map[string][]string{}
	for _, m := range metas {
		if m.Oneof != "" {
			groups[m.Oneof] = append(groups[m.Oneof], m.Name)
		}
	}
	for _, names := range groups {
		c.MarkFlagsMutuallyExclusive(names...)
		c.MarkFlagsOneRequired(names...)
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
