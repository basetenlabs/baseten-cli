package help

import (
	"os"
	"strings"
	"unicode"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func mustColorscheme(cs fang.ColorSchemeFunc) fang.ColorScheme {
	var isDark bool
	if term.IsTerminal(os.Stdout.Fd()) {
		isDark = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	}
	return cs(lipgloss.LightDark(isDark))
}

func makeStyles(cs fang.ColorScheme) fang.Styles {
	return fang.Styles{
		Text: lipgloss.NewStyle().Foreground(cs.Base),
		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(cs.Title).
			Transform(strings.ToUpper).
			Padding(1, 0).
			Margin(0, 2),
		FlagDescription: lipgloss.NewStyle().
			Foreground(cs.Description).
			Transform(titleFirstWord),
		FlagDefault: lipgloss.NewStyle().Foreground(cs.FlagDefault),
		Codeblock: fang.Codeblock{
			Base: lipgloss.NewStyle().
				Background(cs.Codeblock).
				Foreground(cs.Base).
				MarginLeft(2).
				Padding(1, 2),
			Text:    lipgloss.NewStyle().Background(cs.Codeblock),
			Comment: lipgloss.NewStyle().Background(cs.Codeblock).Foreground(cs.Comment),
			Program: fang.Program{
				Name:           lipgloss.NewStyle().Background(cs.Codeblock).Foreground(cs.Program),
				Flag:           lipgloss.NewStyle().Background(cs.Codeblock).Foreground(cs.Flag),
				Argument:       lipgloss.NewStyle().Background(cs.Codeblock).Foreground(cs.Argument),
				DimmedArgument: lipgloss.NewStyle().Background(cs.Codeblock).Foreground(cs.DimmedArgument),
				Command:        lipgloss.NewStyle().Background(cs.Codeblock).Foreground(cs.Command),
				QuotedString:   lipgloss.NewStyle().Background(cs.Codeblock).Foreground(cs.QuotedString),
			},
		},
		Program: fang.Program{
			Name:           lipgloss.NewStyle().Foreground(cs.Program),
			Argument:       lipgloss.NewStyle().Foreground(cs.Argument),
			DimmedArgument: lipgloss.NewStyle().Foreground(cs.DimmedArgument),
			Flag:           lipgloss.NewStyle().Foreground(cs.Flag),
			Command:        lipgloss.NewStyle().Foreground(cs.Command),
			QuotedString:   lipgloss.NewStyle().Foreground(cs.QuotedString),
		},
		Span: lipgloss.NewStyle().Background(cs.Codeblock),
		ErrorText: lipgloss.NewStyle().
			MarginLeft(2).
			Width(termWidth() - 4).
			Transform(titleFirstWord),
		ErrorHeader: lipgloss.NewStyle().
			Foreground(cs.ErrorHeader[0]).
			Background(cs.ErrorHeader[1]).
			Bold(true).
			Padding(0, 1).
			Margin(1).
			MarginLeft(2).
			SetString("ERROR"),
	}
}

// titleFirstWord mirrors fang v2's whitespace-aware capitalization.
func titleFirstWord(s string) string {
	runes := []rune(s)
	start := 0
	for start < len(runes) && unicode.IsSpace(runes[start]) {
		start++
	}
	if start >= len(runes) {
		return s
	}
	end := start
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}
	firstWord := cases.Title(language.AmericanEnglish).String(string(runes[start:end]))
	return string(runes[:start]) + firstWord + string(runes[end:])
}
