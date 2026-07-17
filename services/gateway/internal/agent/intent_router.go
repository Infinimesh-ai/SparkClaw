package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (r Runtime) classifyIntent(ctx context.Context, sessionID, runID, sourceTurnID, content string) (recognizedWorkflow, bool, error) {
	support, supported, err := r.profiles.Recognize(sourceTurnID, content)
	if err != nil || !supported {
		return support, supported, err
	}
	fallback := support.Intent
	fallbackJSON, _ := json.Marshal(fallback)
	system := strings.Join([]string{
		"You normalize one SparkClaw request into stable semantic intent.",
		"Return one compact JSON object only. Never name tools, Skills, workflow profiles, model lanes, risk levels, policy, approval, or execution steps.",
		"The deterministic recognizer has already fixed domain, operation, explicit target reference, data scope, and authorization provenance.",
		"You may only normalize evidence depth and resolution fields within the supplied semantic contract.",
	}, "\n")
	user := strings.Join([]string{
		"Current owner message:", content,
		"Deterministic semantic contract:", string(fallbackJSON),
		"Return JSON with version, source_turn_id, objectives, constraints, and resolution.",
	}, "\n\n")
	started := time.Now().UTC()
	chat, chatErr := r.models.ChatWithProfile(ctx, "fast", system, user)
	completed := time.Now().UTC()
	r.store.SaveModelCall(modelCallFromChat(sessionID, runID, "intent_classification", chat, chatErr, started, completed))
	intent := fallback
	classificationSource := "deterministic_fallback"
	if chatErr == nil {
		if parsed, parseErr := parseIntentEnvelope(chat.Content); parseErr == nil {
			intent = normalizeStableIntent(parsed, fallback)
			if support.Profile.Match(intent) {
				classificationSource = "fast_model"
			} else {
				intent = fallback
			}
		}
	}
	profile, routed, routeErr := r.profiles.Route(intent)
	if routeErr != nil {
		return recognizedWorkflow{}, true, routeErr
	}
	if !routed {
		return recognizedWorkflow{}, true, errors.New("stable intent has no registered workflow profile")
	}
	recognized := recognizedWorkflow{Profile: profile, Intent: intent}
	objective := intent.Objectives[0]
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID, RunID: runID, Actor: "model-router", Type: "intent.classified", Summary: classificationSource,
		Fields: map[string]any{
			"version": intent.Version, "source_turn_id": intent.SourceTurnID, "domain": objective.Domain,
			"operation": objective.Operation, "target_kind": objective.Target.Kind, "data_scope": intent.Constraints.DataScope,
			"evidence_depth": intent.Constraints.EvidenceDepth, "profile_id": recognized.Profile.ID(), "source": classificationSource,
		},
	})
	return recognized, true, nil
}

func parseIntentEnvelope(content string) (app.IntentEnvelope, error) {
	raw := extractJSONObject(content)
	var intent app.IntentEnvelope
	if err := json.Unmarshal([]byte(raw), &intent); err != nil {
		return app.IntentEnvelope{}, err
	}
	return intent, nil
}

func normalizeStableIntent(candidate, fallback app.IntentEnvelope) app.IntentEnvelope {
	candidate.Version = fallback.Version
	candidate.SourceTurnID = fallback.SourceTurnID
	candidate.Resolution = fallback.Resolution
	candidate.Constraints.DataScope = fallback.Constraints.DataScope
	if candidate.Constraints.EvidenceDepth != app.EvidenceDepthSource && candidate.Constraints.EvidenceDepth != app.EvidenceDepthSummary {
		candidate.Constraints.EvidenceDepth = fallback.Constraints.EvidenceDepth
	}
	if len(candidate.Objectives) != len(fallback.Objectives) {
		return fallback
	}
	for i := range fallback.Objectives {
		if candidate.Objectives[i].Domain == "" {
			candidate.Objectives[i].Domain = fallback.Objectives[i].Domain
		}
		if candidate.Objectives[i].Operation == "" {
			candidate.Objectives[i].Operation = fallback.Objectives[i].Operation
		}
		candidate.Objectives[i].ID = fallback.Objectives[i].ID
		candidate.Objectives[i].Target = fallback.Objectives[i].Target
		candidate.Objectives[i].Output = fallback.Objectives[i].Output
		candidate.Objectives[i].Explicit = fallback.Objectives[i].Explicit
	}
	return candidate
}

func hasUnmigratedDomainSignal(lower string) bool {
	return containsEnglishSemanticTerm(lower,
		"calendar", "schedule", "meeting", "email", "inbox", "reminder", "remember", "memory", "workspace", "file", "files", "document", "shell", "command", "patch",
	) || containsAny(lower, "日历", "日程", "会议", "邮件", "收件箱", "提醒", "记住", "记忆", "工作区", "文件", "文档", "命令", "补丁")
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

func webSourceEvidenceRequested(lower string) bool {
	return containsAny(lower,
		"source page", "source-level", "official page", "official source", "original text", "verify from", "citation from",
		"来源页", "来源页面", "官方页面", "官方原文", "原文", "逐页核实", "核验来源", "打开来源", "读取来源",
	)
}
