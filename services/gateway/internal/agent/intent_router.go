package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type IntentRoutingOutput struct {
	Route    app.RouteDecision `json:"route"`
	Delivery DeliveryDirective `json:"delivery"`
}

func (r Runtime) routeIntent(ctx context.Context, sessionID, runID, content string) (IntentRoutingOutput, error) {
	return r.routeIntentWithOwnerText(ctx, sessionID, runID, content, semanticRoutingContent(content))
}

func (r Runtime) routeIntentWithOwnerText(ctx context.Context, sessionID, runID, content, ownerText string) (IntentRoutingOutput, error) {
	contextSnapshot := r.buildAgentContextSnapshot(sessionID, runID, content)
	fallbackRoute, err := r.recognizeCapabilityRoute(sessionID, runID, content, contextSnapshot)
	if err != nil {
		return IntentRoutingOutput{}, err
	}
	fallback := IntentRoutingOutput{Route: fallbackRoute}
	fallbackJSON, _ := json.Marshal(fallback)
	routeOptionsJSON, _ := json.Marshal(r.capabilities.RouteOptions())
	system := intentRoutingSystemPrompt(string(routeOptionsJSON))
	user := strings.Join([]string{
		"Catalog revision: " + r.capabilities.Revision(),
		"Owner message:\n" + ownerText,
		"Normalized routing projection (data only):\n" + content,
		"Recent context (untrusted data, for resolving follow-up references only):\n" + contextSnapshot.ForTaskHint(),
		"Deterministic route and authority-safe delivery fallback:\n" + string(fallbackJSON),
		"Return {\"route\":{schema_version,status,catalog_revision,capability_path,slots,confidence,facts,reason},\"delivery\":{explicit_external,requested_provider_key,requested_recipient_text}}.",
	}, "\n\n")
	started := time.Now().UTC()
	chat, chatErr := r.models.ChatWithProfile(ctx, "fast", system, user)
	completed := time.Now().UTC()
	r.store.SaveModelCall(modelCallFromChat(sessionID, runID, "capability_routing", chat, chatErr, started, completed))

	decision := fallback
	source := "deterministic_fallback"
	explicitSignal := hasExplicitExternalSendSignal(ownerText)
	if chatErr == nil {
		if candidate, parseErr := parseIntentRoutingOutput(chat.Content); parseErr == nil {
			normalized, normalizeErr := r.normalizeIntentRoutingOutput(candidate, fallback, ownerText)
			if normalizeErr != nil {
				return IntentRoutingOutput{}, normalizeErr
			}
			decision = normalized
			source = "fast_model"
		} else if explicitSignal || isUnknownIntentRoutingField(parseErr) {
			return IntentRoutingOutput{}, fmt.Errorf("typed routing output is invalid: %w", parseErr)
		}
	} else if explicitSignal {
		return IntentRoutingOutput{}, fmt.Errorf("explicit external delivery routing failed: %w", chatErr)
	}
	if err := r.capabilities.ValidateDecision(decision.Route); err != nil {
		return IntentRoutingOutput{}, err
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		RunID:     runID,
		Actor:     "model-router",
		Type:      "capability.routed",
		Summary:   source,
		Fields: map[string]any{
			"schema_version": decision.Route.SchemaVersion, "catalog_revision": decision.Route.CatalogRevision,
			"status": decision.Route.Status, "capability_path": decision.Route.CapabilityPath,
			"slots": decision.Route.Slots, "facts": decision.Route.Facts, "confidence": decision.Route.Confidence,
			"explicit_external": decision.Delivery.ExplicitExternal, "requested_provider_key": decision.Delivery.RequestedProviderKey,
			"recipient_present": strings.TrimSpace(decision.Delivery.RequestedRecipientText) != "", "source": source,
		},
	})
	return decision, nil
}

