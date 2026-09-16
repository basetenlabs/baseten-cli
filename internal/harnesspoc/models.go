// Package harnesspoc backs `baseten harness-poc`, a proof of concept that
// points agentic harnesses at a local mock gateway to find out how far each
// one lets us control its model list. It is not part of the shipping CLI.
package harnesspoc

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Model is one entry in the models YAML. It stands in for what a route would
// carry: the name a caller sends and the label a picker shows.
type Model struct {
	// Slug is the model ID sent in the request body, standing in for a route
	// name.
	Slug string `yaml:"slug"`
	// DisplayName is the label a model picker shows. Defaults to the slug.
	DisplayName string `yaml:"display_name"`
	// Description is the secondary line a picker shows where it has room.
	Description string `yaml:"description"`
	// AnthropicFamilyTier maps the entry onto a Claude family for the
	// Anthropic harnesses that ask for one: haiku, sonnet, opus, fable, or
	// mythos.
	AnthropicFamilyTier string `yaml:"anthropic_family_tier"`

	ContextWindow   int `yaml:"context_window"`
	MaxOutputTokens int `yaml:"max_output_tokens"`
}

// Label returns the picker label for the model.
func (m Model) Label() string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return m.Slug
}

// Catalog is the parsed models YAML.
type Catalog struct {
	Models []Model `yaml:"models"`
}

// Resolve returns the model with the given slug. A harness sending anything
// else is the case we want to see, so the caller reports the miss rather than
// falling back.
func (c Catalog) Resolve(slug string) (Model, bool) {
	for _, m := range c.Models {
		if m.Slug == slug {
			return m, true
		}
	}
	return Model{}, false
}

// LoadCatalog reads and validates the models YAML at path.
func LoadCatalog(path string) (Catalog, error) {
	if path == "" {
		return Catalog{}, fmt.Errorf("--models is required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("reading models file: %w", err)
	}
	var catalog Catalog
	if err := yaml.Unmarshal(b, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("parsing models file: %w", err)
	}
	if len(catalog.Models) == 0 {
		return Catalog{}, fmt.Errorf("models file lists no models")
	}
	for i, m := range catalog.Models {
		if m.Slug == "" {
			return Catalog{}, fmt.Errorf("model %d has no slug", i)
		}
	}
	return catalog, nil
}
