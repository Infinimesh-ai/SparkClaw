package execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
)

func TestClarificationKeyBindsExecutionAndSurvivesRestart(t *testing.T) {
	var service *Service
	var signed clarification.Record
	var binding clarification.Binding
	service, e, _ := setup(t, func(_ context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		binding = clarification.Binding{OwnerID: e.OwnerID, ClientID: e.ClientID, InstallationID: e.InstallationID, ConversationID: e.ConversationID}
		record := clarification.Record{SchemaVersion: clarification.SchemaVersion, ID: newUUID(), RequestID: e.RequestID, SourceMessageID: e.Messages[len(e.Messages)-1].ID, ContextSHA256: Digest([]byte("context")), PromptSHA256: Digest([]byte("choose")), SourceTextSHA256: Digest([]byte(e.Messages[len(e.Messages)-1].Content)), GraphRevision: "graph.1", CatalogRevision: "catalog.1", CalibrationRevision: "calibration.1", Options: []clarification.Option{{Number: 1, Kind: "route", CandidateID: "document.read#read", BindingSHA256: Digest([]byte("target"))}}}
		var err error
		signed, err = service.SignClarification(binding, record)
		return Output{Content: "choose", Clarification: &signed}, err
	})
	raw, _ := json.Marshal(e)
	if _, err := service.Submit(t.Context(), e, Digest(raw)); err != nil {
		t.Fatal(err)
	}
	status := completed(t, service, e)
	var payload Payload
	if err := json.Unmarshal([]byte(status.Result.Payload), &payload); err != nil || payload.Clarification == nil {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
	revisions := clarification.Revisions{Graph: "graph.1", Catalog: "catalog.1", Calibration: "calibration.1"}
	if err := service.VerifyClarification(binding, signed, revisions); err != nil {
		t.Fatal(err)
	}
	changed := binding
	changed.ConversationID = newUUID()
	if !errors.Is(service.VerifyClarification(changed, signed, revisions), clarification.ErrInvalid) {
		t.Fatal("unbound conversation accepted")
	}
	root := service.Root()
	service.Close()
	restarted, err := New(root, func(context.Context, Envelope, map[string][]byte) (Output, error) {
		t.Fatal("replayed execution")
		return Output{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err = restarted.VerifyClarification(binding, signed, revisions); err != nil {
		t.Fatalf("restart invalidated signature: %v", err)
	}
}

func TestUnsignedClarificationCannotBeDelivered(t *testing.T) {
	service, e, digest := setup(t, func(_ context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		return Output{Content: "choose", Clarification: &clarification.Record{SchemaVersion: 1, RequestID: e.RequestID, MAC: "forged"}}, nil
	})
	if _, err := service.Submit(t.Context(), e, digest); err != nil {
		t.Fatal(err)
	}
	service.Wait()
	status, err := service.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || status.State != "failed" || status.Result != nil {
		t.Fatalf("unsigned delivery: %+v %v", status, err)
	}
}

func testClarificationRecord(request string) clarification.Record {
	return clarification.Record{SchemaVersion: clarification.SchemaVersion, ID: newUUID(), RequestID: request, SourceMessageID: "owner-message", SourceTextSHA256: Digest([]byte("private owner request")), ContextSHA256: Digest([]byte("private history")), PromptSHA256: Digest([]byte("choose")), GraphRevision: "graph.1", CatalogRevision: "catalog.1", CalibrationRevision: "calibration.1", Options: []clarification.Option{{Number: 1, Kind: "route", CandidateID: "document.read#read", BindingSHA256: Digest([]byte("private target"))}}}
}

func TestWorkbenchClarificationAtomicClaimRestartAndScope(t *testing.T) {
	s, original := workbenchFixture(t)
	lease, _, err := s.BeginWorkbench(t.Context(), original)
	if err != nil {
		t.Fatal(err)
	}
	binding := ClarificationBindingForWorkbench(original)
	record, err := s.WorkbenchClarifications(original).SignClarification(binding, testClarificationRecord(original.RequestID))
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish("completed", "question"); err != nil {
		t.Fatal(err)
	}
	requests := []WorkbenchBinding{original, original}
	leases := make([]*WorkbenchLease, 2)
	for i := range requests {
		requests[i].RequestID = newUUID()
		leases[i], _, err = s.BeginWorkbench(t.Context(), requests[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	type answer struct {
		request, winner string
		err             error
	}
	results := make(chan answer, 2)
	for _, next := range requests {
		go func(next WorkbenchBinding) {
			winner, err := s.WorkbenchClarifications(next).ClaimClarification(binding, record, 1, next.RequestID)
			results <- answer{next.RequestID, winner, err}
		}(next)
	}
	first, second := <-results, <-results
	if first.err != nil {
		first, second = second, first
	}
	if first.err != nil || !errors.Is(second.err, ErrConflict) || second.winner != first.request {
		t.Fatalf("claims=%+v %+v", first, second)
	}
	winnerBinding := requests[0]
	if winnerBinding.RequestID != first.request {
		winnerBinding = requests[1]
	}
	if winner, err := s.WorkbenchClarifications(winnerBinding).ClaimClarification(binding, record, 1, first.request); err != nil || winner != first.request {
		t.Fatalf("idempotent claim=%s %v", winner, err)
	}
	changed := binding
	changed.ConversationID = "different-session"
	if _, err := s.WorkbenchClarifications(winnerBinding).ClaimClarification(changed, record, 1, first.request); err == nil {
		t.Fatal("cross-session claim accepted")
	}
	for _, lease := range leases {
		if err := lease.Finish("unknown", ""); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(s.root, "control.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private owner request", "private history", "private target", "document.read#read", "owner-message"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("claim leaked %q", private)
		}
	}
	root := s.Root()
	s.Close()
	restored, err := New(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	next := original
	next.RequestID = newUUID()
	replayLease, _, err := restored.BeginWorkbench(t.Context(), next)
	if err != nil {
		t.Fatal(err)
	}
	defer replayLease.Finish("unknown", "")
	winner, err := restored.WorkbenchClarifications(next).ClaimClarification(binding, record, 1, next.RequestID)
	if !errors.Is(err, ErrConflict) || winner != first.request {
		t.Fatalf("restart reclaimed unknown selection: winner=%s err=%v", winner, err)
	}
}

func TestClarificationFailedClaimPersistenceNeverGrantsDispatch(t *testing.T) {
	s, original := workbenchFixture(t)
	lease, _, err := s.BeginWorkbench(t.Context(), original)
	if err != nil {
		t.Fatal(err)
	}
	binding := ClarificationBindingForWorkbench(original)
	record, err := s.WorkbenchClarifications(original).SignClarification(binding, testClarificationRecord(original.RequestID))
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish("completed", "question"); err != nil {
		t.Fatal(err)
	}
	next := original
	next.RequestID = newUUID()
	nextLease, _, err := s.BeginWorkbench(t.Context(), next)
	if err != nil {
		t.Fatal(err)
	}
	defer nextLease.Finish("unknown", "")
	root := s.root
	s.root = filepath.Join(root, "nonexistent")
	_, err = s.WorkbenchClarifications(next).ClaimClarification(binding, record, 1, next.RequestID)
	s.root = root
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("persistence failed open: %v", err)
	}
	if _, err = s.WorkbenchClarifications(next).ClaimClarification(binding, record, 1, next.RequestID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("uncertain claim retried: %v", err)
	}
}

func TestSignedClarificationPromptCannotBeReplaced(t *testing.T) {
	var service *Service
	service, e, digest := setup(t, func(_ context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		binding := clarification.Binding{OwnerID: e.OwnerID, ClientID: e.ClientID, InstallationID: e.InstallationID, ConversationID: e.ConversationID}
		record, err := service.SignClarification(binding, testClarificationRecord(e.RequestID))
		return Output{Content: "different displayed question", Clarification: &record}, err
	})
	if _, err := service.Submit(t.Context(), e, digest); err != nil {
		t.Fatal(err)
	}
	service.Wait()
	status, err := service.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || status.State != "failed" || status.Result != nil {
		t.Fatalf("changed prompt delivered: %+v %v", status, err)
	}
}
