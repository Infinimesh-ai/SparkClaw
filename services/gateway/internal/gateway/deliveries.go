package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type publicDeliveryRecipient struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type publicDeliveryEndpoint struct {
	ID                  app.EndpointID           `json:"id"`
	Channel             string                   `json:"channel"`
	SoftwareDisplayName string                   `json:"software_display_name"`
	AccountDisplayName  string                   `json:"account_display_name"`
	ConversationLabel   string                   `json:"conversation_label,omitempty"`
	Recipient           publicDeliveryRecipient  `json:"recipient"`
	Capabilities        app.DeliveryCapabilities `json:"capabilities"`
}

type createDeliveryInput struct {
	Target         app.EndpointID `json:"target"`
	IdempotencyKey string         `json:"idempotency_key"`
	Confirmed      bool           `json:"confirmed"`
	Content        struct {
		Parts []browserDeliveryPart `json:"parts"`
	} `json:"content"`
}

type browserDeliveryPart struct {
	ID          string                     `json:"id"`
	Kind        app.MessagePartKind        `json:"kind"`
	Disposition app.MessagePartDisposition `json:"disposition"`
	Text        string                     `json:"text,omitempty"`
	ArtifactID  string                     `json:"artifact_id,omitempty"`
	Name        string                     `json:"name,omitempty"`
	Caption     string                     `json:"caption,omitempty"`
}

