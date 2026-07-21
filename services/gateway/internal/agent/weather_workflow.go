package agent

import (
	"regexp"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var (
	englishWeatherInPattern       = regexp.MustCompile(`(?i)\b(?:weather|forecast)(?:\s+(?:today|tomorrow))?\s+(?:in|for)\s+([a-z][a-z .'-]{0,80})`)
	englishLocationWeatherPattern = regexp.MustCompile(`(?i)^\s*([a-z][a-z .'-]{0,80}?)\s+(?:weather|forecast)\b`)
	leadingChineseDatePattern     = regexp.MustCompile(`^\s*(?:(?:\d{4}-\d{1,2}-\d{1,2})|(?:(?:\d{4}年)?\d{1,2}月\d{1,2}日))\s*`)
	trailingChineseTimePattern    = regexp.MustCompile(`(?:今天|今日|明天|明日|后天|现在|当前|实时|未来\s*[一二两三四五六七八九十\d]+\s*(?:小时|天)|今后\s*[一二两三四五六七八九十\d]+\s*(?:小时|天))(?:的)?$`)
)

type browserWeatherProfile struct{}

func (browserWeatherProfile) ID() app.WorkflowID           { return app.WorkflowBrowserWeather }
func (browserWeatherProfile) Revision() int                { return 1 }
func (browserWeatherProfile) Capability() app.CapabilityID { return app.CapabilityBrowserWeather }
func (browserWeatherProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}

func (browserWeatherProfile) Recognize(input workflowRecognitionContext) (workflowRecognition, bool) {
	content := semanticRoutingContent(input.Content)
	if !ordinaryWeatherRequest(content) {
		return workflowRecognition{}, false
	}
	location := weatherLocationFromRequest(content)
	if location == "" {
		return workflowRecognition{
			Status: app.RouteClarify, Confidence: 0.95,
			Reason: "The weather request requires an explicit location.",
		}, true
	}
	return workflowRecognition{
		Slots: app.RouteSlots{
			Operation: app.RouteOperationRead, FactScope: app.RouteFactScopeWeatherSnapshot, Query: strings.TrimSpace(content),
			TargetKind: string(app.TargetKindLocation), TargetRef: location, Location: location, Format: "image",
		},
		Facts:      map[string]string{"location_source": "current_turn"},
		Confidence: 0.96,
		Reason:     "The request asks for a direct weather card for an explicit location.",
	}, true
}

func ordinaryWeatherRequest(content string) bool {
	lower := strings.ToLower(strings.TrimSpace(content))
	if lower == "" || len(extractURLs(content)) != 0 || !weatherIntent(lower) {
		return false
	}
	if weatherResearchRequest(lower) {
		return false
	}
	if containsEnglishSemanticTerm(lower, "text only", "plain text", "no image", "no card") ||
		containsAny(lower, "只要文字", "纯文字", "不要图片", "不要卡片") {
		return false
	}
	return true
}

func weatherResearchRequest(lower string) bool {
	environmentalData := containsEnglishSemanticTerm(lower, "air quality", "aqi") || containsAny(lower, "空气质量", "污染指数")
	return environmentalData || weatherIntent(lower) && (containsEnglishSemanticTerm(lower, "warning", "alert", "news", "source", "sources", "compare", "comparison", "website", "websites", "official") ||
		containsAny(lower, "预警", "警报", "新闻", "来源", "出处", "对比", "比较", "网站", "网页", "官方"))
}

func weatherIntent(lower string) bool {
	return containsEnglishSemanticTerm(lower, "weather", "forecast", "temperature", "rain", "snow", "wind", "umbrella") ||
		containsAny(lower, "天气", "气温", "温度", "预报", "下雨", "降雨", "下雪", "降雪", "刮风", "风力", "带伞")
}

func weatherLocationFromRequest(content string) string {
	content = strings.TrimSpace(content)
	if match := englishWeatherInPattern.FindStringSubmatch(content); len(match) == 2 {
		return cleanEnglishWeatherLocation(match[1])
	}
	if match := englishLocationWeatherPattern.FindStringSubmatch(content); len(match) == 2 {
		return cleanEnglishWeatherLocation(match[1])
	}
	return chineseWeatherLocation(content)
}

func cleanEnglishWeatherLocation(value string) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	for _, suffix := range []string{" today", " tomorrow", " now", " currently"} {
		if strings.HasSuffix(lower, suffix) {
			value = strings.TrimSpace(value[:len(value)-len(suffix)])
			lower = strings.ToLower(value)
		}
	}
	return strings.Trim(value, " \t\r\n,.;:!?'")
}

