package document

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ModelViewVersion identifies the model-facing rendering of source nodes.
// Node IDs, hashes, byte offsets and availability bookkeeping stay in tool
// outputs and execution evidence; the program records what was read. A model
// receives only what it must read: native locators, heading structure, text,
// typed values that change meaning, and explicit gaps.
const ModelViewVersion = "document_model_view_v1"

// ModelViewBytesPerToken matches the conservative CJK conversion used by
// content budgets. Estimates and delivery are both measured on this rendering.
const ModelViewBytesPerToken = 3

// ModelViewFragment is one node as delivered to a model. Node.Text is the text
// to show. Context marks a required dependency outside the selected content;
// Partial marks that Node.Text is only source bytes ByteStart..ByteEnd of a
// longer node.
type ModelViewFragment struct {
	Node      StructureNode
	Context   bool
	Partial   bool
	ByteStart int
	ByteEnd   int
}

// ModelView accumulates fragments, deduplicates them by node ID and renders
// them in original source order. Len is exact for the rendered body.
type ModelView struct {
	fragments map[string]ModelViewFragment
	byID      map[string]StructureNode
	// Body lines do not depend on other fragments; heading depth does.
	bodyBytes map[string]int
	bodyTotal int
	headings  map[string]bool
}

func NewModelView() *ModelView {
	return &ModelView{fragments: map[string]ModelViewFragment{}, byID: map[string]StructureNode{}, bodyBytes: map[string]int{}, headings: map[string]bool{}}
}

// Add inserts a fragment. A selected fragment replaces a context copy of the
// same node; a context copy never downgrades selected content.
func (view *ModelView) Add(fragment ModelViewFragment) {
	key := fragment.Node.ID
	if key == "" {
		key = fragment.Node.Locator.Block + "#" + strconv.Itoa(fragment.Node.Order)
	}
	if fragment.Partial {
		key += fmt.Sprintf("@%d:%d", fragment.ByteStart, fragment.ByteEnd)
	}
	if existing, ok := view.fragments[key]; ok && (!existing.Context || fragment.Context) {
		return
	}
	if fragment.Node.ID != "" {
		view.byID[fragment.Node.ID] = fragment.Node
	}
	view.fragments[key] = fragment
	view.bodyTotal -= view.bodyBytes[key]
	delete(view.bodyBytes, key)
	if fragment.Node.Kind == "heading_label" {
		view.headings[key] = true
		return
	}
	if line := view.line(fragment); line != "" {
		view.bodyBytes[key] = len(line) + 1
		view.bodyTotal += len(line) + 1
	}
}

// Len is the exact byte length String would return.
func (view *ModelView) Len() int {
	total := view.bodyTotal
	for key := range view.headings {
		if line := view.line(view.fragments[key]); line != "" {
			total += len(line) + 1
		}
	}
	if total == 0 {
		return 0
	}
	return total - 1
}

func (view *ModelView) Empty() bool { return view.Len() == 0 }

func (view *ModelView) String() string {
	keys := make([]string, 0, len(view.fragments))
	for key := range view.fragments {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		a, b := view.fragments[keys[i]], view.fragments[keys[j]]
		if a.Node.Order != b.Node.Order {
			return a.Node.Order < b.Node.Order
		}
		if a.ByteStart != b.ByteStart {
			return a.ByteStart < b.ByteStart
		}
		return keys[i] < keys[j]
	})
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		if line := view.line(view.fragments[key]); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func (view *ModelView) headingDepth(node StructureNode, seen map[string]bool) int {
	if seen[node.ID] {
		return 1
	}
	seen[node.ID] = true
	depth := 1
	for _, id := range node.Dependencies {
		parent, ok := view.byID[id]
		if !ok || parent.Kind != "heading_label" {
			continue
		}
		depth = max(depth, view.headingDepth(parent, seen)+1)
	}
	return depth
}

func (view *ModelView) line(fragment ModelViewFragment) string {
	node := fragment.Node
	locator := ModelViewLocator(node.Locator)
	values := ModelViewValues(node)
	if node.Kind == "heading_label" {
		title := strings.TrimSpace(node.Title)
		if title == "" {
			title = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(node.Text), "#"))
		}
		depth := min(view.headingDepth(node, map[string]bool{}), 6)
		line := strings.Repeat("#", depth) + " " + title + " [" + locator + "]"
		if values != "" {
			line += " " + values
		}
		return line
	}
	text := modelViewText(node)
	if fragment.Partial {
		locator += fmt.Sprintf(", partial text bytes %d-%d", fragment.ByteStart, fragment.ByteEnd)
	}
	parts := []string{"[" + locator + "]"}
	if node.Availability != "" && node.Availability != NodeAvailable {
		gap := "(" + string(node.Availability)
		if node.Reason != "" {
			gap += ": " + node.Reason
		}
		parts = append(parts, gap+")")
	} else if text == "" && values == "" {
		return ""
	}
	if text != "" {
		parts = append(parts, text)
	}
	if values != "" {
		parts = append(parts, values)
	}
	if fragment.Context {
		parts = append(parts, "(context)")
	}
	return strings.Join(parts, " ")
}

func modelViewText(node StructureNode) string {
	text := strings.TrimRight(node.Text, " \t\r\n")
	if node.Kind == "table_row" {
		text = strings.ReplaceAll(text, "\t", " | ")
	}
	return text
}

// ModelViewLocator is the native locator a model may cite.
func ModelViewLocator(locator StructureLocator) string {
	block := locator.Block
	if locator.Page > 0 && !strings.Contains(block, "page") && !strings.Contains(block, "slide") {
		block += " page " + strconv.Itoa(locator.Page)
	}
	return block
}

