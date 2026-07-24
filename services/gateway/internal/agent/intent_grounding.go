package agent

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
)

type groundedTarget struct {
	Kind  string
	Ref   string
	Facts map[string]string
}

type intentGroundingProjection struct {
	Targets       []groundedTarget
	WorkspaceRoot string
}

func (r Runtime) projectIntentGrounding(sessionID, runID, content string, resources []app.MessagePart) intentGroundingProjection {
	projection := intentGroundingProjection{}
	if r.store != nil {
		if session, ok := r.store.GetSession(sessionID); ok {
			projection.WorkspaceRoot = session.WorkspaceRoot
		}
	}
	if strings.TrimSpace(projection.WorkspaceRoot) == "" && r.tools != nil {
		projection.WorkspaceRoot = r.tools.Config().Workspaces.DefaultRoot
	}
	for _, rawURL := range extractURLs(content) {
		url := normalizeBrowserURL(rawURL)
		projection.Targets = append(projection.Targets, groundedTarget{Kind: "url", Ref: url, Facts: map[string]string{"url": url}})
	}
	if destination, ok := matchRegisteredBrowserDestination(content); ok {
		url := normalizeBrowserURL(destination.Destination.URL)
		projection.Targets = append(projection.Targets, groundedTarget{Kind: "url", Ref: url, Facts: map[string]string{
			"url": url, "browser_destination": destination.Destination.ID,
		}})
	}
	if explicitCurrentBrowserTab(strings.ToLower(content)) {
		projection.Targets = append(projection.Targets, groundedTarget{Kind: string(app.TargetKindBrowserCurrentTab), Ref: "selected"})
	}
	if location := weatherLocationFromRequest(content); location != "" {
		projection.Targets = append(projection.Targets, groundedTarget{Kind: string(app.TargetKindLocation), Ref: location, Facts: map[string]string{"location_source": "current_turn"}})
	}
	paths := append(documentRoutePaths(content), attachedWorkspaceDocumentPaths(resources)...)
	if len(paths) == 0 {
		if path := recentDocumentContextPath(r.buildAgentContextSnapshot(sessionID, runID, content)); path != "" {
			paths = append(paths, path)
		}
	}
	for _, path := range paths {
		projection.Targets = append(projection.Targets, groundedTarget{Kind: "workspace_path", Ref: path})
	}
	projection.Targets = dedupeGroundedTargets(projection.Targets)
	return projection
}

func dedupeGroundedTargets(targets []groundedTarget) []groundedTarget {
	seen := make(map[string]bool, len(targets))
	out := make([]groundedTarget, 0, len(targets))
	for _, target := range targets {
		key := target.Kind + "\x00" + target.Ref
		if strings.TrimSpace(target.Ref) == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, target)
	}
	return out
}

func (r Runtime) routeFromFusionDecision(content string, grounding intentGroundingProjection, decision semanticrouting.Decision) (app.RouteDecision, error) {
	base := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, CatalogRevision: r.capabilities.Revision(),
		Confidence: decision.Confidence, Reason: decision.ReasonCode,
	}
	switch decision.Verdict {
	case semanticrouting.VerdictBlocked:
		base.Status = app.RouteBlocked
		return base, nil
	case semanticrouting.VerdictLow:
		base.Status = app.RouteUnmatched
		return base, nil
	case semanticrouting.VerdictAmbiguous:
		base.Status = app.RouteClarify
		return base, nil
	case semanticrouting.VerdictClear:
		if len(decision.Candidates) == 0 {
			return app.RouteDecision{}, errors.New("clear semantic decision has no candidate")
		}
	default:
		return app.RouteDecision{}, fmt.Errorf("unsupported semantic routing verdict %q", decision.Verdict)
	}
	candidate := decision.Candidates[0].Candidate
	node, ok := r.capabilities.Node(candidate.Capability)
	if !ok || node.Route == nil {
		return app.RouteDecision{}, errors.New("semantic candidate no longer resolves to a Catalog route")
	}
	base.CapabilityPath = append([]app.CapabilityID(nil), candidate.CapabilityPath...)
	if candidate.Capability == app.CapabilityBrowserInteraction {
		intentLower := strings.ToLower(content)
		for _, rawURL := range extractURLs(content) {
			intentLower = strings.ReplaceAll(intentLower, strings.ToLower(rawURL), " ")
		}
		if unsupportedBrowserInteractionIntent(intentLower) {
			base.Status = app.RouteBlocked
			base.Reason = "browser_interaction_outside_registered_boundary"
			return base, nil
		}
	}
	base.Status = app.RouteMatched
	base.Slots.Operation = candidate.Route.Operation
	base.Slots.FactScope = candidate.Route.FactScope
	if node.Route.RequireQuery {
		base.Slots.Query = materializeRoutedQuery(candidate.Capability, content, currentSearchDate())
	}
	if node.Route.RequireTarget {
		compatible := make([]groundedTarget, 0, 1)
		for _, target := range grounding.Targets {
			if slices.Contains(node.Route.TargetKinds, target.Kind) {
				compatible = append(compatible, target)
			}
		}
		if len(compatible) != 1 {
			base.Status = app.RouteClarify
			base.Slots = app.RouteSlots{}
			base.Facts = nil
			if len(compatible) == 0 {
				base.Reason = "required_grounded_target_missing"
			} else {
				base.Reason = "required_grounded_target_ambiguous"
			}
			return base, nil
		}
		target := compatible[0]
		if target.Kind == "workspace_path" {
			edit := candidate.Route.Operation == app.RouteOperationEdit || candidate.Route.Operation == app.RouteOperationTransform
			preflight, err := preflightDocumentPath(grounding.WorkspaceRoot, target.Ref, edit)
			if err != nil {
				base.Status, base.Slots, base.Facts = app.RouteBlocked, app.RouteSlots{}, nil
				base.Reason = "document_preflight_failed: " + err.Error()
				return base, nil
			}
			if candidate.Route.Operation == app.RouteOperationTransform && preflight.Format != app.DocumentFormatPDF ||
				candidate.Route.Operation == app.RouteOperationEdit && preflight.Format == app.DocumentFormatPDF {
				base.Status, base.Slots, base.Facts = app.RouteClarify, app.RouteSlots{}, nil
				base.Reason = "document_operation_variant_conflicts_with_format"
				return base, nil
			}
			target.Ref = preflight.InputRef
			target.Facts = map[string]string{"path": preflight.InputRef, "document_format": preflight.Format}
			base.Slots.OutputRef, base.Slots.Format = preflight.OutputRef, preflight.Format
			if edit {
				target.Facts["output_path"] = preflight.OutputRef
			}
		}
		base.Slots.TargetKind, base.Slots.TargetRef = target.Kind, target.Ref
		base.Facts = cloneFacts(target.Facts)
		if target.Kind == string(app.TargetKindLocation) {
			base.Slots.Location, base.Slots.Format = target.Ref, "image"
		}
	}
	if err := r.capabilities.ValidateDecision(base); err != nil {
		return app.RouteDecision{}, err
	}
	return base, nil
}
