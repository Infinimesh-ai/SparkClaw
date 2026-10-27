package emailmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// Exercise a retained failure and a new original in the same subsequent poll.
// The parsed Date deliberately differs from the provider's receipt timestamp.
type retryIsolationBrowser struct {
	*intakeFixture
	provider, scenario string
	received           time.Time
	retryRound         bool
	checkedRetry       bool
}

func (b *retryIsolationBrowser) AdmitIntake(context.Context, string, string) (app.EmailAdmissionBinding, error) {
	return app.EmailAdmissionBinding{Provider: b.provider, Account: app.EmailAccountDefault}, nil
}

func (b *retryIsolationBrowser) target(id string) app.EmailCaptureTarget {
	target := app.EmailCaptureTarget{AccountAddress: "owner@example.com", ProviderMessageID: id, ProviderSelectionID: "thread-" + id, ProviderThreadID: "thread-" + id, Folder: "inbox"}
	if b.provider != app.EmailProviderGmail {
		target.ReceivedAt = &b.received
	}
	if b.provider == app.EmailProviderOutlook {
		target.ProviderNativeID = "native-" + id
	}
	return target
}

func (b *retryIsolationBrowser) capture(owner string, r app.EmailReadRequest, target app.EmailCaptureTarget) (app.EmailPageCapture, error) {
	id := "cap_" + ownerScope(target.ProviderMessageID)[:32]
	dir := path.Join("email", "2026/09/07", ownerScope(owner), "fixture-box", target.ProviderMessageID, "source", id)
	raw := []byte("From: sender@example.com\r\nTo: owner@example.com\r\nDate: Mon, 07 Sep 2026 02:00:00 +0000\r\nSubject: " + target.ProviderMessageID + "\r\n\r\nRetained source.")
	m := sourceManifest{SchemaVersion: 1, Stage: "script_capture", Provider: b.provider, AccountAddress: target.AccountAddress, MailboxID: "fixture-box", MailID: target.ProviderMessageID, ProviderMessageID: target.ProviderMessageID, CaptureID: id, InvocationID: emailautomation.PageCaptureInvocationID(r.InvocationID, b.provider, target), Status: "collected", Acquisition: "rfc822", DatePath: "2026/09/07", ReceivedAt: "2026-09-07T02:00:00Z", ReceivedSource: "eml_date"}
	if target.ReceivedAt != nil && b.scenario != "legacy missing receipt" {
		m.ReceivedDisplay = target.ReceivedAt.Format(time.RFC3339Nano)
	}
	m.Coverage.InventoryComplete, m.Coverage.AttachmentsComplete = true, true
	m.Files = []sourceFile{{Path: path.Join(dir, "message.eml"), Bytes: int64(len(raw)), SHA256: sourceHash(raw)}}
	manifest, _ := json.Marshal(m)
	if err := os.MkdirAll(filepath.Join(b.root, dir), 0700); err != nil {
		return app.EmailPageCapture{}, err
	}
	for name, content := range map[string][]byte{"message.eml": raw, "capture.json": manifest} {
		if err := os.WriteFile(filepath.Join(b.root, dir, name), content, 0600); err != nil {
			return app.EmailPageCapture{}, err
		}
	}
	return app.EmailPageCapture{Target: target, Result: app.EmailReadResult{Status: "collected", Capture: &app.EmailCaptureReceipt{CaptureID: id, MailboxID: m.MailboxID, MailID: m.MailID, ManifestPath: path.Join(dir, "capture.json"), ManifestSHA256: sourceHash(manifest), ReadState: "read"}}}, nil
}

