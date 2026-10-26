package emaildiagnostics

import (
	"os"
	"testing"
)

var guardTestWorker func()

// The package test executable is also the private subprocess under test.
func TestMain(m *testing.M) {
	if guardTestWorker != nil {
		guardTestWorker()
	}
	if code, handled := RunLedgerGuard(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
