package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestFileStorePersistsOnlyISCPOnboardingReceipt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	receipt, err := st.SaveISCPOnboarding(context.Background(), testISCPOnboarding(now, "iscp_onboarding_file", app.DefaultOwnerID))
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := reloaded.GetISCPOnboarding(context.Background(), receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.AuthorityRef != receipt.AuthorityRef || got.TicketID != receipt.TicketID {
		t.Fatalf("ISCP onboarding receipt did not survive restart: %#v ok=%v", got, ok)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"signature"`) || strings.Contains(string(raw), "signed-ticket-value") {
		t.Fatalf("file Store persisted Pairing Ticket secret material: %s", raw)
	}
}

func TestFileStoreDoesNotRetainISCPOnboardingWhenPersistenceFails(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := newTestFileStore(filepath.Join(parentFile, "state.json"))
	receipt, err := st.SaveISCPOnboarding(context.Background(), testISCPOnboarding(time.Now().UTC(), "iscp_onboarding_rollback", app.DefaultOwnerID))
	listed, listErr := st.ListISCPOnboardings(context.Background(), "")
	if err == nil || receipt.ID != "" || listErr != nil || len(listed) != 0 {
		t.Fatalf("failed onboarding persistence returned or retained a receipt: receipt=%#v count=%d err=%v list_err=%v", receipt, len(listed), err, listErr)
	}
}

func TestMCPAccessTicketRedemptionIsAtomicAndDeviceBound(t *testing.T) {
	st := NewMemoryStore()
	now := time.Now().UTC()
	ticket, err := st.SaveMCPAccessTicket(testMCPAccessTicket(now, "secret-hash"))
	if err != nil {
		t.Fatal(err)
	}
	peer := app.MCPPeerIdentity{DomainID: ticket.DomainID, DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}

	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.RedeemMCPAccessTicket(ticket.SecretHash, peer, now.Add(time.Second))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrMCPAccessTicketInvalid) {
			t.Fatalf("unexpected redemption error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful redemptions = %d, want 1", succeeded)
	}
	binding, ok := st.FindMCPBindingForPeer(peer.DomainID, peer.DeviceID, peer.KeyThumbprint)
	if !ok || binding.OwnerID != ticket.OwnerID || binding.ActorID != ticket.ActorID || binding.RequesterDeviceID == binding.ActorID {
		t.Fatalf("binding did not preserve requester/executor separation: %#v ok=%v", binding, ok)
	}
	if session, ok := mustGetSession(t, st, binding.LinkedSessionID); !ok || session.Hidden || session.OwnerID != binding.OwnerID || session.Title != "AI · device-a" || session.Source != "mcp" {
		t.Fatalf("binding session was not created atomically: %#v ok=%v", session, ok)
	}
	if sessions := mustListSessions(t, st); len(sessions) != 1 || sessions[0].ID != binding.LinkedSessionID {
		t.Fatalf("binding conversation was not visible in the ordinary session list: %#v", sessions)
	}
	if _, ok := st.FindMCPBindingForPeer(peer.DomainID, "device-b", peer.KeyThumbprint); ok {
		t.Fatal("device substitution found a binding")
	}
}

func TestFileStorePersistsMCPAccessWithoutPlaintextSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ticket, err := st.SaveMCPAccessTicket(testMCPAccessTicket(now, "sha256-only"))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := st.RedeemMCPAccessTicket(ticket.SecretHash, app.MCPPeerIdentity{
		DomainID: ticket.DomainID, DeviceID: "device-file", KeyThumbprint: "thumb-file", ISCPSessionID: "iscp-file",
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	operation, created, err := st.CreateMCPOperation(app.MCPOperation{
		ID: "mcp_operation_file", BindingID: binding.ID, IdempotencyKey: "idem-file", Fingerprint: "fp-file",
		Invocation: app.MCPInvocationContext{SchemaVersion: app.MCPInvocationSchemaVersion, ID: "inv-file", RunID: "run-file"},
	})
	if err != nil || !created {
		t.Fatalf("create operation: created=%v err=%v", created, err)
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reloaded.GetMCPBinding(binding.ID); !ok || got.RequesterDeviceID != binding.RequesterDeviceID {
		t.Fatalf("binding did not persist: %#v ok=%v", got, ok)
	}
	if got, ok := reloaded.GetMCPOperation(operation.ID); !ok || got.Invocation.ID != "inv-file" {
		t.Fatalf("operation did not persist: %#v ok=%v", got, ok)
	}
	if session, ok := mustGetSession(t, reloaded, binding.LinkedSessionID); !ok || session.Hidden || session.Title != "AI · device-file" {
		t.Fatalf("visible MCP conversation did not survive restart: %#v ok=%v", session, ok)
	}
	if got, ok := reloaded.FindMCPAccessTicketBySecretHash(ticket.SecretHash); !ok || got.SecretHash != "sha256-only" {
		t.Fatalf("ticket hash did not persist: %#v ok=%v", got, ok)
	}
}

func TestFileStoreNormalizesLegacyHiddenMCPConversation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-mcp-state.json")
	now := time.Now().UTC()
	binding := app.MCPBinding{
		ID: "mcp_binding_legacy", OwnerID: app.DefaultOwnerID, RequesterDeviceID: "legacy-device-identifier",
		LinkedSessionID: "s_mcp_binding_legacy", CreatedAt: now, UpdatedAt: now,
	}
	snapshot := Snapshot{
		Sessions: map[string]app.Session{binding.LinkedSessionID: {
			ID: binding.LinkedSessionID, OwnerID: binding.OwnerID, Title: "External MCP", Source: "mcp", Hidden: true,
		}},
		MCPBindings: map[string]app.MCPBinding{binding.ID: binding},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session, ok := mustGetSession(t, st, binding.LinkedSessionID)
	if !ok || session.Hidden || session.Title != "AI · legacy-devic" || session.Source != "mcp" {
		t.Fatalf("legacy MCP conversation was not normalized: %#v ok=%v", session, ok)
	}
}

func TestFileStorePersistsRequestedMediaRequirements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requested-media-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSessionWithScope(t, st, "AI · device", app.DefaultOwnerID, "", "mcp", false)
	mustAddMessage(t, st, app.Message{
		SessionID: session.ID, Role: "user", Content: "",
		RequestedMedia: []app.MessageMediaLocator{{Name: "report.pdf", Caption: "Latest report"}},
	})
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	messages := mustListMessages(t, reloaded, session.ID)
	if len(messages) != 1 || strings.TrimSpace(messages[0].Content) != "" || len(messages[0].Attachments) != 0 ||
		len(messages[0].RequestedMedia) != 1 || messages[0].RequestedMedia[0].Name != "report.pdf" ||
		messages[0].RequestedMedia[0].Caption != "Latest report" {
		t.Fatalf("requested media requirement did not survive file Store restart: %#v", messages)
	}
}

func TestMCPOperationIdempotencyRejectsChangedRequest(t *testing.T) {
	st := NewMemoryStore()
	first, created, err := st.CreateMCPOperation(app.MCPOperation{BindingID: "binding-a", IdempotencyKey: "same", Fingerprint: "fp-a"})
	if err != nil || !created {
		t.Fatalf("first operation: %#v created=%v err=%v", first, created, err)
	}
	if replay, created, err := st.CreateMCPOperation(app.MCPOperation{BindingID: "binding-a", IdempotencyKey: "same", Fingerprint: "fp-a"}); err != nil || created || replay.ID != first.ID {
		t.Fatalf("same replay mismatch: %#v created=%v err=%v", replay, created, err)
	}
	if _, _, err := st.CreateMCPOperation(app.MCPOperation{BindingID: "binding-a", IdempotencyKey: "same", Fingerprint: "fp-b"}); !errors.Is(err, ErrMCPOperationConflict) {
		t.Fatalf("changed replay error = %v", err)
	}
}

func TestMCPBindingRevocationTerminatesOnlyNonterminalOperations(t *testing.T) {
	st := NewMemoryStore()
	now := time.Now().UTC()
	ticket, err := st.SaveMCPAccessTicket(testMCPAccessTicket(now, "revoke-binding-hash"))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := st.RedeemMCPAccessTicket(ticket.SecretHash, app.MCPPeerIdentity{
		DomainID: ticket.DomainID, DeviceID: "device-revoke", KeyThumbprint: "thumb-revoke", ISCPSessionID: "iscp-revoke",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	running, _, err := st.CreateMCPOperation(app.MCPOperation{BindingID: binding.ID, IdempotencyKey: "running", Fingerprint: "running"})
	if err != nil {
		t.Fatal(err)
	}
	completedAt := now.Add(-time.Second)
	succeeded, _, err := st.CreateMCPOperation(app.MCPOperation{
		BindingID: binding.ID, IdempotencyKey: "succeeded", Fingerprint: "succeeded",
		State: app.MCPOperationSucceeded, CompletedAt: &completedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RevokeMCPBinding(binding.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	running, _ = st.GetMCPOperation(running.ID)
	if running.State != app.MCPOperationRevoked || running.ErrorCode != "binding_revoked" || running.CompletedAt == nil {
		t.Fatalf("running operation did not become revoked: %#v", running)
	}
	succeeded, _ = st.GetMCPOperation(succeeded.ID)
	if succeeded.State != app.MCPOperationSucceeded || succeeded.CompletedAt == nil || !succeeded.CompletedAt.Equal(completedAt) {
		t.Fatalf("terminal operation changed during revocation: %#v", succeeded)
	}
}

func TestMCPAccessRecordsCanBeDeletedIndividuallyAndByOwner(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		testMCPAccessRecordDeletion(t, NewMemoryStore())
	})
	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		st, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		otherTicketID := testMCPAccessRecordDeletion(t, st)
		reloaded, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(reloaded.ListMCPAccessTickets(app.DefaultOwnerID)) != 0 || len(reloaded.ListMCPBindings(app.DefaultOwnerID)) != 0 {
			t.Fatal("deleted MCP access records returned after FileStore restart")
		}
		if _, ok := reloaded.GetMCPAccessTicket(otherTicketID); !ok {
			t.Fatal("owner-scoped deletion removed another owner's ticket after restart")
		}
	})
}

func testMCPAccessRecordDeletion(t *testing.T, st Store) string {
	t.Helper()
	now := time.Now().UTC()
	expired := testMCPAccessTicket(now, "expired-delete-hash")
	expired.Status = app.MCPAccessExpired
	expired, err := st.SaveMCPAccessTicket(expired)
	if err != nil {
		t.Fatal(err)
	}
	deletedTicket, err := st.DeleteMCPAccessTicket(app.DefaultOwnerID, expired.ID)
	if err != nil || deletedTicket.Status != app.MCPAccessExpired {
		t.Fatalf("delete expired ticket: ticket=%#v err=%v", deletedTicket, err)
	}
	if _, ok := st.GetMCPAccessTicket(expired.ID); ok {
		t.Fatal("deleted expired ticket is still available")
	}

	consumed, err := st.SaveMCPAccessTicket(testMCPAccessTicket(now, "consumed-delete-hash"))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := st.RedeemMCPAccessTicket(consumed.SecretHash, app.MCPPeerIdentity{
		DomainID: consumed.DomainID, DeviceID: "delete-device", KeyThumbprint: "delete-thumb", ISCPSessionID: "delete-iscp",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	operation, _, err := st.CreateMCPOperation(app.MCPOperation{BindingID: binding.ID, IdempotencyKey: "delete", Fingerprint: "delete"})
	if err != nil {
		t.Fatal(err)
	}
	deletedBinding, err := st.DeleteMCPBinding(app.DefaultOwnerID, binding.ID)
	if err != nil || deletedBinding.ID != binding.ID {
		t.Fatalf("delete binding: binding=%#v err=%v", deletedBinding, err)
	}
	if _, ok := st.GetMCPBinding(binding.ID); ok {
		t.Fatal("deleted binding is still available")
	}
	if _, ok := st.GetMCPOperation(operation.ID); ok {
		t.Fatal("binding deletion retained its MCP operation")
	}
	if _, ok := mustGetSession(t, st, binding.LinkedSessionID); !ok {
		t.Fatal("binding record deletion removed conversation history")
	}

	activeTicket, err := st.SaveMCPAccessTicket(testMCPAccessTicket(now, "bulk-active-hash"))
	if err != nil {
		t.Fatal(err)
	}
	activeBinding, err := st.RedeemMCPAccessTicket(activeTicket.SecretHash, app.MCPPeerIdentity{
		DomainID: activeTicket.DomainID, DeviceID: "bulk-device", KeyThumbprint: "bulk-thumb", ISCPSessionID: "bulk-iscp",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	bulkOperation, _, err := st.CreateMCPOperation(app.MCPOperation{BindingID: activeBinding.ID, IdempotencyKey: "bulk", Fingerprint: "bulk"})
	if err != nil {
		t.Fatal(err)
	}
	other := testMCPAccessTicket(now, "other-owner-hash")
	other.OwnerID, other.ActorID = "owner-other", "owner-other"
	other, err = st.SaveMCPAccessTicket(other)
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := st.DeleteMCPAccessRecords(app.DefaultOwnerID)
	if err != nil || deleted.DeletedTickets != 2 || deleted.DeletedBindings != 1 {
		t.Fatalf("delete all owner records: deleted=%#v err=%v", deleted, err)
	}
	if len(st.ListMCPAccessTickets(app.DefaultOwnerID)) != 0 || len(st.ListMCPBindings(app.DefaultOwnerID)) != 0 {
		t.Fatal("owner records remain after delete all")
	}
	if _, ok := st.GetMCPOperation(bulkOperation.ID); ok {
		t.Fatal("delete all retained an operation for a deleted binding")
	}
	if _, ok := st.GetMCPAccessTicket(other.ID); !ok {
		t.Fatal("delete all removed another owner's ticket")
	}
	return other.ID
}

func TestMemoryStoreMCPRecordsCannotBeMutatedOutsideStore(t *testing.T) {
	st := NewMemoryStore()
	now := time.Now().UTC()
	ticket, err := st.SaveMCPAccessTicket(app.MCPAccessTicket{
		SchemaVersion: app.MCPAccessTicketSchemaVersion, SecretHash: "clone-secret", DomainID: "domain-a", Scope: app.MCPAccessConversation,
		Status: app.MCPAccessPending, MaxUses: 1, IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	storedTicket, _ := st.GetMCPAccessTicket(ticket.ID)
	if storedTicket.Scope != app.MCPAccessConversation {
		t.Fatalf("ticket mutation escaped Store boundary: %#v", storedTicket)
	}
	binding, err := st.RedeemMCPAccessTicket("clone-secret", app.MCPPeerIdentity{
		DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	storedBinding, _ := st.GetMCPBinding(binding.ID)
	if storedBinding.Scope != app.MCPAccessConversation {
		t.Fatalf("binding mutation escaped Store boundary: %#v", storedBinding)
	}
	operation, _, err := st.CreateMCPOperation(app.MCPOperation{
		BindingID: binding.ID, IdempotencyKey: "clone-operation", Fingerprint: "clone-fingerprint",
		Invocation: app.MCPInvocationContext{Arguments: map[string]any{
			"nested": map[string]any{"value": "original"}, "items": []any{map[string]any{"value": "original"}},
		}},
		Result: []byte(`{"value":"original"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	operation.Invocation.Arguments["nested"].(map[string]any)["value"] = "changed"
	operation.Invocation.Arguments["items"].([]any)[0].(map[string]any)["value"] = "changed"
	operation.Result[0] = '['
	storedOperation, _ := st.GetMCPOperation(operation.ID)
	if storedOperation.Invocation.Arguments["nested"].(map[string]any)["value"] != "original" ||
		storedOperation.Invocation.Arguments["items"].([]any)[0].(map[string]any)["value"] != "original" ||
		string(storedOperation.Result) != `{"value":"original"}` {
		t.Fatalf("operation mutation escaped Store boundary: %#v", storedOperation)
	}
}

func TestFileStoreDoesNotReturnOrRetainTicketWhenPersistenceFails(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := NewFileStore(filepath.Join(parentFile, "state.json"))
	if err == nil {
		t.Fatal("FileStore unexpectedly opened a state path below a regular file")
	}

	st = newTestFileStore(filepath.Join(parentFile, "state.json"))
	ticket, err := st.SaveMCPAccessTicket(testMCPAccessTicket(time.Now().UTC(), "must-not-survive"))
	if err == nil || ticket.ID != "" || len(st.ListMCPAccessTickets("")) != 0 {
		t.Fatalf("failed ticket persistence returned or retained a ticket: ticket=%#v count=%d err=%v", ticket, len(st.ListMCPAccessTickets("")), err)
	}
}

func TestFileStoreRollsBackMCPStateWhenPersistenceFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ticket, err := st.SaveMCPAccessTicket(testMCPAccessTicket(now, "rollback-hash"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Dir(path), []byte("occupied"), 0o600); err == nil {
		t.Fatal("unexpectedly replaced the state directory with a file")
	}
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	st.path = filepath.Join(parentFile, "state.json")
	peer := app.MCPPeerIdentity{DomainID: ticket.DomainID, DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}
	if binding, err := st.RedeemMCPAccessTicket(ticket.SecretHash, peer, now.Add(time.Second)); err == nil || binding.ID != "" {
		t.Fatalf("failed redemption returned a binding: binding=%#v err=%v", binding, err)
	}
	stored, ok := st.GetMCPAccessTicket(ticket.ID)
	if !ok || stored.Status != app.MCPAccessPending || stored.UseCount != 0 {
		t.Fatalf("failed redemption consumed the ticket: %#v ok=%v", stored, ok)
	}
	if _, ok := st.FindMCPBindingForPeer(peer.DomainID, peer.DeviceID, peer.KeyThumbprint); ok {
		t.Fatal("failed redemption retained a binding")
	}
}

func TestFileStoreRollsBackBindingAndOperationsWhenRevocationPersistenceFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ticket, err := st.SaveMCPAccessTicket(testMCPAccessTicket(now, "revoke-rollback-hash"))
	if err != nil {
		t.Fatal(err)
	}
	peer := app.MCPPeerIdentity{DomainID: ticket.DomainID, DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "iscp-a"}
	binding, err := st.RedeemMCPAccessTicket(ticket.SecretHash, peer, now)
	if err != nil {
		t.Fatal(err)
	}
	operation, _, err := st.CreateMCPOperation(app.MCPOperation{BindingID: binding.ID, IdempotencyKey: "revoke-rollback", Fingerprint: "revoke-rollback"})
	if err != nil {
		t.Fatal(err)
	}
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	st.path = filepath.Join(parentFile, "state.json")
	if revoked, err := st.RevokeMCPBinding(binding.ID, now.Add(time.Second)); err == nil || revoked.ID != "" {
		t.Fatalf("failed revocation returned a binding: binding=%#v err=%v", revoked, err)
	}
	storedBinding, _ := st.GetMCPBinding(binding.ID)
	storedOperation, _ := st.GetMCPOperation(operation.ID)
	if storedBinding.Status != app.MCPBindingActive || storedOperation.State != app.MCPOperationRunning {
		t.Fatalf("failed revocation was retained: binding=%#v operation=%#v", storedBinding, storedOperation)
	}
}

func TestFileStoreRollsBackMCPRecordDeletionWhenPersistenceFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ticket, err := st.SaveMCPAccessTicket(testMCPAccessTicket(now, "delete-rollback-hash"))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := st.RedeemMCPAccessTicket(ticket.SecretHash, app.MCPPeerIdentity{
		DomainID: ticket.DomainID, DeviceID: "delete-rollback-device", KeyThumbprint: "delete-rollback-thumb", ISCPSessionID: "delete-rollback-iscp",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	operation, _, err := st.CreateMCPOperation(app.MCPOperation{BindingID: binding.ID, IdempotencyKey: "delete-rollback", Fingerprint: "delete-rollback"})
	if err != nil {
		t.Fatal(err)
	}
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	st.path = filepath.Join(parentFile, "state.json")
	if deleted, err := st.DeleteMCPAccessTicket(app.DefaultOwnerID, ticket.ID); err == nil || deleted.ID != "" {
		t.Fatalf("failed ticket deletion returned a record: ticket=%#v err=%v", deleted, err)
	}
	if _, ok := st.GetMCPAccessTicket(ticket.ID); !ok {
		t.Fatal("failed persistence removed the access ticket")
	}
	if deleted, err := st.DeleteMCPBinding(app.DefaultOwnerID, binding.ID); err == nil || deleted.ID != "" {
		t.Fatalf("failed binding deletion returned a record: binding=%#v err=%v", deleted, err)
	}
	if _, ok := st.GetMCPBinding(binding.ID); !ok {
		t.Fatal("failed persistence removed the binding")
	}
	if _, ok := st.GetMCPOperation(operation.ID); !ok {
		t.Fatal("failed persistence removed the binding operation")
	}
	if deleted, err := st.DeleteMCPAccessRecords(app.DefaultOwnerID); err == nil || deleted.DeletedTickets != 0 || deleted.DeletedBindings != 0 {
		t.Fatalf("failed bulk deletion returned counts: deleted=%#v err=%v", deleted, err)
	}
	if len(st.ListMCPAccessTickets(app.DefaultOwnerID)) != 1 || len(st.ListMCPBindings(app.DefaultOwnerID)) != 1 {
		t.Fatal("failed bulk persistence removed MCP access records")
	}
}

func testMCPAccessTicket(now time.Time, secretHash string) app.MCPAccessTicket {
	return app.MCPAccessTicket{
		SchemaVersion: app.MCPAccessTicketSchemaVersion, SecretHash: secretHash, OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
		DomainID: "domain-a", Scope: app.MCPAccessConversation, Status: app.MCPAccessPending, MaxUses: 1,
		IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	}
}

func testISCPOnboarding(now time.Time, id, ownerID string) app.ISCPOnboarding {
	return app.ISCPOnboarding{
		SchemaVersion: app.ISCPOnboardingSchemaVersion, ID: id, OwnerID: ownerID, ActorID: ownerID,
		DisplayName: "External gateway", DomainID: "domain-a", AuthorityRef: "authority-ref-" + id, TicketID: "pairing-ticket-" + id,
		TicketType: "iscp.pairing_ticket.v2", RelayID: "relay-a", TrustRootID: "root-a", MaxUses: 1,
		Status: app.ISCPOnboardingTicketIssued, TicketIssuedAt: now, TicketExpiresAt: now.Add(10 * time.Minute), CreatedAt: now, UpdatedAt: now,
	}
}
