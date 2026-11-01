package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
)

type clarificationProposal struct {
	Text   string
	Record *clarification.Record
}
type clarificationProposalKey struct{}

func chineseRequest(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0
}
func (r Runtime) terminalRouting(status app.RouteStatus, reason string) IntentRoutingOutput {
	return IntentRoutingOutput{Route: app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: status, CatalogRevision: r.capabilities.Revision(), Reason: reason}}
}
func (r Runtime) signProposal(text, ownerText string, options []clarification.Option) *clarificationProposal {
	if r.clarification == nil || r.clarification.signer == nil || r.clarification.sourceMessageID == "" || r.clarification.contextDigest == "" || len(options) == 0 {
		return nil
	}
	if r.clarification.sourceText != nil {
		ownerText = *r.clarification.sourceText
	}
	revisions := r.clarificationRevisions()
	record, err := r.clarification.signer.SignClarification(r.clarification.binding, clarification.Record{
		SchemaVersion: clarification.SchemaVersion, ID: app.NewID("clarify"), RequestID: r.clarification.requestID,
		SourceMessageID: r.clarification.sourceMessageID, SourceTextSHA256: clarification.TextDigest(ownerText), ContextSHA256: r.clarification.contextDigest, PromptSHA256: clarification.TextDigest(text),
		GraphRevision: revisions.Graph, CatalogRevision: revisions.Catalog, CalibrationRevision: revisions.Calibration, Options: options,
	})
	if err != nil {
		return nil
	}
	return &clarificationProposal{Text: text, Record: &record}
}
func stableDocumentReference(d documentContextReference) string {
	if d.HandleID != "" && d.SHA256 != "" {
		return "handle:" + d.HandleID
	}
	return d.Ref
}
func fileBinding(d documentContextReference) string {
	source := d.SourceID
	if at := strings.LastIndex(source, ":part:"); at >= 0 {
		source = source[:at]
	}
	digest, _ := clarification.BindingDigest(struct {
		ID            string `json:"id"`
		SourceMessage string `json:"source_message_id"`
		Ref           string `json:"ref"`
		Name          string `json:"name"`
		SHA           string `json:"sha256"`
		Size          int    `json:"size"`
	}{d.HandleID, source, stableDocumentReference(d), d.Name, d.SHA256, d.Size})
	return digest
}
func selectClarificationDocument(documents documentContextResolution, option clarification.Option) (documentContextResolution, bool) {
	if documents.Unavailable {
		return documents, false
	}
	for _, d := range documents.References {
		if d.HandleID != "" && d.HandleID == option.FileID && fileBinding(d) == option.BindingSHA256 {
			return documentContextResolution{References: []documentContextReference{d}}, true
		}
	}
	return documents, false
}
func (r Runtime) fileClarification(ownerText string, documents documentContextResolution) *clarificationProposal {
	if documents.Unavailable || len(documents.References) < 2 || len(documents.References) > 2 {
		return nil
	}
	text := "Which file should I use?"
	if chineseRequest(ownerText) {
		text = "你希望处理哪一个文件？"
	}
	options := make([]clarification.Option, 0, 2)
	for _, d := range documents.References {
		if d.HandleID == "" || d.SHA256 == "" {
			return nil
		}
		n := len(options) + 1
		label := d.Name
		// Same-name files remain distinguishable without exposing local paths.
		if documents.References[0].Name == documents.References[1].Name {
			label = fmt.Sprintf("%s (%s)", label, d.HandleID[max(0, len(d.HandleID)-8):])
		}
		text += fmt.Sprintf("\n%d. %s", n, label)
		options = append(options, clarification.Option{Number: n, Kind: "file", FileID: d.HandleID, BindingSHA256: fileBinding(d)})
	}
	return r.signProposal(text, ownerText, options)
}
func (r Runtime) candidateExecutable(candidate semanticrouting.Candidate, grounding intentGroundingProjection, source app.MessageSourceKind, degraded bool) bool {
	node, ok := r.capabilities.Node(candidate.Capability)
	if !ok || node.Route == nil || !candidate.SupportsSource(source) || !node.Route.SupportsSource(candidate.Route.Operation, source) {
		return false
	}
	if degraded && routeOperationMutates(candidate.Route.Operation) {
		return false
	}
	if grounding.HasUnavailableDocuments && node.Route.RequireTarget && slices.Contains(node.Route.TargetKinds, "workspace_path") {
		return false
	}
	if node.Route.RequireTarget {
		targets := compatibleClarificationTargets(grounding, node.Route.TargetKinds)
		if len(targets) != 1 {
			return len(targets) == 0 && node.Route.AllowsWorkflowTargetResolution()
		}
		t := targets[0]
		if t.Kind == "workspace_path" {
			if !r.jingSiDocumentPreflightAuthorized(grounding.Run, t.Ref) {
				return false
			}
			if (candidate.Route.Operation == app.RouteOperationEdit || candidate.Route.Operation == app.RouteOperationTransform) && t.Document.Format != "" && !registeredAgentDocumentFormatPolicies().allowsRouteOperation(t.Document.Format, candidate.Route.Operation) {
				return false
			}
		}
	}
	return true
}
func compatibleClarificationTargets(g intentGroundingProjection, kinds []string) []groundedTarget {
	out := make([]groundedTarget, 0)
	for _, t := range g.Targets {
		if slices.Contains(kinds, t.Kind) {
			out = append(out, t)
		}
	}
	return out
}
func routeOperationMutates(operation app.RouteOperation) bool {
	switch operation {
	case app.RouteOperationCreate, app.RouteOperationEdit, app.RouteOperationTransform, app.RouteOperationDelete, app.RouteOperationPublish:
		return true
	}
	return false
}
func (r Runtime) routeBinding(ctx context.Context, candidate semanticrouting.Candidate, g intentGroundingProjection, delivery DeliveryDirective) (string, string, error) {
	type targetBinding struct {
		Kind    string `json:"kind"`
		Ref     string `json:"ref"`
		FileSHA string `json:"file_binding"`
	}
	targets := make([]targetBinding, 0, len(g.Targets))
	for _, t := range g.Targets {
		ref := t.Ref
		if t.Kind == "workspace_path" {
			if t.Document.SHA256 == "" || t.Document.HandleID == "" {
				return "", "", clarification.ErrInvalid
			}
			ref = stableDocumentReference(t.Document)
		}
		targets = append(targets, targetBinding{t.Kind, ref, fileBinding(t.Document)})
	}
	endpoint := ""
	if delivery.ExplicitExternal {
		var err error
		endpoint, err = r.deliveryEndpoint(ctx, g.Run, delivery)
		if err != nil {
			return "", "", err
		}
	}
	digest, err := clarification.BindingDigest(struct {
		Candidate string          `json:"candidate_id"`
		Targets   []targetBinding `json:"targets"`
		Endpoint  string          `json:"endpoint_id"`
		Provider  string          `json:"provider"`
		Recipient string          `json:"recipient"`
	}{candidate.ID, targets, endpoint, delivery.RequestedProviderKey, delivery.RequestedRecipientText})
	return digest, endpoint, err
}
func (r Runtime) routingClarification(ctx context.Context, ownerText string, source app.MessageSourceKind, decision semanticrouting.Decision, g intentGroundingProjection, documents documentContextResolution, route app.RouteDecision, delivery DeliveryDirective) *clarificationProposal {
	if source != app.MessageSourceWeb || g.ExternalMCP || route.Status == app.RouteBlocked {
		return nil
	}
	if route.Reason == "required_grounded_target_ambiguous" {
		return r.fileClarification(ownerText, documents)
	}
	if decision.Verdict != semanticrouting.VerdictLow && decision.Verdict != semanticrouting.VerdictAmbiguous {
		return nil
	}
	text := "What would you like me to do?"
	if chineseRequest(ownerText) {
		text = "你希望我怎样处理？"
	}
	options := make([]clarification.Option, 0, 2)
	// Filter only the original Top2. Never backfill a lower-ranked candidate.
	for _, score := range decision.Candidates[:min(2, len(decision.Candidates))] {
		c := score.Candidate
		if score.FusionScore < r.semanticRouter.calibration.ClarifyFloor || !r.candidateExecutable(c, g, source, decision.Degraded) {
			continue
		}
		binding, _, err := r.routeBinding(ctx, c, g, delivery)
		if err != nil {
			continue
		}
		label := c.ClarifyLabel.EN
		if chineseRequest(ownerText) {
			label = c.ClarifyLabel.ZH
		}
		n := len(options) + 1
		text += fmt.Sprintf("\n%d. %s", n, label)
		options = append(options, clarification.Option{Number: n, Kind: "route", CandidateID: c.ID, BindingSHA256: binding})
	}
	return r.signProposal(text, ownerText, options)
}
func (r Runtime) applySelectedRoute(ctx context.Context, s *clarificationSelection, d semanticrouting.Decision, g intentGroundingProjection, source app.MessageSourceKind, delivery DeliveryDirective) (semanticrouting.Decision, error) {
	candidate, ok := r.semanticRouter.graph.Candidate(s.option.CandidateID)
	if !ok || d.Verdict == semanticrouting.VerdictBlocked || !r.candidateExecutable(candidate, g, source, d.Degraded) {
		return d, clarification.ErrInvalid
	}
	if d.Degraded && (d.Verdict != semanticrouting.VerdictClear || len(d.Candidates) == 0 || d.Candidates[0].Candidate.ID != candidate.ID) {
		return d, clarification.ErrInvalid
	}
	digest, endpoint, err := r.routeBinding(ctx, candidate, g, delivery)
	if err != nil || digest != s.option.BindingSHA256 {
		return d, clarification.ErrInvalid
	}
	s.endpoint = endpoint
	if err = r.claimClarification(s); err != nil {
		return d, err
	}
	d.Verdict = semanticrouting.VerdictClear
	d.ReasonCode = "owner_selected_candidate"
	d.Candidates = []semanticrouting.CandidateScore{{Candidate: candidate, FusionScore: d.Confidence}}
	return d, nil
}
func isScheduleCreateCandidate(c semanticrouting.Candidate) bool {
	return c.Capability == app.CapabilityScheduleManage && c.Route.Operation == app.RouteOperationCreate
}
