package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// runInterruptedSummary is the owner-visible outcome of a run that a previous
// gateway process left in the executing state.
const runInterruptedSummary = "The gateway restarted while this run was executing; it did not complete."

// FailInterruptedRuns terminates every run still marked executing at startup.
// Workflows execute inside the gateway process, so after a restart no such run
// can still be making progress; without this sweep it would report executing
// forever and LocalMind or JingSi callers would wait on a run nobody drives.
// Approval-pending and browser-login-blocked runs are untouched: their resume
// paths are owner-driven and remain valid across restarts.
func (r Runtime) FailInterruptedRuns(ctx context.Context) (int, error) {
	if r.store == nil {
		return 0, nil
	}
	sessions, err := r.store.ListSessions(ctx)
	if err != nil {
		return 0, fmt.Errorf("list sessions for interrupted runs: %w", err)
	}
	failed := 0
	for _, session := range sessions {
		runs, err := r.store.ListRuns(ctx, session.ID)
		if err != nil {
			return failed, fmt.Errorf("list runs of session %s: %w", session.ID, err)
		}
		for _, run := range runs {
			if run.State != "executing" {
				continue
			}
			now := time.Now().UTC()
			run.State = "failed"
			run.CompletedAt = &now
			if run.Summary == "" {
				run.Summary = runInterruptedSummary
			}
			if _, err := r.store.SaveRun(ctx, run); err != nil {
				return failed, fmt.Errorf("fail interrupted run %s: %w", run.ID, err)
			}
			r.addAudit(ctx, app.AuditEvent{
				ID: app.NewID("audit"), Time: now, Type: "run.interrupted_by_restart", SessionID: run.SessionID, RunID: run.ID,
				Actor: "agent", Summary: "Marked a run left executing by a previous gateway process as failed",
			})
			failed++
		}
	}
	return failed, nil
}
