package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
)

type IntentRoutingOutput struct {
	Route    app.RouteDecision         `json:"route"`
	Delivery DeliveryDirective         `json:"delivery"`
	Fusion   *app.IntentFusionDecision `json:"intent_fusion,omitempty"`
}

type semanticIntentRouter struct {
	graph       *semanticrouting.Graph
	calibration semanticrouting.Calibration
	index       *semanticrouting.EmbeddingIndex
}

func newSemanticIntentRouter(catalogRevision string, graph *semanticrouting.Graph) *semanticIntentRouter {
	if graph == nil || graph.CatalogRevision() != catalogRevision {
		panic("semantic routing graph does not match the capability Catalog")
	}
	return &semanticIntentRouter{graph: graph, calibration: semanticrouting.DefaultCalibration()}
}

func (s *semanticIntentRouter) initializeEmbeddingIndex(ctx context.Context, models modelrouter.Router) (modelrouter.EmbeddingResult, error) {
	if s == nil || s.graph == nil {
		return modelrouter.EmbeddingResult{}, errors.New("semantic routing graph is unavailable")
	}
	if s.index != nil {
		return modelrouter.EmbeddingResult{}, errors.New("semantic embedding index is already initialized")
	}
	corpus := s.graph.EmbeddingCorpus()
	inputs := make([]string, 0, len(corpus))
	for _, entry := range corpus {
		inputs = append(inputs, entry.Text)
	}
	if len(inputs) == 0 {
		return modelrouter.EmbeddingResult{}, errors.New("semantic routing graph has no embedding corpus")
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(s.calibration.EmbeddingTimeoutMS)*time.Millisecond)
	defer cancel()
	result, err := models.Embed(callCtx, inputs)
	if err != nil {
		return result, err
	}
	if len(result.Vectors) != len(inputs) {
		return result, fmt.Errorf("embedding index received %d vectors for %d corpus entries", len(result.Vectors), len(inputs))
	}
	index, err := semanticrouting.BuildEmbeddingIndex(s.graph, result.Model, result.Vectors)
	if err != nil {
		return result, err
	}
	s.index = index
	return result, nil
}

func (r Runtime) routeIntent(ctx context.Context, sessionID, runID, content string) (IntentRoutingOutput, error) {
	return r.routeIntentWithOwnerText(ctx, sessionID, runID, content, semanticRoutingContent(content))
}

func (r Runtime) routeIntentWithOwnerText(ctx context.Context, sessionID, runID, content, ownerText string) (IntentRoutingOutput, error) {
	return r.routeIntentWithRequest(ctx, sessionID, runID, content, ownerText, nil, "")
}

