package emailmanagement

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type browserJobExecution struct {
	owner, mailbox string
	cancel         context.CancelFunc
}

func (s *Service) trackBrowserJob(job app.EmailJob, cancel context.CancelFunc) func() {
	switch job.Kind {
	case app.EmailJobDiscover:
	default:
		return func() {}
	}
	s.mu.Lock()
	if s.browserJobs == nil {
		s.browserJobs = make(map[string]browserJobExecution)
	}
	s.browserJobs[job.ID] = browserJobExecution{owner: job.OwnerID, mailbox: job.MailboxID, cancel: cancel}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.browserJobs, job.ID)
		s.mu.Unlock()
	}
}

func (s *Service) cancelMailboxBrowserJobs(owner, mailbox string) {
	s.mu.Lock()
	var pending []context.CancelFunc
	for _, running := range s.browserJobs {
		if running.owner == owner && running.mailbox == mailbox {
			pending = append(pending, running.cancel)
		}
	}
	s.mu.Unlock()
	for _, cancel := range pending {
		cancel()
	}
}
