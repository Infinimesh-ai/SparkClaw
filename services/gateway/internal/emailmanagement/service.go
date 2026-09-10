package emailmanagement

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type Repository interface {
	store.EmailRepository
	store.EmailPresentationRepository
	store.OwnerRepository
	store.RunRepository
}

type Browser interface {
	AdmitIntake(context.Context, string, string) (app.EmailAdmissionBinding, error)
	DiscoverForOwner(context.Context, string, app.EmailReadRequest) (app.EmailDiscoveryResult, error)
	CaptureForOwner(context.Context, string, app.EmailReadRequest) (app.EmailReadResult, error)
	EnumerateThreadForOwner(context.Context, string, app.EmailThreadRequest) (app.EmailThreadResult, error)
	MarkReadForOwner(context.Context, string, app.EmailMarkReadRequest) (app.EmailMarkReadResult, error)
}

type Options struct {
	WorkspaceRoot string
	ScanInterval  time.Duration
	LeaseDuration time.Duration
	JobTimeout    time.Duration
	ModelWorkers  int
}

type Service struct {
	repository  Repository
	browser     Browser
	registry    emailautomation.Registry
	analyzer    Analyzer
	extractor   DocumentExtractor
	opts        Options
	mu          sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
	wake        chan struct{}
	now         func() time.Time
	ownerTurn   atomic.Uint64
	browserJobs map[string]browserJobExecution
}

