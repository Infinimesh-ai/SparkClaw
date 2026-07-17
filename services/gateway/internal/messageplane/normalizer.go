package messageplane

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type Ingress struct {
	Session       app.Session
	Message       app.Message
	SourceKind    app.MessageSourceKind
	Adapter       string
	EndpointID    app.EndpointID
	ScheduleID    app.ScheduleID
	ReturnRoute   *app.ReturnRoute
	Authorization app.MessageAuthorization
}

func Normalize(ingress Ingress) (app.MessageEnvelope, error) {
	if strings.TrimSpace(ingress.Message.ID) == "" {
		return app.MessageEnvelope{}, errors.New("message id is required before ingress normalization")
	}
	if strings.TrimSpace(ingress.Session.ID) == "" {
		return app.MessageEnvelope{}, errors.New("session id is required for ingress normalization")
	}
	sourceKind := ingress.SourceKind
	if sourceKind == "" {
		sourceKind = inferSourceKind(ingress.Session.Source)
	}
	adapter := strings.ToLower(strings.TrimSpace(ingress.Adapter))
	if adapter == "" {
		adapter = normalizedAdapter(ingress.Session.Source, sourceKind)
	}
	endpointID := ingress.EndpointID
	if endpointID == "" && sourceKind != app.MessageSourceTimer {
		endpointID = app.EndpointID("session:" + ingress.Session.ID)
	}
	returnRoute := app.ReturnRoute{Mode: app.ReturnNowhere}
	if ingress.ReturnRoute != nil {
		returnRoute = *ingress.ReturnRoute
	} else if endpointID != "" {
		returnRoute = app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: endpointID}
	}
	ownerID := strings.TrimSpace(ingress.Session.OwnerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	authorization := ingress.Authorization
	if strings.TrimSpace(authorization.PrincipalID) == "" {
		authorization.PrincipalID = ownerID
	}
	content, err := normalizeContent(ingress.Message)
	if err != nil {
		return app.MessageEnvelope{}, err
	}
	envelope := app.MessageEnvelope{
		SchemaVersion:  app.MessageEnvelopeSchemaVersion,
		ID:             "env_" + ingress.Message.ID,
		IdempotencyKey: ingress.Message.ID,
		CorrelationID:  ingress.Session.ID,
		Source: app.MessageSourceContext{
			Kind:            sourceKind,
			Adapter:         adapter,
			EndpointID:      endpointID,
			NativeMessageID: ingress.Message.ID,
			ScheduleID:      ingress.ScheduleID,
		},
		OwnerID:       ownerID,
		ActorID:       authorization.PrincipalID,
		Content:       content,
		ReturnRoute:   returnRoute,
		Authorization: authorization,
		CreatedAt:     ingress.Message.CreatedAt,
	}
	if err := ValidateEnvelope(envelope); err != nil {
		return app.MessageEnvelope{}, err
	}
	return envelope, nil
}

func ValidateEnvelope(envelope app.MessageEnvelope) error {
	if envelope.SchemaVersion != app.MessageEnvelopeSchemaVersion {
		return fmt.Errorf("unsupported message envelope schema version %d", envelope.SchemaVersion)
	}
	if strings.TrimSpace(envelope.ID) == "" || strings.TrimSpace(envelope.IdempotencyKey) == "" {
		return errors.New("message envelope identity and idempotency key are required")
	}
	switch envelope.Source.Kind {
	case app.MessageSourceWeb, app.MessageSourceThirdPartyDevice, app.MessageSourceTimer:
	default:
		return fmt.Errorf("unsupported message source kind %q", envelope.Source.Kind)
	}
	if strings.TrimSpace(envelope.OwnerID) == "" || strings.TrimSpace(envelope.ActorID) == "" {
		return errors.New("message envelope owner and actor are required")
	}
	if strings.TrimSpace(envelope.Authorization.PrincipalID) == "" || envelope.Authorization.PrincipalID != envelope.ActorID {
		return errors.New("message authorization principal must match the acting principal")
	}
	if envelope.CreatedAt.IsZero() {
		return errors.New("message envelope creation time is required")
	}
	if err := validateReturnRoute(envelope.ReturnRoute); err != nil {
		return err
	}
	if len(envelope.Content.Parts) == 0 {
		return errors.New("message content requires at least one part")
	}
	seen := make(map[string]bool, len(envelope.Content.Parts))
	for _, part := range envelope.Content.Parts {
		if strings.TrimSpace(part.ID) == "" {
			return errors.New("message part id is required")
		}
		if seen[part.ID] {
			return fmt.Errorf("message part %q appears more than once", part.ID)
		}
		seen[part.ID] = true
		if err := validatePart(part); err != nil {
			return fmt.Errorf("message part %q: %w", part.ID, err)
		}
	}
	return nil
}