func (b *retryIsolationBrowser) CollectPageForOwner(_ context.Context, owner string, r app.EmailReadRequest) (app.EmailPageResult, error) {
	old := b.target("old")
	page := app.EmailPageResult{SchemaVersion: 1, Provider: b.provider, AccountAddress: old.AccountAddress, PageID: "page_" + strings.Repeat("c", 64), DiscoveryOptions: *r.Discovery, Status: "partial", ObservedAt: r.Discovery.IntervalEnd.Add(time.Second), Captures: []app.EmailPageCapture{}, Failures: []app.EmailPageFailure{}}
	page.Discovery = app.EmailDiscoveryResult{SchemaVersion: 1, Provider: b.provider, Status: "listed", AccountAddress: old.AccountAddress, ObservedAt: page.ObservedAt, Candidates: []app.EmailCaptureTarget{old}, Coverage: app.EmailDiscoveryCoverage{Scope: "inbound_received", Lane: "recent_inbound", ScanComplete: true, BoundaryQualified: true, ScannedRows: 1}}
	if !b.retryRound && b.scenario != "uncaptured" {
		if slices.Contains(r.Discovery.SkipProviderMessageIDs, "old") {
			return page, nil
		}
		captured, err := b.capture(owner, r, old)
		page.Captures = append(page.Captures, captured)
		return page, err
	}
	if b.retryRound {
		if !b.checkedRetry {
			b.checkedRetry = true
			if b.scenario == "legacy missing receipt" && b.provider != app.EmailProviderGmail {
				if len(r.Discovery.RetryTargets) != 0 {
					return page, errors.New("unqualified historical receipt was used as a retry target")
				}
			} else {
				if len(r.Discovery.RetryTargets) != 1 {
					return page, errors.New("retained retry missing")
				}
				retry := r.Discovery.RetryTargets[0]
				if b.provider == app.EmailProviderGmail {
					if retry.ReceivedAt != nil {
						return page, errors.New("Gmail acquired a receipt-time target")
					}
				} else if retry.ReceivedAt == nil || !retry.ReceivedAt.Truncate(time.Microsecond).Equal(b.received.Truncate(time.Microsecond)) {
					return page, errors.New("retry used parsed Date instead of listed receipt time")
				}
				if b.scenario != "uncaptured" && (retry.RecoveryCapture == nil || retry.RecoveryCapture.OriginalSHA256 == "") {
					return page, errors.New("immutable recovery descriptor missing")
				}
				old = retry // Reader keeps the explicit recovery target.
			}
		}
		fresh := b.target("new")
		page.Discovery.Candidates = append(page.Discovery.Candidates, fresh)
		page.Discovery.Coverage.ScannedRows++
		captured, err := b.capture(owner, r, fresh)
		if err != nil {
			return page, err
		}
		if !slices.Contains(r.Discovery.SkipProviderMessageIDs, "new") {
			page.Captures = append(page.Captures, captured)
		}
	}
	if slices.Contains(r.Discovery.SkipProviderMessageIDs, "old") {
		return page, nil
	}
	page.Failures = append(page.Failures, app.EmailPageFailure{Target: old, Scope: app.EmailSyncFailureProviderOperational, ErrorCode: "email_provider_unavailable"})
	return page, nil
}

func TestOldRetryFailureDoesNotBlockNewMailAfterRestart(t *testing.T) {
	for _, provider := range []string{app.EmailProviderGmail, app.EmailProviderOutlook, app.EmailProviderQQMail} {
		for _, scenario := range []string{"uncaptured", "parsed source recovery", "legacy missing receipt"} {
			t.Run(provider+"/"+scenario, func(t *testing.T) {
				state := filepath.Join(t.TempDir(), "state.json")
				repo, err := store.NewFileStore(state)
				if err != nil {
					t.Fatal(err)
				}
				s, base, _ := newFixtureService(t, repo)
				b := &retryIsolationBrowser{intakeFixture: base, provider: provider, scenario: scenario, received: time.Date(2026, 9, 7, 2, 0, 7, 226778200, time.UTC)}
				if provider == app.EmailProviderQQMail {
					b.received = b.received.Truncate(time.Second)
				}
				s.browser = b
				s.opts.QualifiedProviderModes[provider] = app.EmailProviderModeTimeRange
				now := time.Now().UTC().Truncate(time.Microsecond)
				s.now = func() time.Time { return now }
				box, err := s.Configure(t.Context(), "email-owner", provider, true, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.plan(t.Context()); err != nil {
					t.Fatal(err)
				}
				now = now.Add(time.Second)
				if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
					t.Fatal(worked, err)
				}
				if scenario != "uncaptured" {
					if worked, err := s.workOne(t.Context(), []string{app.EmailJobParse}); err != nil || !worked {
						t.Fatal(worked, err)
					}
					mail, _, _ := repo.GetEmailMail(t.Context(), "email-owner", store.EmailMailID("email-owner", box.ID, "old"))
					if mail.SourceTime.Equal(b.received) || mail.ParseState != "ready" {
						t.Fatalf("fixture did not parse a distinct Date: %+v", mail)
					}
					capture, _, _ := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
					if err := os.Remove(filepath.Join(b.root, capture.OriginalPath)); err != nil {
						t.Fatal(err)
					}
					if err := s.reconcileCommittedSources(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				reopened, err := store.NewFileStore(state)
				if err != nil {
					t.Fatal(err)
				}
				s.repository = reopened
				b.retryRound = true
				now = now.Add(time.Minute)
				if _, err := s.Sync(t.Context(), "email-owner", box.ID); err != nil {
					t.Fatal(err)
				}
				if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
					t.Fatal(worked, err)
				}
				after, _, _ := reopened.GetEmailMailbox(t.Context(), "email-owner", box.ID)
				fresh, found, _ := reopened.GetEmailMail(t.Context(), "email-owner", store.EmailMailID("email-owner", box.ID, "new"))
				if !found || fresh.CaptureState != app.EmailCaptureComplete || !after.PollThrough.Equal(now) || after.PendingFailureCount != 1 || after.RefreshPending || after.LastSyncErrorCode != "" {
					t.Fatalf("old failure blocked new original or progress: found=%t capture=%s mailbox=%+v", found, fresh.CaptureState, after)
				}
			})
		}
	}
}