func New(repository Repository, browser Browser, registry emailautomation.Registry, analyzer Analyzer, extractor DocumentExtractor, opts Options) (*Service, error) {
	if repository == nil || browser == nil || opts.WorkspaceRoot == "" {
		return nil, errors.New("email management dependencies are required")
	}
	if opts.ScanInterval == 0 {
		opts.ScanInterval = 20 * time.Minute
	}
	if opts.LeaseDuration == 0 {
		opts.LeaseDuration = 3 * time.Minute
	}
	if opts.JobTimeout == 0 {
		opts.JobTimeout = 5 * time.Minute
	}
	if opts.ModelWorkers == 0 {
		opts.ModelWorkers = 2
	}
	if opts.ScanInterval < time.Second || opts.LeaseDuration < 3*time.Second || opts.JobTimeout < time.Second || opts.ModelWorkers < 1 || opts.ModelWorkers > 4 {
		return nil, errors.New("email management limits are invalid")
	}
	return &Service{repository: repository, browser: browser, registry: registry, analyzer: analyzer, extractor: extractor, opts: opts, wake: make(chan struct{}, 1), now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) Start(parent context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel, s.done = cancel, make(chan struct{})
	go func() {
		defer close(s.done)
		var workers sync.WaitGroup
		start := func(kinds []string) { workers.Add(1); go func() { defer workers.Done(); s.worker(ctx, kinds) }() }
		// One slot per registered provider lets independent mailboxes receive
		// together. Store claims serialize all browser job kinds per mailbox;
		// provider admission also protects the shared account's browser effects.
		browserKinds := []string{app.EmailJobDiscover, app.EmailJobCapture, app.EmailJobMarkRead, app.EmailJobCapture, app.EmailJobThreadSync, app.EmailJobCapture}
		if _, pageMode := s.browser.(PageBrowser); pageMode {
			browserKinds = []string{app.EmailJobDiscover, app.EmailJobThreadSync, app.EmailJobCapture, app.EmailJobMarkRead}
		}
		for range s.registry.List() {
			start(browserKinds)
		}
		start([]string{app.EmailJobParse})
		for i := 0; i < s.opts.ModelWorkers; i++ {
			start([]string{app.EmailJobMessageSummary, app.EmailJobClassification, app.EmailJobAssignment, app.EmailJobRelationshipCheck, app.EmailJobConversationSummary, app.EmailJobPresentation})
		}
		s.planLoop(ctx)
		workers.Wait()
	}()
}

func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) planLoop(ctx context.Context) {
	ticker := time.NewTicker(s.opts.ScanInterval)
	defer ticker.Stop()
	for {
		if err := s.plan(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("email scheduling failed", "code", safeCode(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

func (s *Service) plan(ctx context.Context) error {
	owners, err := s.repository.ListOwnerProfiles(ctx)
	if err != nil {
		return err
	}
	for _, owner := range owners {
		if _, err := s.repository.ActivateEmailEventPolicy(ctx, command(owner.ID, "activate-source-events-v4")); err != nil {
			return err
		}
		mailboxes, err := s.repository.ListEmailMailboxes(ctx, owner.ID)
		if err != nil {
			return err
		}
		slot := s.now().UnixNano() / int64(s.opts.ScanInterval)
		for _, mailbox := range mailboxes {
			if !mailbox.Active || !mailbox.IntakeEnabled {
				continue
			}
			_, err = s.repository.RequestEmailJob(ctx, store.EmailJobRequest{EmailCommand: command(owner.ID, fmt.Sprintf("scan:%s:%d:%d", mailbox.ID, mailbox.BindingGeneration, slot)), Kind: app.EmailJobDiscover, TargetID: mailbox.ID, MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, Rearm: true, RepeatInterval: s.opts.ScanInterval})
			if err != nil {
				return err
			}
		}
		refresh, refreshErr := s.repository.ExpandEmailRefresh(ctx, store.EmailRefreshCommand{EmailCommand: command(owner.ID, app.NewID("refresh")), Limit: 50})
		err = refreshErr
		if refresh.Remaining {
			s.signal()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) worker(ctx context.Context, kinds []string) {
	turn := 0
	for {
		ordered := append(append([]string{}, kinds[turn:]...), kinds[:turn]...)
		turn = (turn + 1) % len(kinds)
		worked, err := s.workOne(ctx, ordered)
		if err != nil && ctx.Err() == nil {
			slog.Warn("email worker failed", "code", safeCode(err))
		}
		if ctx.Err() != nil {
			return
		}
		if worked {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Service) workOne(ctx context.Context, kinds []string) (bool, error) {
	owners, err := s.repository.ListOwnerProfiles(ctx)
	if err != nil {
		return false, err
	}
	if len(owners) == 0 {
		return false, nil
	}
	start := int(s.ownerTurn.Add(1)-1) % len(owners)
	for offset := range owners {
		owner := owners[(start+offset)%len(owners)]
		if _, err := s.repository.ActivateEmailEventPolicy(ctx, command(owner.ID, "activate-source-events-v4")); err != nil {
			return false, err
		}
		// A single-kind claim keeps a historical capture backlog from delaying
		// discovery, remote read effects, or the other semantic job classes.
		for _, kind := range kinds {
			job, found, err := s.repository.ClaimEmailJob(ctx, store.EmailJobClaim{OwnerID: owner.ID, Kinds: []string{kind}, Now: s.now(), LeaseDuration: s.opts.LeaseDuration})
			if err != nil {
				return false, err
			}
			if !found {
				continue
			}
			s.execute(ctx, job)
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) execute(parent context.Context, job app.EmailJob) {
	timeout := s.opts.JobTimeout
	if _, pageMode := s.browser.(PageBrowser); pageMode && job.Kind == app.EmailJobDiscover {
		// Up to three bounded page lanes; renew the mailbox lease throughout.
		timeout = 95 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer s.trackBrowserJob(job, cancel)()
	leaseDone := make(chan struct{})
	go func() {
		defer close(leaseDone)
		ticker := time.NewTicker(s.opts.LeaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.repository.RenewEmailJob(ctx, store.EmailJobRenew{EmailJobLease: lease(job, s.now()), LeaseDuration: s.opts.LeaseDuration}); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	err := s.dispatch(ctx, job)
	cancel()
	<-leaseDone
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer finishCancel()
	finish := store.EmailJobFinish{EmailJobLease: lease(job, s.now())}
	if err != nil {
		finish.ErrorCode = safeCode(err)
		delay := time.Duration(min(job.Attempt, 6)*min(job.Attempt, 6)) * 10 * time.Second
		if _, pageMode := s.browser.(PageBrowser); pageMode && job.Kind == app.EmailJobDiscover {
			delay = max(delay, s.opts.ScanInterval)
		}
		finish.RetryAt = s.now().Add(delay)
	}
	if _, finishErr := s.repository.FinishEmailJob(finishCtx, finish); finishErr != nil && store.StoreErrorCodeOf(finishErr) != store.StoreErrorConflict {
		slog.Warn("email job completion unavailable", "job_id", job.ID, "code", safeCode(finishErr))
	}
	s.signal()
}

func (s *Service) dispatch(ctx context.Context, job app.EmailJob) error {
	switch job.Kind {
	case app.EmailJobDiscover:
		return s.discover(ctx, job)
	case app.EmailJobCapture:
		return s.capture(ctx, job)
	case app.EmailJobMarkRead:
		return s.markRead(ctx, job)
	case app.EmailJobThreadSync:
		return s.syncThread(ctx, job)
	case app.EmailJobParse:
		return s.parse(ctx, job)
	case app.EmailJobPresentation:
		return s.present(ctx, job)
	case app.EmailJobMessageSummary, app.EmailJobClassification, app.EmailJobAssignment, app.EmailJobRelationshipCheck, app.EmailJobConversationSummary:
		return s.analyze(ctx, job)
	default:
		return errors.New("email_job_kind_invalid")
	}
}

func command(owner, key string) store.EmailCommand {
	return store.EmailCommand{OwnerID: owner, CommandKey: key}
}
func lease(job app.EmailJob, now time.Time) store.EmailJobLease {
	return store.EmailJobLease{OwnerID: job.OwnerID, JobID: job.ID, LeaseToken: job.LeaseToken, Now: now}
}
func jobCommand(job app.EmailJob, operation string) store.EmailCommand {
	return command(job.OwnerID, fmt.Sprintf("%s:%s:%d:%s", job.ID, operation, job.Generation, job.InputFingerprint))
}

func safeCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, store.ErrEmailBacklogFull) {
		return "email_backlog_full"
	}
	if code := store.StoreErrorCodeOf(err); code != "" {
		return "store_" + string(code)
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if code := emailautomation.ErrorCode(err); code != "" {
		return string(code)
	}
	for _, code := range []string{"email_login_required", "email_model_unavailable", "email_model_mock_unqualified", "email_model_output_invalid", "email_model_evidence_invalid", "email_model_input_limit", "email_representation_missing", "email_mail_not_found"} {
		if err.Error() == code {
			return code
		}
	}
	// Internal diagnostics are deliberately not copied into durable/UI error text.
	return "email_processing_failed"
}