func (r Runtime) routeIntentWithRequest(ctx context.Context, sessionID, runID, canonical, ownerText string, resources []app.MessagePart, sourceKind app.MessageSourceKind) (IntentRoutingOutput, error) {
	if r.semanticRouter == nil || r.semanticRouter.graph == nil {
		return IntentRoutingOutput{}, errors.New("semantic intent router is unavailable")
	}
	groundingContent := strings.TrimSpace(semanticRoutingContent(canonical))
	content := semanticOwnerProjection(groundingContent)
	delivery, businessContent, err := projectDeliveryDirective(ownerText, content)
	if err != nil {
		return IntentRoutingOutput{}, err
	}
	businessContent = semanticQueryProjection(businessContent, resources)
	grounding := r.projectIntentGrounding(sessionID, runID, groundingContent, resources)
	eligible := r.semanticRouter.graph.EligibleCandidates(sourceKind)
	if len(eligible) == 0 {
		return IntentRoutingOutput{}, errors.New("semantic routing graph has no source-eligible candidates")
	}
	routingCtx, cancel := context.WithTimeout(ctx, time.Duration(r.semanticRouter.calibration.RoutingTimeoutMS)*time.Millisecond)
	defer cancel()

	embeddingCh := make(chan embeddingChannelResult, 1)
	treeCh := make(chan treeChannelResult, 1)
	go func() {
		embeddingCh <- r.scoreEmbeddingChannel(routingCtx, sessionID, runID, businessContent, eligible)
	}()
	go func() {
		treeCh <- r.scoreTreeChannel(routingCtx, sessionID, runID, businessContent, sourceKind, eligible)
	}()
	embeddingResult, treeResult := <-embeddingCh, <-treeCh
	channels := map[string]semanticrouting.ChannelState{
		"embedding": embeddingResult.state,
		"tree":      treeResult.state,
		"reranker":  {Status: semanticrouting.ChannelFailed, ReasonCode: "not_run"},
	}
	shortlist, fusionErr := semanticrouting.RankFusion(
		eligible, embeddingResult.evidence, treeResult.evidence, channels, r.semanticRouter.calibration,
	)
	var decision semanticrouting.Decision
	if errors.Is(fusionErr, semanticrouting.ErrSemanticChannelsUnavailable) {
		decision = semanticrouting.Decision{Verdict: semanticrouting.VerdictBlocked, Degraded: true, ReasonCode: "semantic_channels_unavailable"}
	} else if fusionErr != nil {
		return IntentRoutingOutput{}, fusionErr
	} else {
		reranked, rerankerState := r.scoreRerankerChannel(routingCtx, sessionID, runID, businessContent, shortlist)
		channels["reranker"] = rerankerState
		decision, err = semanticrouting.Decide(shortlist, reranked, channels, r.semanticRouter.calibration)
		if err != nil {
			return IntentRoutingOutput{}, err
		}
	}
	decision = enforceDeliveryFusionBoundary(decision, delivery)
	fusion := persistedIntentFusion(r.semanticRouter, channels, decision)
	route, err := r.routeFromFusionDecision(content, grounding, decision)
	if err != nil {
		return IntentRoutingOutput{}, err
	}
	if err := r.capabilities.ValidateDecision(route); err != nil {
		return IntentRoutingOutput{}, err
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID, RunID: runID, Actor: "semantic-router", Type: "capability.routed",
		Summary: decision.ReasonCode,
		Fields: map[string]any{
			"graph_revision": fusion.GraphRevision, "calibration_revision": fusion.CalibrationRevision,
			"verdict": fusion.Verdict, "degraded": fusion.Degraded, "confidence": fusion.Confidence,
			"margin": fusion.Margin, "candidate_count": len(fusion.Candidates), "route_status": route.Status,
			"capability_path": route.CapabilityPath, "explicit_external": delivery.ExplicitExternal,
		},
	})
	return IntentRoutingOutput{Route: route, Delivery: delivery, Fusion: &fusion}, nil
}

func enforceDeliveryFusionBoundary(decision semanticrouting.Decision, delivery DeliveryDirective) semanticrouting.Decision {
	if delivery.ExplicitExternal && decision.Degraded {
		decision.Verdict = semanticrouting.VerdictBlocked
		decision.ReasonCode = "external_delivery_requires_healthy_semantic_pipeline"
	}
	return decision
}

type embeddingChannelResult struct {
	evidence map[string]semanticrouting.EmbeddingEvidence
	state    semanticrouting.ChannelState
}

func (r Runtime) scoreEmbeddingChannel(ctx context.Context, sessionID, runID, query string, eligible []semanticrouting.Candidate) embeddingChannelResult {
	state := semanticrouting.ChannelState{Status: semanticrouting.ChannelFailed, ReasonCode: "embedding_failed"}
	if strings.TrimSpace(query) == "" {
		state.ReasonCode = "empty_semantic_query"
		return embeddingChannelResult{state: state}
	}
	timeout := time.Duration(r.semanticRouter.calibration.EmbeddingTimeoutMS) * time.Millisecond
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	index := r.semanticRouter.index
	if index == nil {
		state.ReasonCode = "embedding_index_unavailable"
		return embeddingChannelResult{state: state}
	}
	started := time.Now().UTC()
	inputs := []string{query}
	result, callErr := r.models.Embed(callCtx, inputs)
	completed := time.Now().UTC()
	r.store.SaveModelCall(modelCallFromEmbedding(sessionID, runID, "intent_embedding", result, callErr, started, completed))
	if callErr != nil {
		return embeddingChannelResult{state: state}
	}
	if len(result.Vectors) != 1 {
		state.ReasonCode = "embedding_vector_count_invalid"
		return embeddingChannelResult{state: state}
	}
	if index.Model != result.Model {
		state.ReasonCode = "embedding_model_changed"
		return embeddingChannelResult{state: state}
	}
	eligibleIDs := make(map[string]bool, len(eligible))
	for _, candidate := range eligible {
		eligibleIDs[candidate.ID] = true
	}
	evidence, scoreErr := index.Score(result.Vectors[0], eligibleIDs, r.semanticRouter.calibration)
	if scoreErr != nil {
		state.ReasonCode = "embedding_score_invalid"
		return embeddingChannelResult{state: state}
	}
	state = semanticrouting.ChannelState{Status: semanticrouting.ChannelHealthy, Model: result.Model}
	return embeddingChannelResult{evidence: evidence, state: state}
}

