package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// This opt-in qualification measures storage behavior using preseeded synthetic
// records. It does not qualify browser source bytes or model semantic accuracy.
// Fixture construction deliberately avoids timing 10,000 historical imports.
func TestEmailManagementCapacityQualification(t *testing.T) {
	if os.Getenv("SPARKCLAW_TEST_EMAIL_CAPACITY") != "1" {
		t.Skip("set SPARKCLAW_TEST_EMAIL_CAPACITY=1 for 10,000-mail/1,000-job storage qualification")
	}
	for _, backend := range []string{"file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			const owner = "capacity-owner"
			var repo EmailRepository
			var restart func() EmailRepository
			var box app.EmailMailbox
			if backend == "file" {
				seed := NewMemoryStore()
				f := &emailContractFixture{t: t, repo: seed, owner: owner}
				var err error
				box, err = seed.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderGmail, Address: "capacity@example.com", Enabled: true})
				f.must(err)
				_, err = emailMemoryRun(seed, t.Context(), OperationAdmitEmailDiscovery, owner, "capacity-fixture", nil, true, func(e *emailEngine) (struct{}, error) { return struct{}{}, seedEmailCapacity(e, box) })
				f.must(err)
				raw, err := json.Marshal(seed.snapshot())
				f.must(err)
				path := filepath.Join(t.TempDir(), "capacity.json")
				f.must(os.WriteFile(path, raw, 0600))
				t.Logf("fixture mails=10000 captured=9000 conversations=900 queued_jobs=1000 file_bytes=%d", len(raw))
				restart = func() EmailRepository {
					s, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return s
				}
				repo = restart()
			} else {
				dsn := newPostgresMigrationTestSchema(t)
				s, err := NewPostgresStore(t.Context(), dsn)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(s.Close)
				box, err = s.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: EmailCommand{OwnerID: owner, CommandKey: "binding"}, Provider: app.EmailProviderGmail, Address: "capacity@example.com", Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				_, err = emailPostgresRun(s, t.Context(), OperationAdmitEmailDiscovery, owner, "capacity-fixture", nil, true, func(e *emailEngine) (struct{}, error) { return struct{}{}, seedEmailCapacity(e, box) })
				if err != nil {
					t.Fatal(err)
				}
				repo = s
				restart = func() EmailRepository {
					next, err := NewPostgresStore(t.Context(), dsn)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(next.Close)
					return next
				}
			}
			start := time.Now()
			repo = restart()
			restartLatency := time.Since(start)
			start = time.Now()
			status, err := repo.GetEmailOwnerStatus(t.Context(), owner)
			if err != nil {
				t.Fatal(err)
			}
			statusLatency := time.Since(start)
			if status.CapturedCount != 9000 || status.BacklogCount != 1000 {
				t.Fatalf("fixture counts %+v", status)
			}
			start = time.Now()
			page, err := repo.ListEmailMails(t.Context(), EmailQuery{OwnerID: owner, CapturedOnly: true, Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			listLatency := time.Since(start)
			if len(page.Items) != 50 || page.NextCursor == "" {
				t.Fatal("bounded mail page failed")
			}
			start = time.Now()
			conversations, err := repo.ListEmailConversations(t.Context(), EmailQuery{OwnerID: owner, MailboxID: box.ID, Search: "invoice", Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			searchLatency := time.Since(start)
			if len(conversations.Items) != 50 {
				t.Fatal("bounded conversation content search failed")
			}

			queries, searches := make([]time.Duration, 0, 100), make([]time.Duration, 0, 100)
			for request := 0; request < 100; request++ {
				started := time.Now()
				if request%2 == 0 {
					result, queryErr := repo.ListEmailMails(t.Context(), EmailQuery{OwnerID: owner, CapturedOnly: true, Limit: 50})
					if queryErr != nil || len(result.Items) != 50 {
						t.Fatalf("list request %d: %v", request, queryErr)
					}
				} else {
					mail, found, queryErr := repo.GetEmailMail(t.Context(), owner, page.Items[request%len(page.Items)].ID)
					if queryErr != nil || !found {
						t.Fatalf("detail request %d: %v", request, queryErr)
					}
					representation, found, queryErr := repo.GetEmailRepresentation(t.Context(), owner, mail.RepresentationID)
					if queryErr != nil || !found || representation.BodyText == "" {
						t.Fatalf("detail source %d: %v", request, queryErr)
					}
				}
				queries = append(queries, time.Since(started))
				started = time.Now()
				result, queryErr := repo.ListEmailConversations(t.Context(), EmailQuery{OwnerID: owner, MailboxID: box.ID, Search: "invoice", Limit: 50})
				if queryErr != nil || len(result.Items) != 50 {
					t.Fatalf("search request %d: %v", request, queryErr)
				}
				searches = append(searches, time.Since(started))
			}
			slices.Sort(queries)
			slices.Sort(searches)
			t.Logf("backend=%s list_detail_requests=100 p95=%s max=%s conversation_search_requests=100 p95=%s max=%s", backend, queries[94], queries[99], searches[94], searches[99])
			if queries[94] > time.Second {
				t.Errorf("list/detail p95 %s exceeds the stage5 1-second threshold", queries[94])
			}
			_, overflowErr := repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: EmailCommand{OwnerID: owner, CommandKey: "capacity-overflow"}, MailboxID: box.ID, BindingGeneration: box.BindingGeneration, MaxPendingJobs: 1000, ObservedAt: time.Now(), Cursor: "unadmitted-boundary", Coverage: "partial", Members: []EmailDiscoveryMember{{ProviderMessageID: "overflow-candidate", ProviderSelectionID: "overflow-candidate", Direction: "inbound"}}})
			if StoreErrorCodeOf(overflowErr) != StoreErrorConflict {
				t.Fatalf("capacity overflow outcome: %v", overflowErr)
			}
			afterOverflow, _, queryErr := repo.GetEmailMailbox(t.Context(), owner, box.ID)
			if queryErr != nil || afterOverflow.Cursor != "" || !afterOverflow.Boundary.Equal(box.Boundary) {
				t.Fatal("overflow advanced unadmitted discovery progress")
			}
			start = time.Now()
			j, ok, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: owner, Kinds: []string{app.EmailJobCapture}})
			if err != nil || !ok {
				t.Fatalf("recovery claim ok=%v err=%v", ok, err)
			}
			claimLatency := time.Since(start)
			start = time.Now()
			_, err = repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: EmailCommand{OwnerID: owner, CommandKey: "capacity-new-arrival"}, MailboxID: box.ID, BindingGeneration: box.BindingGeneration, ObservedAt: time.Now(), Coverage: "partial", Trigger: "recent_inbound", Members: []EmailDiscoveryMember{{ProviderMessageID: "after-restart", ProviderSelectionID: "after-restart", Direction: "inbound"}}})
			if err != nil {
				t.Fatal(err)
			}
			admitLatency := time.Since(start)
			t.Logf("backend=%s restart=%s status=%s mail_page_50=%s conversation_search_50=%s recovered_claim=%s admit_one=%s job_attempt=%d", backend, restartLatency, statusLatency, listLatency, searchLatency, claimLatency, admitLatency, j.Attempt)
			if restartLatency > 60*time.Second || claimLatency > 30*time.Second || admitLatency > 30*time.Second {
				t.Fatal("bounded recovery/write budget exceeded")
			}
		})
	}
}
func seedEmailCapacity(e *emailEngine, box app.EmailMailbox) error {
	now := e.now
	for n := 0; n < 10000; n++ {
		id := fmt.Sprintf("fixture-mail-%05d", n)
		m := app.EmailMail{ID: id, OwnerID: e.owner, MailboxID: box.ID, ProviderMessageID: id, ProviderSelectionID: id, Direction: "inbound", SourceTime: now.Add(time.Duration(n) * time.Second), DiscoveredAt: now, ArrivalSequence: int64(n + 1), InputVersion: 1, Subject: "invoice reference", Participants: []string{"supplier@example.com"}, AssignmentState: app.EmailAssignmentPending, CaptureState: "pending", ParseState: "pending"}
		if n < 9000 {
			m.CaptureID = "capture-" + id
			m.RepresentationID = "representation-" + id
			m.ConversationID = fmt.Sprintf("fixture-conversation-%04d", n/10)
			m.CaptureState = app.EmailCaptureComplete
			m.ParseState = app.EmailParseReady
			m.AssignmentState = app.EmailAssignmentAssigned
			if n%10 == 0 {
				emailSaveConversation(e, app.EmailConversation{ID: m.ConversationID, OwnerID: e.owner, Title: "procurement", MemberCount: 10, UnseenCount: 10, MembershipVersion: 10, InputVersion: 10, MailboxIDs: []string{box.ID}, CreatedAt: now, UpdatedAt: m.SourceTime})
			}
			emailPut(e, "capture", m.CaptureID, id, "", "", "", m.CaptureID, app.EmailCaptureVersion{ID: m.CaptureID, MailID: id, ManifestPath: "fixtures/manifest.json", ManifestSHA256: strings.Repeat("a", 64), OriginalPath: "fixtures/original.eml", OriginalSHA256: strings.Repeat("b", 64), State: app.EmailCaptureComplete, CreatedAt: now})
			emailPut(e, "representation", m.RepresentationID, id, "", "", "", m.RepresentationID, app.EmailRepresentation{ID: m.RepresentationID, MailID: id, CaptureID: m.CaptureID, Subject: m.Subject, BodyText: strings.Repeat("invoice item details ", 32), State: app.EmailParseReady, CreatedAt: now})
		}
		emailSaveMail(e, m)
		if n >= 9000 {
			if _, err := emailRequest(e, EmailJobRequest{Kind: app.EmailJobCapture, TargetID: id, MailboxID: box.ID, BindingGeneration: box.BindingGeneration}); err != nil {
				return err
			}
		}
		if e.err != nil {
			return e.err
		}
	}
	return e.err
}
