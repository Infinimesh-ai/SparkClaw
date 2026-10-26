//go:build !linux

package emaildiagnostics

import (
	"context"
	"os/exec"
)

func ledgerCommand(ctx context.Context, node string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, node, "--no-warnings", "--input-type=module", "--eval", ledgerReaderSource+"\nawait runCLI();"), nil
}

func ledgerCommandFailure() string { return "ledger_reader_failed" }

// RunLedgerGuard handles the command's private Linux reader subprocess.
func RunLedgerGuard(args []string) (int, bool) { return 0, false }