type treeRoutingCandidate struct {
	CandidateID string   `json:"candidate_id"`
	TreeScore   *float64 `json:"tree_score"`
}

type treeRoutingOutput struct {
	GraphRevision string                 `json:"graph_revision"`
	Candidates    []treeRoutingCandidate `json:"candidates"`
}

type treeChannelResult struct {
	evidence map[string]semanticrouting.TreeEvidence
	state    semanticrouting.ChannelState
}

func (r Runtime) scoreTreeChannel(ctx context.Context, sessionID, runID, query string, sourceKind app.MessageSourceKind, eligible []semanticrouting.Candidate) treeChannelResult {
	state := semanticrouting.ChannelState{Status: semanticrouting.ChannelFailed, ReasonCode: "tree_failed"}
	graphJSON, err := json.Marshal(treeGraphProjection(eligible))
	if err != nil {
		state.ReasonCode = "tree_graph_projection_invalid"
		return treeChannelResult{state: state}
	}
	system := strings.Join([]string{
		"Score one SparkClaw owner request against the supplied immutable semantic capability tree.",
		"Reason over meaning, paraphrases, sibling distinctions, and hard negatives. Do not use substring rules.",
		"Return one compact JSON object only with graph_revision and candidates.",
		"Each candidate has exactly candidate_id and tree_score in [0,1]. A higher tree_score means a stronger semantic match.",
		"Return at most the requested top_k. Candidate IDs must come from the supplied graph.",
		"Do not return routes, paths, workflows, tools, slots, facts, resources, delivery targets, policy, or prose.",
	}, "\n")
	user := strings.Join([]string{
		"INTENT_FUSION_TREE_REQUEST",
		"Graph revision: " + r.semanticRouter.graph.Revision(),
		fmt.Sprintf("Top K: %d", r.semanticRouter.calibration.TreeTopK),
		"Source kind: " + firstNonEmptyString(string(sourceKind), "unspecified"),
		"Semantic graph:\n" + string(graphJSON),
		"Owner semantic query:\n" + query,
		"Return the scored registered candidates now.",
	}, "\n\n")
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(r.semanticRouter.calibration.TreeTimeoutMS)*time.Millisecond)
	defer cancel()
	started := time.Now().UTC()
	chat, callErr := r.models.ChatWithProfile(callCtx, "fast", system, user)
	completed := time.Now().UTC()
	r.store.SaveModelCall(modelCallFromChat(sessionID, runID, "intent_tree_graph", chat, callErr, started, completed))
	if callErr != nil {
		return treeChannelResult{state: state}
	}
	output, parseErr := parseTreeRoutingOutput(chat.Content)
	if parseErr == nil {
		parseErr = validateTreeRoutingOutput(output, r.semanticRouter.graph.Revision(), eligible, r.semanticRouter.calibration.TreeTopK)
	}
	if parseErr != nil {
		output, parseErr = r.repairTreeRoutingOutput(callCtx, sessionID, runID, query, string(graphJSON), chat.Content, parseErr, eligible)
		if parseErr != nil {
			state.ReasonCode = "tree_output_invalid"
			return treeChannelResult{state: state}
		}
	}
	evidence := make(map[string]semanticrouting.TreeEvidence, len(output.Candidates))
	for _, candidate := range output.Candidates {
		evidence[candidate.CandidateID] = semanticrouting.TreeEvidence{Score: *candidate.TreeScore}
	}
	state = semanticrouting.ChannelState{Status: semanticrouting.ChannelHealthy, Model: chat.Model}
	return treeChannelResult{evidence: evidence, state: state}
}

func (r Runtime) repairTreeRoutingOutput(ctx context.Context, sessionID, runID, query, graphJSON, raw string, parseErr error, eligible []semanticrouting.Candidate) (treeRoutingOutput, error) {
	system := strings.Join([]string{
		"Repair one semantic candidate-ranking response into the exact supplied JSON contract.",
		"Candidate objects have exactly candidate_id and tree_score in [0,1].",
		"Do not reinterpret the request or introduce candidate IDs. Return JSON only.",
		"Semantic graph: " + graphJSON,
	}, "\n")
	user := strings.Join([]string{
		"INTENT_FUSION_TREE_REPAIR_REQUEST",
		"Graph revision: " + r.semanticRouter.graph.Revision(),
		"Owner semantic query:\n" + trimForEpisode(query, 4000),
		"Parser error:\n" + parseErr.Error(),
		"Invalid output:\n" + trimForEpisode(raw, 8000),
	}, "\n\n")
	started := time.Now().UTC()
	chat, callErr := r.models.ChatWithProfile(ctx, "fast", system, user)
	completed := time.Now().UTC()
	r.store.SaveModelCall(modelCallFromChat(sessionID, runID, "intent_tree_graph_repair", chat, callErr, started, completed))
	if callErr != nil {
		return treeRoutingOutput{}, callErr
	}
	output, err := parseTreeRoutingOutput(chat.Content)
	if err == nil {
		err = validateTreeRoutingOutput(output, r.semanticRouter.graph.Revision(), eligible, r.semanticRouter.calibration.TreeTopK)
	}
	return output, err
}

