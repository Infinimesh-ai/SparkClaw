package semanticrouting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
)

var variantKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

type RouteTemplate struct {
	Operation app.RouteOperation `json:"operation"`
	FactScope app.RouteFactScope `json:"fact_scope,omitempty"`
}

type IntentVariant struct {
	Key             string                  `json:"key"`
	Route           RouteTemplate           `json:"route"`
	EmbedTexts      []string                `json:"embed_texts"`
	TreeDescription string                  `json:"tree_description"`
	HardNegatives   []string                `json:"hard_negatives,omitempty"`
	SourceKinds     []app.MessageSourceKind `json:"source_kinds,omitempty"`
}

type WorkflowSemantics struct {
	Variants []IntentVariant `json:"variants"`
}

type Registration struct {
	Capability app.CapabilityID
	Workflow   app.WorkflowContractRef
	Semantics  WorkflowSemantics
}

type Candidate struct {
	ID              string                  `json:"candidate_id"`
	Key             string                  `json:"key"`
	Capability      app.CapabilityID        `json:"capability"`
	CapabilityPath  []app.CapabilityID      `json:"capability_path"`
	Workflow        app.WorkflowContractRef `json:"workflow"`
	Route           RouteTemplate           `json:"route"`
	LeafDescription string                  `json:"leaf_description"`
	EmbedTexts      []string                `json:"embed_texts"`
	TreeDescription string                  `json:"tree_description"`
	HardNegatives   []string                `json:"hard_negatives,omitempty"`
	SourceKinds     []app.MessageSourceKind `json:"source_kinds,omitempty"`
}

func (c Candidate) SupportsSource(kind app.MessageSourceKind) bool {
	return len(c.SourceKinds) == 0 || slices.Contains(c.SourceKinds, kind)
}

func (c Candidate) RerankCard() string {
	positives := strings.Join(c.EmbedTexts, " | ")
	negatives := strings.Join(c.HardNegatives, " | ")
	return fmt.Sprintf(
		"candidate_id=%s\npath=%s\noperation=%s\nfact_scope=%s\nboundary=%s\npositive_semantics=%s\nhard_negatives=%s",
		c.ID, joinPath(c.CapabilityPath), c.Route.Operation, c.Route.FactScope,
		c.TreeDescription, positives, negatives,
	)
}

type Graph struct {
	revision        string
	catalogRevision string
	candidates      []Candidate
	byID            map[string]Candidate
}

func Compile(catalog capability.Catalog, registrations []Registration) (*Graph, error) {
	candidates := make([]Candidate, 0)
	seenCapabilities := make(map[app.CapabilityID]bool, len(registrations))
	seenIDs := make(map[string]bool)
	for _, registration := range registrations {
		if registration.Capability == "" || registration.Workflow.ID == "" || registration.Workflow.Revision < 1 {
			return nil, errors.New("semantic routing registration is incomplete")
		}
		if seenCapabilities[registration.Capability] {
			return nil, fmt.Errorf("semantic routing capability %q is registered more than once", registration.Capability)
		}
		seenCapabilities[registration.Capability] = true
		node, ok := catalog.Node(registration.Capability)
		if !ok || node.Kind != capability.NodeLeaf || node.Route == nil || node.Workflow == nil {
			return nil, fmt.Errorf("semantic routing capability %q is not a registered Catalog leaf", registration.Capability)
		}
		if *node.Workflow != registration.Workflow {
			return nil, fmt.Errorf("semantic routing capability %q workflow contract does not match the Catalog", registration.Capability)
		}
		if len(registration.Semantics.Variants) == 0 {
			return nil, fmt.Errorf("semantic routing capability %q has no variants", registration.Capability)
		}
		path, err := catalog.PathTo(registration.Capability)
		if err != nil {
			return nil, err
		}
		for _, variant := range registration.Semantics.Variants {
			candidate, err := compileCandidate(node, path, registration, variant)
			if err != nil {
				return nil, err
			}
			if seenIDs[candidate.ID] {
				return nil, fmt.Errorf("semantic routing candidate %q is registered more than once", candidate.ID)
			}
			seenIDs[candidate.ID] = true
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return nil, errors.New("semantic routing graph has no candidates")
	}
	slices.SortFunc(candidates, func(left, right Candidate) int { return strings.Compare(left.ID, right.ID) })
	payload, err := json.Marshal(struct {
		CatalogRevision string      `json:"catalog_revision"`
		Candidates      []Candidate `json:"candidates"`
	}{CatalogRevision: catalog.Revision(), Candidates: candidates})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	revision := catalog.Revision() + "." + hex.EncodeToString(digest[:8])
	byID := make(map[string]Candidate, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.ID] = cloneCandidate(candidate)
	}
	return &Graph{revision: revision, catalogRevision: catalog.Revision(), candidates: candidates, byID: byID}, nil
}

