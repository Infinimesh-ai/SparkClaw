package emailmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

func (s *Service) collectPages(ctx context.Context, job app.EmailJob, browser PageBrowser) error {
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
	if !upper.After(lower) {
		upper = lower.Add(time.Microsecond)
	}
	beginKey := fmt.Sprintf("timeline-begin:%s:%d:%d", mailbox.ID, mailbox.BindingGeneration, mailbox.CheckpointRevision+1)
	checkpoint, err := s.repository.BeginEmailSync(ctx, store.EmailSyncBeginCommand{EmailCommand: command(job.OwnerID, beginKey), MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, ProviderMode: mode, ProviderCursor: mailbox.ProviderCursor, UpperBound: upper, Trigger: syncTrigger(job), Actor: syncActor(job)})
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
		mail, found, readErr := s.repository.GetEmailMail(ctx, job.OwnerID, failure.MailID)
		if readErr != nil {
			return readErr
		}
		if !found || mail.SyncState == app.EmailMailSyncSuppressed {
			continue
		}
		target := app.EmailCaptureTarget{AccountAddress: mailbox.Address, ProviderMessageID: mail.ProviderMessageID, ProviderNativeID: mail.ProviderNativeID, ProviderSelectionID: mail.ProviderSelectionID, ProviderThreadID: mail.ProviderThreadID, Folder: mail.Folder}
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
	if recovered, found, recoveryErr := s.recoverTimelineBatch(ctx, job.OwnerID, binding); found || recoveryErr != nil {
		if recoveryErr != nil {
			return recoveryErr
		}
		return s.publishIncrementalPage(ctx, job, mailbox, binding, recovered, checkpoint, retryMails, mode)
	}
	admitted, err := s.browserBinding(ctx, job.OwnerID, mailbox.Provider, binding.InvocationID)
	if err != nil {
		return s.commitListFailure(ctx, job, checkpoint, mode, registered.CollectPage.Revision, err)
	}
	admitted.ScriptRevision, admitted.Discovery = registered.CollectPage.Revision, binding.Discovery
	binding = admitted
	page, err := browser.CollectPageForOwner(ctx, job.OwnerID, binding)
	if err != nil {
		return s.commitListFailure(ctx, job, checkpoint, mode, registered.CollectPage.Revision, err)
	}
	if page.DiscoveryOptions.Lane != "recent_inbound" || !page.DiscoveryOptions.IntervalStart.Equal(checkpoint.IntervalStart) || !page.DiscoveryOptions.IntervalEnd.Equal(checkpoint.IntervalEnd) || !strings.EqualFold(page.AccountAddress, mailbox.Address) {
		return s.commitListFailure(ctx, job, checkpoint, mode, registered.CollectPage.Revision, errors.New("email_account_changed"))
	}
	return s.publishIncrementalPage(ctx, job, mailbox, binding, page, checkpoint, retryMails, mode)
}

func (s *Service) publishIncrementalPage(ctx context.Context, job app.EmailJob, mailbox app.EmailMailbox, request app.EmailReadRequest, page app.EmailPageResult, checkpoint store.EmailSyncCheckpoint, retryMails map[string]app.EmailMail, mode string) error {
	key := fmt.Sprintf("timeline-source:%s:%d:%s:%s", mailbox.ID, checkpoint.Mailbox.CheckpointRevision, checkpoint.IntervalStart.UTC().Format(time.RFC3339Nano), checkpoint.IntervalEnd.UTC().Format(time.RFC3339Nano))
	members := make([]store.EmailDiscoveryMember, 0, len(page.Discovery.Candidates))
	for _, target := range page.Discovery.Candidates {
		members = append(members, store.EmailDiscoveryMember{ProviderMessageID: target.ProviderMessageID, ProviderNativeID: target.ProviderNativeID, ProviderSelectionID: target.ProviderSelectionID, ProviderThreadID: target.ProviderThreadID, Folder: target.Folder, Direction: pageDirection(target.Folder), Reason: "recent_inbound", RemoteReadState: "unknown"})
	}
	captures := []store.EmailSyncCapture{}
	outcomes := []store.EmailSyncFailureOutcome{}
	for _, captured := range page.Captures {
		invocation := emailautomation.PageCaptureInvocationID(request.InvocationID, mailbox.Provider, captured.Target)
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
	for _, failure := range page.Failures {
		outcomes = append(outcomes, store.EmailSyncFailureOutcome{ProviderMessageID: failure.Target.ProviderMessageID, Stage: "original", Scope: failure.Scope, ErrorCode: failure.ErrorCode, Qualified: failure.Qualified})
	}
	coverage := page.Discovery.Coverage
	overflow := coverage.Continuation != "" || coverage.Reason == "network_page_continues"
	complete := coverage.ScanComplete && coverage.BoundaryQualified && coverage.UnsupportedRows == 0 && !coverage.Limited && !overflow
	commit := store.EmailSyncCommitCommand{EmailCommand: command(job.OwnerID, key+":commit"), MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, IntervalStart: checkpoint.IntervalStart, IntervalEnd: checkpoint.IntervalEnd, ProviderMode: mode, Trigger: syncTrigger(job), Actor: syncActor(job), InvocationID: request.InvocationID, ReaderRevision: request.ScriptRevision, Complete: complete, Overflow: overflow, UnsupportedItems: coverage.UnsupportedRows, ErrorCode: coverage.Reason, FailureScope: app.EmailSyncFailureProviderOperational, Outcomes: outcomes, Members: members, Captures: captures, Lease: lease(job, s.now()), ObservedAt: page.Discovery.ObservedAt}
	_, err := s.repository.CommitEmailSync(ctx, commit)
	if err = s.reconcileError(ctx, commit.EmailCommand, err); err != nil {
		return err
	}
	// Refetchable source bytes were journaled before their atomic rename. The
	// journal may be removed only after the composite Store receipt is known.
	digest := sha256.Sum256([]byte(mailbox.Provider + "\x00" + request.InvocationID))
	root, openErr := os.OpenRoot(s.opts.WorkspaceRoot)
	if openErr == nil {
		defer root.Close()
		_ = root.Remove(path.Join("email", ownerScope(job.OwnerID), "batches", hex.EncodeToString(digest[:])+".json"))
	}
	return nil
}

func (s *Service) commitListFailure(ctx context.Context, job app.EmailJob, checkpoint store.EmailSyncCheckpoint, mode string, readerRevision int, cause error) error {
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
