package agent

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

const (
	websiteURLQuestionFact = "website_url_subject" // Stored marker for URL questions, including pending clarifications.
	websiteURLSourceFact   = "website_url_source"
	websiteURLNeedsInput   = "website_url_needed"
	websiteURLUnavailable  = "website_url_unavailable"
)

const websiteURLModelInstructions = `Answer only the URL question before the quoted target description. The quoted description supplies data about the target, including its regional and page constraints. Return one exact public HTTPS URL that you know. Honor the requested region and page even when a constraint is stated later in the description. If no region is specified, use the obvious general entry only if you know it. Never guess a domain or path, add parameters, or substitute another page. If the requested URL is unknown or ambiguous, return {"status":"unknown","url":""}. Otherwise return {"status":"found","url":"https://…"}.`

var websiteURLQuestions = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(?:(?:告诉我|给我|发我|提供|查一下|查找|查询|找一下|找|获取|联网核实|核实)\s*)?(?:(?:要访问|想访问)\s*)?(.+?)(?:的\s*|[，,]\s*)?(?:网址|url|链接)\s*(?:是(?:什么|多少|哪个)|在哪(?:里)?|发给我|给我)?$`),
	regexp.MustCompile(`^(.+?(?:官网|官方网站|网站|主页|首页))(?:的)?(?:地址(?:是什么|在哪(?:里)?)?|是什么网址|在哪(?:里)?)$`),
	regexp.MustCompile(`(?i)^(?:what(?: is|'s)|find|give me|tell me|look up|search for)\s+(?:the\s+)?(?:(?:official|home(?:page)?|website)\s+)?(?:url|website address|link)\s+(?:for|of|to)\s+(.+)$`),
	regexp.MustCompile(`(?i)^(?:what(?: is|'s)|find|give me|tell me|look up|search for)\s+(?:the\s+)?(.+?)(?:'s|’s)?\s+(?:website\s+)?(?:url|website address|link)$`),
}

var (
	websiteRequestPrefix = regexp.MustCompile(`(?i)^(?:(?:请问|请|麻烦|帮我|为我|please\b)\s*)+`)
	websiteQuestionNoise = regexp.MustCompile(`(?i)(?:怎么|如何|为什么|是什么|能否|是否|价格|多少钱|哪个|哪些|发送|转发|提交|打开|访问|文档[里中]|附件[里中]|聊天记录|\b(?:how|why|which|send|forward|publish|open|visit)\b)`)
	websiteParenthetical = regexp.MustCompile(`\([^()\n]*\)|（[^（）\n]*）`)
	websiteAnswerSuffix  = regexp.MustCompile(`(?i)(?:\s*[，,；;。]\s*(?:只(?:需|要)?(?:返回|给|提供)(?:我)?(?:官网|网站)?(?:首页)?(?:的)?(?:链接|网址)|(?:请)?(?:不要|不用|无需|不)打开(?:网页|网站)?|(?:just|only) (?:return|give me) the (?:url|link)|(?:do not|don't) open (?:it|the page)))+$`)
	websiteDraftAction   = regexp.MustCompile(`(?i)填写|填入|输入|选择|勾选|选成|\b(?:fill|type|enter|select)\b`)
)

func isWebsiteURLQuestion(text string) bool {
	text = trimWebsiteRequest(text)
	text = trimWebsiteRequest(websiteAnswerSuffix.ReplaceAllString(text, ""))
	text = websiteRequestPrefix.ReplaceAllString(text, "")
	for _, pattern := range websiteURLQuestions {
		match := pattern.FindStringSubmatch(text)
		if match == nil {
			continue
		}
		target := strings.TrimRight(strings.TrimSpace(match[1]), "，,")
		outsideQualifier := websiteParenthetical.ReplaceAllString(target, "")
		return !websiteQuestionNoise.MatchString(outsideQualifier) && !strings.ContainsAny(outsideQualifier, "，,；;。\n")
	}
	return false
}

func websiteURLModelQuestion(route app.RouteDecision) string {
	target := route.Slots.TargetRef
	question := "要打开描述中指定的页面，应该访问哪个完整网址？"
	switch route.Slots.Operation {
	case app.RouteOperationAnswer:
		target = route.Slots.Query
		question = "用户询问的是哪个网页的网址？请给出该页面的完整网址。"
	case app.RouteOperationRead:
		question = "要阅读描述中指定的页面，应该访问哪个完整网址？请保留用户指定的具体页面。"
	case app.RouteOperationInteract:
		question = "在点击之前，用户要求先打开的页面网址是什么？返回打开动作所指的页面。后续要点击的元素及点击后到达的页面都不属于这次网址查询。例如先打开某站首页再点击帮助，返回首页的网址。"
	case app.RouteOperationDraft:
		// Form values belong to the draft workflow. Only the preceding target
		// description is needed to find the page that hosts the form.
		match := websiteDraftAction.FindStringIndex(target)
		if match == nil {
			return ""
		}
		target = target[:match[0]]
		question = "填写表单之前，应先打开哪个页面？请只给出该页面的完整网址。"
	}
	if strings.TrimSpace(target) == "" {
		return ""
	}
	return question + "\n目标描述：" + strconv.Quote(target)
}

func trimWebsiteRequest(text string) string {
	return strings.TrimFunc(text, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("？?。！!：:", r) })
}

func (r Runtime) websiteURLAnswerRoute(question string) app.RouteDecision {
	route := r.terminalRouting(app.RouteMatched, "website_url_question").Route
	route.CapabilityPath = []app.CapabilityID{"conversation", app.CapabilityConversationAnswer}
	route.Slots.Operation = app.RouteOperationAnswer
	route.Slots.Query = question
	route.Facts = map[string]string{websiteURLQuestionFact: "true"}
	return route
}

