package trace

import "regexp"

var diagnosticRunID = regexp.MustCompile(`^run_[0-9a-f]{16}$`)

// DiagnosticRunID returns only a generated opaque Agent run ID. Other
// caller-provided identities may contain names or private text. An absent
// diagnostic ID remains incomplete, never an invented execution.
func DiagnosticRunID(id string) string {
	if diagnosticRunID.MatchString(id) {
		return id
	}
	return ""
}
