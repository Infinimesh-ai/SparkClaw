//go:build !windows

package browserautomation

import (
	"os/exec"
	"syscall"
)

func configureAdapterCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateAdapterProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if groupID, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		_ = syscall.Kill(-groupID, syscall.SIGKILL)
		return
	}
	_ = cmd.Process.Kill()
}
