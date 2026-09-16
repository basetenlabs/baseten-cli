package harnesspoc

import (
	"fmt"
	"path/filepath"
)

// claudeCode configures the Claude Code CLI through its own CLAUDE_CONFIG_DIR.
// The config dir is what makes the run isolated: it relocates the credentials
// file, the history, the user settings, and the top-level state file, so the
// developer's own login is neither read nor written. The file written is the
// user-tier settings.json, which is the same tier the real product would write.
type claudeCode struct{}

func (claudeCode) Name() string { return "claude-code" }

func (c claudeCode) dir(root string) string { return filepath.Join(root, "claude") }

// claudeCodeSettings is the subset of Claude Code's settings.json we write.
type claudeCodeSettings struct {
	Env             map[string]string      `json:"env"`
	ModelPicker     *claudeCodeModelPicker `json:"modelPicker,omitempty"`
	AvailableModels []string               `json:"availableModels,omitempty"`
}

// claudeCodeModelPicker is the local model list: rows shown in /model, and
// whether they replace the built-in lineup or add to it.
type claudeCodeModelPicker struct {
	Options               []claudeCodeModelRow `json:"options"`
	ReplaceBuiltInOptions bool                 `json:"replaceBuiltInOptions,omitempty"`
}

// claudeCodeModelRow is one picker row.
type claudeCodeModelRow struct {
	Model       string `json:"model"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

func (c claudeCode) Setup(opts SetupOptions) (SetupResult, error) {
	first := opts.Catalog.Models[0].Slug
	settings := claudeCodeSettings{
		Env: map[string]string{
			"ANTHROPIC_BASE_URL":   opts.BaseURL,
			"ANTHROPIC_AUTH_TOKEN": opts.APIKey,
			// Claude Code reaches for built-in slugs for background work and
			// subagents regardless of what the picker offers, so both are
			// pinned at a model the gateway actually serves.
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": first,
			"CLAUDE_CODE_SUBAGENT_MODEL":    first,
		},
	}
	notes := []string{}

	switch opts.Mode {
	case ModeLocal:
		rows := make([]claudeCodeModelRow, 0, len(opts.Catalog.Models))
		slugs := make([]string, 0, len(opts.Catalog.Models))
		for _, m := range opts.Catalog.Models {
			rows = append(rows, claudeCodeModelRow{
				Model:       m.Slug,
				Label:       m.Label(),
				Description: m.Description,
			})
			slugs = append(slugs, m.Slug)
		}
		settings.ModelPicker = &claudeCodeModelPicker{
			Options:               rows,
			ReplaceBuiltInOptions: opts.ReplaceBuiltins,
		}
		settings.AvailableModels = slugs
		notes = append(notes,
			"availableModels is documented as a managed-settings field, so verify it is honored from a --settings file.")
	case ModeRemote:
		settings.Env["CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"] = "1"
		notes = append(notes,
			"Discovery appends to the built-in lineup and cannot replace it.",
			"Discovered IDs must contain 'claude' or 'anthropic', so plain route names never appear.")
	default:
		return SetupResult{}, fmt.Errorf("unsupported mode %q", opts.Mode)
	}

	path := filepath.Join(c.dir(opts.Root), "settings.json")
	if err := writeJSON(path, settings); err != nil {
		return SetupResult{}, err
	}
	return SetupResult{
		Harness: c.Name(),
		Paths:   []string{path},
		Launch:  fmt.Sprintf("CLAUDE_CONFIG_DIR=%s claude", c.dir(opts.Root)),
		Notes:   notes,
	}, nil
}