func treeGraphProjection(candidates []semanticrouting.Candidate) []map[string]any {
	out := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, map[string]any{
			"candidate_id": candidate.ID, "capability_path": candidate.CapabilityPath,
			"operation": candidate.Route.Operation, "fact_scope": candidate.Route.FactScope,
			"leaf_description": candidate.LeafDescription, "semantic_boundary": candidate.TreeDescription,
			"positive_semantics": candidate.EmbedTexts, "hard_negatives": candidate.HardNegatives,
		})
	}
	return out
}

func parseTreeRoutingOutput(content string) (treeRoutingOutput, error) {
	candidates := extractJSONObjects(strings.TrimSpace(content))
	if len(candidates) == 0 {
		return treeRoutingOutput{}, errors.New("Tree semantic routing output is missing a JSON object")
	}
	var parseErr error
	for _, raw := range candidates {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		var output treeRoutingOutput
		if err := decoder.Decode(&output); err != nil {
			parseErr = err
			continue
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			parseErr = errors.New("Tree semantic routing output contains trailing JSON")
			continue
		}
		return output, nil
	}
	return treeRoutingOutput{}, parseErr
}

func validateTreeRoutingOutput(output treeRoutingOutput, graphRevision string, eligible []semanticrouting.Candidate, topK int) error {
	if output.GraphRevision != graphRevision {
		return errors.New("Tree semantic routing output uses a stale graph revision")
	}
	if len(output.Candidates) > topK {
		return errors.New("Tree semantic routing output exceeds top_k")
	}
	allowed := make(map[string]bool, len(eligible))
	for _, candidate := range eligible {
		allowed[candidate.ID] = true
	}
	seen := make(map[string]bool, len(output.Candidates))
	for _, candidate := range output.Candidates {
		if !allowed[candidate.CandidateID] || seen[candidate.CandidateID] {
			return fmt.Errorf("Tree semantic routing output contains invalid candidate %q", candidate.CandidateID)
		}
		if candidate.TreeScore == nil || *candidate.TreeScore < 0 || *candidate.TreeScore > 1 {
			return fmt.Errorf("Tree semantic routing candidate %q has invalid score", candidate.CandidateID)
		}
		seen[candidate.CandidateID] = true
	}
	return nil
}

func (r Runtime) scoreRerankerChannel(ctx context.Context, sessionID, runID, query string, shortlist []semanticrouting.CandidateScore) (map[string]float64, semanticrouting.ChannelState) {
	state := semanticrouting.ChannelState{Status: semanticrouting.ChannelFailed, ReasonCode: "reranker_failed"}
	documents := make([]string, 0, len(shortlist))
	for _, score := range shortlist {
		documents = append(documents, score.Candidate.RerankCard())
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(r.semanticRouter.calibration.RerankerTimeoutMS)*time.Millisecond)
	defer cancel()
	started := time.Now().UTC()
	result, callErr := r.models.Rerank(callCtx, query, documents, len(documents))
	completed := time.Now().UTC()
	r.store.SaveModelCall(modelCallFromRerank(sessionID, runID, "intent_rerank", result, callErr, started, completed))
	if callErr != nil {
		return nil, state
	}
	if len(result.Results) != len(shortlist) {
		state.ReasonCode = "reranker_result_count_invalid"
		return nil, state
	}
	scores := make(map[string]float64, len(shortlist))
	seen := make(map[int]bool, len(shortlist))
	for _, item := range result.Results {
		if item.Index < 0 || item.Index >= len(shortlist) || seen[item.Index] {
			state.ReasonCode = "reranker_result_invalid"
			return nil, state
		}
		seen[item.Index] = true
		scores[shortlist[item.Index].Candidate.ID] = item.Score
	}
	return scores, semanticrouting.ChannelState{Status: semanticrouting.ChannelHealthy, Model: result.Model}
}

