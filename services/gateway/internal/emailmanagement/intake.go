package emailmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
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
	request, err := s.browserBinding(ctx, owner, provider, app.NewID("email_bind"), app.EmailJobDiscover)
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

func (s *Service) browserBinding(ctx context.Context, owner, provider, invocation, kind string) (app.EmailReadRequest, error) {
	admission, err := s.browser.AdmitIntake(ctx, owner, provider)
	if err != nil {
		return app.EmailReadRequest{}, err
	}
	registered, ok := s.registry.Get(provider)
	if !ok || admission.Provider != provider {
		return app.EmailReadRequest{}, errors.New("email_provider_invalid")
	}
	revision := registered.Discover.Revision
	switch kind {
	case app.EmailJobCapture:
		revision = registered.Capture.Revision
	case app.EmailJobMarkRead:
		revision = registered.MarkRead.Revision
	case app.EmailJobThreadSync:
		revision = registered.EnumerateThread.Revision
	}
	return app.EmailReadRequest{Provider: provider, Account: admission.Account, OwnerScope: ownerScope(owner), InvocationID: invocation,
		SettingVersion: admission.SettingVersion, BrowserCredentialGeneration: admission.BrowserCredentialGeneration, ProbeRevision: admission.ProbeRevision, ScriptRevision: revision}, nil
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

type discoveryCursor struct {
	Continuation    string    `json:"continuation"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	observationOnly bool
}

func (s *Service) discover(ctx context.Context, job app.EmailJob) error {
	if browser, ok := s.browser.(PageBrowser); ok {
		collectErr := s.collectPages(ctx, job, browser)
		mailbox, err := s.activeMailbox(ctx, job)
		if err == nil {
			err = s.scheduleThreads(ctx, job, mailbox)
		}
		return errors.Join(collectErr, err)
	}
	mailbox, err := s.activeMailbox(ctx, job)
	if err != nil {
		return err
	}
	binding, err := s.browserBinding(ctx, job.OwnerID, mailbox.Provider, app.NewID("email_discover"), app.EmailJobDiscover)
	if err != nil {
		return err
	}
	var failures []error
	for _, scan := range []string{"recent_observation", "recent_inbound"} {
		mailbox, err := s.activeMailbox(ctx, job)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		// A retained partial interval must not hide mail arriving after its
		// fixed upper bound. Probe the latest overlap independently; only the
		// durable catch-up scan may advance the completed boundary or cursor.
		if scan == "recent_observation" && mailbox.Cursor == "" {
			continue
		}
		request := binding
		request.InvocationID = app.NewID("email_discover")
		position := discoveryCursor{}
		lane := scan
		if scan == "recent_observation" {
			lane = "recent_inbound"
			position.observationOnly = true
		}
		if lane == "recent_inbound" {
			start := mailbox.Boundary.Add(-24 * time.Hour)
			if position.observationOnly {
				start = s.now().Add(-24 * time.Hour)
			}
			if start.Before(mailbox.ActivatedAt) {
				start = mailbox.ActivatedAt
			}
			position.Start, position.End = start, s.now()
			if !position.observationOnly && mailbox.Cursor != "" && json.Unmarshal([]byte(mailbox.Cursor), &position) != nil {
				return errors.New("email_cursor_invalid")
			}
		}
		request.Discovery = &app.EmailDiscoveryOptions{Lane: lane, AccountAddress: mailbox.Address, IntervalStart: position.Start, IntervalEnd: position.End, Continuation: position.Continuation, Limit: 50}
		result, err := s.browser.DiscoverForOwner(ctx, job.OwnerID, request)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if result.Coverage.Lane != lane {
			failures = append(failures, errors.New("email_discovery_lane_invalid"))
			continue
		}
		if err = s.admitDiscovery(ctx, job, mailbox, result, position, request.InvocationID); err != nil {
			failures = append(failures, err)
		}
	}
	mailbox, err = s.activeMailbox(ctx, job)
	if err == nil {
		err = s.scheduleThreads(ctx, job, mailbox)
	}
	return errors.Join(append(failures, err)...)
}

func (s *Service) admitDiscovery(ctx context.Context, job app.EmailJob, mailbox app.EmailMailbox, result app.EmailDiscoveryResult, position discoveryCursor, key string) error {
	if !strings.EqualFold(result.AccountAddress, mailbox.Address) {
		if mailbox.ErrorCode == string(app.ToolErrorEmailLoginRequired) {
			return errors.New("email_login_required")
		}
		_, err := s.repository.PauseEmailMailbox(ctx, store.EmailPauseCommand{EmailCommand: command(job.OwnerID, key+":mismatch"), MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, ErrorCode: "email_account_changed"})
		return errors.Join(errors.New("email_account_changed"), err)
	}
	input := store.EmailDiscoveryCommand{MaxPendingJobs: 1000, Lease: lease(job, s.now()), EmailCommand: command(job.OwnerID, key+":admit"), MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, ObservedAt: result.ObservedAt,
		Cursor: mailbox.Cursor, Coverage: "partial", Trigger: result.Coverage.Lane}
	if position.observationOnly {
		input.Trigger = "recent_observation"
	}
	for _, target := range result.Candidates {
		if !strings.EqualFold(target.AccountAddress, mailbox.Address) {
			return errors.New("email_account_changed")
		}
		input.Members = append(input.Members, store.EmailDiscoveryMember{ProviderMessageID: target.ProviderMessageID, ProviderSelectionID: target.ProviderSelectionID, ProviderThreadID: target.ProviderThreadID, Folder: target.Folder, Direction: "inbound", Reason: result.Coverage.Lane, RemoteReadState: "unknown"})
	}
	if result.Coverage.ScanComplete {
		input.Coverage = "complete_for_observation"
	}
	if result.Coverage.Reason != "" {
		input.Gaps = []string{result.Coverage.Reason}
	}
	if !position.End.IsZero() && !position.observationOnly {
		if result.Coverage.ScanComplete && result.Coverage.BoundaryQualified {
			input.CompletedBoundary = position.End
			input.Cursor = ""
			input.Coverage = "complete"
		} else {
			position.Continuation = result.Coverage.Continuation
			raw, _ := json.Marshal(position)
			input.Cursor = string(raw)
			input.Coverage = "partial"
		}
	}
	// Register every encountered grouped thread before the recent boundary can
	// advance. A crash between these commits merely replays the same interval.
	for index, thread := range result.Threads {
		if !strings.EqualFold(thread.AccountAddress, mailbox.Address) {
			return errors.New("email_account_changed")
		}
		threadCommand := store.EmailDiscoveryCommand{Lease: lease(job, s.now()), EmailCommand: command(job.OwnerID, fmt.Sprintf("%s:thread:%d", key, index)), MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration,
			ThreadID: thread.ProviderThreadID, ProviderSelectionID: thread.ProviderSelectionID, Folder: thread.Folder, Coverage: "pending", Trigger: "thread_discovery", ObservedAt: result.ObservedAt}
		_, err := s.repository.AdmitEmailDiscovery(ctx, threadCommand)
		if err = s.reconcileError(ctx, threadCommand.EmailCommand, err); err != nil {
			return err
		}
	}
	input.Lease = lease(job, s.now())
	_, err := s.repository.AdmitEmailDiscovery(ctx, input)
	return s.reconcileError(ctx, input.EmailCommand, err)
}

func (s *Service) scheduleThreads(ctx context.Context, job app.EmailJob, mailbox app.EmailMailbox) error {
	// Do not let the first batch of unavailable/paused threads starve later
	// encountered events. Only schedule known local identities; this does not
	// discover or scan any additional remote mailbox history.
	cursor := ""
	for {
		threads, err := s.repository.ListEmailThreads(ctx, store.EmailQuery{OwnerID: job.OwnerID, MailboxID: mailbox.ID, After: cursor, Limit: 100})
		if err != nil {
			return err
		}
		for _, thread := range threads {
			interval := s.opts.ScanInterval
			if thread.Coverage == "complete_for_observation" && s.now().Sub(thread.LastCheckedAt) < 10*time.Minute {
				continue
			}
			if thread.Coverage == "complete_for_observation" {
				interval = 10 * time.Minute
			}
			_, err = s.repository.RequestEmailJob(ctx, store.EmailJobRequest{EmailCommand: command(job.OwnerID, fmt.Sprintf("thread:%s:%d:%d", thread.ID, mailbox.BindingGeneration, s.now().Unix()/60)), Kind: app.EmailJobThreadSync, TargetID: thread.ID, MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, Rearm: true, RepeatInterval: interval})
			if err != nil {
				return err
			}
		}
		if len(threads) < 100 {
			break
		}
		cursor = store.EmailThreadCursor(threads[len(threads)-1])
	}
	return nil
}

// retryKnownHistory is reserved for an explicit user sync. Periodic scheduling
// keeps exhausted jobs stopped; a user retry preserves current leases/backoff
// while rearming terminal failures against known native identities only.
func (s *Service) retryKnownHistory(ctx context.Context, owner string, mailbox app.EmailMailbox) error {
	cursor := ""
	for {
		threads, err := s.repository.ListEmailThreads(ctx, store.EmailQuery{OwnerID: owner, MailboxID: mailbox.ID, After: cursor, Limit: 100})
		if err != nil {
			return err
		}
		for _, thread := range threads {
			_, err = s.repository.RequestEmailJob(ctx, store.EmailJobRequest{EmailCommand: command(owner, app.NewID("email_history_retry")), Kind: app.EmailJobThreadSync, TargetID: thread.ID, MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, Rearm: true})
			if err != nil {
				return err
			}
			memberCursor := ""
			for {
				members, err := s.repository.ListEmailMails(ctx, store.EmailQuery{OwnerID: owner, MailboxID: mailbox.ID, MailThreadID: thread.ProviderThreadID, After: memberCursor, Limit: 100})
				if err != nil {
					return err
				}
				for _, member := range members.Items {
					if member.CaptureState == app.EmailCaptureComplete || member.ProviderMessageID == "" || member.ProviderSelectionID == "" {
						continue
					}
					_, err = s.repository.RequestEmailJob(ctx, store.EmailJobRequest{EmailCommand: command(owner, app.NewID("email_history_capture_retry")), Kind: app.EmailJobCapture, TargetID: member.ID, MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, Rearm: true})
					if err != nil {
						return err
					}
				}
				if members.NextCursor == "" {
					break
				}
				memberCursor = members.NextCursor
			}
		}
		if len(threads) < 100 {
			return nil
		}
		cursor = store.EmailThreadCursor(threads[len(threads)-1])
	}
}

func (s *Service) capture(ctx context.Context, job app.EmailJob) error {
	cmd := jobCommand(job, "capture")
	if done, err := s.committed(ctx, cmd); err != nil || done {
		return err
	}
	mailbox, err := s.activeMailbox(ctx, job)
	if err != nil {
		return err
	}
	mail, found, err := s.repository.GetEmailMail(ctx, job.OwnerID, job.TargetID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("email_mail_not_found")
	}
	if mail.CaptureID != "" && mail.CaptureState == app.EmailCaptureComplete {
		return nil
	}
	request, err := s.browserBinding(ctx, job.OwnerID, mailbox.Provider, "email_capture_"+job.ID, app.EmailJobCapture)
	if err != nil {
		return err
	}
	request.Target = mailTarget(mailbox, mail)
	result, err := s.browser.CaptureForOwner(ctx, job.OwnerID, request)
	if err != nil {
		return err
	}
	if result.Capture == nil {
		return errors.New("email_capture_missing")
	}
	ref := result.Capture
	version := app.EmailCaptureVersion{ID: ref.CaptureID, MailID: mail.ID, ManifestPath: ref.ManifestPath, ManifestSHA256: ref.ManifestSHA256, State: app.EmailCapturePartial, CreatedAt: s.now()}
	if result.Status == "collected" {
		version.State = app.EmailCaptureComplete
	}
	manifest, files, err := loadManifest(ctx, s.opts.WorkspaceRoot, job.OwnerID, version)
	if err != nil {
		return err
	}
	if manifest.Provider != mailbox.Provider || !strings.EqualFold(manifest.AccountAddress, mailbox.Address) || manifest.ProviderMessageID != mail.ProviderMessageID || manifest.InvocationID != request.InvocationID {
		return errors.New("email_capture_identity")
	}
	root, err := os.OpenRoot(s.opts.WorkspaceRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, file := range files {
		verified, err := openVerifiedFile(ctx, root, file, 110<<20)
		if err != nil {
			return err
		}
		verified.Close()
	}
	original, ok := files[path.Join(path.Dir(ref.ManifestPath), "message.eml")]
	if !ok {
		return errors.New("email_original_missing")
	}
	version.OriginalPath, version.OriginalSHA256 = original.Path, original.SHA256
	_, err = s.repository.PublishEmailCapture(ctx, store.EmailCaptureCommand{EmailCommand: cmd, MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, Lease: lease(job, s.now()), Capture: version})
	return s.reconcileError(ctx, cmd, err)
}

func mailTarget(mailbox app.EmailMailbox, mail app.EmailMail) *app.EmailCaptureTarget {
	return &app.EmailCaptureTarget{AccountAddress: mailbox.Address, ProviderMessageID: mail.ProviderMessageID, ProviderSelectionID: mail.ProviderSelectionID, ProviderThreadID: mail.ProviderThreadID, Folder: mail.Folder}
}

func (s *Service) markRead(ctx context.Context, job app.EmailJob) error {
	mailbox, err := s.activeMailbox(ctx, job)
	if err != nil {
		return err
	}
	mail, found, err := s.repository.GetEmailMail(ctx, job.OwnerID, job.TargetID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("email_mail_not_found")
	}
	version, found, err := s.repository.GetEmailCapture(ctx, job.OwnerID, mail.CaptureID)
	if err != nil {
		return err
	}
	if !found || version.State != app.EmailCaptureComplete {
		return errors.New("email_capture_incomplete")
	}
	manifest, _, err := loadManifest(ctx, s.opts.WorkspaceRoot, job.OwnerID, version)
	if err != nil {
		return err
	}
	request, err := s.browserBinding(ctx, job.OwnerID, mailbox.Provider, "email_read_"+job.ID, app.EmailJobMarkRead)
	if err != nil {
		return err
	}
	request.Target = mailTarget(mailbox, mail)
	result, err := s.browser.MarkReadForOwner(ctx, job.OwnerID, app.EmailMarkReadRequest{Binding: request, CaptureInvocationID: manifest.InvocationID, CommittedCapture: app.EmailCaptureReceipt{ManifestPath: version.ManifestPath, ManifestSHA256: version.ManifestSHA256, MailID: manifest.MailID, MailboxID: manifest.MailboxID, CaptureID: manifest.CaptureID, AttachmentsCount: len(manifest.Attachments), ReadState: "unknown"}})
	if err == nil && result.ReadState != "read" {
		return errors.New("email_read_unconfirmed")
	}
	return err
}

// A bounded number of pages per lease lets unrelated mailbox work proceed.
// Every page is durably admitted before advancing, so retries resume at the
// saved cursor and already captured sources are reused by the Store.
const threadPagesPerRun = 4

func (s *Service) syncThread(ctx context.Context, job app.EmailJob) error {
	seen := map[string]bool{}
	for page := 0; page < threadPagesPerRun; page++ {
		mailbox, err := s.activeMailbox(ctx, job)
		if err != nil {
			return err
		}
		thread, found, err := s.repository.GetEmailThread(ctx, job.OwnerID, job.TargetID)
		if err != nil {
			return err
		}
		if !found || thread.MailboxID != mailbox.ID {
			return errors.New("email_thread_missing")
		}
		if seen[thread.Cursor] {
			return errors.New("email_thread_cursor_stalled")
		}
		seen[thread.Cursor] = true
		request, err := s.browserBinding(ctx, job.OwnerID, mailbox.Provider, app.NewID("email_thread"), app.EmailJobThreadSync)
		if err != nil {
			return err
		}
		target := app.EmailThreadTarget{AccountAddress: mailbox.Address, ProviderThreadID: thread.ProviderThreadID, ProviderSelectionID: thread.ProviderSelectionID, Folder: thread.Folder}
		result, err := s.browser.EnumerateThreadForOwner(ctx, job.OwnerID, app.EmailThreadRequest{Binding: request, Thread: target, Continuation: thread.Cursor, Limit: 50})
		if err != nil {
			return err
		}
		if !strings.EqualFold(result.Thread.AccountAddress, mailbox.Address) || result.Thread.ProviderThreadID != thread.ProviderThreadID || result.Thread.ProviderSelectionID != thread.ProviderSelectionID || result.Thread.Folder != thread.Folder {
			return errors.New("email_thread_identity")
		}
		if result.Coverage.ScanComplete && (result.Coverage.Continuation != "" || result.Coverage.Reason != "") {
			return errors.New("email_thread_coverage_invalid")
		}
		input := store.EmailDiscoveryCommand{MaxPendingJobs: 1000, Lease: lease(job, s.now()), EmailCommand: command(job.OwnerID, request.InvocationID+":admit"), MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, ThreadID: thread.ProviderThreadID, ProviderSelectionID: thread.ProviderSelectionID, Folder: thread.Folder, Cursor: result.Coverage.Continuation, Coverage: "partial", Trigger: app.EmailJobThreadSync, ObservedAt: result.ObservedAt}
		if result.Coverage.ScanComplete {
			input.Coverage = "complete_for_observation"
		}
		if result.Coverage.Reason != "" {
			input.Gaps = []string{result.Coverage.Reason}
		}
		memberIDs := map[string]bool{}
		for _, member := range result.Members {
			if !strings.EqualFold(member.Target.AccountAddress, mailbox.Address) || member.Target.ProviderThreadID != thread.ProviderThreadID || member.Target.ProviderSelectionID != thread.ProviderSelectionID || member.Target.ProviderMessageID == "" || memberIDs[member.Target.ProviderMessageID] {
				return errors.New("email_thread_identity")
			}
			memberIDs[member.Target.ProviderMessageID] = true
			if member.ReceivedAt == nil {
				input.Coverage = "partial"
				if !slices.Contains(input.Gaps, "thread_member_receipt_time_unqualified") {
					input.Gaps = append(input.Gaps, "thread_member_receipt_time_unqualified")
				}
				continue
			}
			if member.ReceivedAt.Before(mailbox.ActivatedAt) {
				continue
			}
			direction := member.Direction
			if direction == "outbound" {
				direction = "sent"
			}
			input.Members = append(input.Members, store.EmailDiscoveryMember{ProviderMessageID: member.Target.ProviderMessageID, ProviderSelectionID: member.Target.ProviderSelectionID, ProviderThreadID: thread.ProviderThreadID, Folder: member.Target.Folder, Direction: direction, Reason: app.EmailJobThreadSync, RemoteReadState: member.ReadState, Draft: member.Draft})
		}
		_, err = s.repository.AdmitEmailDiscovery(ctx, input)
		if err = s.reconcileError(ctx, input.EmailCommand, err); err != nil {
			return err
		}
		if result.Coverage.ScanComplete || result.Coverage.Continuation == "" {
			return nil
		}
	}
	return nil
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
