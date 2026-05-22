package help

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"charm.land/fang/v2"
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/charmbracelet/colorprofile"
	mango "github.com/muesli/mango-cobra"
	"github.com/muesli/roff"
	"github.com/spf13/cobra"
)

// HelpOutputFlag is the name of the hidden persistent flag that, when set,
// appends the "Output" section to help text.
const HelpOutputFlag = "help-output"

// Options configures [Execute].
type Options struct {
	// Args is the argv passed to the root command (e.g. os.Args[1:]).
	Args []string
	// Version is the CLI version string shown in --version and the man page.
	Version string
	// Signals interrupt the run via context cancellation. Empty disables it.
	Signals []os.Signal
	// Tree is the declarative [cmd.Command] root. Used to resolve the cobra
	// command being help-rendered back to its declarative definition for the
	// --help-output section (exit codes, text description, JSON schema).
	Tree cmd.Command
}

// Execute applies our help rendering + manpage subcommand + signal handling
// to root and runs it. Replaces [fang.Execute]; we keep parity with the
// pieces we used (manpages, signal notify, version, error styling) but
// drop fang's own helpFn so we can render --help-output.
//
// --help-output implies --help: it triggers help-mode rendering and appends
// the Output section. Passed alone (without --help) we inject --help into
// the args so leaf commands surface help instead of running.
func Execute(ctx context.Context, root *cobra.Command, opts Options) error {
	args := opts.Args
	if hasFlag(args, HelpOutputFlag) && !hasFlag(args, "help") {
		args = append(args, "--help")
	}
	root.SetArgs(args)

	styles := makeStyles(mustColorscheme(fang.AnsiColorScheme))
	helpOutputSet := hasFlag(args, HelpOutputFlag)
	tree := opts.Tree
	helpFunc := func(c *cobra.Command, _ []string) {
		w := colorprofile.NewWriter(c.OutOrStdout(), os.Environ())
		segments := strings.Fields(c.CommandPath())
		def, _ := resolveCommand(tree, segments[1:])
		examples := composeExamples(def.Output)
		var extra string
		if helpOutputSet {
			extra = renderOutputSection(segments, tree, styles)
		}
		render(c, w, styles, examples, extra)
	}

	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SetHelpFunc(helpFunc)
	root.PersistentFlags().Bool(HelpOutputFlag, false, "Show help with appended Output section")
	if f := root.PersistentFlags().Lookup(HelpOutputFlag); f != nil {
		f.Hidden = true
	}

	if opts.Version != "" {
		root.Version = opts.Version
	}

	root.AddCommand(&cobra.Command{
		Use:                   "man",
		Short:                 "Generates manpages",
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		Hidden:                true,
		Args:                  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			page, err := mango.NewManPage(1, cmd.Root())
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(os.Stdout, page.Build(roff.NewDocument()))
			return err
		},
	})

	if len(opts.Signals) > 0 {
		var cancel context.CancelFunc
		ctx, cancel = signal.NotifyContext(ctx, opts.Signals...)
		defer cancel()
	}

	if err := root.ExecuteContext(ctx); err != nil {
		w := colorprofile.NewWriter(root.ErrOrStderr(), os.Environ())
		defaultErrorHandler(w, styles, err)
		return err
	}
	return nil
}

// hasFlag reports whether `--name` (or `--name=...`) appears in args. We
// can't rely on cobra-parsed flag state because help mode short-circuits
// before flag parsing finishes.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--"+name || strings.HasPrefix(a, "--"+name+"=") {
			return true
		}
	}
	return false
}
