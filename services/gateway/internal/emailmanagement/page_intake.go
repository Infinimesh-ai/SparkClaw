package emailmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type PageBrowser interface {
	CollectPageForOwner(context.Context, string, app.EmailReadRequest) (app.EmailPageResult, error)
}

type PageMarkReadBrowser interface {
	MarkReadForOwner(context.Context, string, app.EmailMarkReadRequest) (app.EmailMarkReadResult, error)
}

type timelinePagePart struct {
	request app.EmailReadRequest
	page    app.EmailPageResult
}

func (s *Service) collectPages(ctx context.Context, job app.EmailJob, browser PageBrowser) error {
	hasTrigger := job.SyncTrigger == "manual_refresh" || job.SyncTrigger == "notification_hint"
	for slice := 0; slice < 8; slice++ {
		before, err := s.activeMailbox(ctx, job)
		if err != nil {
			return err
		}
		hasTrigger = hasTrigger || before.SignalRevision > before.ReconciledRevision
		finalCheck := before.InflightKind == "final_check"
		if !finalCheck {
			if wait := 2*time.Second - time.Since(before.LastIncrementStartedAt); !before.LastIncrementStartedAt.IsZero() && wait > 0 {
				wait = min(wait, 2*time.Second)
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
		if err := s.collectPageIncrement(ctx, job, browser, finalCheck); err != nil {
			return err
		}
		after, found, err := s.repository.GetEmailMailbox(ctx, job.OwnerID, job.MailboxID)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("email_mailbox_missing")
		}
		if after.SyncState != app.EmailSyncIdle && after.SyncState != app.EmailSyncCoverageGap {
			if finalCheck {
				return errors.New("email_final_check_incomplete")
			}
			return nil
		}
		if after.SyncState == app.EmailSyncCoverageGap && after.LastSyncErrorCode == "email_interval_overflow" {
			return nil
		}
		if after.SignalRevision > after.ReconciledRevision {
			hasTrigger = true
			continue
		}
		if finalCheck {
			return nil
		}
		if hasTrigger && after.LastIntervalEnd.Equal(after.PollThrough) && after.LastIntervalStart.Before(after.LastIntervalEnd) {
			if err := s.collectPageIncrement(ctx, job, browser, true); err != nil {
				return err
			}
			checked, found, err := s.repository.GetEmailMailbox(ctx, job.OwnerID, job.MailboxID)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("email_mailbox_missing")
			}
			if checked.SyncState != app.EmailSyncIdle && checked.SyncState != app.EmailSyncCoverageGap {
				return errors.New("email_final_check_incomplete")
			}
			if checked.SignalRevision > checked.ReconciledRevision {
				continue
			}
		}
		return nil
	}
	// The unfinished intent remains durable; FinishEmailJob will requeue it.
	return nil
}

