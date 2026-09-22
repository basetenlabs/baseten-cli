package harness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Detection struct {
	Name      string `json:"harness"`
	Path      string `json:"config"`
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	Supported bool   `json:"supported"`
}

// Execer is satisfied by the CLI's shared subprocess executor.
type Execer interface {
	LookPath(string) (string, error)
	Exec(*exec.Cmd) error
}

func (h baseHarness) Detect(ctx context.Context, executor Execer, path string) (Detection, error) {
	name := h.Name()
	d := Detection{Name: name, Path: path}
	binaryName := name
	switch name {
	case ClaudeCode:
		binaryName = "claude"
	case codexName, openCodeName:
	default:
		return d, fmt.Errorf("unknown harness %s", name)
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return d, err
		}
		switch name {
		case ClaudeCode:
			dir := os.Getenv("CLAUDE_CONFIG_DIR")
			if dir == "" {
				dir = filepath.Join(home, ".claude")
			}
			d.Path = filepath.Join(dir, "settings.json")
		case codexName:
			dir := os.Getenv("CODEX_HOME")
			if dir == "" {
				dir = filepath.Join(home, ".codex")
			}
			d.Path = filepath.Join(dir, "config.toml")
		case openCodeName:
			dir := os.Getenv("XDG_CONFIG_HOME")
			if dir == "" {
				dir = filepath.Join(home, ".config")
			}
			d.Path = filepath.Join(dir, "opencode", "opencode.json")
			if _, err := os.Stat(d.Path + "c"); err == nil {
				d.Path += "c"
			} else if !os.IsNotExist(err) {
				return d, err
			}
		}
	}
	var err error
	d.Path, err = filepath.Abs(d.Path)
	if err != nil {
		return d, err
	}
	binary, err := executor.LookPath(binaryName)
	if err != nil {
		return d, nil
	}
	d.Installed = true
	// Version detection is informational; setup is currently supported on macOS.
	d.Supported = runtime.GOOS == "darwin"
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	command := exec.CommandContext(ctx, binary, "--version")
	command.Stdout = &out
	err = executor.Exec(command)
	if err != nil {
		return d, nil
	}
	v := strings.TrimSpace(out.String())
	if len(v) > 100 || strings.ContainsAny(v, "\r\n\x1b") {
		return d, nil
	}
	d.Version = strings.TrimPrefix(strings.TrimSuffix(v, " (Claude Code)"), "codex-cli ")
	return d, nil
}
