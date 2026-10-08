package reminder

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// claimDue separates resident service schedules from host-workbench schedules.
// A healthy poll interval is dispatch jitter, but the first poll, a failed
// repository operation or a clock/suspension gap is an offline boundary.
func (s *Scheduler) claimDue(ctx context.Context) ([]app.MessageSchedule, error) {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	now := s.observePollLocked()
	s.unavailable = true
	workbench, err := s.claimWorkbenchDue(ctx, now)
	if err != nil {
		return nil, err
	}
	service, err := s.schedules.ClaimDue(ctx, now, now.Add(-sendingLease), tickBatchLimit)
	if err != nil {
		return workbench, err
	}
	s.unavailable = false
	return append(workbench, service...), nil
}

// Backpressure is still online time. Observe timer pulses while a bounded batch
// waits for workers, so queue congestion cannot masquerade as process suspension.
func (s *Scheduler) observePoll() {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	s.observePollLocked()
}

func (s *Scheduler) observePollLocked() time.Time {
	pulse := s.now()
	now := pulse.UTC()
	// Keep one availability horizon across healthy ticks. Bounded scans and
	// worker queues can delay online occurrences without making them offline.
	wallElapsed := now.Sub(s.lastPoll.UTC())
	clockGap := wallElapsed - pulse.Sub(s.lastPoll)
	if s.lastPoll.IsZero() || s.unavailable || now.Before(s.lastPoll) || wallElapsed > 2*s.interval || clockGap > 250*time.Millisecond || s.publisher == nil {
		s.eligibleAfter = now
	}
	s.lastPoll = pulse
	return now
}

func (s *Scheduler) claimWorkbenchDue(ctx context.Context, now time.Time) ([]app.MessageSchedule, error) {
	schedules, err := s.schedules.List(ctx, app.ReminderFilter{Status: "pending", To: &now, WorkbenchOnly: true, Limit: tickBatchLimit})
	if err != nil {
		return nil, err
	}
	claimed := make([]app.MessageSchedule, 0, len(schedules))
	// Bound repository work per tick even after a long offline period. Any
	// remaining overdue occurrences retain their original due time and will
	// only be recorded as missed by later scans; they are never a runnable backlog.
	remaining := tickBatchLimit
	for _, schedule := range schedules {
		for !schedule.DueTime.After(now) && remaining > 0 {
			remaining--
			status := "submitted"
			if !schedule.DueTime.After(s.eligibleAfter) || s.publisher == nil {
				status = "missed"
			}
			record := app.ReminderDelivery{
				ID: workbenchOccurrenceID(schedule), ReminderID: string(schedule.ID), Status: status,
				Provider: "workbench", ProviderStatus: status, RetryState: "none", CreatedAt: now,
			}
			next, _ := nextOccurrence(app.Reminder{DueTime: schedule.DueTime, Timezone: schedule.Timezone, Recurrence: schedule.Recurrence}, schedule.DueTime)
			updated, err := s.store.AdvanceWorkbenchSchedule(ctx, string(schedule.ID), schedule.UpdatedAt, next, record)
			if errors.Is(err, store.ErrReminderConflict) {
				break
			}
			if err != nil {
				return claimed, err
			}
			if status == "submitted" {
				claimed = append(claimed, schedule)
			}
			if next.IsZero() {
				break
			}
			schedule.DueTime, schedule.UpdatedAt = updated.DueTime, updated.UpdatedAt
		}
		if remaining == 0 {
			break
		}
	}
	return claimed, nil
}

// The occurrence ID is also the normal Message Runtime's envelope/request ID.
// A persisted submitted record is reconciled under this identity, never replayed.
func workbenchOccurrenceID(schedule app.MessageSchedule) string {
	return fmt.Sprintf("env_%s_%d", schedule.ID, schedule.DueTime.UTC().UnixNano())
}