func (s *Service) collectPageIncrement(ctx context.Context, job app.EmailJob, browser PageBrowser, finalCheck bool) error {
	mailbox, err := s.activeMailbox(ctx, job)
	if err != nil {
		return err
	}
	mode := s.incrementalMode(mailbox.Provider)
	if mode == app.EmailProviderModeUnqualified {
		return errors.New("email_incremental_unqualified")
	}
	upper := s.now()
	lower := mailbox.PollThrough
	if lower.IsZero() {
		lower = mailbox.Boundary
	}
	if finalCheck {
		upper = mailbox.PollThrough
	} else if !upper.After(lower) {
		upper = lower.Add(time.Microsecond)
	}
	// A resource yield keeps the checkpoint revision frozen. A new worker
	// lease must not replay a prior Begin command with a different upper-bound
	// proposal (or trigger); the Store itself retains the frozen interval.
	leaseDigest := sha256.Sum256([]byte(job.LeaseToken))
	beginKey := fmt.Sprintf("timeline-begin:%s:%d:%d:%x", mailbox.ID, mailbox.BindingGeneration, mailbox.CheckpointRevision+1, leaseDigest)
	checkpoint, err := s.repository.BeginEmailSync(ctx, store.EmailSyncBeginCommand{EmailCommand: command(job.OwnerID, beginKey), MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, ProviderMode: mode, ProviderCursor: mailbox.ProviderCursor, UpperBound: upper, FinalCheck: finalCheck, Trigger: syncTrigger(job), Actor: syncActor(job)})
	if err != nil {
		return err
	}
	mailbox = checkpoint.Mailbox
	registered, _ := s.registry.Get(mailbox.Provider)
	binding := app.EmailReadRequest{Provider: mailbox.Provider, ScriptRevision: registered.CollectPage.Revision}
	retryMails := map[string]app.EmailMail{}
	retryTargets := make([]app.EmailCaptureTarget, 0, len(checkpoint.RetryFailures))
	retryBudget := timelineRetryBudget{} // Omitted failures remain open for a later poll.
	for _, failure := range checkpoint.RetryFailures {
		if finalCheck && !failure.LastFailedAt.Before(job.RoundStartedAt) {
			continue
		}
		mail, found, readErr := s.repository.GetEmailMail(ctx, job.OwnerID, failure.MailID)
		if readErr != nil {
			return readErr
		}
		if !found || mail.SyncState == app.EmailMailSyncSuppressed {
			continue
		}
		target := app.EmailCaptureTarget{AccountAddress: mailbox.Address, ProviderMessageID: mail.ProviderMessageID, ProviderNativeID: mail.ProviderNativeID, ProviderSelectionID: mail.ProviderSelectionID, ProviderThreadID: mail.ProviderThreadID, Folder: mail.Folder}
		if mailbox.Provider == app.EmailProviderOutlook && !mail.SourceTime.IsZero() {
			received := mail.SourceTime
			target.ReceivedAt = &received
		}
		if mail.CaptureState == app.EmailCaptureSourceMissing && mail.CaptureID != "" {
			capture, exists, err := s.repository.GetEmailCapture(ctx, job.OwnerID, mail.CaptureID)
			if err != nil {
				return err
			}
			if exists && capture.PurgedAt == nil && capture.ManifestJSON != "" {
				target.RecoveryCapture = &capture
			}
		}
		admitted, budgetErr := retryBudget.admit(target)
		if budgetErr != nil {
			return s.commitListFailure(ctx, job, checkpoint, mode, registered.CollectPage.Revision, budgetErr)
		}
		if !admitted {
			continue
		}
		retryTargets = append(retryTargets, target)
		retryMails[mail.ProviderMessageID] = mail
	}
	intervalID := sha256.Sum256([]byte(mailbox.ID + "\x00" + checkpoint.IntervalStart.UTC().Format(time.RFC3339Nano) + "\x00" + checkpoint.IntervalEnd.UTC().Format(time.RFC3339Nano)))
	binding.InvocationID = fmt.Sprintf("email_changes_%s_r%d", hex.EncodeToString(intervalID[:]), mailbox.CheckpointRevision)
	binding.Discovery = &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: mailbox.Address, IntervalStart: checkpoint.IntervalStart, IntervalEnd: checkpoint.IntervalEnd, Limit: 50, ProviderMode: mode, RetryTargets: retryTargets}
	if checkpoint.IntervalStart.Before(checkpoint.OverlapEnd) {
		for _, id := range mailbox.LastIntervalCapturedIDs {
			if _, repairing := retryMails[id]; !repairing {
				binding.Discovery.SkipProviderMessageIDs = append(binding.Discovery.SkipProviderMessageIDs, id)
			}
		}
	}
	if finalCheck {
		// A mail first failed in this round cannot spend its second automatic
		// attempt during the final list check.
		seen := map[string]bool{}
		ids := make([]string, 0, 100)
		for _, failure := range checkpoint.RetryFailures {
			if !failure.LastFailedAt.Before(job.RoundStartedAt) && failure.ProviderMessageID != "" && !seen[failure.ProviderMessageID] {
				seen[failure.ProviderMessageID] = true
				ids = append(ids, failure.ProviderMessageID)
			}
		}
		for _, id := range binding.Discovery.SkipProviderMessageIDs {
			if len(ids) == 100 {
				break
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		binding.Discovery.SkipProviderMessageIDs = ids
	}
	part, err := s.collectTimelinePart(ctx, job, browser, mailbox, binding)
	if err != nil {
		return s.commitListFailure(ctx, job, checkpoint, mode, registered.CollectPage.Revision, err)
	}
	parts := []timelinePagePart{part}
	if checkpoint.IntervalStart.Before(checkpoint.OverlapEnd) && pageOverflow(part.page) {
		// A combined overflow proves neither constituent range overflowed.
		// Query them separately, each with the unchanged 50-mail Reader limit.
		parts = make([]timelinePagePart, 0, 2)
		for index, bounds := range [][2]time.Time{{checkpoint.IntervalStart, checkpoint.OverlapEnd}, {checkpoint.OverlapEnd, checkpoint.IntervalEnd}} {
			segment := binding
			options := *binding.Discovery
			options.IntervalStart, options.IntervalEnd = bounds[0], bounds[1]
			options.RetryTargets = nil // existing failures remain open for a later round
			if index == 1 {
				options.SkipProviderMessageIDs = nil
			}
			segment.Discovery = &options
			digest := sha256.Sum256([]byte(mailbox.ID + "\x00" + bounds[0].UTC().Format(time.RFC3339Nano) + "\x00" + bounds[1].UTC().Format(time.RFC3339Nano)))
			segment.InvocationID = fmt.Sprintf("email_changes_%s_r%d", hex.EncodeToString(digest[:]), mailbox.CheckpointRevision)
			piece, segmentErr := s.collectTimelinePart(ctx, job, browser, mailbox, segment)
			if segmentErr != nil {
				return s.commitListFailure(ctx, job, checkpoint, mode, registered.CollectPage.Revision, segmentErr)
			}
			parts = append(parts, piece)
		}
	}
	return s.publishIncrementalPage(ctx, job, mailbox, binding, parts, checkpoint, retryMails, mode)
}

func pageOverflow(page app.EmailPageResult) bool {
	coverage := page.Discovery.Coverage
	return coverage.Continuation != "" || coverage.Reason == "network_page_continues"
}

func (s *Service) collectTimelinePart(ctx context.Context, job app.EmailJob, browser PageBrowser, mailbox app.EmailMailbox, request app.EmailReadRequest) (timelinePagePart, error) {
	if recovered, found, err := s.recoverTimelineBatch(ctx, job.OwnerID, request); found || err != nil {
		return timelinePagePart{request, recovered}, err
	}
	admitted, err := s.browserBinding(ctx, job.OwnerID, mailbox.Provider, request.InvocationID)
	if err != nil {
		return timelinePagePart{}, err
	}
	admitted.ScriptRevision, admitted.Discovery = request.ScriptRevision, request.Discovery
	page, err := browser.CollectPageForOwner(ctx, job.OwnerID, admitted)
	if err != nil {
		return timelinePagePart{}, err
	}
	if page.DiscoveryOptions.Lane != "recent_inbound" || !page.DiscoveryOptions.IntervalStart.Equal(request.Discovery.IntervalStart) || !page.DiscoveryOptions.IntervalEnd.Equal(request.Discovery.IntervalEnd) || !strings.EqualFold(page.AccountAddress, mailbox.Address) {
		return timelinePagePart{}, errors.New("email_account_changed")
	}
	return timelinePagePart{admitted, page}, nil
}

func (s *Service) publishIncrementalPage(ctx context.Context, job app.EmailJob, mailbox app.EmailMailbox, request app.EmailReadRequest, parts []timelinePagePart, checkpoint store.EmailSyncCheckpoint, retryMails map[string]app.EmailMail, mode string) error {
	key := fmt.Sprintf("timeline-source:%s:%d:%s:%s", mailbox.ID, checkpoint.Mailbox.CheckpointRevision, checkpoint.IntervalStart.UTC().Format(time.RFC3339Nano), checkpoint.IntervalEnd.UTC().Format(time.RFC3339Nano))
	members := make([]store.EmailDiscoveryMember, 0, 100)
	seenMembers := map[string]bool{}
	for _, part := range parts {
		for _, target := range part.page.Discovery.Candidates {
			if seenMembers[target.ProviderMessageID] {
				return errors.New("email_duplicate_split_identity")
			}
			seenMembers[target.ProviderMessageID] = true
			received := time.Time{}
			if target.ReceivedAt != nil {
				received = *target.ReceivedAt
			}
			members = append(members, store.EmailDiscoveryMember{ProviderMessageID: target.ProviderMessageID, ProviderNativeID: target.ProviderNativeID, ProviderSelectionID: target.ProviderSelectionID, ProviderThreadID: target.ProviderThreadID, Folder: target.Folder, Direction: pageDirection(target.Folder), Reason: "recent_inbound", SourceTime: received, RemoteReadState: "unknown"})
		}
	}
	captures := []store.EmailSyncCapture{}
	outcomes := []store.EmailSyncFailureOutcome{}
	for _, part := range parts {
		for _, captured := range part.page.Captures {
			invocation := emailautomation.PageCaptureInvocationID(part.request.InvocationID, mailbox.Provider, captured.Target)
			prepared, err := s.preparePageCapture(ctx, job.OwnerID, mailbox, captured.Target.ProviderMessageID, captured.Result, invocation)
			if err != nil {
				outcomes = append(outcomes, store.EmailSyncFailureOutcome{ProviderMessageID: captured.Target.ProviderMessageID, Stage: "source_validation", Scope: app.EmailSyncFailureLocalOperational, ErrorCode: safeCode(err)})
				continue
			}
			if prior, repairing := retryMails[captured.Target.ProviderMessageID]; repairing && prior.CaptureID != "" {
				old, found, err := s.repository.GetEmailCapture(ctx, job.OwnerID, prior.CaptureID)
				if err != nil {
					return err
				}
				if !found || old.PurgedAt != nil || old.OriginalSHA256 != prepared.Capture.OriginalSHA256 ||
					(old.ID == prepared.Capture.ID && old.ManifestSHA256 != prepared.Capture.ManifestSHA256) {
					outcomes = append(outcomes, store.EmailSyncFailureOutcome{ProviderMessageID: captured.Target.ProviderMessageID, Stage: "source_validation", Scope: app.EmailSyncFailureLocalOperational, ErrorCode: "email_source_conflict"})
					continue
				}
			}
			captures = append(captures, prepared)
			outcomes = append(outcomes, store.EmailSyncFailureOutcome{ProviderMessageID: captured.Target.ProviderMessageID, Stage: "original", Success: true})
		}
		for _, failure := range part.page.Failures {
			outcomes = append(outcomes, store.EmailSyncFailureOutcome{ProviderMessageID: failure.Target.ProviderMessageID, Stage: "original", Scope: failure.Scope, ErrorCode: failure.ErrorCode, Qualified: failure.Qualified})
		}
	}
	complete, overflow, unsupported, reason := true, false, 0, ""
	observedAt := time.Time{}
	overlapQualified := true
	for index, part := range parts {
		coverage := part.page.Discovery.Coverage
		if part.page.Discovery.ObservedAt.After(observedAt) {
			observedAt = part.page.Discovery.ObservedAt
		}
		unsupported += coverage.UnsupportedRows
		qualified := coverage.ScanComplete && coverage.BoundaryQualified && coverage.UnsupportedRows == 0 && !coverage.Limited && !pageOverflow(part.page)
		if index == 0 {
			overlapQualified = qualified
		}
		if !qualified {
			complete = false
			reason = coverage.Reason
			if pageOverflow(part.page) && (len(parts) == 1 || index == 1) {
				overflow = true
			}
		}
	}
	if len(parts) == 2 && !overlapQualified {
		// The overlap must qualify independently before the new tail can be
		// certified; its failure cannot be promoted to new-tail overflow.
		complete, overflow, reason = false, false, "email_overlap_incomplete"
	}
	commit := store.EmailSyncCommitCommand{EmailCommand: command(job.OwnerID, key+":commit"), MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, IntervalStart: checkpoint.IntervalStart, IntervalEnd: checkpoint.IntervalEnd, ProviderMode: mode, Trigger: syncTrigger(job), Actor: syncActor(job), InvocationID: request.InvocationID, ReaderRevision: request.ScriptRevision, Complete: complete, Overflow: overflow, SplitOverlap: len(parts) == 2, UnsupportedItems: unsupported, ErrorCode: reason, FailureScope: app.EmailSyncFailureProviderOperational, Outcomes: outcomes, Members: members, Captures: captures, Lease: lease(job, s.now()), ObservedAt: observedAt}
	if len(parts) == 2 && overflow {
		commit.OverflowIntervalStart = checkpoint.OverlapEnd
	}
	_, err := s.repository.CommitEmailSync(ctx, commit)
	if err = s.reconcileError(ctx, commit.EmailCommand, err); err != nil {
		return err
	}
	// QQ's qualified reader confirms the remote effect from a fresh provider
	// list response. Run it only after the complete original is canonical in
	// Store; a failed or uncertain effect never rolls back source durability.
	if mailbox.Provider == app.EmailProviderQQMail {
		if marker, ok := s.browser.(PageMarkReadBrowser); ok {
			for _, part := range parts {
				s.markCommittedQQPageRead(ctx, job, mailbox, part.request, part.page, captures, marker)
			}
		}
	}
	// Refetchable source bytes were journaled before their atomic rename. The
	// journal may be removed only after the composite Store receipt is known.
	root, openErr := os.OpenRoot(s.opts.WorkspaceRoot)
	if openErr == nil {
		defer root.Close()
		invocations := []string{request.InvocationID}
		for _, part := range parts {
			invocations = append(invocations, part.request.InvocationID)
		}
		for _, invocation := range invocations {
			digest := sha256.Sum256([]byte(mailbox.Provider + "\x00" + invocation))
			_ = root.Remove(path.Join("email", ownerScope(job.OwnerID), "batches", hex.EncodeToString(digest[:])+".json"))
		}
	}
	return nil
}

func (s *Service) markCommittedQQPageRead(ctx context.Context, job app.EmailJob, mailbox app.EmailMailbox, request app.EmailReadRequest, page app.EmailPageResult, committed []store.EmailSyncCapture, marker PageMarkReadBrowser) {
	registered, ok := s.registry.Get(app.EmailProviderQQMail)
	if !ok {
		return
	}
	committedIDs := make(map[string]bool, len(committed))
	for _, capture := range committed {
		if capture.Capture.State == app.EmailCaptureComplete {
			committedIDs[capture.ProviderMessageID] = true
		}
	}
	if len(committedIDs) == 0 {
		return
	}
	mails, err := s.repository.ListEmailMails(ctx, store.EmailQuery{OwnerID: job.OwnerID, MailboxID: mailbox.ID, CapturedOnly: true, Limit: 100})
	if err != nil {
		slog.Warn("QQ remote read confirmation lookup failed", "code", safeCode(err))
		return
	}
	byProviderID := make(map[string]app.EmailMail, len(mails.Items))
	for _, mail := range mails.Items {
		byProviderID[mail.ProviderMessageID] = mail
	}
	for _, captured := range page.Captures {
		if !committedIDs[captured.Target.ProviderMessageID] || captured.Result.Status != "collected" || captured.Result.Capture == nil {
			continue
		}
		mail, found := byProviderID[captured.Target.ProviderMessageID]
		if !found || mail.CaptureID != captured.Result.Capture.CaptureID || mail.RemoteReadState == "read" {
			continue
		}
		target := app.EmailCaptureTarget{AccountAddress: captured.Target.AccountAddress, ProviderMessageID: captured.Target.ProviderMessageID, ProviderNativeID: captured.Target.ProviderNativeID, ProviderSelectionID: captured.Target.ProviderSelectionID, ProviderThreadID: captured.Target.ProviderThreadID, Folder: captured.Target.Folder}
		target.ReceivedAt = captured.Target.ReceivedAt
		binding := request
		binding.InvocationID = app.NewID("email_mark_read")
		binding.ScriptRevision = registered.MarkRead.Revision
		binding.Target, binding.Discovery = &target, nil
		result, markErr := marker.MarkReadForOwner(ctx, job.OwnerID, app.EmailMarkReadRequest{Binding: binding, CommittedCapture: *captured.Result.Capture, CaptureInvocationID: emailautomation.PageCaptureInvocationID(request.InvocationID, mailbox.Provider, target)})
		if markErr != nil || result.ReadState != "read" {
			code := "email_remote_read_unknown"
			if markErr != nil {
				code = safeCode(markErr)
			}
			slog.Warn("QQ remote read was not confirmed", "code", code)
			continue
		}
		version, exists, loadErr := s.repository.GetEmailCapture(ctx, job.OwnerID, mail.CaptureID)
		if loadErr != nil || !exists || version.State != app.EmailCaptureComplete || version.PurgedAt != nil {
			slog.Warn("QQ remote read Store receipt unavailable", "code", safeCode(loadErr))
			continue
		}
		cmd := store.EmailCaptureCommand{EmailCommand: command(job.OwnerID, fmt.Sprintf("timeline-read-v1:%s:%s", mail.ID, version.ID)), PageBatch: true, ReadState: "read", MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, Lease: lease(job, s.now()), Capture: version}
		if _, publishErr := s.repository.PublishEmailCapture(ctx, cmd); s.reconcileError(ctx, cmd.EmailCommand, publishErr) != nil {
			slog.Warn("QQ remote read Store confirmation failed", "code", safeCode(publishErr))
		}
	}
}

func (s *Service) commitListFailure(ctx context.Context, job app.EmailJob, checkpoint store.EmailSyncCheckpoint, mode string, readerRevision int, cause error) error {
	if emailautomation.ErrorCode(cause) == app.ToolErrorEmailBrowserBusy {
		// No provider attempt occurred. Keep the frozen interval/revision for
		// a short resource yield; do not create a mailbox failure or warning.
		return cause
	}
	code := safeCode(cause)
	if code == "" {
		code = "email_provider_unavailable"
	}
	key := fmt.Sprintf("timeline-list-failure:%s:%d:%s:%s", checkpoint.Mailbox.ID, checkpoint.Mailbox.CheckpointRevision, checkpoint.IntervalStart.UTC().Format(time.RFC3339Nano), checkpoint.IntervalEnd.UTC().Format(time.RFC3339Nano))
	command := store.EmailSyncCommitCommand{EmailCommand: command(job.OwnerID, key), MailboxID: checkpoint.Mailbox.ID, BindingGeneration: checkpoint.Mailbox.BindingGeneration, IntervalStart: checkpoint.IntervalStart, IntervalEnd: checkpoint.IntervalEnd, ProviderMode: mode, Trigger: syncTrigger(job), Actor: syncActor(job), ReaderRevision: readerRevision, ErrorCode: code, FailureScope: app.EmailSyncFailureProviderOperational}
	if emailautomation.LocalOperationalFailure(cause) || code == "email_retry_target_too_large" {
		command.FailureScope = app.EmailSyncFailureLocalOperational
	}
	_, err := s.repository.CommitEmailSync(ctx, command)
	return s.reconcileError(ctx, command.EmailCommand, err)
}

func (s *Service) preparePageCapture(ctx context.Context, owner string, mailbox app.EmailMailbox, providerMessageID string, result app.EmailReadResult, invocation string) (store.EmailSyncCapture, error) {
	out := store.EmailSyncCapture{ProviderMessageID: providerMessageID}
	if result.Capture == nil {
		return out, errors.New("email_capture_missing")
	}
	ref := result.Capture
	version := app.EmailCaptureVersion{ID: ref.CaptureID, ManifestPath: ref.ManifestPath, ManifestSHA256: ref.ManifestSHA256, State: app.EmailCapturePartial, CreatedAt: s.now()}
	if result.Status == "collected" {
		version.State = app.EmailCaptureComplete
	}
	manifest, files, err := loadManifest(ctx, s.opts.WorkspaceRoot, owner, version)
	if err != nil {
		return out, err
	}
	if manifest.Provider != mailbox.Provider || !strings.EqualFold(manifest.AccountAddress, mailbox.Address) || manifest.ProviderMessageID != providerMessageID || manifest.InvocationID != invocation {
		return out, errors.New("email_capture_identity")
	}
	version.CreatedAt = manifest.CapturedAt
	version.ManifestJSON = manifest.RawJSON
	if version.CreatedAt.IsZero() {
		version.CreatedAt = s.now()
	}
	root, err := os.OpenRoot(s.opts.WorkspaceRoot)
	if err != nil {
		return out, err
	}
	defer root.Close()
	for _, file := range files {
		info, err := root.Stat(file.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.Bytes {
			return out, errors.New("email_source_invalid")
		}
	}
	original, ok := files[path.Join(path.Dir(ref.ManifestPath), "message.eml")]
	if !ok {
		return out, errors.New("email_original_missing")
	}
	version.OriginalPath, version.OriginalSHA256 = original.Path, original.SHA256
	readState := ref.ReadState
	if readState != "read" || version.State != app.EmailCaptureComplete {
		readState = "unknown"
	}
	out.Capture, out.ReadState = version, readState
	return out, nil
}

func replayedEmailCommand[T any](ctx context.Context, repository Repository, cmd store.EmailCommand, operation store.StoreOperation) (T, bool, error) {
	var out T
	receipt, found, err := repository.ReconcileEmailCommand(ctx, cmd.OwnerID, cmd.CommandKey)
	if err != nil || !found {
		return out, false, err
	}
	if receipt.Operation != string(operation) || json.Unmarshal(receipt.Result, &out) != nil {
		return out, false, errors.New("email_command_receipt_invalid")
	}
	return out, true, nil
}

func pageDirection(folder string) string {
	if folder == "sent" {
		return "sent"
	}
	if folder == "all" {
		return "unknown"
	}
	return "inbound"
}

func syncTrigger(job app.EmailJob) string {
	if job.SyncTrigger == "manual_refresh" {
		return job.SyncTrigger
	}
	return "scheduled"
}
func syncActor(job app.EmailJob) string {
	if job.SyncTrigger == "manual_refresh" && job.SyncActor != "" {
		return job.SyncActor
	}
	return "system"
}
