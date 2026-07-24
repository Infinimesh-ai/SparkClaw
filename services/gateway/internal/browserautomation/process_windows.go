//go:build windows

package browserautomation

import "os/exec"

func configureAdapterCommand(_ *exec.Cmd) {}

func terminateAdapterProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