func RoutingProjection(envelope app.MessageEnvelope) string {
	textParts := make([]string, 0, len(envelope.Content.Parts))
	attachments := make([]app.MessagePart, 0, len(envelope.Content.Parts))
	for _, part := range envelope.Content.Parts {
		if part.Kind == app.MessagePartText {
			textParts = append(textParts, part.Text)
		} else {
			attachments = append(attachments, part)
		}
	}
	content := strings.Join(textParts, "\n")
	if len(attachments) == 0 {
		return content
	}
	lines := []string{strings.TrimSpace(content), "", "Attached files for this user turn:"}
	for _, part := range attachments {
		ref := ""
		if part.Resource != nil {
			ref = strings.TrimSpace(part.Resource.Ref)
		}
		if ref == "" {
			continue
		}
		name := strings.TrimSpace(part.Name)
		if name == "" {
			name = path.Base(ref)
		}
		detail := "- " + name + " path=" + ref
		if part.ContentType != "" {
			detail += " content_type=" + part.ContentType
		}
		if part.Bytes > 0 {
			detail += fmt.Sprintf(" bytes=%d", part.Bytes)
		}
		if part.Width > 0 && part.Height > 0 {
			detail += fmt.Sprintf(" size=%dx%d", part.Width, part.Height)
		}
		if part.SHA256 != "" {
			detail += " sha256=" + part.SHA256
		}
		if part.Kind == app.MessagePartImage {
			detail += " media_kind=image"
		}
		lines = append(lines, detail)
	}
	lines = append(lines, "When the user asks about an attached image, use images.inspect with the listed path. For attached documents or text files, use the appropriate read/document tool. If the user wants an image as the response, return a single Markdown media link after generating or locating it with visible tools.")
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func ContentKinds(content app.MessageContent) []string {
	kinds := make([]string, 0, len(content.Parts))
	seen := map[app.MessagePartKind]bool{}
	for _, part := range content.Parts {
		if !seen[part.Kind] {
			seen[part.Kind] = true
			kinds = append(kinds, string(part.Kind))
		}
	}
	return kinds
}

func normalizeContent(message app.Message) (app.MessageContent, error) {
	parts := make([]app.MessagePart, 0, len(message.Attachments)+1)
	if strings.TrimSpace(message.Content) != "" {
		parts = append(parts, app.MessagePart{
			ID:          message.ID + ":part:0",
			Kind:        app.MessagePartText,
			Disposition: app.MessageDispositionInline,
			Text:        message.Content,
		})
	}
	for index, attachment := range message.Attachments {
		resource, err := attachmentResource(attachment)
		if err != nil {
			return app.MessageContent{}, fmt.Errorf("attachment %d: %w", index, err)
		}
		kind := attachmentKind(attachment)
		disposition := app.MessageDispositionAttachment
		if kind == app.MessagePartAudio && strings.Contains(strings.ToLower(attachment.Source), "voice") {
			disposition = app.MessageDispositionVoiceNote
		}
		parts = append(parts, app.MessagePart{
			ID:          fmt.Sprintf("%s:part:%d", message.ID, index+1),
			Kind:        kind,
			Disposition: disposition,
			ArtifactID:  strings.TrimSpace(attachment.ArtifactID),
			Resource:    &resource,
			Name:        strings.TrimSpace(attachment.Name),
			ContentType: strings.TrimSpace(attachment.ContentType),
			Bytes:       attachment.Bytes,
			Width:       attachment.Width,
			Height:      attachment.Height,
			SHA256:      strings.TrimSpace(attachment.SHA256),
			Caption:     strings.TrimSpace(attachment.Caption),
		})
	}
	return app.MessageContent{Parts: parts}, nil
}

func attachmentResource(attachment app.MessageAttachment) (app.ResourceRef, error) {
	relPath := strings.TrimSpace(strings.ReplaceAll(attachment.RelPath, "\\", "/"))
	if relPath != "" {
		cleaned := path.Clean(relPath)
		if strings.HasPrefix(cleaned, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return app.ResourceRef{}, errors.New("workspace attachment path must stay inside the workspace")
		}
		return app.ResourceRef{Kind: "workspace_file", Ref: cleaned, Provenance: "message_attachment"}, nil
	}
	if uri := strings.TrimSpace(attachment.URI); uri != "" {
		return app.ResourceRef{Kind: "artifact", Ref: uri, Provenance: "message_attachment"}, nil
	}
	if artifactID := strings.TrimSpace(attachment.ArtifactID); artifactID != "" {
		return app.ResourceRef{Kind: "artifact", Ref: artifactID, Provenance: "message_attachment"}, nil
	}
	return app.ResourceRef{}, errors.New("binary message part requires a governed resource reference")
}

func attachmentKind(attachment app.MessageAttachment) app.MessagePartKind {
	contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return app.MessagePartImage
	case strings.HasPrefix(contentType, "audio/"):
		return app.MessagePartAudio
	}
	extension := strings.ToLower(path.Ext(attachment.Name))
	switch extension {
	case ".avif", ".gif", ".heic", ".jpeg", ".jpg", ".png", ".webp":
		return app.MessagePartImage
	case ".aac", ".flac", ".m4a", ".mp3", ".ogg", ".opus", ".wav":
		return app.MessagePartAudio
	default:
		return app.MessagePartFile
	}
}

