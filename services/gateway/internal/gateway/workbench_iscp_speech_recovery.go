package gateway

import (
	"encoding/json"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
)

// Terminal speech events have the same data authority as the original stream.
// They use the existing encrypted receipt store, in a separate namespace. An
// incomplete checkpoint after a process loss proves neither success nor absence.
type iscpSpeechCheckpoint struct {
	SessionID string            `json:"session_id"`
	After     uint64            `json:"after"`
	Sequence  uint64            `json:"sequence"`
	Events    []iscpSpeechEvent `json:"events"`
	Closed    bool              `json:"closed"`
}

func speechReceiptScope(scope string) string { return scope + "\x00speech-terminal" }

func (a *iscpDomainAdapter) prepareSpeechCheckpoint(scope, id string) error {
	a.receipts.mu.Lock()
	defer a.receipts.mu.Unlock()
	if !a.receipts.hasCapacity(domainReceiptMaxBytes) {
		return errors.New("speech receipt capacity unavailable")
	}
	receipt := iscpDomainReceipt{Version: 1, Digest: execution.Digest([]byte(id)), Operation: iscpworkbench.OperationSpeechSessionEvents}
	if err := a.receipts.save(speechReceiptScope(scope), id, receipt); err != nil {
		return err
	}
	a.receipts.reserved += domainReceiptMaxBytes
	return nil
}

// Called with session.mu held. The immutable terminal snapshot is durable
// before any caller can observe it; ACK/cancel must never overwrite it.
func (a *iscpDomainAdapter) persistSpeechTerminal(session *iscpSpeechSession) error {
	if session.terminalStored {
		return nil
	}
	if len(session.events) == 0 || !speechTerminal(session.events[len(session.events)-1].Event) {
		session.sequence++
		session.events = append(session.events, iscpSpeechEvent{session.sequence, speech.RealtimeEvent{Event: "error", Code: "speech_stream_closed"}})
	}
	checkpoint := iscpSpeechCheckpoint{SessionID: session.id, After: session.ack, Sequence: session.sequence, Events: session.events, Closed: true}
	body, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	a.receipts.mu.Lock()
	defer a.receipts.mu.Unlock()
	receipt := iscpDomainReceipt{Version: 1, Digest: execution.Digest([]byte(session.id)), Operation: iscpworkbench.OperationSpeechSessionEvents, Complete: true, Status: 200, Body: body}
	if err := a.receipts.save(speechReceiptScope(session.scope), session.id, receipt); err != nil {
		return err
	}
	session.terminalStored = true
	if session.storageReserved {
		a.receipts.reserved -= domainReceiptMaxBytes
		session.storageReserved = false
	}
	return nil
}

func speechTerminal(event speech.RealtimeEvent) bool {
	return event.Event == "final" || event.Event == "error" || event.Event == "cancelled"
}

func (a *iscpDomainAdapter) recoveredSpeechEvents(scope, id string, after uint64) iscpDomainResult {
	a.receipts.mu.Lock()
	receipt, exists, err := a.receipts.load(speechReceiptScope(scope), id)
	a.receipts.mu.Unlock()
	if err != nil {
		return domainError(503, "speech_receipt_unavailable")
	}
	if !exists {
		return domainError(404, "speech_session_not_found")
	}
	if !receipt.Complete {
		return domainError(409, "speech_session_outcome_unknown")
	}
	var checkpoint iscpSpeechCheckpoint
	if json.Unmarshal(receipt.Body, &checkpoint) != nil || checkpoint.SessionID != id || !checkpoint.Closed || len(checkpoint.Events) == 0 {
		return domainError(503, "speech_receipt_unavailable")
	}
	if after < checkpoint.After || after > checkpoint.Sequence {
		return domainError(409, "cursor_gap")
	}
	events := []iscpSpeechEvent{}
	for _, event := range checkpoint.Events {
		if event.Sequence > after {
			events = append(events, event)
		}
	}
	return domainJSON(200, map[string]any{"session_id": id, "events": events, "closed": true})
}
