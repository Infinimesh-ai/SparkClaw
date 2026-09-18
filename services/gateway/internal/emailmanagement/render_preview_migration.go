package emailmanagement

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// RenderPreviewMigrationReport is emitted by the operator-only cutover command.
// Failed records are never silently skipped: the caller must keep the old
// release in place until Failed is zero.
type RenderPreviewMigrationReport struct {
	OwnerID          string                          `json:"owner_id"`
	SanitizerVersion string                          `json:"sanitizer_version"`
	Total            int                             `json:"total"`
	Migrated         int                             `json:"migrated"`
	Reused           int                             `json:"reused"`
	Failed           int                             `json:"failed"`
	Failures         []RenderPreviewMigrationFailure `json:"failures"`
}

type RenderPreviewMigrationFailure struct {
	MailID           string `json:"mail_id"`
	RepresentationID string `json:"representation_id,omitempty"`
	Code             string `json:"code"`
}

// MigrateRenderPreviews performs the one-time serial migration for one owner.
// It does not run in the gateway lifecycle and does not enqueue work. Repeated
// operator runs reuse committed current-version previews and resume the rest.
func (s *Service) MigrateRenderPreviews(ctx context.Context, owner string) (RenderPreviewMigrationReport, error) {
	report := RenderPreviewMigrationReport{OwnerID: owner, SanitizerVersion: emailRenderSanitizerVersion, Failures: []RenderPreviewMigrationFailure{}}
	if strings.TrimSpace(owner) == "" {
		return report, ErrInvalidInput
	}
	q := store.EmailQuery{OwnerID: owner, Limit: 100}
	for {
		page, err := s.repository.ListEmailMails(ctx, q)
		if err != nil {
			return report, err
		}
		for _, mail := range page.Items {
			report.Total++
			if err := ctx.Err(); err != nil {
				return report, err
			}
			code, migrated, err := s.migrateRenderPreview(ctx, owner, mail)
			if err != nil {
				return report, err
			}
			if code != "" {
				report.Failed++
				report.Failures = append(report.Failures, RenderPreviewMigrationFailure{MailID: mail.ID, RepresentationID: mail.RepresentationID, Code: code})
				continue
			}
			if migrated {
				report.Migrated++
			} else {
				report.Reused++
			}
		}
		if page.NextCursor == "" {
			break
		}
		q.After = page.NextCursor
	}
	return report, nil
}

func (s *Service) migrateRenderPreview(ctx context.Context, owner string, mail app.EmailMail) (code string, migrated bool, err error) {
	if mail.RepresentationID == "" {
		return "representation_missing", false, nil
	}
	representation, found, err := s.repository.GetEmailRepresentation(ctx, owner, mail.RepresentationID)
	if err != nil {
		return "", false, err
	}
	if !found || representation.MailID != mail.ID {
		return "representation_missing", false, nil
	}
	if mail.CaptureID == "" {
		return "capture_missing", false, nil
	}
	capture, found, err := s.repository.GetEmailCapture(ctx, owner, mail.CaptureID)
	if err != nil {
		return "", false, err
	}
	if !found || capture.MailID != mail.ID || capture.PurgedAt != nil {
		return "source_unavailable", false, nil
	}
	current, found, err := s.repository.GetEmailRenderPreview(ctx, owner, representation.ID, emailRenderSanitizerVersion)
	if err != nil {
		return "", false, err
	}
	if found && current.State == app.EmailRenderReady {
		if current.SourceSHA256 != capture.OriginalSHA256 || !s.validStoredRenderPreview(ctx, owner, representation.ID, current) {
			return "preview_artifact_invalid", false, nil
		}
		return "", false, nil
	}
	if found {
		return "preview_record_conflict", false, nil
	}
	mailbox, found, err := s.repository.GetEmailMailbox(ctx, owner, mail.MailboxID)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "mailbox_missing", false, nil
	}
	candidate, candidateCode, err := renderCandidateFromStoredCapture(ctx, s.opts.WorkspaceRoot, owner, mail, mailbox, capture, representation)
	if err != nil {
		return "", false, err
	}
	if candidateCode != "" {
		return candidateCode, false, nil
	}
	preview, err := buildEmailRenderPreview(ctx, s.opts.WorkspaceRoot, owner, representation, capture, candidate)
	if err != nil {
		return "", false, err
	}
	if preview.State != app.EmailRenderReady {
		return preview.FailureCode, false, nil
	}
	_, err = s.repository.PublishEmailRenderPreview(ctx, store.EmailRenderPreviewCommand{
		EmailCommand: store.EmailCommand{OwnerID: owner, CommandKey: "render-preview:" + emailRenderSanitizerVersion + ":" + representation.ID},
		Preview:      preview,
	})
	return "", true, err
}

func renderCandidateFromStoredCapture(ctx context.Context, workspace, owner string, mail app.EmailMail, mailbox app.EmailMailbox, capture app.EmailCaptureVersion, representation app.EmailRepresentation) (emailRenderCandidate, string, error) {
	manifest, files, err := loadManifest(ctx, workspace, owner, capture)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return emailRenderCandidate{}, "", ctxErr
		}
		return emailRenderCandidate{}, migrationFailureCode(err), nil
	}
	if manifest.Provider != mailbox.Provider || !strings.EqualFold(manifest.AccountAddress, mailbox.Address) || manifest.ProviderMessageID != mail.ProviderMessageID {
		return emailRenderCandidate{}, "source_identity_mismatch", nil
	}
	ref, ok := files[capture.OriginalPath]
	if !ok || ref.SHA256 != capture.OriginalSHA256 {
		return emailRenderCandidate{}, "original_missing", nil
	}
	scratch := app.EmailRepresentation{ID: representation.ID, MailID: mail.ID, CaptureID: capture.ID,
		HeaderSignals: map[string]string{"_owner_scope": ownerScope(owner)}}
	candidate, err := parseMIMEOriginalContent(ctx, workspace, ref, &scratch, false)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return emailRenderCandidate{}, "", ctxErr
		}
		return emailRenderCandidate{}, migrationFailureCode(err), nil
	}
	return candidate, "", nil
}

func migrationFailureCode(err error) string {
	if err == nil {
		return ""
	}
	for _, code := range []string{
		"email_source_scope", "email_source_missing", "email_source_invalid", "email_source_integrity",
		"email_source_mime_invalid", "email_source_body_invalid", "email_original_missing",
	} {
		if err.Error() == code {
			return strings.TrimPrefix(code, "email_")
		}
	}
	return "render_source_error"
}

func (s *Service) validStoredRenderPreview(ctx context.Context, owner, representationID string, preview app.EmailRenderPreview) bool {
	expectedPrefix := path.Join("email", ownerScope(owner), "normalized", representationID, "render", emailRenderSanitizerVersion) + "/"
	if !strings.HasPrefix(preview.ArtifactPath, expectedPrefix) || path.Base(preview.ArtifactPath) != "content.json" {
		return false
	}
	root, err := os.OpenRoot(s.opts.WorkspaceRoot)
	if err != nil {
		return false
	}
	defer root.Close()
	raw, err := readVerifiedFile(ctx, root, sourceFile{Path: preview.ArtifactPath, SHA256: preview.ArtifactSHA256, Bytes: preview.ArtifactBytes}, maxEmailRenderArtifactBytes)
	if err != nil {
		return false
	}
	var document emailRenderDocument
	return json.Unmarshal(raw, &document) == nil && validateEmailRenderDocument(document) == nil
}
