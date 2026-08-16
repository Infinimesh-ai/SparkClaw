//go:build !linux

package browserautomation

import "os/exec"

// The Ubuntu deployment baseline owns the real process-group semantics in
// process_linux.go. This fallback only keeps non-Linux development hosts
// (macOS CI-less laptops) compiling and able to run the portable test suite;
// it terminates the adapter process directly without group bookkeeping.
func configureAdapterCommand(*exec.Cmd) {}

func terminateAdapterProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