// ModelViewValues keeps typed facts that can change meaning (formulas,
// cached results, recalculation state, hidden rows, merged cells) and drops
// parser bookkeeping or copies of the visible text.
func ModelViewValues(node StructureNode) string {
	rendered := []string{}
	for _, raw := range node.Values {
		value := map[string]any{}
		for key, item := range raw {
			value[key] = item
		}
		delete(value, "display_text_source")
		delete(value, "type")
		if address := stringOf(value["address"]); address != "" && strings.HasSuffix(node.Locator.Block, "["+address+"]") {
			delete(value, "address")
		}
		if stringOf(value["sheet_state"]) == "visible" {
			delete(value, "sheet_state")
		}
		if name := stringOf(value["sheet_name"]); name != "" && name == node.Title {
			delete(value, "sheet_name")
		}
		for _, key := range []string{"row_hidden", "column_hidden"} {
			if flag, ok := value[key].(bool); ok && !flag {
				delete(value, key)
			}
		}
		if format, ok := value["number_format"].(string); ok && format == "" {
			delete(value, "number_format")
		}
		for _, key := range []string{"display_text", "value", "text"} {
			if node.Kind != "table_row" && sameAsText(value[key], node.Text) {
				delete(value, key)
			}
		}
		if formula := stringOf(value["formula"]); formula != "" {
			if nested, ok := value["value"].(map[string]any); ok && stringOf(nested["formula"]) == formula {
				delete(value, "value")
			}
		}
		if node.Kind == "table_row" {
			if alias, _ := value["is_merge_alias"].(bool); alias {
				value["merged_from"] = fmt.Sprintf("r%sc%s", stringOf(value["merge_anchor_row"]), stringOf(value["merge_anchor_column"]))
			} else {
				delete(value, "column")
			}
			delete(value, "text")
			delete(value, "is_merge_alias")
			delete(value, "merge_anchor_row")
			delete(value, "merge_anchor_column")
		}
		if len(value) == 0 {
			continue
		}
		raw, err := json.Marshal(value)
		if err == nil {
			rendered = append(rendered, string(raw))
		}
	}
	return strings.Join(rendered, " ")
}

func sameAsText(value any, text string) bool {
	if value == nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == strings.TrimSpace(text)
	case float64, int, int64, json.Number:
		return strings.TrimSpace(fmt.Sprint(typed)) == strings.TrimSpace(text)
	}
	return false
}

func stringOf(value any) string {
	if value == nil {
		return ""
	}
	if typed, ok := value.(string); ok {
		return typed
	}
	return fmt.Sprint(value)
}

// RenderModelView renders fragments in source order.
func RenderModelView(fragments []ModelViewFragment) string {
	view := NewModelView()
	for _, fragment := range fragments {
		view.Add(fragment)
	}
	return view.String()
}

// ModelViewNodeBytes is the delivered size of one canonical leaf with its
// line terminator; containers and previews cost nothing until read.
func ModelViewNodeBytes(node StructureNode, headingDepth int) int {
	view := NewModelView()
	fragment := ModelViewFragment{Node: node}
	if node.Kind == "heading_label" {
		// Depth only changes the number of '#'. Avoid needing ancestors.
		title := strings.TrimSpace(node.Title)
		if title == "" {
			title = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(node.Text), "#"))
		}
		line := strings.Repeat("#", min(max(headingDepth, 1), 6)) + " " + title + " [" + ModelViewLocator(node.Locator) + "]"
		if values := ModelViewValues(node); values != "" {
			line += " " + values
		}
		return len(line) + 1
	}
	line := view.line(fragment)
	if line == "" {
		return 0
	}
	return len(line) + 1
}

// ModelViewTokens converts delivered bytes to the estimate used in budgets.
func ModelViewTokens(bytes int) int {
	return (bytes + ModelViewBytesPerToken - 1) / ModelViewBytesPerToken
}

// ModelViewSourceLocations renders the native heading map. block and
// parent_block keep their names because final instructions refer to them;
// node IDs, ordinals and structure provenance remain program records.
func ModelViewSourceLocations(locations []map[string]any) string {
	lines := make([]string, 0, len(locations))
	for _, location := range locations {
		parts := []string{"block=" + stringOf(location["block"])}
		if parent := stringOf(location["parent_block"]); parent != "" {
			parts = append(parts, "parent_block="+parent)
		} else if top, _ := location["top_level"].(bool); top {
			parts = append(parts, "parent_block=none(top-level heading)")
		}
		if span, ok := location["span"].(map[string]any); ok {
			first, last := stringOf(span["first_block"]), stringOf(span["last_block"])
			if first != "" && last != "" && last != first {
				parts = append(parts, "range="+first+".."+last)
			}
		}
		if page := stringOf(location["page"]); page != "" && page != "0" {
			parts = append(parts, "page="+page)
		}
		parts = append(parts, "title="+strconv.Quote(stringOf(location["title"])))
		lines = append(lines, "- "+strings.Join(parts, " "))
	}
	return strings.Join(lines, "\n")
}

// ModelViewVisualEvidence keeps what the model must read from a slide image
// observation. Source, PDF and PNG hashes and model call IDs stay recorded in
// the tool output and execution evidence.
func ModelViewVisualEvidence(items []map[string]any) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		value := map[string]any{}
		for key, entry := range item {
			switch key {
			case "model_call_id", "pdf_sha256", "png_sha256", "source_sha256", "provenance":
				continue
			}
			value[key] = entry
		}
		raw, err := json.Marshal(value)
		if err == nil {
			lines = append(lines, "- "+string(raw))
		}
	}
	return strings.Join(lines, "\n")
}
