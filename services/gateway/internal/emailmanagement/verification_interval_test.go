package emailmanagement

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestVerificationIntervalRequiresQualifiedWatchingProvider(t *testing.T) {
	s := &Service{opts: Options{ScanInterval: time.Minute, QualifiedWakeProviders: map[string]bool{app.EmailProviderQQMail: true}}}
	box := app.EmailMailbox{Provider: app.EmailProviderQQMail, WatchState: "watching", WakeAdapterQualified: true}
	if got := s.verificationInterval(box); got != 5*time.Minute {
		t.Fatalf("qualified quiet interval = %s", got)
	}
	for _, change := range []func(*app.EmailMailbox){
		func(box *app.EmailMailbox) { box.WatchState = "degraded" },
		func(box *app.EmailMailbox) { box.WakeAdapterQualified = false },
		func(box *app.EmailMailbox) { box.Provider = app.EmailProviderGmail },
	} {
		candidate := box
		change(&candidate)
		if got := s.verificationInterval(candidate); got != time.Minute {
			t.Fatalf("unqualified or degraded interval = %s", got)
		}
	}
	box.LastPeriodicOnlyDiscovery = time.Now().UTC()
	for streak, want := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		box.PeriodicEmptyStreak = streak
		if got := s.verificationInterval(box); got != want {
			t.Fatalf("recovery streak %d interval = %s, want %s", streak, got, want)
		}
	}
}