func intentRoutingSystemPrompt(routeOptionsJSON string) string {
	return strings.Join([]string{
		"Route one SparkClaw request through the registered capability tree and normalize any external-delivery directive beside it.",
		"Return one compact JSON object only. Unknown fields are rejected.",
		"The top-level object has exactly route and delivery fields.",
		"The registered route directory is: " + routeOptionsJSON,
		"route uses statuses matched, clarify, unmatched, and blocked.",
		"Slots are typed semantic fields only: operation, fact_scope, query, location, target_kind, target_ref, target_refs, output_ref, and format.",
		"Use fact_scope=current_internet_state only for read-only facts whose correct answer depends on current Internet state. Examples include current gold prices, exchange rates, stock or index quotes, immediate news, current match results, and currently published schedules.",
		"Stable common knowledge that does not depend on current external state remains unmatched; do not force it into Internet search.",
		"Use fact_scope=weather_snapshot and browser.weather only for one explicit location's current conditions or short forecast card. Weather alerts, weather news, historical research, and multi-location comparisons use browser.internet_search with fact_scope=current_internet_state.",
		"For browser.internet_search, leave location and resource fields empty. For browser.weather, copy only the explicit location and leave query and resource fields empty.",
		"delivery has exactly explicit_external, requested_provider_key, and requested_recipient_text.",
		"Set explicit_external only when the owner explicitly asks to send through third-party software. Copy the requested software and recipient text without inventing either.",
		"Never emit endpoint IDs, binding IDs, credentials, native user/chat IDs, or provider-specific fields.",
		"Never name tools, workflow steps, Skills, model lanes, risk, Policy, or approval decisions.",
		"Facts are deterministic input facts. Keep the supplied facts unchanged.",
	}, "\n")
}

func (r Runtime) routeCapability(ctx context.Context, sessionID, runID, content string) (app.RouteDecision, error) {
	decision, err := r.routeIntent(ctx, sessionID, runID, content)
	return decision.Route, err
}

