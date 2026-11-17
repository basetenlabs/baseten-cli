//go:build e2e && windows

package e2etests

import (
	"os/exec"
)

func harnessProcessGroupSetup(*exec.Cmd) {}

func harnessProcessGroupKill(*exec.Cmd) {}
