//go:build e2e && unix

package e2etests

import (
	"os/exec"
	"syscall"
)

func harnessProcessGroupSetup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func harnessProcessGroupKill(command *exec.Cmd) {
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}
