package emailmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// PageBrowser keeps discovery, export and read confirmation in one task page.
// Legacy single-message APIs remain available to explicit read workflows.
var errPageIncomplete = errors.New("email_page_capture_incomplete")

type PageBrowser interface {
	CollectPageForOwner(context.Context, string, app.EmailReadRequest) (app.EmailPageResult, error)
}

func (s *Service) collectPages(ctx context.Context, job app.EmailJob, browser PageBrowser) error {
	mailbox, err := s.activeMailbox(ctx, job)
	if err != nil {
		return err
	}
	binding, err := s.browserBinding(ctx, job.OwnerID, mailbox.Provider, "email_page_"+job.ID, app.EmailJobDiscover)
	if err != nil {
		return err
	}
	registered, _ := s.registry.Get(mailbox.Provider)
	binding.ScriptRevision = registered.CollectPage.Revision
	var failures []error
	for _, scan := range []string{"recent_observation", "recent_inbound"} {
		mailbox, err := s.activeMailbox(ctx, job)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		if scan == "recent_observation" && mailbox.Cursor == "" {
			continue
		}
		position := discoveryCursor{observationOnly: scan == "recent_observation"}
		lane := scan
		if position.observationOnly {
			lane = "recent_inbound"
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
			if !position.observationOnly && mailbox.Cursor != "" {
				if json.Unmarshal([]byte(mailbox.Cursor), &position) != nil {
					return errors.New("email_cursor_invalid")
				}
			}
		}
		request := binding
		// Mailbox identity survives pause/re-enable while account switches get
		// a different identity. Thus paused interval checkpoints remain recoverable.
		request.InvocationID = "email_page_" + mailbox.ID + "_" + scan
		request.AckPageID = mailbox.PageAcks[scan]
		request.Discovery = &app.EmailDiscoveryOptions{Lane: lane, AccountAddress: mailbox.Address, IntervalStart: position.Start, IntervalEnd: position.End, Continuation: position.Continuation, Limit: 50}
		result, err := browser.CollectPageForOwner(ctx, job.OwnerID, request)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if result.DiscoveryOptions.Lane != lane || !strings.EqualFold(result.AccountAddress, mailbox.Address) {
			return errors.New("email_account_changed")
		}
		// Replay carries its original interval: a restarted caller's new wall clock
		// must never advance a boundary beyond what the checkpoint actually scanned.
		position.Start, position.End, position.Continuation = result.DiscoveryOptions.IntervalStart, result.DiscoveryOptions.IntervalEnd, result.DiscoveryOptions.Continuation
		if err := s.publishPage(ctx, job, mailbox, request, result, position, scan); err != nil {
			if errors.Is(err, errPageIncomplete) {
				continue
			}
			return err
		}
	}
	return errors.Join(failures...)
}

