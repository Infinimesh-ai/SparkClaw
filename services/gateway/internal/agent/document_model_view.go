package agent

import (
	"encoding/json"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

// documentModelViewRevision is the first document.read revision whose model
// inputs use document.ModelView. Earlier revisions keep their persisted JSON
// projections. Tool outputs and execution evidence are unchanged: the program
// records node IDs, hashes, offsets and coverage; models read source content.
const documentModelViewRevision = 10

func documentUsesModelView(state *app.WorkflowState) bool {
	return state != nil && state.Plan.ProfileID == app.WorkflowDocumentRead && state.Plan.ProfileRevision >= documentModelViewRevision
}

const documentModelViewContentHeader = "Source content (# lines are native headings; every other line is [native locator] source text; (context) marks required context outside the selected range):"

// documentFinalEvidence selects the persisted projection of the workflow's
// revision.
func documentFinalEvidence(state *app.WorkflowState, output map[string]any, text string, budget int) (string, bool) {
	if documentUsesModelView(state) {
		return documentModelViewFinalEvidence(output, text, budget)
	}
	return scopedDocumentFinalEvidence(output, text, budget)
}

func documentModelViewFinalEvidence(output map[string]any, text string, budget int) (string, bool) {
	supplement := documentModelViewSupplement(output)
	if supplement != "" && len(supplement)+2 > budget {
		return boundedUTF8Prefix([]byte("[Source-location or slide-visual evidence exceeds the final model budget. Do not infer chapter locations, diagram relationships, or chart values.]"), max(0, budget)), true
	}
	remaining := budget
	if supplement != "" {
		remaining -= len(supplement) + 2
	}
	body, truncated := "", false
	if view := documentModelViewOfOutput(output); view != nil {
		if !view.Empty() {
			body = documentModelViewContentHeader + "\n" + view.String()
		}
		if len(body) > remaining {
			body, truncated = boundedUTF8Prefix([]byte("[Source values and their required context exceed the final model budget and were not projected.]"), max(0, remaining)), true
		}
	} else {
		body = sampledDocumentEvidence(text, remaining)
		truncated = body != text
	}
	if supplement == "" {
		return body, truncated
	}
	return supplement + "\n\n" + body, truncated
}

func documentModelViewSupplement(output map[string]any) string {
	sections := []string{}
	if raw, ok := output["source_locations"]; ok && raw != nil {
		sections = append(sections, "source_locations (native headings in original order; block and parent_block are authoritative; location metadata alone does not mean a body was read):\n"+document.ModelViewSourceLocations(documentModelViewMaps(raw)))
	}
	if relations := stringListValue(output["source_location_relations"]); len(relations) > 0 {
		sections = append(sections, "source_location_relations (computed from native parent/order):\n- "+strings.Join(relations, "\n- "))
	}
	if coverage, ok := output["visual_coverage"]; ok && coverage != nil {
		raw, _ := json.Marshal(coverage)
		sections = append(sections, "visual_coverage: "+string(raw))
	}
	if evidence, ok := output["visual_evidence"]; ok && evidence != nil {
		sections = append(sections, "visual_evidence (fallible slide image observations bound to these pages):\n"+document.ModelViewVisualEvidence(documentModelViewMaps(evidence)))
	}
	return strings.Join(sections, "\n\n")
}

// documentModelViewOfOutput returns nil when the output carries no canonical
// nodes, so text-only results keep their text projection.
func documentModelViewOfOutput(output map[string]any) *document.ModelView {
	nodes, dependencies := anySlice(output["nodes"]), anySlice(output["dependencies"])
	if len(nodes) == 0 && len(dependencies) == 0 {
		return nil
	}
	view := document.NewModelView()
	for _, raw := range nodes {
		if fragment, ok := documentModelViewFragment(raw, false); ok {
			view.Add(fragment)
		}
	}
	for _, raw := range dependencies {
		if fragment, ok := documentModelViewFragment(raw, true); ok {
			view.Add(fragment)
		}
	}
	return view
}

type documentModelViewNode struct {
	document.StructureNode
	ByteStart *int  `json:"byte_start"`
	ByteEnd   *int  `json:"byte_end"`
	WholeNode *bool `json:"whole_node"`
}

func documentModelViewFragment(raw any, context bool) (document.ModelViewFragment, bool) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return document.ModelViewFragment{}, false
	}
	var node documentModelViewNode
	if json.Unmarshal(encoded, &node) != nil {
		return document.ModelViewFragment{}, false
	}
	fragment := document.ModelViewFragment{Node: node.StructureNode, Context: context}
	// A read fragment already carries only its selected text. Offsets mark a
	// continued node so it is not presented as complete.
	if node.WholeNode != nil && !*node.WholeNode && node.ByteStart != nil && node.ByteEnd != nil {
		fragment.Partial, fragment.ByteStart, fragment.ByteEnd = true, *node.ByteStart, *node.ByteEnd
	}
	return fragment, true
}

func documentModelViewMaps(raw any) []map[string]any {
	result := []map[string]any{}
	for _, item := range anySlice(raw) {
		if value, ok := anyMap(item); ok {
			result = append(result, value)
		}
	}
	return result
}
