package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
)

type groundedTarget struct {
	Kind     string
	Ref      string
	Facts    map[string]string
	Document documentContextReference
}

type intentGroundingProjection struct {
	Targets       []groundedTarget
	WorkspaceRoot string
	SessionID     string
	RunID         string
	ExternalMCP   bool
}

func (r Runtime) projectIntentGrounding(ctx context.Context, sessionID, runID, content string, documents documentContextResolution) (intentGroundingProjection, error) {
	projection := intentGroundingProjection{SessionID: sessionID, RunID: runID}
	if r.store != nil {
		session, ok, err := r.store.GetSession(ctx, sessionID)
		if err != nil {
			return intentGroundingProjection{}, fmt.Errorf("resolve intent session: %w", err)
		}
		if ok {
			projection.WorkspaceRoot = session.WorkspaceRoot
		}
		if run, ok, err := r.store.GetRun(ctx, runID); err != nil {
			return intentGroundingProjection{}, fmt.Errorf("resolve intent run: %w", err)
		} else if ok && run.MessageContext != nil {
			projection.ExternalMCP = isExternalMCPInvocation(run.MessageContext.MCP)
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
	for _, document := range documents.References {
		facts := map[string]string{
			"target_provenance":  document.Provenance,
			"document_id":        document.DocumentID,
			"document_parent_id": document.ParentDocumentID,
			"document_format":    document.Format,
			"document_source":    document.Source,
			"document_source_id": document.SourceID,
			"document_activity":  document.Activity,
		}
		projection.Targets = append(projection.Targets, groundedTarget{
			Kind: "workspace_path", Ref: document.Ref, Facts: facts, Document: document,
		})
	}
	projection.Targets = dedupeGroundedTargets(projection.Targets)
	return projection, nil
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

func (r Runtime) routeFromFusionDecision(ctx context.Context, content string, grounding intentGroundingProjection, decision semanticrouting.Decision, clientTimezone string) (app.RouteDecision, error) {
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
	base.Status = app.RouteMatched
	base.Slots.Operation = candidate.Route.Operation
	base.Slots.FactScope = candidate.Route.FactScope
	if node.Route.RequireQuery {
		base.Slots.Query = materializeRoutedQuery(candidate.Capability, content, currentSearchDateForTimezone(time.Now(), clientTimezone))
	}
	if node.Route.RequireTarget {
		compatible := make([]groundedTarget, 0, 1)
		for _, target := range grounding.Targets {
			if slices.Contains(node.Route.TargetKinds, target.Kind) {
				compatible = append(compatible, target)
			}
		}
		if len(compatible) == 0 && node.Route.AllowsWorkflowTargetResolution() {
			compatible = append(compatible, groundedTarget{
				Kind: string(app.TargetKindPublicNamedTarget), Ref: strings.TrimSpace(content),
				Facts: map[string]string{"browser_target_source": "owner_named_public_target"},
			})
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
			var preflight documentPreflight
			var err error
			if grounding.ExternalMCP {
				preflight, err = preflightExternalMCPDocumentPath(target.Ref, edit)
			} else {
				preflight, err = preflightDocumentPath(grounding.WorkspaceRoot, target.Ref, edit)
			}
			if err != nil {
				base.Status, base.Slots, base.Facts = app.RouteBlocked, app.RouteSlots{}, nil
				base.Reason = "document_preflight_failed: " + err.Error()
				return base, nil
			}
			if (candidate.Route.Operation == app.RouteOperationTransform || candidate.Route.Operation == app.RouteOperationEdit) &&
				!registeredAgentDocumentFormatPolicies().allowsRouteOperation(preflight.Format, candidate.Route.Operation) {
				base.Status, base.Slots, base.Facts = app.RouteClarify, app.RouteSlots{}, nil
				base.Reason = "document_operation_variant_conflicts_with_format"
				return base, nil
			}
			target.Ref = preflight.InputRef
			target.Facts = cloneFacts(target.Facts)
			if target.Facts == nil {
				target.Facts = map[string]string{}
			}
			target.Facts["path"] = preflight.InputRef
			target.Facts["document_format"] = preflight.Format
			if r.store != nil && !grounding.ExternalMCP {
				record, err := r.confirmDocumentRecord(ctx, grounding.SessionID, grounding.RunID, target.Document, preflight)
				if err != nil {
					return app.RouteDecision{}, err
				}
				target.Facts["document_id"] = record.ID
				target.Facts["document_source"] = record.Source
				target.Facts["document_source_id"] = firstNonEmptyString(
					record.SourceToolCallID,
					record.SourceMessageID,
					record.SourceRunID,
					record.ID,
				)
				target.Facts["document_activity"] = record.LastActivity
			}
			base.Slots.OutputRef, base.Slots.Format = preflight.OutputRef, preflight.Format
			if edit {
				target.Facts["output_path"] = preflight.OutputRef
			}
			if policy, ok := registeredAgentDocumentFormatPolicies().format(preflight.Format); ok && policy.GroundRoute != nil {
				grounding := policy.GroundRoute(content, candidate.Route.Operation, target.Facts)
				if grounding.Status != "" {
					base.Status, base.Slots, base.Facts = grounding.Status, app.RouteSlots{}, nil
					base.Reason = grounding.Reason
					return base, nil
				}
			}
		}
		base.Slots.TargetKind, base.Slots.TargetRef = target.Kind, target.Ref
		base.Facts = cloneFacts(target.Facts)
		if target.Kind == string(app.TargetKindLocation) {
			base.Slots.Location, base.Slots.Format = target.Ref, "image"
		}
	}
	if isManagedBrowserWorkflow(candidate.Workflow.ID) && asksForBrowserScreenshot(content) {
		if base.Facts == nil {
			base.Facts = map[string]string{}
		}
		base.Facts["browser_visual_reason"] = "owner_requested"
	}
	if err := r.capabilities.ValidateDecision(base); err != nil {
		return app.RouteDecision{}, err
	}
	return base, nil
}
