package agent

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
)

type testClarificationSigner struct {
	key    []byte
	claims *sync.Map
}

func (s testClarificationSigner) SignClarification(b clarification.Binding, r clarification.Record) (clarification.Record, error) {
	if r.IssuedAt == "" {
		r.IssuedAt = time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
		r.ExpiresAt = time.Now().UTC().Add(time.Hour).Truncate(time.Second).Format(time.RFC3339)
	}
	return clarification.Sign(s.key, b, r)
}
func (s testClarificationSigner) VerifyClarification(b clarification.Binding, r clarification.Record, v clarification.Revisions) error {
	return clarification.VerifyAt(s.key, b, r, v, time.Now())
}
func (s testClarificationSigner) ClaimClarification(b clarification.Binding, r clarification.Record, n int, next string) (string, error) {
	if s.claims == nil {
		return "", clarification.ErrInvalid
	}
	winner, _ := s.claims.LoadOrStore(r.ID, next)
	return winner.(string), nil
}
func TestSelectedClarificationChecksIdentityRevisionAndOriginalText(t *testing.T) {
	signer := testClarificationSigner{key: make([]byte, 32)}
	binding := clarification.Binding{OwnerID: "owner", ClientID: "client", InstallationID: "installation", ConversationID: "conversation"}
	text := "summarize this document"
	prompt := "Choose an action:\n1. Read the document"
	original := app.Message{ID: "original", Role: "user", Content: text, CreatedAt: time.Unix(1, 0)}
	question := app.Message{ID: "question", Role: "assistant", Content: prompt, CreatedAt: time.Unix(2, 0)}
	digest, err := clarification.ContextDigest(ClarificationSnapshot([]app.Message{original}), "web")
	if err != nil {
		t.Fatal(err)
	}
	record, err := signer.SignClarification(binding, clarification.Record{SchemaVersion: 2, ID: "id", RequestID: "request", SourceMessageID: original.ID, SourceTextSHA256: clarification.TextDigest(text), ContextSHA256: digest, PromptSHA256: clarification.TextDigest(prompt), GraphRevision: "graph", CatalogRevision: "catalog", CalibrationRevision: "calibration", Options: []clarification.Option{{Number: 1, Kind: "route", CandidateID: "document.read#read", BindingSHA256: clarification.TextDigest("binding")}}})
	if err != nil {
		t.Fatal(err)
	}
	r := (Runtime{}).WithClarificationScope(signer, binding, "next", []clarification.Record{record})
	history := invocationHistory{MessageCandidates: []app.Message{question, original}}
	revisions := clarification.Revisions{Graph: "graph", Catalog: "catalog", Calibration: "calibration"}
	for _, reply := range []string{"1", "第一个", "Read the document"} {
		option, source, err := r.selectedClarification(reply, history, revisions)
		if err != nil || source != text || option.CandidateID != "document.read#read" {
			t.Fatalf("selection=%+v %q %v", option, source, err)
		}
	}
	if _, _, err = r.selectedClarification("1 and delete", history, revisions); !errors.Is(err, clarification.ErrInvalid) {
		t.Fatal("mixed text was accepted")
	}
	tampered := history
	tampered.MessageCandidates = append([]app.Message(nil), history.MessageCandidates...)
	tampered.MessageCandidates[0].Content = "different question"
	if _, _, err = r.selectedClarification("1", tampered, revisions); !errors.Is(err, clarification.ErrInvalid) {
		t.Fatal("modified prompt accepted")
	}
	revisions.Graph = "changed"
	if _, _, err = r.selectedClarification("1", history, revisions); !errors.Is(err, clarification.ErrStale) {
		t.Fatal("stale revision accepted")
	}
}
