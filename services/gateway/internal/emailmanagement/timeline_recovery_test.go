package emailmanagement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestTimelineJournalRecoveryBeforeRenameAndMissingOriginal(t *testing.T) {
	for _, mode := range []string{"staged", "landed", "missing", "tampered", "foreign_stage"} {
		t.Run(mode, func(t *testing.T) {
			repo := store.NewMemoryStore()
			s, base, _ := newFixtureService(t, repo)
			target := app.EmailCaptureTarget{AccountAddress: "owner@example.com", ProviderMessageID: "message-1", ProviderSelectionID: "selection-1", Folder: "inbox"}
			request := app.EmailReadRequest{Provider: app.EmailProviderGmail, InvocationID: "email_changes_" + strings.Repeat("a", 64) + "_r2", Discovery: &app.EmailDiscoveryOptions{ProviderMode: app.EmailProviderModeTimeRange, AccountAddress: target.AccountAddress, Lane: "recent_inbound", IntervalStart: time.Now().UTC(), IntervalEnd: time.Now().UTC().Add(time.Minute), Limit: 50}}
			invocation := emailautomation.PageCaptureInvocationID(request.InvocationID, request.Provider, target)
			receipt, err := fixtureCapture(t.Context(), base.root, "email-owner", invocation, target.ProviderMessageID)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256([]byte(request.Provider + "\x00" + invocation))
			staging := path.Join("email", ownerScope("email-owner"), "staging", receipt.MailboxID, receipt.MailID, "attempt_"+hex.EncodeToString(digest[:]))
			final := filepath.Join(base.root, path.Dir(receipt.ManifestPath))
			if mode == "staged" {
				if err = os.MkdirAll(filepath.Dir(filepath.Join(base.root, staging)), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.Rename(final, filepath.Join(base.root, staging)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing" {
				if err = os.Remove(filepath.Join(final, "message.eml")); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "tampered" {
				file := filepath.Join(final, "message.eml")
				raw, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				raw[len(raw)-1] ^= 1
				if err = os.WriteFile(file, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			result := app.EmailReadResult{Provider: request.Provider, Status: "collected", Capture: &receipt}
			page := app.EmailPageResult{Provider: request.Provider, AccountAddress: target.AccountAddress, DiscoveryOptions: *request.Discovery, Captures: []app.EmailPageCapture{{Target: target, Result: result}}, Failures: []app.EmailPageFailure{}}
			if mode == "foreign_stage" {
				staging = "../outside"
			}
			batch := map[string]any{"schema_version": 1, "provider": request.Provider, "invocation_id": request.InvocationID, "entries": []any{map[string]any{"target": target, "result": result, "staging": staging}}, "result": page}
			raw, _ := json.Marshal(batch)
			batchDigest := sha256.Sum256([]byte(request.Provider + "\x00" + request.InvocationID))
			journal := filepath.Join(base.root, "email", ownerScope("email-owner"), "batches", hex.EncodeToString(batchDigest[:])+".json")
			if err = os.MkdirAll(filepath.Dir(journal), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(journal, raw, 0600); err != nil {
				t.Fatal(err)
			}
			recovered, found, err := s.recoverTimelineBatch(t.Context(), "email-owner", request)
			if mode == "foreign_stage" {
				if err == nil {
					t.Fatal("foreign staging accepted")
				}
				return
			}
			if err != nil || !found {
				t.Fatalf("recover: %v %v", found, err)
			}
			if mode == "missing" || mode == "tampered" {
				if len(recovered.Captures) != 0 || len(recovered.Failures) != 1 || recovered.Failures[0].Qualified || recovered.Failures[0].Scope != app.EmailSyncFailureLocalOperational {
					t.Fatalf("missing source did not become exact operational failure: %+v", recovered)
				}
			} else if len(recovered.Captures) != 1 || len(recovered.Failures) != 0 {
				t.Fatalf("valid source was not adopted: %+v", recovered)
			}
			if base.captureCalls() != 0 {
				t.Fatal("local recovery downloaded from provider")
			}
		})
	}
}
