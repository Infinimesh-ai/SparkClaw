package emailmanagement

import (
	"context"
	"os"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// previewHeaders is the allow-list rendered alongside the body. It mirrors the
// signal headers the parser already retains, plus the two a human checking an
// original actually looks for.
var previewHeaders = []string{"Date", "Message-ID", "Auto-Submitted", "List-Id", "Precedence", "X-Auto-Response-Suppress"}

// PreviewView is the safe reading surface for an original. It is a projection of
// what the parser already committed to the database, never a second byte route:
// the body is plain text and the captured HTML is deliberately never returned,
// so nothing an untrusted sender wrote can execute with WebChat's authority.
// Because it reads the stored representation, it keeps working after the local
// original has been cleaned up — which is exactly when the raw .eml cannot.
type PreviewView struct {
	Version           int64            `json:"version"`
	ID                string           `json:"id"`
	Subject           string           `json:"subject"`
	From              string           `json:"from"`
	To                []string         `json:"to"`
	CC                []string         `json:"cc"`
	SentAt            string           `json:"sent_at,omitempty"`
	BodyText          string           `json:"body_text"`
	HeaderLines       []string         `json:"header_lines"`
	Attachments       []AttachmentView `json:"attachments"`
	OriginalAvailable bool             `json:"original_available"`
	OriginalPurged    bool             `json:"original_purged"`
}

func (s *Service) Preview(ctx context.Context, owner, mailID string) (PreviewView, error) {
	return stableProjection(ctx, s, owner, func(version int64) (PreviewView, error) {
		mail, found, err := s.repository.GetEmailMail(ctx, owner, mailID)
		if err != nil {
			return PreviewView{}, err
		}
		if !found {
			return PreviewView{}, ErrNotFound
		}
		root, err := os.OpenRoot(s.opts.WorkspaceRoot)
		if err != nil {
			return PreviewView{}, err
		}
		defer root.Close()
		view, err := s.messageView(ctx, owner, mail, version, root)
		if err != nil {
			return PreviewView{}, err
		}
		out := PreviewView{Version: version, ID: mail.ID, Subject: view.Subject, From: view.From, To: view.To, CC: view.CC,
			SentAt: view.SentAt, BodyText: view.BodyText, HeaderLines: []string{},
			Attachments: view.Attachments, OriginalAvailable: view.OriginalAvailable, OriginalPurged: view.OriginalPurged}
		if out.Attachments == nil {
			out.Attachments = []AttachmentView{}
		}
		if mail.RepresentationID == "" {
			return out, nil
		}
		representation, found, err := s.repository.GetEmailRepresentation(ctx, owner, mail.RepresentationID)
		if err != nil {
			return out, err
		}
		if !found {
			return out, ErrNotFound
		}
		out.HeaderLines = previewHeaderLines(representation)
		return out, nil
	})
}

func previewHeaderLines(representation app.EmailRepresentation) []string {
	values := map[string]string{"Date": emailTime(representation.SourceTime), "Message-ID": representation.MessageID}
	for key, value := range representation.HeaderSignals {
		values[key] = value
	}
	lines := []string{}
	for _, key := range previewHeaders {
		if value := strings.TrimSpace(values[key]); value != "" {
			lines = append(lines, key+": "+value)
		}
	}
	return lines
}
