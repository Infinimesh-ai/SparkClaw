package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// Exercise the same typed Runtime -> backend assembly used by bootstrap;
// direct concrete-store fixtures would not catch a missing repository field.
func TestBackendRuntimeWiresEmailPresentationRepository(t *testing.T) {
	for _, kind := range []store.BackendKind{store.BackendMemory, store.BackendFile} {
		t.Run(string(kind), func(t *testing.T) {
			runtime, err := store.NewRuntime(t.Context(), store.RuntimeOptions{Backend: kind, File: store.FileStoreOptions{Path: filepath.Join(t.TempDir(), "state.json")}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close(context.Background()) })
			st := backendFromRuntime(runtime)
			if st.EmailPresentationRepository == nil {
				t.Fatal("bootstrap lost mandatory presentation repository")
			}
			owner := "presentation-bootstrap-owner"
			command := func() store.EmailCommand { return store.EmailCommand{OwnerID: owner, CommandKey: app.NewID("test")} }
			box, err := st.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: command(), Provider: app.EmailProviderGmail, Address: "owner@example.com", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			admission, err := st.AdmitEmailDiscovery(t.Context(), store.EmailDiscoveryCommand{EmailCommand: command(), MailboxID: box.ID, BindingGeneration: box.BindingGeneration, ObservedAt: time.Now(), Coverage: "partial", Members: []store.EmailDiscoveryMember{{ProviderMessageID: "source", ProviderSelectionID: "source", Direction: "inbound"}}})
			if err != nil {
				t.Fatal(err)
			}
			query := store.EmailPresentationQuery{OwnerID: owner, TargetKind: "mail", TargetIDs: []string{admission.Mails[0].ID}, Language: "zh"}
			items, err := st.EnsureEmailPresentations(t.Context(), store.EmailPresentationCommand{EmailCommand: command(), Query: query})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].State != "queued" {
				t.Fatal("production backend did not queue presentation")
			}
			job, found, err := st.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: owner, Kinds: []string{app.EmailJobPresentation}, LeaseDuration: time.Minute})
			if err != nil || !found || job.TargetID != items[0].ID {
				t.Fatalf("runtime repositories do not share durable state: found=%v err=%v", found, err)
			}
		})
	}
}