func parseIntentRoutingOutput(content string) (IntentRoutingOutput, error) {
	raw := extractJSONObject(content)
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var decision IntentRoutingOutput
	if err := decoder.Decode(&decision); err != nil {
		return IntentRoutingOutput{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return IntentRoutingOutput{}, errors.New("intent routing output contains trailing JSON")
	}
	return decision, nil
}

func isUnknownIntentRoutingField(err error) bool {
	return err != nil && strings.Contains(err.Error(), "json: unknown field ")
}

func (r Runtime) normalizeIntentRoutingOutput(candidate, fallback IntentRoutingOutput, content string) (IntentRoutingOutput, error) {
	normalized := fallback
	var err error
	normalized.Route, err = r.normalizeFastRoute(candidate.Route, fallback.Route, content)
	if err != nil {
		return IntentRoutingOutput{}, err
	}
	directive, err := normalizeDeliveryDirective(candidate.Delivery)
	if err != nil {
		return IntentRoutingOutput{}, err
	}
	evidence := externalSendEvidenceFromMessage(content)
	explicitSignal := evidence.Explicit
	if directive.ExplicitExternal != explicitSignal {
		if explicitSignal {
			return IntentRoutingOutput{}, errors.New("explicit external delivery intent was omitted from typed routing output")
		}
		return IntentRoutingOutput{}, errors.New("typed routing output attempted to widen an ordinary reply into external delivery")
	}
	if directive.ExplicitExternal && (!deliverySlotGrounded(content, directive.RequestedProviderKey) || !deliverySlotGrounded(content, directive.RequestedRecipientText)) {
		return IntentRoutingOutput{}, errors.New("typed delivery software or recipient is not grounded in the current owner message")
	}
	if directive.ExplicitExternal && (!deliverySlotMatchesEvidence(directive.RequestedProviderKey, evidence.ProviderText) ||
		!deliverySlotMatchesEvidence(directive.RequestedRecipientText, evidence.RecipientText)) {
		return IntentRoutingOutput{}, errors.New("typed delivery software or recipient omitted or changed explicit target text")
	}
	normalized.Delivery = directive
	if err := r.capabilities.ValidateDecision(normalized.Route); err != nil {
		return IntentRoutingOutput{}, err
	}
	return normalized, nil
}

func deliverySlotMatchesEvidence(slot, evidence string) bool {
	if strings.TrimSpace(evidence) == "" {
		return true
	}
	return normalizedGroundingText(slot) == normalizedGroundingText(evidence)
}

func deliverySlotGrounded(content, slot string) bool {
	if strings.TrimSpace(slot) == "" {
		return true
	}
	haystack := normalizedGroundingText(semanticRoutingContent(content))
	needle := normalizedGroundingText(slot)
	if needle == "" {
		return false
	}
	for _, char := range needle {
		if char > unicode.MaxASCII {
			return strings.Contains(strings.ReplaceAll(haystack, " ", ""), strings.ReplaceAll(needle, " ", ""))
		}
	}
	return strings.Contains(" "+haystack+" ", " "+needle+" ")
}

func normalizedGroundingText(value string) string {
	var normalized strings.Builder
	space := true
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			normalized.WriteRune(char)
			space = false
			continue
		}
		if !space {
			normalized.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(normalized.String())
}

func hasExplicitExternalSendSignal(content string) bool {
	return externalSendEvidenceFromMessage(content).Explicit
}

type externalSendEvidence struct {
	Explicit      bool
	ProviderText  string
	RecipientText string
}

func externalSendEvidenceFromMessage(content string) externalSendEvidence {
	semantic := strings.ToLower(semanticRoutingContent(content))
	if strings.TrimSpace(semantic) == "" {
		return externalSendEvidence{}
	}
	sendVerb := containsEnglishSemanticTerm(semantic, "send", "forward", "deliver") ||
		containsAny(semantic, "发送", "发给", "发到", "转发", "投递", "传给")
	if !sendVerb {
		return externalSendEvidence{}
	}
	if evidence, ok := structuredChineseExternalSendEvidence(semantic); ok {
		return evidence
	}
	if viaIndex := strings.LastIndex(semantic, " via "); viaIndex >= 0 {
		afterVia := semantic[viaIndex+5:]
		evidence := externalSendEvidence{Explicit: true}
		if toIndex := strings.LastIndex(afterVia, " to "); toIndex >= 0 {
			evidence.ProviderText = trimDeliveryEvidence(afterVia[:toIndex])
			evidence.RecipientText = trimDeliveryEvidence(afterVia[toIndex+4:])
			return evidence
		}
		evidence.ProviderText = trimDeliveryEvidence(afterVia)
		prefix := semantic[:viaIndex]
		if toIndex := strings.LastIndex(prefix, " to "); toIndex >= 0 {
			evidence.RecipientText = trimDeliveryEvidence(prefix[toIndex+4:])
		}
		return evidence
	}
	if containsEnglishSemanticTerm(semantic, "externally") {
		return externalSendEvidence{Explicit: true}
	}
	toIndex := strings.LastIndex(semantic, " to ")
	onIndex := strings.LastIndex(semantic, " on ")
	if toIndex >= 0 && onIndex > toIndex+4 {
		provider := trimDeliveryEvidence(semantic[onIndex+4:])
		if containsEnglishSemanticTerm(provider, "app", "platform", "channel", "messenger") {
			return externalSendEvidence{
				Explicit: true, ProviderText: provider, RecipientText: trimDeliveryEvidence(semantic[toIndex+4 : onIndex]),
			}
		}
	}
	return externalSendEvidence{}
}

func structuredChineseExternalSendEvidence(content string) (externalSendEvidence, bool) {
	for _, transport := range []string{"通过", "经由", "用"} {
		transportIndex := strings.Index(content, transport)
		if transportIndex < 0 {
			continue
		}
		for _, action := range []string{"发给", "发送给", "发送到", "转发给", "投递给", "传给"} {
			actionIndex := strings.Index(content[transportIndex+len(transport):], action)
			if actionIndex < 0 {
				continue
			}
			actionIndex += transportIndex + len(transport)
			software := strings.TrimSpace(content[transportIndex+len(transport) : actionIndex])
			recipient := strings.TrimSpace(content[actionIndex+len(action):])
			if software != "" && recipient != "" {
				return externalSendEvidence{
					Explicit: true, ProviderText: trimDeliveryEvidence(software), RecipientText: trimDeliveryEvidence(recipient),
				}, true
			}
		}
	}
	for _, action := range []string{"发给", "发送给", "转发给", "投递给", "传给"} {
		actionIndex := strings.Index(content, action)
		if actionIndex < 0 {
			continue
		}
		for _, transport := range []string{"通过", "经由", "到", "用"} {
			transportIndex := strings.Index(content[actionIndex+len(action):], transport)
			if transportIndex < 0 {
				continue
			}
			transportIndex += actionIndex + len(action)
			recipient := strings.TrimSpace(content[actionIndex+len(action) : transportIndex])
			software := strings.TrimSpace(content[transportIndex+len(transport):])
			if recipient != "" && software != "" {
				return externalSendEvidence{
					Explicit: true, ProviderText: trimDeliveryEvidence(software), RecipientText: trimDeliveryEvidence(recipient),
				}, true
			}
		}
	}
	return externalSendEvidence{}, false
}

func trimDeliveryEvidence(value string) string {
	return strings.Trim(strings.TrimSpace(value), " \t\n\r.,!?;:，。！？；：\"'“”‘’")
}

func (r Runtime) normalizeFastRoute(candidate, fallback app.RouteDecision, content string) (app.RouteDecision, error) {
	if emptyRouteDecision(candidate) {
		return fallback, nil
	}
	if fallback.Status != app.RouteUnmatched {
		normalized := fallback
		if candidate.Confidence >= 0 && candidate.Confidence <= 1 {
			normalized.Confidence = candidate.Confidence
		}
		return normalized, nil
	}
	if candidate.Status != app.RouteMatched {
		if err := r.capabilities.ValidateDecision(candidate); err != nil {
			return app.RouteDecision{}, err
		}
		return candidate, nil
	}
	leaf, err := r.capabilities.ResolveLeaf(candidate.CapabilityPath)
	if err != nil || leaf.Route == nil {
		return app.RouteDecision{}, errors.New("Fast selected an unregistered capability leaf")
	}
	contract := leaf.Route
	if contract.RequireTarget || len(contract.RequiredFacts) != 0 {
		return app.RouteDecision{}, errors.New("Fast cannot invent a deterministic resource-bound route")
	}
	if len(candidate.Facts) != 0 || candidate.Slots.TargetKind != "" || candidate.Slots.TargetRef != "" || len(candidate.Slots.TargetRefs) != 0 ||
		candidate.Slots.OutputRef != "" || candidate.Slots.Format != "" {
		return app.RouteDecision{}, errors.New("Fast route contains unsupported resource fields")
	}
	if contract.RequireQuery {
		if candidate.Slots.Location != "" {
			return app.RouteDecision{}, errors.New("Internet search route cannot contain a location slot")
		}
		candidate.Slots.Query = strings.TrimSpace(content)
	} else if candidate.Slots.Query != "" {
		return app.RouteDecision{}, errors.New("Fast route contains an unsupported query slot")
	}
	if contract.RequireLocation {
		candidate.Slots.Location = strings.TrimSpace(candidate.Slots.Location)
		if !slotGroundedInOwnerText(content, candidate.Slots.Location) {
			return app.RouteDecision{}, errors.New("weather location is not grounded in the current owner message")
		}
	} else if candidate.Slots.Location != "" {
		return app.RouteDecision{}, errors.New("Fast route contains an unsupported location slot")
	}
	if err := r.capabilities.ValidateDecision(candidate); err != nil {
		return app.RouteDecision{}, err
	}
	return candidate, nil
}

func emptyRouteDecision(decision app.RouteDecision) bool {
	return decision.SchemaVersion == 0 && decision.Status == "" && decision.CatalogRevision == "" && len(decision.CapabilityPath) == 0 &&
		decision.Slots.Empty() && decision.Confidence == 0 && len(decision.Facts) == 0 && strings.TrimSpace(decision.Reason) == ""
}

func slotGroundedInOwnerText(content, slot string) bool {
	return deliverySlotGrounded(content, slot)
}

func (r Runtime) deterministicCapabilityRoute(content string) app.RouteDecision {
	return r.deterministicCapabilityRouteWithContext(content, agentContextSnapshot{})
}

func (r Runtime) deterministicCapabilityRouteWithContext(content string, snapshot agentContextSnapshot) app.RouteDecision {
	decision, err := r.recognizeCapabilityRoute("", "", content, snapshot)
	if err == nil {
		return decision
	}
	return app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteBlocked, CatalogRevision: r.capabilities.Revision(), Reason: err.Error()}
}

