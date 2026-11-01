package execution

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"

// WorkbenchClarificationService binds a signing capability to one authenticated
// host-workbench admission. It never signs arbitrary client-provided records.
type WorkbenchClarificationService struct {
	service   *Service
	admission WorkbenchBinding
}

func ClarificationBindingForWorkbench(b WorkbenchBinding) clarification.Binding {
	return clarification.Binding{OwnerID: b.OwnerID, ClientID: "host-" + Digest([]byte(b.ActorID+"\x00"+b.ClientID)), InstallationID: b.WorkspaceID, ConversationID: b.SessionID}
}
func (s *Service) WorkbenchClarifications(binding WorkbenchBinding) *WorkbenchClarificationService {
	return &WorkbenchClarificationService{s, binding}
}
func (w *WorkbenchClarificationService) bound(binding clarification.Binding) bool {
	return binding == ClarificationBindingForWorkbench(w.admission)
}
func (w *WorkbenchClarificationService) fenceLocked(request string) (workbenchFence, bool) {
	b := w.admission
	b.RequestID = request
	f, ok := w.service.control.WorkbenchFences[workbenchKey(b)]
	return f, ok && sameWorkbenchAuthority(f.WorkbenchBinding, b)
}
func (w *WorkbenchClarificationService) SignClarification(binding clarification.Binding, record clarification.Record) (clarification.Record, error) {
	s := w.service
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := w.fenceLocked(record.RequestID)
	if !w.bound(binding) || record.RequestID != w.admission.RequestID || s.closed || !ok || f.State != "running" || !f.Deadline.After(s.now()) || s.revokedClients[w.admission.ClientID] {
		return clarification.Record{}, ErrConflict
	}
	return s.signClarificationLocked(binding, record)
}
func (w *WorkbenchClarificationService) VerifyClarification(binding clarification.Binding, record clarification.Record, revisions clarification.Revisions) error {
	s := w.service
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := w.fenceLocked(record.RequestID)
	if !w.bound(binding) || s.closed || !ok || s.revokedClients[w.admission.ClientID] {
		return clarification.ErrInvalid
	}
	return clarification.VerifyAt(s.clarificationKey, binding, record, revisions, s.now())
}
func (w *WorkbenchClarificationService) ClaimClarification(binding clarification.Binding, record clarification.Record, optionNumber int, successorRequestID string) (string, error) {
	s := w.service
	s.mu.Lock()
	defer s.mu.Unlock()
	prior, exists := w.fenceLocked(record.RequestID)
	next, admitted := w.fenceLocked(successorRequestID)
	if !w.bound(binding) || successorRequestID != w.admission.RequestID || s.closed || !exists || !admitted || next.State != "running" || !next.Deadline.After(s.now()) || s.revokedClients[w.admission.ClientID] || (prior.State != "completed" && prior.State != "clarification_required" && prior.State != "blocked") {
		return "", ErrConflict
	}
	if err := clarification.VerifyAt(s.clarificationKey, binding, record, clarification.Revisions{Graph: record.GraphRevision, Catalog: record.CatalogRevision, Calibration: record.CalibrationRevision}, s.now()); err != nil {
		return "", err
	}
	return s.claimClarificationLocked(binding, record, optionNumber, successorRequestID)
}
