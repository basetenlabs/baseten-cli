package cmd

import (
	"reflect"
	"strings"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/spf13/pflag"
)

func init() {
	Register("truss", commandTruss)
}

func commandTruss(ctx *CommandContext, flags *cmd.TrussPassthroughFlags) error {
	args, err := trussExtractFlags(&flags.TrussAuthFlags, ctx.Args)
	if err != nil {
		return err
	}
	// With nothing left to forward, show this command's help rather than truss's
	// bare usage: it is the only place the --truss-* flags are documented, since
	// --help is forwarded to truss like any other argument.
	if len(args) == 0 {
		return ctx.Command.Help()
	}
	return trussRun(ctx, trussInvocation{
		Flags:       flags.TrussFlags,
		Args:        args,
		ForwardAuth: !flags.TrussNoForwardAuth,
	})
}

// trussExtractFlags pulls this CLI's own --truss-* flags out of raw arguments,
// wherever they appear, and returns the rest untouched for forwarding to truss.
// Flag parsing is disabled for `baseten truss`, so this stands in for Cobra:
// pflag's unknown-flag whitelist looks like the built-in answer but consumes the
// token after an unknown flag as its value, which would silently drop truss's
// own arguments.
//
// The flag set is built from the same struct tags Cobra binds elsewhere, so the
// names, types, and defaults cannot drift from the declared flags.
func trussExtractFlags(flags *cmd.TrussAuthFlags, args []string) ([]string, error) {
	fs := pflag.NewFlagSet("truss", pflag.ContinueOnError)
	val := reflect.ValueOf(flags).Elem()
	bindFlags(fs, val, cmd.LoadFlagsFromType(val.Type()))

	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			rest = append(rest, arg)
			continue
		}
		name, inline, hasInline := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		f := fs.Lookup(name)
		if f == nil {
			rest = append(rest, arg)
			continue
		}

		value := inline
		if !hasInline {
			switch {
			case f.Value.Type() == "bool":
				value = "true"
			// A following flag is a missing value, not the value: no truss
			// version or executable starts with a dash, and consuming one would
			// silently drop an argument meant for truss.
			case i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
				i++
				value = args[i]
			default:
				return nil, cmd.NewErrUsagef("flag --%s needs a value", name)
			}
		}
		if err := f.Value.Set(value); err != nil {
			return nil, cmd.NewErrUsagef("invalid value %q for --%s: %v", value, name, err)
		}
	}
	return rest, nil
}