func (s *Service) publishPage(ctx context.Context, job app.EmailJob, mailbox app.EmailMailbox, request app.EmailReadRequest, page app.EmailPageResult, position discoveryCursor, scan string) error {
	key := request.InvocationID + ":" + page.PageID + ":" + job.LeaseToken
	incomplete := len(page.Failures) > 0

	mails := make(map[string]app.EmailMail, len(page.Discovery.Candidates))
	if len(page.Discovery.Candidates) > 0 {
		admission := store.EmailDiscoveryCommand{EmailCommand: command(job.OwnerID, key+":admit"), Lease: lease(job, s.now()), MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, PageBatch: true, MaxPendingJobs: 1000, ObservedAt: page.Discovery.ObservedAt, Cursor: mailbox.Cursor, Coverage: "partial", Trigger: scan}
		for _, target := range page.Discovery.Candidates {
			admission.Members = append(admission.Members, store.EmailDiscoveryMember{ProviderMessageID: target.ProviderMessageID, ProviderSelectionID: target.ProviderSelectionID, ProviderThreadID: target.ProviderThreadID, Folder: target.Folder, Direction: pageDirection(target.Folder), Reason: scan, RemoteReadState: "unknown"})
		}
		admitted, err := s.repository.AdmitEmailDiscovery(ctx, admission)
		if err != nil {
			return err
		}
		for _, mail := range admitted.Mails {
			mails[mail.ProviderMessageID] = mail
		}
	}
	for _, captured := range page.Captures {
		mail, ok := mails[captured.Target.ProviderMessageID]
		if !ok {
			return errors.New("email_page_target_missing")
		}
		invocation := emailautomation.PageCaptureInvocationID(request.InvocationID, mailbox.Provider, captured.Target)
		cmd := command(job.OwnerID, key+":capture:"+mail.ID)
		if err := s.publishPageCapture(ctx, job, mailbox, mail, captured.Result, invocation, cmd); err != nil {
			return err
		}
	}
	// Only after all durable receipts are ingested may progress and acknowledgement
	// move forward. A replay of the same page uses identical command receipts.
	// An empty page goes straight here: record only its actual observed coverage
	// and acknowledge it normally, without admission/capture work or a retry.
	progress := store.EmailDiscoveryCommand{EmailCommand: command(job.OwnerID, key+":progress"), Lease: lease(job, s.now()), MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, PageBatch: true, ObservedAt: page.Discovery.ObservedAt, Cursor: mailbox.Cursor, Coverage: "partial", Trigger: scan}
	if page.Discovery.Coverage.Reason != "" {
		progress.Gaps = append(progress.Gaps, page.Discovery.Coverage.Reason)
	}
	for _, failure := range page.Failures {
		progress.Gaps = append(progress.Gaps, failure.ErrorCode)
	}
	if page.Discovery.Coverage.ScanComplete && !incomplete {
		progress.Coverage = "complete_for_observation"
	}
	if !position.End.IsZero() && !position.observationOnly {
		if page.Discovery.Coverage.ScanComplete && page.Discovery.Coverage.BoundaryQualified && !incomplete {
			progress.CompletedBoundary = position.End
			progress.Cursor = ""
			progress.Coverage = "complete"
		} else {
			// An unfinished page is revisited on a later round instead of losing failed
			// exports by advancing to the next page. Successful originals are reusable.
			if !incomplete {
				position.Continuation = page.Discovery.Coverage.Continuation
			}
			raw, _ := json.Marshal(position)
			progress.Cursor = string(raw)
		}
	}
	if _, err := s.repository.AdmitEmailDiscovery(ctx, progress); err != nil {
		return err
	}
	if incomplete {
		return errPageIncomplete
	}
	ack := store.EmailDiscoveryCommand{EmailCommand: command(job.OwnerID, key+":ack"), Lease: lease(job, s.now()), MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, PageBatch: true, Trigger: scan, AcknowledgedPageID: page.PageID}
	_, err := s.repository.AdmitEmailDiscovery(ctx, ack)
	return err
}

func (s *Service) publishPageCapture(ctx context.Context, job app.EmailJob, mailbox app.EmailMailbox, mail app.EmailMail, result app.EmailReadResult, invocation string, cmd store.EmailCommand) error {
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
	if manifest.Provider != mailbox.Provider || !strings.EqualFold(manifest.AccountAddress, mailbox.Address) || manifest.ProviderMessageID != mail.ProviderMessageID || manifest.InvocationID != invocation {
		return errors.New("email_capture_identity")
	}
	version.CreatedAt = manifest.CapturedAt
	if version.CreatedAt.IsZero() {
		version.CreatedAt = mail.DiscoveredAt
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
	readState := ref.ReadState
	if readState != "read" || version.State != app.EmailCaptureComplete {
		readState = "unknown"
	}
	_, err = s.repository.PublishEmailCapture(ctx, store.EmailCaptureCommand{EmailCommand: cmd, MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, Lease: lease(job, s.now()), Capture: version, PageBatch: true, ReadState: readState})
	return s.reconcileError(ctx, cmd, err)
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