func chineseWeatherLocation(content string) string {
	index := -1
	for _, marker := range []string{"天气", "气温", "温度", "预报", "下雨", "降雨", "下雪", "降雪", "刮风", "风力", "带伞"} {
		if candidate := strings.Index(content, marker); candidate >= 0 && (index < 0 || candidate < index) {
			index = candidate
		}
	}
	if index < 0 {
		return ""
	}
	location := strings.TrimSpace(content[:index])
	location = strings.Trim(location, " \t\r\n，。！？；：,.!?;:")
	for changed := true; changed; {
		changed = false
		location = leadingChineseDatePattern.ReplaceAllString(location, "")
		for _, prefix := range []string{"请帮我查一下", "帮我查一下", "请查询一下", "查询一下", "查一下", "请帮我看看", "帮我看看", "请看看", "看看", "请告诉我", "告诉我", "请查", "查询", "请问", "请", "今天", "今日", "明天", "明日", "后天", "现在", "当前", "实时"} {
			if strings.HasPrefix(location, prefix) {
				location = strings.TrimSpace(strings.TrimPrefix(location, prefix))
				changed = true
				break
			}
		}
	}
	location = trailingChineseTimePattern.ReplaceAllString(location, "")
	location = strings.TrimSpace(strings.TrimSuffix(location, "的"))
	for _, suffix := range []string{"可能会", "是否会", "会不会", "会", "是否", "可能", "要不要", "需要"} {
		if strings.HasSuffix(location, suffix) {
			location = strings.TrimSpace(strings.TrimSuffix(location, suffix))
			break
		}
	}
	location = trailingChineseTimePattern.ReplaceAllString(location, "")
	location = strings.Trim(location, " \t\r\n，。！？；：,.!?;:")
	if location == "" || weatherIntent(strings.ToLower(location)) {
		return ""
	}
	return location
}

func (p browserWeatherProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	target := app.TargetRef{Kind: app.TargetKindLocation, Ref: route.Slots.TargetRef}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperationRead, target, app.DataScopePublic)
	intent.Objectives[0].Output = app.OutputKindImage
	nodeID := app.WorkflowNodeID("weather")
	structureScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityWeatherStructure}}}
	renderScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityWeatherRender}}}
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(),
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence, ResultProjection: app.WorkflowResultOutputsOnly,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: "query_info",
			Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Query Info, structure grounded weather fields, and render one weather card", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
				Name: app.ToolCapabilityInfoQuestion, Qualifiers: map[string]string{app.CapabilityQualifierProvider: app.CapabilityProviderInfo},
			}}},
			Transitions: []app.ScopeTransition{
				{ID: "info_answer_ready", NextStage: "structure_weather", On: app.TransitionPredicate{OutcomeSignals: []app.OutcomeSignal{app.OutcomeSignalInfoAnswerAvailable}, Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}}, Replace: &structureScope, MaxActivations: 1},
				{ID: "weather_payload_ready", NextStage: "render_card", On: app.TransitionPredicate{OutcomeSignals: []app.OutcomeSignal{app.OutcomeSignalWeatherPayloadAvailable}, Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}}, Replace: &renderScope, MaxActivations: 1},
			},
			ArgumentBindings: []app.ArgumentBinding{
				{Capability: app.ToolCapabilityInfoQuestion, Argument: "query", ResourceKind: "query", Source: app.ArgumentBindingRouteSlot, SourceKey: "query"},
				{Capability: app.ToolCapabilityWeatherStructure, Argument: "info_answer_ref", ResourceKind: "info_answer", Source: app.ArgumentBindingOutcomeRef},
				{Capability: app.ToolCapabilityWeatherStructure, Argument: "location", ResourceKind: "location", Source: app.ArgumentBindingRouteSlot, SourceKey: "target_ref"},
				{Capability: app.ToolCapabilityWeatherRender, Argument: "weather_payload_ref", ResourceKind: "weather_payload", Source: app.ArgumentBindingOutcomeRef},
			},
			AllowedRisks: []app.RiskLevel{app.RiskRead, app.RiskDraft}, MaxAttempts: 3,
		}},
	}, nil
}

