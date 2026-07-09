package cmd

import (
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// inlineCodeStyle renders a string like inline code in a markdown renderer:
// subtle foreground color, no bold. lipgloss / termenv handle NO_COLOR and
// non-TTY downgrade automatically.
var inlineCodeStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "63", Dark: "117"})

// linkStyle renders a hyperlink's visible text: the inline-code color plus an
// underline so links read as links. lipgloss handles NO_COLOR and non-TTY
// downgrade automatically.
var linkStyle = inlineCodeStyle.Underline(true)

// hyperlink renders url as an OSC 8 terminal hyperlink whose visible text is
// the url itself, so terminals that support it make the URL clickable and
// others simply show it. When w is not a terminal (piped or redirected), it
// returns url unchanged so scripts and greps see exactly the URL.
func hyperlink(w io.Writer, url string) string {
	if !isTerminalWriter(w) {
		return url
	}
	return ansi.SetHyperlink(url) + linkStyle.Render(url) + ansi.ResetHyperlink()
}

// isTerminalWriter reports whether w is a terminal, so escape sequences are
// only emitted when a terminal will interpret them.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(f.Fd())
}
