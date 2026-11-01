package agent

import (
	"context"
	"regexp"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messageplane"
)

type clarificationSelectionKey struct{}
type clarificationSelection struct {
	record   clarification.Record
	option   clarification.Option
	original app.Message
	prior    []app.Message
	endpoint string
}

// ClarificationSnapshot preserves stable attachment identities without reading
// file bytes. Legacy entries without immutable metadata carry a missing marker.
var clarificationSnapshotID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$`)
var clarificationSnapshotSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ClarificationSnapshot(messages []app.Message) []clarification.SnapshotMessage {
	out := make([]clarification.SnapshotMessage, 0, len(messages))
	for _, m := range messages {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		v := clarification.SnapshotMessage{ID: m.ID, Role: m.Role, Content: m.Content, FilesUnavailable: m.FilesUnavailable}
		for _, f := range m.Attachments {
			if !clarificationSnapshotID.MatchString(f.ArtifactID) || !clarificationSnapshotSHA.MatchString(f.SHA256) || f.Bytes < 0 || f.Name == "" || strings.ContainsAny(f.Name, "/\\\x00") {
				v.FilesUnavailable = true
				continue
			}
			source := "conversation_attachment"
			if m.Role == "assistant" {
				source = "conversation_result"
			}
			v.Files = append(v.Files, clarification.SnapshotFile{ID: f.ArtifactID, Name: f.Name, Size: f.Bytes, SHA256: f.SHA256, Source: source})
		}
		out = append(out, v)
	}
	return out
}

func (r Runtime) clarificationRevisions() clarification.Revisions {
	if r.semanticRouter == nil || r.semanticRouter.graph == nil {
		return clarification.Revisions{}
	}
	return clarification.Revisions{Graph: r.semanticRouter.graph.Revision(), Catalog: r.capabilities.Revision(), Calibration: r.semanticRouter.calibration.Revision}
}

func clarificationNumber(reply, prompt string, options []clarification.Option) int {
	switch strings.ToLower(strings.TrimSpace(reply)) {
	case "1", "第一个", "第一個", "first":
		return 1
	case "2", "第二个", "第二個", "second":
		return 2
	}
	// Exact displayed labels are choices. Mixed text remains a new request.
	for _, o := range options {
		prefix := string(rune('0'+o.Number)) + ". "
		for _, line := range strings.Split(prompt, "\n") {
			if strings.HasPrefix(line, prefix) && strings.TrimSpace(reply) == strings.TrimPrefix(line, prefix) {
				return o.Number
			}
		}
	}
	return 0
}

func (r Runtime) readClarificationSelection(reply string, history invocationHistory, revisions clarification.Revisions) (*clarificationSelection, error) {
	if r.clarification == nil || r.clarification.signer == nil || len(r.clarification.history) != 1 {
		return nil, clarification.ErrInvalid
	}
	record := r.clarification.history[0]
	// ListRecentMessages is ordered by (created_at DESC, id DESC). Reverse
	// the complete tuple order, including mixed timestamp ties.
	chronological := reverseMessages(history.MessageCandidates)
	var prompt string
	if len(r.clarification.snapshot) > 0 {
		snapshot := r.clarification.snapshot
		if snapshot[len(snapshot)-1].Role == "user" {
			snapshot = snapshot[:len(snapshot)-1]
		}
		if len(snapshot) == 0 || snapshot[len(snapshot)-1].Role != "assistant" {
			return nil, clarification.ErrInvalid
		}
		prompt = snapshot[len(snapshot)-1].Content
	} else {
		if len(chronological) == 0 || chronological[len(chronological)-1].Role != "assistant" {
			return nil, clarification.ErrInvalid
		}
		prompt = chronological[len(chronological)-1].Content
	}
	number := clarificationNumber(reply, prompt, record.Options)
	if number < 1 || number > len(record.Options) || clarification.TextDigest(prompt) != record.PromptSHA256 {
		return nil, clarification.ErrInvalid
	}
	if err := r.clarification.signer.VerifyClarification(r.clarification.binding, record, revisions); err != nil {
		return nil, err
	}
	for i, m := range chronological {
		if m.ID != record.SourceMessageID || m.Role != "user" || clarification.TextDigest(m.Content) != record.SourceTextSHA256 {
			continue
		}
		var snapshot []clarification.SnapshotMessage
		if len(r.clarification.snapshot) > 0 {
			for j, v := range r.clarification.snapshot {
				if v.ID == m.ID {
					snapshot = r.clarification.snapshot[:j+1]
					break
				}
			}
		} else {
			prior := recentContextMessages(chronological[:i], "", defaultContextMessageLimit)
			snapshot = ClarificationSnapshot(append(prior, m))
		}
		source := r.clarification.source
		if source == "" {
			source = string(app.MessageSourceWeb)
		}
		digest, err := clarification.ContextDigest(snapshot, source)
		if err != nil || digest != record.ContextSHA256 {
			return nil, clarification.ErrInvalid
		}
		return &clarificationSelection{record: record, option: record.Options[number-1], original: m, prior: append([]app.Message(nil), chronological[:i]...)}, nil
	}
	return nil, clarification.ErrInvalid
}

func (r Runtime) claimClarification(selection *clarificationSelection) error {
	if selection == nil {
		return nil
	}
	claimer, ok := r.clarification.signer.(interface {
		ClaimClarification(clarification.Binding, clarification.Record, int, string) (string, error)
	})
	if !ok {
		return clarification.ErrInvalid
	}
	winner, err := claimer.ClaimClarification(r.clarification.binding, selection.record, selection.option.Number, r.clarification.requestID)
	if err != nil {
		return err
	}
	if winner != r.clarification.requestID {
		return clarification.ErrInvalid
	}
	return nil
}

// restoreClarification uses only the admitted snapshot and repeats guard/split
// on the original request. The visible reply remains an ordinary stored turn.
func (r Runtime) restoreClarification(ctx context.Context, reply string, history invocationHistory, envelope app.MessageEnvelope) (Runtime, context.Context, string, []app.MessagePart, invocationHistory, error) {
	if r.clarification == nil || len(r.clarification.history) != 1 {
		return r, ctx, reply, nil, history, nil
	}
	record := r.clarification.history[0]
	prompt := ""
	for _, m := range history.MessageCandidates {
		if m.Role == "assistant" && clarification.TextDigest(m.Content) == record.PromptSHA256 {
			prompt = m.Content
			break
		}
	}
	if clarificationNumber(reply, prompt, record.Options) == 0 {
		return r, ctx, reply, nil, history, nil
	}
	selected, err := r.readClarificationSelection(reply, history, r.clarificationRevisions())
	if err != nil {
		return r, ctx, reply, nil, history, err
	}
	m := selected.original
	normalized, err := messageplane.Normalize(messageplane.Ingress{Session: app.Session{ID: m.SessionID, OwnerID: envelope.OwnerID, Source: "web"}, Message: m})
	if err != nil {
		return r, ctx, reply, nil, history, err
	}
	prior := reverseMessages(selected.prior)
	history.MessageCandidates = prior
	history.Selected.Messages = recentContextMessages(reverseMessages(prior), "", defaultContextMessageLimit)
	history.ToolCandidates = filterPriorTools(history.ToolCandidates, m)
	history.Selected.ToolResults = recentContextToolResults(reverseToolCalls(history.ToolCandidates), "", defaultContextToolLimit)
	history.Selected.Episodes = nil
	history.EpisodeCandidates = nil
	r = r.WithClarificationContext(m.ID, record.ContextSHA256).withClarificationSourceText(m.Content)
	ctx = context.WithValue(ctx, clarificationSelectionKey{}, selected)
	ctx = WithSourceMessageFilesUnavailable(ctx, m.FilesUnavailable)
	return r, ctx, m.Content, messageplane.ProjectRequest(normalized).Resources, history, nil
}
func filterPriorTools(calls []app.ToolCall, m app.Message) []app.ToolCall {
	out := make([]app.ToolCall, 0, len(calls))
	for _, v := range calls {
		if v.StartedAt.Before(m.CreatedAt) {
			out = append(out, v)
		}
	}
	return out
}