type publicDelivery struct {
	ID                   app.DeliveryID       `json:"id"`
	Direction            app.MessageDirection `json:"direction"`
	Origin               app.DeliveryOrigin   `json:"origin"`
	Status               app.DeliveryStatus   `json:"status"`
	Target               app.EndpointID       `json:"target"`
	SoftwareDisplayName  string               `json:"software_display_name"`
	RecipientDisplayName string               `json:"recipient_display_name"`
	AccountDisplayName   string               `json:"account_display_name"`
	Content              app.MessageContent   `json:"content"`
	Receipt              *app.DeliveryReceipt `json:"receipt,omitempty"`
	Attempts             int                  `json:"attempts"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

func (s *Server) listDeliveryEndpoints(w http.ResponseWriter, r *http.Request) {
	if s.endpoints == nil || s.providers == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("message delivery is unavailable"))
		return
	}
	principal := principalForRequest(r)
	endpoints, err := s.endpoints.List(r.Context(), principal.OwnerID, principal.ActorID)
	if err != nil {
		writeError(w, deliveryHTTPStatus(errorCode(err)), err)
		return
	}
	out := make([]publicDeliveryEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		capabilities, ok := s.providers.Capabilities(endpoint.ProviderKey)
		if !ok {
			continue
		}
		out = append(out, publicDeliveryEndpoint{
			ID: endpoint.ID, Channel: endpoint.ProviderKey, SoftwareDisplayName: endpoint.SoftwareDisplayName,
			AccountDisplayName: endpoint.AccountDisplayName, ConversationLabel: endpoint.ConversationLabel,
			Recipient:    publicDeliveryRecipient{ID: opaqueRecipientID(principal, endpoint), DisplayName: endpoint.RecipientDisplayName},
			Capabilities: capabilities,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": out})
}

func (s *Server) createDelivery(w http.ResponseWriter, r *http.Request) {
	if s.endpoints == nil || s.providers == nil || s.delivery == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("message delivery is unavailable"))
		return
	}
	var input createDeliveryInput
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid delivery request"))
		return
	}
	if !input.Confirmed {
		writeError(w, http.StatusBadRequest, errors.New("delivery confirmation is required"))
		return
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.Target == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 200 {
		writeError(w, http.StatusBadRequest, errors.New("delivery target and idempotency key are required"))
		return
	}
	principal := principalForRequest(r)
	endpoint, err := s.endpoints.GetForMessageSend(r.Context(), input.Target, principal.OwnerID, principal.ActorID)
	if err != nil {
		writeError(w, deliveryHTTPStatus(errorCode(err)), err)
		return
	}
	content, err := browserMessageContent(input.Content.Parts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	content, err = delivery.ResolveBrowserContent(r.Context(), s.store, principal.OwnerID, s.cfg.Workspaces.DefaultRoot, content)
	if err != nil {
		writeError(w, deliveryHTTPStatus(delivery.ErrorCode(err)), err)
		return
	}
	capabilities, ok := s.providers.Capabilities(endpoint.ProviderKey)
	if !ok {
		writeError(w, http.StatusConflict, delivery.NewError(delivery.CodeBindingUnavailable, "delivery provider is unavailable", "blocked"))
		return
	}
	if err := delivery.ValidateCapabilities(capabilities, content); err != nil {
		writeError(w, deliveryHTTPStatus(delivery.ErrorCode(err)), err)
		return
	}
	digest, err := delivery.ContentDigest(input.Target, content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("delivery content could not be verified"))
		return
	}

	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	if existing, ok, err := s.store.FindMessageDeliveryByIdempotency(r.Context(), principal.OwnerID, principal.ActorID, input.IdempotencyKey); err != nil {
		writeDeliveryStoreError(w, err)
		return
	} else if ok {
		if existing.ContentDigest != digest || existing.Request.Target != input.Target {
			writeError(w, http.StatusConflict, delivery.NewError(delivery.CodeIdempotencyConflict, "idempotency key was already used for another delivery", "blocked"))
			return
		}
		writeJSON(w, http.StatusOK, publicMessageDelivery(existing))
		return
	}
	now := time.Now().UTC()
	request := app.DeliveryRequest{
		SchemaVersion: app.DeliveryRequestSchemaVersion, ID: app.DeliveryID(app.NewID("del")),
		IdempotencyKey: input.IdempotencyKey, OwnerID: principal.OwnerID, ActorID: principal.ActorID,
		Authorization: app.MessageAuthorization{PrincipalID: principal.ActorID, Scope: []string{app.BindingScopeMessageSendSelf}},
		Target:        endpoint.ID, Content: content, Origin: app.DeliveryOriginWebDirect,
		ApprovalSource: "web_review_confirmation", ContentDigest: digest, CreatedAt: now,
	}
	record := app.MessageDeliveryRecord{
		ID: request.ID, Direction: app.MessageDirectionSend, OwnerID: principal.OwnerID, ActorID: principal.ActorID,
		Origin: app.DeliveryOriginWebDirect, Request: request,
		TargetSelection: app.DeliveryTargetSelection{Status: app.TargetResolved, ResolvedEndpointID: endpoint.ID, ResolutionRule: "explicit_opaque_endpoint"},
		Status:          app.DeliveryApproved, ApprovalSource: request.ApprovalSource, ContentDigest: digest,
		SoftwareDisplayName: endpoint.SoftwareDisplayName, RecipientDisplayName: endpoint.RecipientDisplayName,
		AccountDisplayName: endpoint.AccountDisplayName, CreatedAt: now, UpdatedAt: now,
	}
	record, err = persistMessageDelivery(r.Context(), s.store, record)
	if err != nil {
		writeDeliveryStoreError(w, err)
		return
	}
	record.Status = app.DeliverySending
	record.Attempts = 1
	record, err = persistMessageDelivery(r.Context(), s.store, record)
	if err != nil {
		writeDeliveryStoreError(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	receipt, sendErr := s.delivery.Deliver(ctx, request)
	receipt.Attempt = record.Attempts
	record.Receipt = &receipt
	record.Status = receipt.Status
	record, err = persistMessageDelivery(r.Context(), s.store, record)
	if err != nil {
		writeDeliveryStoreError(w, err)
		return
	}
	if sendErr != nil {
		writeDeliveryFailure(w, record, sendErr)
		return
	}
	writeJSON(w, http.StatusCreated, publicMessageDelivery(record))
}

func (s *Server) getDelivery(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	record, ok, err := s.store.GetMessageDelivery(r.Context(), app.DeliveryID(strings.TrimSpace(r.PathValue("id"))))
	if err != nil {
		writeDeliveryStoreError(w, err)
		return
	}
	if !ok || record.OwnerID != principal.OwnerID || record.ActorID != principal.ActorID {
		writeError(w, http.StatusNotFound, errors.New("delivery not found"))
		return
	}
	writeJSON(w, http.StatusOK, publicMessageDelivery(record))
}

func (s *Server) listDeliveries(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	records, err := s.store.ListMessageDeliveries(r.Context(), principal.OwnerID, principal.ActorID, boundedHistoryLimit(r))
	if err != nil {
		writeDeliveryStoreError(w, err)
		return
	}
	out := make([]publicDelivery, 0, len(records))
	for _, record := range records {
		out = append(out, publicMessageDelivery(record))
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": out})
}

func (s *Server) retryDelivery(w http.ResponseWriter, r *http.Request) {
	if s.delivery == nil || s.endpoints == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("message delivery is unavailable"))
		return
	}
	var input struct {
		Confirmed bool `json:"confirmed"`
	}
	if err := readJSON(r, &input); err != nil || !input.Confirmed {
		writeError(w, http.StatusBadRequest, errors.New("delivery retry confirmation is required"))
		return
	}
	principal := principalForRequest(r)
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	record, ok, err := s.store.GetMessageDelivery(r.Context(), app.DeliveryID(strings.TrimSpace(r.PathValue("id"))))
	if err != nil {
		writeDeliveryStoreError(w, err)
		return
	}
	if !ok || record.OwnerID != principal.OwnerID || record.ActorID != principal.ActorID {
		writeError(w, http.StatusNotFound, errors.New("delivery not found"))
		return
	}
	if record.Receipt == nil || record.Receipt.RetryState != "retryable" || record.Receipt.ErrorCode == delivery.CodeOutcomeUnknown {
		writeError(w, http.StatusConflict, errors.New("delivery is not safe to retry"))
		return
	}
	if _, err := s.endpoints.GetForMessageSend(r.Context(), record.Request.Target, principal.OwnerID, principal.ActorID); err != nil {
		writeError(w, deliveryHTTPStatus(errorCode(err)), err)
		return
	}
	retryContent := failedDeliveryParts(record.Request.Content, record.Receipt.PartReceipts)
	if len(retryContent.Parts) == 0 {
		writeError(w, http.StatusConflict, errors.New("delivery has no failed parts to retry"))
		return
	}
	record.Attempts++
	record.Status = app.DeliverySending
	record, err = persistMessageDelivery(r.Context(), s.store, record)
	if err != nil {
		writeDeliveryStoreError(w, err)
		return
	}
	request := record.Request
	request.Content = retryContent
	request.IdempotencyKey += ":retry:" + time.Now().UTC().Format("20060102T150405.000000000")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	receipt, sendErr := s.delivery.Deliver(ctx, request)
	receipt.Attempt = record.Attempts
	receipt.PartReceipts = mergePartReceipts(record.Receipt.PartReceipts, receipt.PartReceipts)
	receipt.Status = aggregatePartStatus(receipt.PartReceipts, receipt.Status)
	record.Receipt = &receipt
	record.Status = receipt.Status
	record, err = persistMessageDelivery(r.Context(), s.store, record)
	if err != nil {
		writeDeliveryStoreError(w, err)
		return
	}
	if sendErr != nil {
		writeDeliveryFailure(w, record, sendErr)
		return
	}
	writeJSON(w, http.StatusOK, publicMessageDelivery(record))
}

func (s *Server) listMessageHistory(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	limit := boundedHistoryLimit(r)
	type historyItem struct {
		ID                   string               `json:"id"`
		Direction            app.MessageDirection `json:"direction"`
		Status               string               `json:"status"`
		SoftwareDisplayName  string               `json:"software_display_name"`
		RecipientDisplayName string               `json:"recipient_display_name"`
		AccountDisplayName   string               `json:"account_display_name"`
		Content              string               `json:"content,omitempty"`
		CreatedAt            time.Time            `json:"created_at"`
	}
	items := []historyItem{}
	receives, err := s.store.ListMessageReceives(r.Context(), principal.OwnerID, principal.ActorID, limit)
	if err != nil {
		writeDeliveryStoreError(w, err)
		return
	}
	for _, record := range receives {
		content := ""
		message, ok, err := s.store.GetExternalChatMessage(r.Context(), record.LinkedMessageID)
		if err != nil {
			writeDeliveryStoreError(w, err)
			return
		}
		if ok {
			content = message.Content
		}
		items = append(items, historyItem{ID: record.ID, Direction: record.Direction, Status: record.Status,
			SoftwareDisplayName: record.SoftwareDisplayName, RecipientDisplayName: record.RecipientDisplayName,
			AccountDisplayName: record.AccountDisplayName, Content: content, CreatedAt: record.CreatedAt})
	}
	deliveries, err := s.store.ListMessageDeliveries(r.Context(), principal.OwnerID, principal.ActorID, limit)
	if err != nil {
		writeDeliveryStoreError(w, err)
		return
	}
	for _, record := range deliveries {
		items = append(items, historyItem{ID: string(record.ID), Direction: record.Direction, Status: string(record.Status),
			SoftwareDisplayName: record.SoftwareDisplayName, RecipientDisplayName: record.RecipientDisplayName,
			AccountDisplayName: record.AccountDisplayName, Content: deliveryContentText(record.Request.Content), CreatedAt: record.CreatedAt})
	}
	slices.SortFunc(items, func(a, b historyItem) int { return b.CreatedAt.Compare(a.CreatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": items})
}

func persistMessageDelivery(ctx context.Context, repository store.DeliveryRecordRepository, record app.MessageDeliveryRecord) (app.MessageDeliveryRecord, error) {
	saved, err := repository.SaveMessageDelivery(ctx, record)
	return store.ReconcileMessageDeliveryWrite(ctx, repository, saved, err)
}

func writeDeliveryStoreError(w http.ResponseWriter, err error) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorInvalid:
		writeError(w, http.StatusBadRequest, errors.New("delivery request is invalid"))
	case store.StoreErrorNotFound:
		writeError(w, http.StatusNotFound, errors.New("delivery not found"))
	case store.StoreErrorConflict:
		writeError(w, http.StatusConflict, errors.New("delivery conflicts with existing state"))
	case store.StoreErrorCanceled:
		writeError(w, http.StatusRequestTimeout, errors.New("delivery request was canceled"))
	case store.StoreErrorTimeout:
		writeError(w, http.StatusGatewayTimeout, errors.New("delivery operation timed out"))
	default:
		writeError(w, http.StatusServiceUnavailable, errors.New("delivery service is unavailable"))
	}
}

func browserMessageContent(parts []browserDeliveryPart) (app.MessageContent, error) {
	if len(parts) == 0 {
		return app.MessageContent{}, delivery.NewError(delivery.CodePartUnsupported, "delivery requires at least one part", "blocked")
	}
	content := app.MessageContent{Parts: make([]app.MessagePart, 0, len(parts))}
	seen := map[string]bool{}
	for _, input := range parts {
		input.ID = strings.TrimSpace(input.ID)
		if input.ID == "" || seen[input.ID] {
			return app.MessageContent{}, delivery.NewError(delivery.CodePartUnsupported, "delivery part IDs must be unique", "blocked")
		}
		seen[input.ID] = true
		part := app.MessagePart{ID: input.ID, Kind: input.Kind, Disposition: input.Disposition, Text: strings.TrimSpace(input.Text), ArtifactID: strings.TrimSpace(input.ArtifactID), Name: strings.TrimSpace(input.Name), Caption: strings.TrimSpace(input.Caption)}
		switch part.Kind {
		case app.MessagePartText:
			if part.Disposition != app.MessageDispositionInline || part.Text == "" || part.ArtifactID != "" {
				return app.MessageContent{}, delivery.NewError(delivery.CodePartUnsupported, "text parts must contain inline text only", "blocked")
			}
		case app.MessagePartImage, app.MessagePartFile:
			if part.Disposition != app.MessageDispositionAttachment || part.ArtifactID == "" || part.Text != "" {
				return app.MessageContent{}, delivery.NewError(delivery.CodePartUnsupported, "binary attachment part is invalid", "blocked")
			}
		case app.MessagePartAudio:
			if (part.Disposition != app.MessageDispositionAttachment && part.Disposition != app.MessageDispositionVoiceNote) || part.ArtifactID == "" || part.Text != "" {
				return app.MessageContent{}, delivery.NewError(delivery.CodePartUnsupported, "audio part is invalid", "blocked")
			}
		default:
			return app.MessageContent{}, delivery.NewError(delivery.CodePartUnsupported, "delivery part kind is unsupported", "blocked")
		}
		content.Parts = append(content.Parts, part)
	}
	return content, nil
}

func publicMessageDelivery(record app.MessageDeliveryRecord) publicDelivery {
	content := app.MessageContent{Parts: append([]app.MessagePart(nil), record.Request.Content.Parts...)}
	for index := range content.Parts {
		content.Parts[index].Resource = nil
		content.Parts[index].SHA256 = ""
	}
	return publicDelivery{ID: record.ID, Direction: record.Direction, Origin: record.Origin, Status: record.Status,
		Target: record.Request.Target, SoftwareDisplayName: record.SoftwareDisplayName,
		RecipientDisplayName: record.RecipientDisplayName, AccountDisplayName: record.AccountDisplayName,
		Content: content, Receipt: record.Receipt, Attempts: record.Attempts, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}

func opaqueRecipientID(principal requestPrincipal, endpoint app.MessageEndpoint) string {
	sum := sha256.Sum256([]byte(principal.OwnerID + "\x00" + principal.ActorID + "\x00" + string(endpoint.ID) + "\x00" + endpoint.ExternalUserRef))
	return "recipient:" + hex.EncodeToString(sum[:12])
}

func deliveryHTTPStatus(code string) int {
	switch code {
	case delivery.CodeScopeDenied, delivery.CodeCrossUserDenied:
		return http.StatusForbidden
	case delivery.CodeBindingUnavailable:
		return http.StatusConflict
	case delivery.CodePartUnsupported, delivery.CodeArtifactInvalid:
		return http.StatusUnprocessableEntity
	case delivery.CodePayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	case delivery.CodeIdempotencyConflict:
		return http.StatusConflict
	case delivery.CodeProviderRetryable:
		return http.StatusBadGateway
	case delivery.CodeOutcomeUnknown:
		return http.StatusBadGateway
	default:
		return http.StatusBadRequest
	}
}

func writeDeliveryFailure(w http.ResponseWriter, record app.MessageDeliveryRecord, err error) {
	status := deliveryHTTPStatus(delivery.ErrorCode(err))
	writeJSON(w, status, map[string]any{"error": err.Error(), "code": delivery.ErrorCode(err), "delivery": publicMessageDelivery(record)})
}

func boundedHistoryLimit(r *http.Request) int {
	limit := queryInt(r, "limit", 50)
	if limit <= 0 || limit > 200 {
		return 50
	}
	return limit
}

func failedDeliveryParts(content app.MessageContent, receipts []app.PartDeliveryReceipt) app.MessageContent {
	if len(receipts) == 0 {
		return content
	}
	failed := map[string]bool{}
	for _, receipt := range receipts {
		failed[receipt.PartID] = receipt.Status != "sent"
	}
	out := app.MessageContent{}
	for _, part := range content.Parts {
		if failed[part.ID] {
			out.Parts = append(out.Parts, part)
		}
	}
	return out
}

func mergePartReceipts(existing, retry []app.PartDeliveryReceipt) []app.PartDeliveryReceipt {
	byID := map[string]app.PartDeliveryReceipt{}
	order := []string{}
	for _, receipt := range existing {
		byID[receipt.PartID] = receipt
		order = append(order, receipt.PartID)
	}
	for _, receipt := range retry {
		if _, ok := byID[receipt.PartID]; !ok {
			order = append(order, receipt.PartID)
		}
		byID[receipt.PartID] = receipt
	}
	out := make([]app.PartDeliveryReceipt, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func aggregatePartStatus(receipts []app.PartDeliveryReceipt, fallback app.DeliveryStatus) app.DeliveryStatus {
	if len(receipts) == 0 {
		return fallback
	}
	sent := 0
	for _, receipt := range receipts {
		if receipt.Status == "sent" {
			sent++
		}
	}
	if sent == len(receipts) {
		return app.DeliverySucceeded
	}
	if sent > 0 {
		return app.DeliveryPartiallySent
	}
	return fallback
}

func deliveryContentText(content app.MessageContent) string {
	values := []string{}
	for _, part := range content.Parts {
		if part.Kind == app.MessagePartText {
			values = append(values, part.Text)
		} else {
			values = append(values, "["+string(part.Kind)+": "+part.Name+"]")
		}
	}
	return strings.Join(values, "\n")
}
