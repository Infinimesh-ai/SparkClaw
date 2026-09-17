package emailmanagement

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (s *Service) Configure(ctx context.Context, owner, provider string, enabled bool, expectedVersion int64) (app.EmailMailbox, error) {
	if !app.KnownEmailProvider(provider) || expectedVersion < 0 || strings.TrimSpace(owner) == "" {
		return app.EmailMailbox{}, ErrInvalidInput
	}
	mailboxes, err := s.repository.ListEmailMailboxes(ctx, owner)
	if err != nil {
		return app.EmailMailbox{}, err
	}
	var current app.EmailMailbox
	for _, mailbox := range mailboxes {
		if mailbox.Provider == provider && (current.ID == "" || mailbox.Version > current.Version) {
			current = mailbox
		}
	}
	if current.Version != expectedVersion {
		return current, ErrConflict
	}
	if !enabled {
		if current.ID == "" {
			return current, ErrNotFound
		}
		paused, err := s.repository.BindEmailMailbox(ctx, store.EmailBindCommand{EmailCommand: command(owner, app.NewID("mailbox_pause")), Provider: provider, Address: current.Address, Enabled: false, ExpectedVersion: expectedVersion, Boundary: current.Boundary})
		if err == nil {
			s.cancelMailboxBrowserJobs(owner, current.ID)
		}
		return paused, err
	}
	activation, err := s.deploymentBoundary()
	if err != nil {
		return current, err
	}
	request, err := s.browserBinding(ctx, owner, provider, app.NewID("email_bind"))
	if err != nil {
		return current, err
	}
	observed, err := s.browser.DiscoverForOwner(ctx, owner, request)
	if err != nil {
		return current, err
	}
	if observed.AccountAddress == "" {
		return current, errors.New("email_account_unverified")
	}
	// Recovery must not silently switch the mailbox and abandon its retained gap.
	if current.IntakeEnabled && current.ErrorCode == string(app.ToolErrorEmailLoginRequired) && !strings.EqualFold(observed.AccountAddress, current.Address) {
		return current, ErrConflict
	}
	mailbox, err := s.repository.BindEmailMailbox(ctx, store.EmailBindCommand{EmailCommand: command(owner, request.InvocationID), Provider: provider, Address: observed.AccountAddress, Enabled: true, ExpectedVersion: expectedVersion, Boundary: activation})
	if err == nil {
		if current.ID != "" && (current.ID != mailbox.ID || current.BindingGeneration != mailbox.BindingGeneration) {
			s.cancelMailboxBrowserJobs(owner, current.ID)
		}
		s.signal()
	}
	return mailbox, err
}

func (s *Service) browserBinding(ctx context.Context, owner, provider, invocation string) (app.EmailReadRequest, error) {
	admission, err := s.browser.AdmitIntake(ctx, owner, provider)
	if err != nil {
		return app.EmailReadRequest{}, err
	}
	registered, ok := s.registry.Get(provider)
	if !ok || admission.Provider != provider {
		return app.EmailReadRequest{}, errors.New("email_provider_invalid")
	}
	return app.EmailReadRequest{Provider: provider, Account: admission.Account, OwnerScope: ownerScope(owner), InvocationID: invocation,
		SettingVersion: admission.SettingVersion, BrowserCredentialGeneration: admission.BrowserCredentialGeneration, ProbeRevision: admission.ProbeRevision, ScriptRevision: registered.Discover.Revision}, nil
}

func (s *Service) activeMailbox(ctx context.Context, job app.EmailJob) (app.EmailMailbox, error) {
	mailbox, found, err := s.repository.GetEmailMailbox(ctx, job.OwnerID, job.MailboxID)
	if err != nil {
		return mailbox, err
	}
	if !found || !mailbox.Active || !mailbox.IntakeEnabled || mailbox.BindingGeneration != job.BindingGeneration {
		return mailbox, errors.New("email_binding_stale")
	}
	return mailbox, nil
}

func (s *Service) discover(ctx context.Context, job app.EmailJob) error {
	if browser, ok := s.browser.(PageBrowser); ok {
		return s.collectPages(ctx, job, browser)
	}
	// The old discovery + thread pagination path is deliberately not a fallback:
	// silently taking it would reintroduce full-history scans and their fixed
	// cost. A provider must expose the qualified one-response incremental reader.
	return ErrIncrementalUnqualified
}

func (s *Service) committed(ctx context.Context, cmd store.EmailCommand) (bool, error) {
	_, found, err := s.repository.ReconcileEmailCommand(ctx, cmd.OwnerID, cmd.CommandKey)
	return found, err
}
func (s *Service) reconcileError(ctx context.Context, cmd store.EmailCommand, err error) error {
	if store.StoreErrorCodeOf(err) != store.StoreErrorUnknownOutcome {
		return err
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if done, checkErr := s.committed(checkCtx, cmd); checkErr == nil && done {
		return nil
	} else {
		return errors.Join(err, checkErr)
	}
}
