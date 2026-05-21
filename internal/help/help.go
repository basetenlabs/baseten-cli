// Package help renders our CLI's help output and provides the Execute
// wrapper around cobra used by [internal/cmd.Execute]. The styling functions
// are adapted from charmbracelet/fang (MIT) because fang does not export its
// helpFn / makeStyles. See https://github.com/charmbracelet/fang.
package help

import (
	"cmp"
	"fmt"
	"io"
	"iter"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	minSpace = 10
	shortPad = 2
	longPad  = 4
)

var termWidth = sync.OnceValue(func() int {
	if s := os.Getenv("__FANG_TEST_WIDTH"); s != "" {
		w, _ := strconv.Atoi(s)
		return w
	}
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		return 120
	}
	return min(w, 120)
})

// render writes the help text for a cobra command. exampleText is the raw
// newline-joined examples block (composed lazily by the caller from the
// resolved [cmd.Command]). extra, when non-empty, is appended after the flags
// block as the "Output" section.
func render(c *cobra.Command, w *colorprofile.Writer, styles fang.Styles, exampleText, extra string) {
	writeLongShort(w, styles, cmp.Or(c.Long, c.Short))
	usage := styleUsage(c, styles.Codeblock.Program, true)
	examples := styleExamples(c, exampleText, styles)

	padding := styles.Codeblock.Base.GetHorizontalPadding()
	blockWidth := lipgloss.Width(usage)
	for _, ex := range examples {
		blockWidth = max(blockWidth, lipgloss.Width(ex))
	}
	blockWidth = min(termWidth()-padding, blockWidth+padding)
	blockStyle := styles.Codeblock.Base.Width(blockWidth)

	if w.Profile <= colorprofile.Ascii || reflect.DeepEqual(blockStyle.GetBackground(), lipgloss.NoColor{}) {
		blockStyle = blockStyle.PaddingTop(0).PaddingBottom(0)
	}

	fmt.Fprintln(w, styles.Title.Render("usage"))
	fmt.Fprintln(w, blockStyle.Render(usage))
	if len(examples) > 0 {
		cw := blockStyle.GetWidth() - blockStyle.GetHorizontalPadding()
		fmt.Fprintln(w, styles.Title.Render("examples"))
		for i, example := range examples {
			if lipgloss.Width(example) > cw {
				examples[i] = ansi.Truncate(example, cw, "…")
			}
		}
		fmt.Fprintln(w, blockStyle.Render(strings.Join(examples, "\n")))
	}

	groups, groupKeys := evalGroups(c)
	cmds, cmdKeys := evalCmds(c, styles)
	flagGroups := evalFlagGroups(c, styles)
	var allFlagKeys []string
	for _, g := range flagGroups {
		allFlagKeys = append(allFlagKeys, g.keys...)
	}
	space := calculateSpace(cmdKeys, allFlagKeys)

	for _, groupID := range groupKeys {
		group := cmds[groupID]
		if len(group) == 0 {
			continue
		}
		renderGroup(w, styles, space, groups[groupID], func(yield func(string, string) bool) {
			for _, k := range cmdKeys {
				cmds, ok := group[k]
				if !ok {
					continue
				}
				if !yield(k, cmds) {
					return
				}
			}
		})
	}

	for _, g := range flagGroups {
		if len(g.keys) == 0 {
			continue
		}
		renderGroup(w, styles, space, g.name+" flags", func(yield func(string, string) bool) {
			for _, k := range g.keys {
				if !yield(k, g.flags[k]) {
					return
				}
			}
		})
	}

	if extra != "" {
		fmt.Fprint(w, extra)
		fmt.Fprintln(w)
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, lipgloss.NewStyle().PaddingLeft(shortPad).Render(
		"Pass --help-output for the command's text/JSON output shape and exit codes."))
	fmt.Fprintln(w)
}

// defaultErrorHandler mirrors fang's default error handler styling.
func defaultErrorHandler(w io.Writer, styles fang.Styles, err error) {
	if w, ok := w.(term.File); ok {
		if !term.IsTerminal(w.Fd()) {
			fmt.Fprintln(w, err.Error())
			return
		}
	}
	fmt.Fprintln(w, styles.ErrorHeader.String())
	fmt.Fprintln(w, styles.ErrorText.Render(err.Error()+"."))
	fmt.Fprintln(w)
	if isUsageError(err) {
		fmt.Fprintln(w, lipgloss.JoinHorizontal(
			lipgloss.Left,
			styles.ErrorText.UnsetWidth().Render("Try"),
			styles.Program.Flag.Render(" --help "),
			styles.ErrorText.UnsetWidth().UnsetMargins().UnsetTransform().Render("for usage."),
		))
		fmt.Fprintln(w)
	}
}

