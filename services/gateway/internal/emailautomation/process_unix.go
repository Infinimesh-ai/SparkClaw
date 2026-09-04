//go:build unix

package emailautomation

import (
	"os/exec"
	"syscall"
)

func configureScriptCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateScriptCommand(command *exec.Cmd) {
	if command == nil || command.Process == nil {
		return
	}
	if group, err := syscall.Getpgid(command.Process.Pid); err == nil {
		_ = syscall.Kill(-group, syscall.SIGKILL)
		return
	}
	_ = command.Process.Kill()
}