func inferSourceKind(source string) app.MessageSourceKind {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "", "web", "webchat":
		return app.MessageSourceWeb
	case "timer", "schedule", "scheduled":
		return app.MessageSourceTimer
	default:
		return app.MessageSourceThirdPartyDevice
	}
}

func normalizedAdapter(source string, kind app.MessageSourceKind) string {
	adapter := strings.ToLower(strings.TrimSpace(source))
	if adapter != "" {
		return adapter
	}
	return string(kind)
}

func validateReturnRoute(route app.ReturnRoute) error {
	switch route.Mode {
	case app.ReturnToSource:
		if route.SourceEndpointID == "" {
			return errors.New("source return route requires a source endpoint")
		}
	case app.ReturnToEndpoint:
		if route.EndpointID == "" {
			return errors.New("explicit return route requires an endpoint")
		}
	case app.ReturnNowhere:
	default:
		return fmt.Errorf("unsupported return route mode %q", route.Mode)
	}
	return nil
}

func validatePart(part app.MessagePart) error {
	switch part.Kind {
	case app.MessagePartText:
		if strings.TrimSpace(part.Text) == "" {
			return errors.New("text part content is empty")
		}
		if part.Disposition != app.MessageDispositionInline {
			return errors.New("text part must use inline disposition")
		}
	case app.MessagePartImage, app.MessagePartAudio, app.MessagePartFile:
		if part.Resource == nil || strings.TrimSpace(part.Resource.Ref) == "" {
			return errors.New("binary part requires a governed resource reference")
		}
		if part.Disposition != app.MessageDispositionAttachment && !(part.Kind == app.MessagePartAudio && part.Disposition == app.MessageDispositionVoiceNote) {
			return errors.New("binary part has an invalid disposition")
		}
	default:
		return fmt.Errorf("unsupported content kind %q", part.Kind)
	}
	if part.Bytes < 0 || part.Width < 0 || part.Height < 0 {
		return errors.New("binary metadata cannot be negative")
	}
	return nil
}