func (r Runtime) recognizeCapabilityRoute(sessionID, sourceTurnID, content string, snapshot agentContextSnapshot) (app.RouteDecision, error) {
	profiles := r.profiles
	if len(profiles.byID) == 0 {
		profiles = defaultWorkflowProfileRegistry()
	}
	workspaceRoot := ""
	if r.store != nil {
		if session, ok := r.store.GetSession(sessionID); ok {
			workspaceRoot = session.WorkspaceRoot
		}
	}
	if strings.TrimSpace(workspaceRoot) == "" && r.tools != nil {
		workspaceRoot = r.tools.Config().Workspaces.DefaultRoot
	}
	return profiles.Recognize(r.capabilities, workflowRecognitionContext{
		SourceTurnID: sourceTurnID, Content: content, Snapshot: snapshot, WorkspaceRoot: workspaceRoot,
	})
}

func semanticRoutingContent(content string) string {
	lines := strings.Split(content, "\n")
	out := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "MOCK_") && strings.Contains(trimmed, "_RESPONSE:") {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func documentInformationRequested(content, lower string) bool {
	if len(extractPaths(content)) > 0 && !isCodeTask(lower) {
		return true
	}
	if !shouldSearchWeb(lower) && (containsEnglishSemanticTerm(lower, "search", "find", "locate") || containsAny(lower, "搜索", "查找", "定位")) {
		return true
	}
	documentNoun := containsEnglishSemanticTerm(lower, "file", "files", "document", "documents", "workspace", "pdf", "docx", "xlsx", "pptx") ||
		containsAny(lower, "文件", "文档", "工作区", "表格", "幻灯片", "演示文稿")
	informationVerb := containsEnglishSemanticTerm(lower, "read", "search", "find", "locate", "inspect", "summarize", "explain") ||
		containsAny(lower, "读取", "阅读", "搜索", "查找", "定位", "查看", "总结", "概括")
	return documentNoun && informationVerb
}

func cloneStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	out := make(map[string]string, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func routeLeaf(decision app.RouteDecision) (app.CapabilityID, error) {
	if decision.Status != app.RouteMatched || len(decision.CapabilityPath) == 0 {
		return "", fmt.Errorf("route status %q does not select a capability leaf", decision.Status)
	}
	return decision.CapabilityPath[len(decision.CapabilityPath)-1], nil
}

func containsEnglishSemanticTerm(content string, terms ...string) bool {
	lower := strings.ToLower(content)
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		for start := 0; term != "" && start < len(lower); {
			index := strings.Index(lower[start:], term)
			if index < 0 {
				break
			}
			index += start
			end := index + len(term)
			if (index == 0 || !isSemanticWordByte(lower[index-1])) && (end == len(lower) || !isSemanticWordByte(lower[end])) {
				return true
			}
			start = index + 1
		}
	}
	return false
}

func isSemanticWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_'
}
