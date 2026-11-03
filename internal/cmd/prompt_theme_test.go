package cmd

import (
	"strings"
	"testing"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// huh's default Charm theme paints option text ANSI-256 color 252 on light
// backgrounds, which is near-white on white. Prompt text must stay in the
// terminal's default foreground on either background.
func Test_PromptTheme_TextUsesTerminalForeground(t *testing.T) {
	for _, isDark := range []bool{false, true} {
		styles := promptTheme.Theme(isDark)
		for state, fs := range map[string]huh.FieldStyles{"focused": styles.Focused, "blurred": styles.Blurred} {
			for part, s := range map[string]lipgloss.Style{
				"title":             fs.Title,
				"description":       fs.Description,
				"option":            fs.Option,
				"selected option":   fs.SelectedOption,
				"unselected option": fs.UnselectedOption,
			} {
				require.Equal(t, lipgloss.NoColor{}, s.GetForeground(), "%s %s, dark=%v", state, part, isDark)
			}
		}
	}
}

// A field rendered before the terminal answers the background query is drawn
// as if on a light background; the options must not carry a foreground color.
func Test_PromptTheme_MultiSelectRendersWithoutForegroundColor(t *testing.T) {
	field := huh.NewMultiSelect[string]().
		Title("Select harnesses to configure").
		Options(huh.NewOption("claude-code", "claude-code"), huh.NewOption("codex", "codex")).
		WithTheme(promptTheme)
	view := field.View()
	require.Contains(t, view, "claude-code")
	require.False(t, strings.Contains(view, "\x1b[38;"), "unexpected foreground color in %q", view)
}
