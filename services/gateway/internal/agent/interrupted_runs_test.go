package agent

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestFailInterruptedRunsTerminatesOnlyExecutingRuns(t *testing.T) {
	st := store.NewMemoryStore()
	session, err := st.CreateSession(t.Context(), "restart")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-time.Minute)
	for _, state := range []string{"executing", "approval_pending", "completed", "browser_login_blocked"} {
		if _, err := st.SaveRun(t.Context(), app.AgentRun{ID: "run-" + state, SessionID: session.ID, State: state, StartedAt: started}); err != nil {
			t.Fatal(err)
		}
	}
	runtime := Runtime{store: st}
	failed, err := runtime.FailInterruptedRuns(t.Context())
	if err != nil || failed != 1 {
		t.Fatalf("FailInterruptedRuns = %d, %v; want 1 run failed", failed, err)
	}
	runs, err := st.ListRuns(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		switch run.ID {
		case "run-executing":
			if run.State != "failed" || run.CompletedAt == nil || run.Summary != runInterruptedSummary {
				t.Fatalf("interrupted run was not failed: %#v", run)
			}
		default:
			if run.State != run.ID[len("run-"):] || run.CompletedAt != nil {
				t.Fatalf("non-executing run was touched: %#v", run)
			}
		}
	}
	again, err := runtime.FailInterruptedRuns(t.Context())
	if err != nil || again != 0 {
		t.Fatalf("second sweep = %d, %v; want nothing to do", again, err)
	}
}
