package execution

import (
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
)

// A claim is an effect fence, not a continuation or conversation archive.
type clarificationClaim struct {
	ScopeDigest        string    `json:"scope_digest"`
	ClarificationID    string    `json:"clarification_id"`
	SuccessorRequestID string    `json:"successor_request_id"`
	OptionNumber       int       `json:"option_number"`
	RecordDigest       string    `json:"record_digest"`
	ExpiresAt          time.Time `json:"expires_at"`
	CreatedAt          time.Time `json:"created_at"`
}

func (s *Service) signClarificationLocked(binding clarification.Binding, record clarification.Record) (clarification.Record, error) {
	now := s.now().UTC().Truncate(time.Second)
	if record.IssuedAt == "" {
		record.IssuedAt = now.Format(time.RFC3339)
	}
	if record.ExpiresAt == "" {
		record.ExpiresAt = now.Add(clarification.Lifetime).Format(time.RFC3339)
	}
	signed, err := clarification.Sign(s.clarificationKey, binding, record)
	if err != nil {
		return clarification.Record{}, err
	}
	if err = clarification.VerifyAt(s.clarificationKey, binding, signed, clarification.Revisions{Graph: record.GraphRevision, Catalog: record.CatalogRevision, Calibration: record.CalibrationRevision}, s.now()); err != nil {
		return clarification.Record{}, err
	}
	return signed, nil
}

// SignClarification binds the record to the currently executing authenticated request.
func (s *Service) SignClarification(binding clarification.Binding, record clarification.Record) (clarification.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fence, ok := s.control.Fences[keyFor(binding.OwnerID, binding.ClientID, record.RequestID)]
	if s.closed || !ok || fence.InstallationID != binding.InstallationID || (fence.State != "accepted" && fence.State != "running") || !fence.Deadline.After(s.now()) || s.revokedClients[binding.ClientID] {
		return clarification.Record{}, ErrConflict
	}
	return s.signClarificationLocked(binding, record)
}
func (s *Service) VerifyClarification(binding clarification.Binding, record clarification.Record, revisions clarification.Revisions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verifyClarificationLocked(binding, record, revisions)
}
func (s *Service) verifyClarificationLocked(binding clarification.Binding, record clarification.Record, revisions clarification.Revisions) error {
	fence, ok := s.control.Fences[keyFor(binding.OwnerID, binding.ClientID, record.RequestID)]
	if s.closed || !ok || fence.InstallationID != binding.InstallationID || s.revokedClients[binding.ClientID] {
		return clarification.ErrInvalid
	}
	return clarification.VerifyAt(s.clarificationKey, binding, record, revisions, s.now())
}
func (s *Service) ClaimClarification(binding clarification.Binding, record clarification.Record, optionNumber int, successorRequestID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	revisions := clarification.Revisions{Graph: record.GraphRevision, Catalog: record.CatalogRevision, Calibration: record.CalibrationRevision}
	if err := s.verifyClarificationLocked(binding, record, revisions); err != nil {
		return "", err
	}
	original := s.control.Fences[keyFor(binding.OwnerID, binding.ClientID, record.RequestID)]
	successor, ok := s.control.Fences[keyFor(binding.OwnerID, binding.ClientID, successorRequestID)]
	if (original.State != "completed" && original.State != "delivered" && original.State != "delivery_expired") || !ok || successor.InstallationID != binding.InstallationID || (successor.State != "accepted" && successor.State != "running") || !successor.Deadline.After(s.now()) || successorRequestID == record.RequestID {
		return "", ErrConflict
	}
	return s.claimClarificationLocked(binding, record, optionNumber, successorRequestID)
}
func (s *Service) claimClarificationLocked(binding clarification.Binding, record clarification.Record, optionNumber int, successorRequestID string) (string, error) {
	if s.closed || s.clarificationDirty {
		return "", ErrUnavailable
	}
	if optionNumber < 1 || optionNumber > len(record.Options) || !UUID(successorRequestID) {
		return "", ErrConflict
	}
	scope, err := clarification.Digest(binding)
	if err != nil {
		return "", err
	}
	key := Digest([]byte(scope + "\x00" + record.ID))
	recordDigest, err := clarification.Digest(record)
	if err != nil {
		return "", err
	}
	if prior, ok := s.control.ClarificationClaims[key]; ok {
		if prior.ScopeDigest != scope || prior.RecordDigest != recordDigest || prior.OptionNumber != optionNumber || prior.SuccessorRequestID != successorRequestID {
			return prior.SuccessorRequestID, ErrConflict
		}
		return prior.SuccessorRequestID, nil
	}
	if len(s.control.ClarificationClaims)+len(s.control.Fences)+len(s.control.WorkbenchFences) >= MaxFences {
		return "", ErrCapacity
	}
	expires, _ := time.Parse(time.RFC3339, record.ExpiresAt)
	now := s.now()
	if !expires.After(now) {
		return "", clarification.ErrExpired
	}
	s.control.ClarificationClaims[key] = clarificationClaim{ScopeDigest: scope, ClarificationID: record.ID, SuccessorRequestID: successorRequestID, OptionNumber: optionNumber, RecordDigest: recordDigest, ExpiresAt: expires, CreatedAt: now}
	if err = s.saveLocked(); err != nil {
		// A rename may have committed before its fsync failed. Keep the claim in
		// memory and prohibit further dispatch until restart reconciles disk state.
		s.clarificationDirty = true
		return "", ErrUnavailable
	}
	return successorRequestID, nil
}
func (s *Service) validateClarificationClaims() error {
	if s.control.ClarificationClaims == nil || len(s.control.ClarificationClaims)+len(s.control.Fences)+len(s.control.WorkbenchFences) > MaxFences {
		return errors.New("invalid clarification control ledger")
	}
	for key, c := range s.control.ClarificationClaims {
		if !digestPattern.MatchString(c.ScopeDigest) || !identityPattern.MatchString(c.ClarificationID) || key != Digest([]byte(c.ScopeDigest+"\x00"+c.ClarificationID)) || !UUID(c.SuccessorRequestID) || c.OptionNumber < 1 || c.OptionNumber > 2 || !digestPattern.MatchString(c.RecordDigest) || c.CreatedAt.IsZero() || c.ExpiresAt.IsZero() || !c.ExpiresAt.After(c.CreatedAt) {
			return errors.New("invalid clarification claim")
		}
	}
	return nil
}
