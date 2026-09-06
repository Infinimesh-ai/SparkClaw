package agent

import "strings"

func projectDeliveryDirective(ownerText, canonical string) (DeliveryDirective, string, error) {
	evidence := externalSendEvidenceFromMessage(ownerText)
	directive, err := normalizeDeliveryDirective(DeliveryDirective{
		ExplicitExternal: evidence.Explicit, RequestedProviderKey: evidence.ProviderText,
		RequestedRecipientText: evidence.RecipientText,
	})
	if err != nil {
		return DeliveryDirective{}, "", err
	}
	return directive, deliveryBusinessProjection(canonical, evidence), nil
}

func deliveryBusinessProjection(content string, evidence externalSendEvidence) string {
	content = strings.TrimSpace(content)
	original := content
	if !evidence.Explicit {
		return content
	}
	lower := strings.ToLower(content)
	for _, marker := range []string{" via ", "通过", "经由"} {
		if index := strings.LastIndex(lower, marker); index > 0 {
			content, lower = strings.TrimSpace(content[:index]), strings.TrimSpace(lower[:index])
			break
		}
	}
	if recipient := strings.ToLower(strings.TrimSpace(evidence.RecipientText)); recipient != "" {
		if index := strings.LastIndex(lower, " to "+recipient); index > 0 {
			content = strings.TrimSpace(content[:index])
		}
	}
	for _, prefix := range []string{"send ", "forward ", "deliver ", "发送", "转发", "投递"} {
		if strings.HasPrefix(strings.ToLower(content), prefix) {
			content = strings.TrimSpace(content[len(prefix):])
			break
		}
	}
	if content == "" {
		return original
	}
	return content
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
			return externalSendEvidence{Explicit: true, ProviderText: provider, RecipientText: trimDeliveryEvidence(semantic[toIndex+4 : onIndex])}
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
				return externalSendEvidence{Explicit: true, ProviderText: trimDeliveryEvidence(software), RecipientText: trimDeliveryEvidence(recipient)}, true
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
				return externalSendEvidence{Explicit: true, ProviderText: trimDeliveryEvidence(software), RecipientText: trimDeliveryEvidence(recipient)}, true
			}
		}
	}
	return externalSendEvidence{}, false
}

func trimDeliveryEvidence(value string) string {
	return strings.Trim(strings.TrimSpace(value), " \t\n\r.,!?;:，。！？；：\"'“”‘’")
}