func persistedIntentFusion(router *semanticIntentRouter, channels map[string]semanticrouting.ChannelState, decision semanticrouting.Decision) app.IntentFusionDecision {
	channel := func(name string) app.IntentFusionChannel {
		state := channels[name]
		return app.IntentFusionChannel{Status: string(state.Status), Model: state.Model, ReasonCode: state.ReasonCode}
	}
	persisted := app.IntentFusionDecision{
		SchemaVersion: app.IntentFusionDecisionSchemaVersion, GraphRevision: router.graph.Revision(),
		CalibrationRevision: router.calibration.Revision,
		Channels:            app.IntentFusionChannels{Embedding: channel("embedding"), Tree: channel("tree"), Reranker: channel("reranker")},
		Verdict:             string(decision.Verdict), Confidence: decision.Confidence, Margin: decision.Margin,
		Degraded: decision.Degraded, ReasonCode: decision.ReasonCode,
	}
	for _, score := range decision.Candidates {
		persisted.Candidates = append(persisted.Candidates, app.IntentFusionCandidate{
			CandidateID: score.Candidate.ID, CapabilityPath: append([]app.CapabilityID(nil), score.Candidate.CapabilityPath...),
			EmbeddingScore: score.EmbeddingScore, TreeScore: score.TreeScore, FusionScore: score.FusionScore,
			RerankerScore: score.RerankerScore, FinalScore: score.FinalScore,
			NegativeConflict: score.NegativeConflict,
		})
	}
	return persisted
}

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

func semanticQueryProjection(content string, resources []app.MessagePart) string {
	resourceKinds := make(map[string]bool)
	for _, resource := range resources {
		if resource.Resource == nil {
			continue
		}
		switch resource.Kind {
		case app.MessagePartFile:
			resourceKinds["attached governed document"] = true
		case app.MessagePartImage:
			resourceKinds["attached governed image"] = true
		}
	}
	if len(resourceKinds) == 0 {
		return strings.TrimSpace(content)
	}
	kinds := make([]string, 0, len(resourceKinds))
	for kind := range resourceKinds {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	projection := "[trusted resource kinds: " + strings.Join(kinds, ", ") + "]"
	if strings.TrimSpace(content) == "" {
		return "Inspect the " + strings.Join(kinds, " and ") + ". " + projection
	}
	return strings.TrimSpace(content) + "\n" + projection
}

func semanticOwnerProjection(content string) string {
	const attachmentHeader = "Attached files for this user turn:"
	if index := strings.LastIndex(content, attachmentHeader); index >= 0 {
		content = content[:index]
	}
	return strings.TrimSpace(content)
}

func (r Runtime) routeCapability(ctx context.Context, sessionID, runID, content string) (app.RouteDecision, error) {
	decision, err := r.routeIntent(ctx, sessionID, runID, content)
	return decision.Route, err
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

func cloneFacts(value map[string]string) map[string]string {
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

func modelCallFromEmbedding(sessionID, runID, operation string, result modelrouter.EmbeddingResult, err error, started, completed time.Time) app.ModelCall {
	return app.ModelCall{
		ID: app.NewID("mcall"), SessionID: sessionID, RunID: runID, Lane: firstNonEmptyString(result.Lane, "embedding"),
		Profile: firstNonEmptyString(result.Profile, "unknown"), Model: firstNonEmptyString(result.Model, "unknown"), Operation: operation,
		Mock: result.Mock, Status: modelCallStatus(err), PromptTokens: result.PromptTokens, TotalTokens: result.TotalTokens,
		LatencyMS: completed.Sub(started).Milliseconds(), Error: modelCallError(err), StartedAt: started, CompletedAt: &completed,
	}
}

func modelCallFromRerank(sessionID, runID, operation string, result modelrouter.RerankResult, err error, started, completed time.Time) app.ModelCall {
	return app.ModelCall{
		ID: app.NewID("mcall"), SessionID: sessionID, RunID: runID, Lane: firstNonEmptyString(result.Lane, "reranker"),
		Profile: firstNonEmptyString(result.Profile, "unknown"), Model: firstNonEmptyString(result.Model, "unknown"), Operation: operation,
		Mock: result.Mock, Status: modelCallStatus(err), PromptTokens: result.PromptTokens, TotalTokens: result.TotalTokens,
		LatencyMS: completed.Sub(started).Milliseconds(), Error: modelCallError(err), StartedAt: started, CompletedAt: &completed,
	}
}

func modelCallStatus(err error) string {
	if err != nil {
		return "failed"
	}
	return "completed"
}

func modelCallError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
