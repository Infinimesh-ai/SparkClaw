package agent

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func browserCommandFailureReason(outcome app.ToolOutcome, fallback string) string {
	for _, signal := range []app.OutcomeSignal{app.OutcomeSignalBrowserDNSResolutionFailed, app.OutcomeSignalBrowserNavigationFailed, app.OutcomeSignalBrowserCommandTimeout} {
		if containsOutcomeSignal(outcome.Signals, signal) {
			return string(signal)
		}
	}
	return fallback
}