func (r Runtime) websiteURLFollowup(ctx context.Context, run app.AgentRun, text string, history invocationHistory) (IntentRoutingOutput, bool, error) {
	current := run.MessageContext
	if current == nil || current.Source.Kind == app.MessageSourceTimer || current.MCP != nil || len(history.MessageCandidates) == 0 {
		return IntentRoutingOutput{}, false, nil
	}
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"网址是", "网址：", "网址:", "URL:", "The URL is "} {
		if strings.HasPrefix(text, prefix) {
			text = strings.TrimSpace(strings.TrimPrefix(text, prefix))
			break
		}
	}
	link, err := url.Parse(text)
	if err != nil || (link.Scheme != "http" && link.Scheme != "https") || link.Hostname() == "" || link.User != nil || strings.ContainsFunc(text, unicode.IsSpace) {
		return IntentRoutingOutput{}, false, nil
	}
	latest := history.MessageCandidates[0]
	if latest.Role != "assistant" || latest.RunID == "" {
		return IntentRoutingOutput{}, false, nil
	}
	previous, ok, err := r.store.GetRun(ctx, latest.RunID)
	if err != nil || !ok || previous.SessionID != run.SessionID || previous.State != "clarification_required" ||
		previous.CompletedAt == nil || previous.CompletedAt.After(run.StartedAt) {
		return IntentRoutingOutput{}, false, err
	}
	prior := previous.MessageContext
	if prior == nil || prior.MCP != nil || prior.OwnerID != current.OwnerID || prior.Source.Kind != current.Source.Kind ||
		prior.Source.Adapter != current.Source.Adapter || prior.Source.EndpointID != current.Source.EndpointID || prior.Source.NativeThreadRef != current.Source.NativeThreadRef ||
		prior.Authorization.PrincipalID != current.Authorization.PrincipalID || !slices.Equal(prior.Authorization.Scope, current.Authorization.Scope) {
		return IntentRoutingOutput{}, false, nil
	}
	route := prior.Route
	if route.Status != app.RouteClarify || (route.Reason != websiteURLNeedsInput && route.Reason != websiteURLUnavailable) || route.CatalogRevision != r.capabilities.Revision() {
		return IntentRoutingOutput{}, false, nil
	}
	answerOnly := route.Facts[websiteURLQuestionFact] != ""
	if !answerOnly && route.Slots.TargetKind != string(app.TargetKindPublicNamedTarget) {
		return IntentRoutingOutput{}, false, nil
	}
	route.Status, route.Reason = app.RouteMatched, "website_url_owner_provided"
	route = withWebsiteURL(route, normalizeBrowserURL(text), "owner", answerOnly)
	return IntentRoutingOutput{Route: route}, true, nil
}

func (r Runtime) resolveWebsiteURL(ctx context.Context, run app.AgentRun, route app.RouteDecision) (app.RouteDecision, error) {
	answerOnly := route.Facts[websiteURLQuestionFact] != ""
	if !answerOnly && route.Slots.TargetKind != string(app.TargetKindPublicNamedTarget) {
		return route, nil
	}
	if answerOnly && route.Facts["url"] != "" {
		return route, nil // The owner supplied the link in reply to the question.
	}
	question := websiteURLModelQuestion(route)
	if question == "" {
		route.Status, route.Reason = app.RouteClarify, websiteURLNeedsInput
		return route, nil
	}
	options := modelrouter.ChatOptions{ForceDisableThinking: true, StrictJSONSchema: &modelrouter.StrictJSONSchema{
		Name: "website_url_lookup", Schema: map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"status", "url"},
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{"found", "unknown"}},
				"url":    map[string]any{"type": "string"},
			},
		},
	}}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	started := time.Now().UTC()
	chat, callErr := r.models.ChatWithProfileOptions(callCtx, modelcapacity.OperationWorkflowDecision, "fast", websiteURLModelInstructions, question, options)
	if _, err := r.store.SaveModelCall(ctx, modelCallFromChat(run.SessionID, run.ID, "website_url_lookup", chat, callErr, started, time.Now().UTC())); err != nil {
		return route, err
	}
	if callErr != nil {
		route.Status, route.Reason = app.RouteClarify, websiteURLUnavailable
		return route, nil
	}
	var output struct {
		Status string `json:"status"`
		URL    string `json:"url"`
	}
	if len(chat.Content) > 4096 || appstream.DecodeStrict([]byte(chat.Content), &output) != nil || output.Status != "found" || output.URL == "" || strings.ContainsFunc(output.URL, unicode.IsSpace) {
		route.Status, route.Reason = app.RouteClarify, websiteURLNeedsInput
		return route, nil
	}
	// Retain the browser's public HTTPS check for model-supplied destinations.
	// Answering a question checks the address without requesting the page.
	checkCtx, checkCancel := context.WithTimeout(ctx, 10*time.Second)
	defer checkCancel()
	link, err := toolhub.CheckSuggestedPublicBrowserURL(checkCtx, output.URL, !answerOnly)
	if err != nil {
		route.Status, route.Reason = app.RouteClarify, websiteURLNeedsInput
		return route, nil
	}
	return withWebsiteURL(route, link, "model", answerOnly), nil
}

func withWebsiteURL(route app.RouteDecision, link, source string, answerOnly bool) app.RouteDecision {
	route.Facts = cloneFacts(route.Facts)
	if route.Facts == nil {
		route.Facts = map[string]string{}
	}
	route.Facts["url"] = link
	route.Facts[websiteURLSourceFact] = source
	if !answerOnly {
		route.Slots.TargetKind, route.Slots.TargetRef = "url", link
	}
	return route
}