func compileCandidate(node capability.Node, path []app.CapabilityID, registration Registration, variant IntentVariant) (Candidate, error) {
	variant.Key = strings.TrimSpace(variant.Key)
	if !variantKeyPattern.MatchString(variant.Key) {
		return Candidate{}, fmt.Errorf("semantic routing capability %q has invalid variant key %q", registration.Capability, variant.Key)
	}
	if !slices.Contains(node.Route.Operations, variant.Route.Operation) {
		return Candidate{}, fmt.Errorf("semantic routing candidate %q#%s uses operation %q outside its Catalog contract", registration.Capability, variant.Key, variant.Route.Operation)
	}
	if variant.Route.FactScope != "" && !slices.Contains(node.Route.FactScopes, variant.Route.FactScope) {
		return Candidate{}, fmt.Errorf("semantic routing candidate %q#%s uses fact scope %q outside its Catalog contract", registration.Capability, variant.Key, variant.Route.FactScope)
	}
	if len(node.Route.FactScopes) != 0 && variant.Route.FactScope == "" {
		return Candidate{}, fmt.Errorf("semantic routing candidate %q#%s omits its required fact scope", registration.Capability, variant.Key)
	}
	variant.EmbedTexts = cleanUniqueText(variant.EmbedTexts)
	variant.HardNegatives = cleanUniqueText(variant.HardNegatives)
	variant.TreeDescription = strings.TrimSpace(variant.TreeDescription)
	if len(variant.EmbedTexts) == 0 || variant.TreeDescription == "" {
		return Candidate{}, fmt.Errorf("semantic routing candidate %q#%s requires embedding texts and a Tree description", registration.Capability, variant.Key)
	}
	for _, kind := range variant.SourceKinds {
		switch kind {
		case app.MessageSourceWeb, app.MessageSourceThirdPartyDevice, app.MessageSourceTimer:
		default:
			return Candidate{}, fmt.Errorf("semantic routing candidate %q#%s has unsupported source kind %q", registration.Capability, variant.Key, kind)
		}
	}
	return Candidate{
		ID: string(registration.Capability) + "#" + variant.Key, Key: variant.Key,
		Capability: registration.Capability, CapabilityPath: append([]app.CapabilityID(nil), path...), Workflow: registration.Workflow,
		Route: variant.Route, LeafDescription: node.Description,
		EmbedTexts: variant.EmbedTexts, TreeDescription: variant.TreeDescription,
		HardNegatives: variant.HardNegatives, SourceKinds: append([]app.MessageSourceKind(nil), variant.SourceKinds...),
	}, nil
}

func (g *Graph) Revision() string { return g.revision }

func (g *Graph) CatalogRevision() string { return g.catalogRevision }

func (g *Graph) Candidates() []Candidate {
	out := make([]Candidate, 0, len(g.candidates))
	for _, candidate := range g.candidates {
		out = append(out, cloneCandidate(candidate))
	}
	return out
}

func (g *Graph) EligibleCandidates(kind app.MessageSourceKind) []Candidate {
	out := make([]Candidate, 0, len(g.candidates))
	for _, candidate := range g.candidates {
		if candidate.SupportsSource(kind) {
			out = append(out, cloneCandidate(candidate))
		}
	}
	return out
}

func (g *Graph) Candidate(id string) (Candidate, bool) {
	candidate, ok := g.byID[id]
	return cloneCandidate(candidate), ok
}

func cleanUniqueText(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(strings.Join(strings.Fields(value), " "))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func cloneCandidate(candidate Candidate) Candidate {
	candidate.CapabilityPath = append([]app.CapabilityID(nil), candidate.CapabilityPath...)
	candidate.EmbedTexts = append([]string(nil), candidate.EmbedTexts...)
	candidate.HardNegatives = append([]string(nil), candidate.HardNegatives...)
	candidate.SourceKinds = append([]app.MessageSourceKind(nil), candidate.SourceKinds...)
	return candidate
}

func joinPath(path []app.CapabilityID) string {
	parts := make([]string, len(path))
	for index, id := range path {
		parts[index] = string(id)
	}
	return strings.Join(parts, "/")
}
