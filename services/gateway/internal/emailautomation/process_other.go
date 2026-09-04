//go:build !unix

package emailautomation

import "os/exec"

func configureScriptCommand(*exec.Cmd) {}

func terminateScriptCommand(command *exec.Cmd) {
	if command != nil && command.Process != nil {
		_ = command.Process.Kill()
	}
}
