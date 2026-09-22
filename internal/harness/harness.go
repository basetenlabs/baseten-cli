// Package harness configures native coding harnesses and restores their original settings.
package harness

import (
	"context"
	"fmt"
	"os"
)

const (
	ClaudeCode = "claude-code"
	Codex      = "codex"
	OpenCode   = "opencode"
	providerID = "baseten-harness"
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
	return []Harness{claudeHarness{baseHarness{ClaudeCode}}, codexHarness{baseHarness{Codex}}, openCodeHarness{baseHarness{OpenCode}}}
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

func (h baseHarness) Teardown(path string) ([]*Plan, error) {
	p, err := prepareTeardown(path)
	if err != nil {
		return nil, err
	}
	return []*Plan{p}, nil
}

// ApplyPlans checks the preview once before writing. There are no persistent
// locks: concurrent invocations are not supported. Catalogs precede configs on
// setup; teardown reverses that order. A failure leaves backups for recovery.
func ApplyPlans(plans []*Plan, token string) error {
	for _, p := range plans {
		if err := p.snapshot.check(); err != nil {
			return err
		}
		if err := p.journalSnapshot.check(); err != nil {
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

// Configured discovers backups even if the harness is no longer installed.
func (h baseHarness) Configured(path string) (bool, error) {
	target, err := resolveConfigPath(path)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(journalPath(target))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
