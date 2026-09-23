// Package harness configures native coding harnesses and removes its integration settings.
package harness

import (
	"context"
	"fmt"
)

const (
	ClaudeCode   = "claude-code"
	codexName    = "codex"
	openCodeName = "opencode"
	providerID   = "baseten-harness"
)

// Harness owns the configuration behavior of one native integration. API access,
// user interaction, and output formatting belong to the calling command.
type Harness interface {
	Name() string
	Configured(string) (bool, error)
	Detect(context.Context, Execer, string) (Detection, error)
	Prepare(string, []Route, Selection, string, string) ([]*Plan, error)
	Inspect(Detection) (Status, error)
	Teardown(string) ([]*Plan, error)
	SmallTaskModel(Selection) string
}

func All() []Harness {
	return []Harness{claudeHarness{baseHarness{ClaudeCode}}, codexHarness{baseHarness{codexName}}, openCodeHarness{baseHarness{openCodeName}}}
}

func Find(name string) (Harness, error) {
	for _, h := range All() {
		if h.Name() == name {
			return h, nil
		}
	}
	return nil, fmt.Errorf("unknown harness %q", name)
}

type baseHarness struct{ name string }

func (h baseHarness) Name() string { return h.name }

func (h baseHarness) SmallTaskModel(Selection) string { return "" }

// ApplyPlans checks the preview once before writing. There are no persistent
// locks: concurrent invocations are not supported. Catalogs precede configs on
// setup; teardown reverses that order. Rerun an interrupted operation to finish it.
func ApplyPlans(plans []*Plan, token string) error {
	for _, p := range plans {
		if err := p.snapshot.check(); err != nil {
			return err
		}
		if !p.teardown {
			if err := p.setCredential(token); err != nil {
				return err
			}
		}
	}
	for _, p := range plans {
		if err := p.apply(); err != nil {
			return err
		}
	}
	return nil
}

// Configured discovers integrations from native settings, even without an executable.
func (h baseHarness) Configured(path string) (bool, error) {
	adapter, err := Find(h.name)
	if err != nil {
		return false, err
	}
	status, err := adapter.Inspect(Detection{Name: h.name, Path: path})
	return status.State != "not-configured", err
}
