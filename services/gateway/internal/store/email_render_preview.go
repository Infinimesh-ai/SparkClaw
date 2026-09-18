package store

import (
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const maxEmailRenderPreviewBytes = 2 << 20

func emailValidateRenderPreview(p app.EmailRenderPreview, mail app.EmailMail, representation app.EmailRepresentation, capture app.EmailCaptureVersion) error {
	if p.MailID != mail.ID || p.RepresentationID != representation.ID || p.CaptureID != capture.ID ||
		p.SourceSHA256 != capture.OriginalSHA256 || strings.TrimSpace(p.SanitizerVersion) == "" || len(p.SanitizerVersion) > 64 ||
		!slices.Contains([]string{app.EmailRenderReady, app.EmailRenderUnavailable, app.EmailRenderFailed}, p.State) ||
		p.EmbeddedResourceCount < 0 || p.EmbeddedResourceCount > 20 || p.EmbeddedResourceBytes < 0 || p.EmbeddedResourceBytes > 5<<20 {
		return errEmailInvalid
	}
	if p.State == app.EmailRenderReady {
		artifactReady := emailSafePath(p.ArtifactPath) && emailHashValid(p.ArtifactSHA256) && p.ArtifactBytes >= 1 && p.ArtifactBytes <= maxEmailRenderPreviewBytes
		legacyReady := emailSafePath(p.HTMLPath) && emailHashValid(p.HTMLSHA256) && p.HTMLBytes >= 1 && p.HTMLBytes <= maxEmailRenderPreviewBytes
		if artifactReady == legacyReady || p.FailureCode != "" {
			return errEmailInvalid
		}
	} else if p.ArtifactPath != "" || p.ArtifactSHA256 != "" || p.ArtifactBytes != 0 || p.HTMLPath != "" || p.HTMLSHA256 != "" || p.HTMLBytes != 0 || strings.TrimSpace(p.FailureCode) == "" || len(p.FailureCode) > 128 {
		return errEmailInvalid
	}
	return nil
}

func emailSaveRenderPreview(e *emailEngine, p app.EmailRenderPreview, mail app.EmailMail, representation app.EmailRepresentation, capture app.EmailCaptureVersion) (app.EmailRenderPreview, error) {
	p.ID = emailID("render_preview", representation.ID, p.SanitizerVersion)
	if err := emailValidateRenderPreview(p, mail, representation, capture); err != nil {
		return p, err
	}
	if current, ok := emailGet[app.EmailRenderPreview](e, "render_preview", p.ID); ok {
		if current.MailID == p.MailID && current.CaptureID == p.CaptureID && current.RepresentationID == p.RepresentationID &&
			current.SourceSHA256 == p.SourceSHA256 && current.SanitizerVersion == p.SanitizerVersion && current.State == p.State &&
			current.ArtifactPath == p.ArtifactPath && current.ArtifactSHA256 == p.ArtifactSHA256 && current.ArtifactBytes == p.ArtifactBytes &&
			current.HTMLPath == p.HTMLPath && current.HTMLSHA256 == p.HTMLSHA256 && current.HTMLBytes == p.HTMLBytes &&
			current.EmbeddedResourceCount == p.EmbeddedResourceCount && current.EmbeddedResourceBytes == p.EmbeddedResourceBytes &&
			current.FailureCode == p.FailureCode {
			return current, e.err
		}
		return current, errEmailConflict
	}
	p.CreatedAt = e.now
	emailPut(e, "render_preview", p.ID, representation.ID, mail.ID, p.State, "", p.ID, p)
	return p, e.err
}

func emailPublishRenderPreview(e *emailEngine, c EmailRenderPreviewCommand) (app.EmailRenderPreview, error) {
	p := c.Preview
	mail, err := emailMail(e, p.MailID)
	if err != nil {
		return p, err
	}
	representation, ok := emailGet[app.EmailRepresentation](e, "representation", p.RepresentationID)
	if !ok || representation.MailID != mail.ID || mail.RepresentationID != representation.ID {
		return p, errEmailConflict
	}
	capture, ok := emailGet[app.EmailCaptureVersion](e, "capture", p.CaptureID)
	if !ok || capture.MailID != mail.ID || representation.CaptureID != capture.ID {
		return p, errEmailConflict
	}
	return emailSaveRenderPreview(e, p, mail, representation, capture)
}

func emailGetRenderPreview(e *emailEngine, representationID, sanitizerVersion string) (emailOptional[app.EmailRenderPreview], error) {
	if representationID == "" || sanitizerVersion == "" {
		return emailOptional[app.EmailRenderPreview]{}, errEmailInvalid
	}
	id := emailID("render_preview", representationID, sanitizerVersion)
	p, ok := emailGet[app.EmailRenderPreview](e, "render_preview", id)
	return emailOptional[app.EmailRenderPreview]{Value: p, Found: ok}, e.err
}