func (browserWeatherProfile) Prepare(*app.WorkflowState) (app.TransitionID, bool, error) {
	return "", false, nil
}

func (browserWeatherProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	node := state.Nodes[outcome.NodeID]
	switch {
	case node.Stage == "query_info" && containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInfoAnswerAvailable):
		assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "info_answer_available"
	case node.Stage == "structure_weather" && containsOutcomeSignal(outcome.Signals, app.OutcomeSignalWeatherPayloadAvailable):
		assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "weather_payload_available"
	case node.Stage == "render_card" && containsOutcomeSignal(outcome.Signals, app.OutcomeSignalWeatherCardAvailable):
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "weather_card_available"
	default:
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "weather_stage_failed"
	}
	return assessment
}

func (browserWeatherProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	return workflowHint(state, "weather", "info", "public", "", "Dispatched by the browser.weather workflow contract.")
}

func (browserWeatherProfile) TransitionInstruction(outcome app.ToolOutcome, _ app.NodeAssessment) string {
	switch {
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInfoAnswerAvailable):
		return "workflow_requirement: Read the bounded query-relevant Info evidence projection, including available summary, facts, and source snippets. Submit only values explicitly present there; never infer, translate, paraphrase, combine unrelated values, or substitute daily low/high for current temperature. current.temperature_c requires a standalone current reading whose evidence_text includes that one number and its temperature unit; if only a daily range is available, mark current.temperature_c missing. Evidence for any numeric value must include its unit; for daily min/max copy the complete range such as 28~34℃ when the unit is written once. For every submitted value, including date and time strings, copy an exact substring without reformatting and use only its listed summary:0, fact:N, or source:N:snippet:M ref. missing_fields is mandatory: mark current.condition, current.temperature_c, daily, and hourly whenever that category has no reliable supporting data, and do not submit a value or evidence entry for a marked category. Submit zero to five hourly entries strictly after the current system time; never submit past hours, and mark hourly missing when no future entries are supported. The tool argument shape is exact: current may contain only condition, temperature_c, feels_like_c, humidity_pct, wind_kmh, and precipitation_mm; never submit humidity_percent, wind text, uv, or any other property. Unknown optional values may be omitted or null. hourly and daily are arrays; every hourly entry uses exactly time, condition, and temperature_c, never datetime, and all three hourly leaves require separate evidence entries; daily entries use date, condition, min_temperature_c, and max_temperature_c. every evidence entry uses exactly field_path, evidence_ref, and evidence_text. field_path always names one submitted schema leaf such as current.condition, daily[0].date, daily[0].min_temperature_c, hourly[0].time, hourly[0].condition, or hourly[0].temperature_c, never an unsupported field or the daily/hourly category. Never use field/ref/value aliases or put evidence_ref inside current, hourly, or daily."
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalWeatherPayloadAvailable):
		return "workflow_requirement: Render the bound validated weather payload. Call media.render_weather_card without adding or rewriting weather fields."
	default:
		return ""
	}
}
