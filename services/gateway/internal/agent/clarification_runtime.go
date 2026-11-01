package agent

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
)

type clarificationSigner interface {
	SignClarification(clarification.Binding, clarification.Record) (clarification.Record, error)
	VerifyClarification(clarification.Binding, clarification.Record, clarification.Revisions) error
}
type clarificationScope struct {
	signer          clarificationSigner
	binding         clarification.Binding
	requestID       string
	history         []clarification.Record
	sourceMessageID string
	contextDigest   string
	snapshot        []clarification.SnapshotMessage
	sourceText      *string
	source          string
}

type sourceMessageIDKey struct{}
type sourceMessageFilesUnavailableKey struct{}

func WithSourceMessageFilesUnavailable(ctx context.Context, missing bool) context.Context {
	return context.WithValue(ctx, sourceMessageFilesUnavailableKey{}, missing)
}

// WithSourceMessageID preserves an admitted owner's message identity.
func WithSourceMessageID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sourceMessageIDKey{}, id)
}

func (r Runtime) WithClarificationContext(sourceMessageID, contextDigest string) Runtime {
	if r.clarification != nil {
		scope := *r.clarification
		scope.sourceMessageID = sourceMessageID
		scope.contextDigest = contextDigest
		r.clarification = &scope
	}
	return r
}

func (r Runtime) WithClarificationSnapshot(messages []clarification.SnapshotMessage, source string) Runtime {
	if r.clarification != nil {
		scope := *r.clarification
		scope.snapshot = append([]clarification.SnapshotMessage(nil), messages...)
		for i := range scope.snapshot {
			scope.snapshot[i].Files = append([]clarification.SnapshotFile(nil), messages[i].Files...)
		}
		scope.source = source
		r.clarification = &scope
	}
	return r
}

func (r Runtime) withClarificationSourceText(text string) Runtime {
	if r.clarification != nil {
		scope := *r.clarification
		scope.sourceText = &text
		r.clarification = &scope
	}
	return r
}

// WithClarificationScope binds client-owned records to the authenticated
// execution. History is untrusted until each record's MAC has been verified.
func (r Runtime) WithClarificationScope(signer clarificationSigner, binding clarification.Binding, requestID string, history []clarification.Record) Runtime {
	copied := make([]clarification.Record, len(history))
	copy(copied, history)
	for i := range copied {
		copied[i].Options = append([]clarification.Option(nil), history[i].Options...)
	}
	r.clarification = &clarificationScope{signer: signer, binding: binding, requestID: requestID, history: copied}
	return r
}

// selectedClarification verifies an immediately preceding signed record and
// finds the original owner text by digest. The returned option still needs
// current capability, policy, degradation and external-send revalidation.
func (r Runtime) selectedClarification(reply string, history invocationHistory, revisions clarification.Revisions) (clarification.Option, string, error) {
	selection, err := r.readClarificationSelection(reply, history, revisions)
	if err != nil {
		return clarification.Option{}, "", err
	}
	return selection.option, selection.original.Content, nil
}
