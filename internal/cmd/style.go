package cmd

import "github.com/charmbracelet/lipgloss"

// inlineCodeStyle renders a string like inline code in a markdown renderer:
// subtle foreground color, no bold. lipgloss / termenv handle NO_COLOR and
// non-TTY downgrade automatically.
var inlineCodeStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "63", Dark: "117"})
