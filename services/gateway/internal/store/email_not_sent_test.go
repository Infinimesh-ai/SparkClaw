package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func negativeProof(invocation string) *app.EmailNotSentProof {
	return &app.EmailNotSentProof{SchemaVersion: 1, Kind: "pre_dispatch_failure", InvocationID: invocation, TaskID: "original-task", IntentDigest: strings.Repeat("a", 64), ResourceDigest: strings.Repeat("b", 64), BindingDigest: strings.Repeat("c", 64), LedgerEpoch: 2, Reason: "EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED"}
}
func TestEmailDraftNegativeProofRequiresOriginalAttemptAndCAS(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var repo EmailComposeRepository = NewMemoryStore()
			file := filepath.Join(t.TempDir(), "state.json")
			reopen := func() {
				s, err := NewFileStore(file)
				if err != nil {
					t.Fatal(err)
				}
				repo = s
			}
			if backend == "file" {
				reopen()
			}
			if backend == "postgres" {
				s, err := NewPostgresStore(t.Context(), newPostgresMigrationTestSchema(t))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(s.Close)
				repo = s
			}
			saved, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: "owner", Action: "save", Draft: EmailDraft{ID: "same-draft", Body: "approved body", Mode: "compose"}})
			if err != nil {
				t.Fatal(err)
			}
			started, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: "owner", Action: "begin", Draft: EmailDraft{ID: saved.Draft.ID}, ExpectedVersion: saved.Draft.Version, SendKey: "original-click"})
			if err != nil {
				t.Fatal(err)
			}
			unknown, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: "owner", Action: "finish", Draft: EmailDraft{ID: saved.Draft.ID, State: "unknown"}, SendKey: "original-click"})
			if err != nil {
				t.Fatal(err)
			}
			proof := negativeProof(started.Draft.Snapshot.InvocationID)
			command := EmailDraftCommand{OwnerID: "owner", Action: "resolve_not_sent", Draft: EmailDraft{ID: saved.Draft.ID, State: "failed"}, ExpectedVersion: unknown.Draft.Version, SendKey: "original-click", Receipt: &app.EmailSendResult{Provider: "qq_mail", Status: "not_sent", NotSent: proof}, ErrorCode: "email_send_not_dispatched"}
			for _, change := range []string{"version", "key", "invocation", "proof", "sent-id", "owner"} {
				bad := command
				receipt := *command.Receipt
				p := *proof
				receipt.NotSent = &p
				bad.Receipt = &receipt
				switch change {
				case "version":
					bad.ExpectedVersion--
				case "key":
					bad.SendKey = "different"
				case "invocation":
					p.InvocationID = "different"
				case "proof":
					receipt.NotSent = nil
				case "sent-id":
					receipt.ProviderMessageID = "sent-id"
				case "owner":
					bad.OwnerID = "other"
				}
				if _, err := repo.ChangeEmailDraft(t.Context(), bad); err == nil {
					t.Fatalf("%s bypassed original attempt proof", change)
				}
			}
			failed, err := repo.ChangeEmailDraft(t.Context(), command)
			if err != nil {
				t.Fatal(err)
			}
			if failed.Draft.State != "failed" || failed.Draft.ConfirmationSource != "runtime_not_sent_proof" || failed.Draft.Snapshot.InvocationID != proof.InvocationID || failed.Draft.Snapshot.Receipt.NotSent.IntentDigest != proof.IntentDigest || failed.Draft.SendKey != "original-click" {
				t.Fatalf("lost original attempt: %+v", failed.Draft)
			}
			if backend == "file" {
				reopen()
				rows, err := repo.ListEmailDrafts(t.Context(), "owner", saved.Draft.ID)
				if err != nil || len(rows) != 1 || rows[0].Receipt.NotSent.ResourceDigest != proof.ResourceDigest {
					t.Fatalf("proof not durable: %+v %v", rows, err)
				}
			}
			if _, err = repo.ChangeEmailDraft(t.Context(), command); err == nil {
				t.Fatal("stale proof CAS overwrote resolved draft")
			}
			edited, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: "owner", Action: "save", Draft: EmailDraft{ID: saved.Draft.ID, Body: "new review", Mode: "compose"}, ExpectedVersion: failed.Draft.Version})
			if err != nil {
				t.Fatal(err)
			}
			next := EmailDraftCommand{OwnerID: "owner", Action: "begin", Draft: EmailDraft{ID: saved.Draft.ID}, ExpectedVersion: edited.Draft.Version, SendKey: "original-click"}
			if _, err = repo.ChangeEmailDraft(t.Context(), next); err == nil {
				t.Fatal("old attempt key was erased")
			}
			next.SendKey = "new-explicit-review"
			begun, err := repo.ChangeEmailDraft(t.Context(), next)
			if err != nil || !begun.Execute || begun.Draft.ID != saved.Draft.ID || begun.Draft.Snapshot.InvocationID == proof.InvocationID {
				t.Fatalf("new review did not create a distinct attempt on same draft: %+v %v", begun, err)
			}
			if _, err = repo.ChangeEmailDraft(t.Context(), command); err == nil {
				t.Fatal("old proof changed new attempt")
			}
		})
	}
}
