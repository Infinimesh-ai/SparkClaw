//go:build windows

package browserautomation

import "os/exec"

func configureDriverCommand(_ *exec.Cmd) {}

func terminateDriverProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