func isUsageError(err error) bool {
	s := err.Error()
	for _, prefix := range []string{
		"flag needs an argument:",
		"unknown flag:",
		"unknown shorthand flag:",
		"unknown command",
		"invalid argument",
	} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func writeLongShort(w *colorprofile.Writer, styles fang.Styles, longShort string) {
	if longShort == "" {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, styles.Text.Width(termWidth()).PaddingLeft(shortPad).Render(longShort))
}

var otherArgsRe = regexp.MustCompile(`(\[.*\])`)

func styleUsage(c *cobra.Command, styles fang.Program, complete bool) string {
	u := c.Use
	if complete {
		u = c.UseLine()
	}
	hasArgs := strings.Contains(u, "[args]")
	hasFlags := strings.Contains(u, "[flags]") || strings.Contains(u, "[--flags]") || c.HasFlags() || c.HasPersistentFlags() || c.HasAvailableFlags()
	hasCommands := strings.Contains(u, "[command]") || c.HasAvailableSubCommands()
	for _, k := range []string{"[args]", "[flags]", "[--flags]", "[command]"} {
		u = strings.ReplaceAll(u, k, "")
	}
	var otherArgs []string
	for _, arg := range otherArgsRe.FindAllString(u, -1) {
		u = strings.ReplaceAll(u, arg, "")
		otherArgs = append(otherArgs, arg)
	}
	u = strings.TrimSpace(u)

	useLine := []string{}
	if complete {
		parts := strings.Fields(u)
		useLine = append(useLine, styles.Name.Render(parts[0]))
		if len(parts) > 1 {
			useLine = append(useLine, styles.Command.Render(" "+strings.Join(parts[1:], " ")))
		}
	} else {
		useLine = append(useLine, styles.Command.Render(u))
	}
	if hasCommands {
		useLine = append(useLine, styles.DimmedArgument.Render(" [command]"))
	}
	if hasArgs {
		useLine = append(useLine, styles.DimmedArgument.Render(" [args]"))
	}
	for _, arg := range otherArgs {
		useLine = append(useLine, styles.DimmedArgument.Render(" "+arg))
	}
	if hasFlags {
		useLine = append(useLine, styles.DimmedArgument.Render(" [--flags]"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, useLine...)
}

func styleExamples(c *cobra.Command, raw string, styles fang.Styles) []string {
	if raw == "" {
		return nil
	}
	usage := []string{}
	examples := strings.Split(raw, "\n")
	var indent bool
	for i, line := range examples {
		line = strings.TrimSpace(line)
		if (i == 0 || i == len(examples)-1) && line == "" {
			continue
		}
		s := styleExample(c, line, indent, styles.Codeblock)
		usage = append(usage, s)
		indent = len(line) > 1 && (line[len(line)-1] == '\\' || line[len(line)-1] == '|')
	}
	return usage
}

func styleExample(c *cobra.Command, line string, indent bool, styles fang.Codeblock) string {
	if strings.HasPrefix(line, "# ") {
		return lipgloss.JoinHorizontal(lipgloss.Left, styles.Comment.Render(line))
	}
	var isQuotedString bool
	var foundProgramName bool
	var isRedirecting bool
	programName := c.Root().Name()
	args := strings.Fields(line)
	var cleanArgs []string
	for i, arg := range args {
		isQuoteStart := arg[0] == '"' || arg[0] == '\''
		isQuoteEnd := arg[len(arg)-1] == '"' || arg[len(arg)-1] == '\''
		isFlag := arg[0] == '-'
		switch i {
		case 0:
			args[i] = ""
			if indent {
				args[i] = styles.Program.DimmedArgument.Render("  ")
				indent = false
			}
		default:
			args[i] = styles.Program.DimmedArgument.Render(" ")
		}
		if isRedirecting {
			args[i] += styles.Program.DimmedArgument.Render(arg)
			isRedirecting = false
			continue
		}
		switch arg {
		case "\\":
			if i == len(args)-1 {
				args[i] += styles.Program.DimmedArgument.Render(arg)
				continue
			}
		case "|", "||", "-", "&", "&&":
			args[i] += styles.Program.DimmedArgument.Render(arg)
			continue
		}
		if isRedirect(arg) {
			args[i] += styles.Program.DimmedArgument.Render(arg)
			isRedirecting = true
			continue
		}
		if !foundProgramName {
			if isQuotedString {
				args[i] += styles.Program.QuotedString.Render(arg)
				isQuotedString = !isQuoteEnd
				continue
			}
			if left, right, ok := strings.Cut(arg, "="); ok {
				args[i] += styles.Program.Flag.Render(left + "=")
				if right[0] == '"' {
					isQuotedString = true
					args[i] += styles.Program.QuotedString.Render(right)
					continue
				}
				args[i] += styles.Program.Argument.Render(right)
				continue
			}
			if arg == programName || slices.Contains(c.Root().Aliases, arg) {
				args[i] += styles.Program.Name.Render(arg)
				foundProgramName = true
				continue
			}
		}
		if !isQuoteStart && !isQuotedString && !isFlag {
			cleanArgs = append(cleanArgs, arg)
		}
		if !isQuoteStart && !isFlag && isSubCommand(c, cleanArgs, arg) {
			args[i] += styles.Program.Command.Render(arg)
			continue
		}
		isQuotedString = isQuotedString || isQuoteStart
		if isQuotedString {
			args[i] += styles.Program.QuotedString.Render(arg)
			isQuotedString = !isQuoteEnd
			continue
		}
		if isFlag {
			name, value, ok := strings.Cut(arg, "=")
			if ok {
				args[i] += lipgloss.JoinHorizontal(lipgloss.Left,
					styles.Program.Flag.Render(name+"="),
					styles.Program.Argument.Render(value))
				continue
			}
			args[i] += lipgloss.JoinHorizontal(lipgloss.Left, styles.Program.Flag.Render(name))
			continue
		}
		args[i] += styles.Program.Argument.Render(arg)
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, args...)
}

type flagGroup struct {
	name  string
	pri   int
	keys  []string
	flags map[string]string
}

// evalFlagGroups buckets visible flags by their `baseten/group` annotation
// (defaulting to [cmd.DefaultFlagGroup]) and orders the buckets by
// `baseten/group-pri` ascending, then by first-appearance for ties. Flags
// within a bucket follow declaration order from the underlying pflag set.
func evalFlagGroups(c *cobra.Command, styles fang.Styles) []*flagGroup {
	c.Flags().SortFlags = false
	groups := map[string]*flagGroup{}
	var order []string
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		name := cmd.DefaultFlagGroup
		if vs, ok := f.Annotations[cmd.FlagAnnotationGroup]; ok && len(vs) > 0 {
			name = vs[0]
		}
		pri := cmd.DefaultFlagGroupPri
		if vs, ok := f.Annotations[cmd.FlagAnnotationGroupPri]; ok && len(vs) > 0 {
			if n, err := strconv.Atoi(vs[0]); err == nil {
				pri = n
			}
		}
		g, ok := groups[name]
		if !ok {
			g = &flagGroup{name: name, pri: pri, flags: map[string]string{}}
			groups[name] = g
			order = append(order, name)
		}
		key, help := styleFlagEntry(f, styles)
		g.keys = append(g.keys, key)
		g.flags[key] = help
	})
	out := make([]*flagGroup, 0, len(order))
	for _, n := range order {
		out = append(out, groups[n])
	}
	slices.SortStableFunc(out, func(a, b *flagGroup) int { return a.pri - b.pri })
	return out
}

func styleFlagEntry(f *pflag.Flag, styles fang.Styles) (key, help string) {
	var parts []string
	if f.Shorthand == "" {
		parts = append(parts, styles.Program.Flag.Render("--"+f.Name))
	} else {
		parts = append(parts, styles.Program.Flag.Render("-"+f.Shorthand+" --"+f.Name))
	}
	key = lipgloss.JoinHorizontal(lipgloss.Left, parts...)
	noTransform := styles.FlagDescription.UnsetTransform()
	var helpLines []string
	for i, line := range strings.Split(f.Usage, "\n") {
		if line == "" {
			helpLines = append(helpLines, "")
			continue
		}
		if i > 0 {
			helpLines = append(helpLines, noTransform.Render(line))
			continue
		}
		helpLines = append(helpLines, styles.FlagDescription.Render(line))
	}
	help = strings.Join(helpLines, "\n")
	if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "[]" {
		help += styles.FlagDefault.Render(" (" + f.DefValue + ")")
	}
	return key, help
}

func evalCmds(c *cobra.Command, styles fang.Styles) (map[string]map[string]string, []string) {
	padStyle := lipgloss.NewStyle().PaddingLeft(0)
	keys := []string{}
	cmds := map[string]map[string]string{}
	for _, sc := range c.Commands() {
		if sc.Hidden {
			continue
		}
		if _, ok := cmds[sc.GroupID]; !ok {
			cmds[sc.GroupID] = map[string]string{}
		}
		key := padStyle.Render(styleUsage(sc, styles.Program, false))
		help := styles.FlagDescription.Render(sc.Short)
		cmds[sc.GroupID][key] = help
		keys = append(keys, key)
	}
	return cmds, keys
}

func evalGroups(c *cobra.Command) (map[string]string, []string) {
	ids := []string{""}
	groups := map[string]string{"": "commands"}
	for _, g := range c.Groups() {
		groups[g.ID] = g.Title
		ids = append(ids, g.ID)
	}
	return groups, ids
}

func renderGroup(w io.Writer, styles fang.Styles, space int, name string, items iter.Seq2[string, string]) {
	fmt.Fprintln(w, styles.Title.Render(name))
	for key, help := range items {
		fmt.Fprintln(w, lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().PaddingLeft(longPad).Render(key),
			strings.Repeat(" ", space-lipgloss.Width(key)),
			help,
		))
	}
}

func calculateSpace(k1, k2 []string) int {
	const spaceBetween = 2
	space := minSpace
	for _, k := range append(k1, k2...) {
		space = max(space, lipgloss.Width(k)+spaceBetween)
	}
	return space
}

func isSubCommand(c *cobra.Command, args []string, word string) bool {
	cmd, _, _ := c.Root().Traverse(args)
	return cmd != nil && cmd.Name() == word || slices.Contains(cmd.Aliases, word)
}

var redirectPrefixes = []string{">", "<", "&>", "2>", "1>", ">>", "2>>"}

func isRedirect(s string) bool {
	for _, p := range redirectPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
