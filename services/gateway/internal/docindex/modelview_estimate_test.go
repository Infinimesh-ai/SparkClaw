package docindex

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func renderedRead(t *testing.T, index *Index, request RangeRequest) int {
	t.Helper()
	request.MaxBytes = MaxReadBytes
	read, err := index.ReadRange(request)
	if err != nil {
		t.Fatal(err)
	}
	view := document.NewModelView()
	for _, fragment := range read.Nodes {
		view.Add(document.ModelViewFragment{Node: fragment.StructureNode})
	}
	for _, dependency := range read.Dependencies {
		view.Add(document.ModelViewFragment{Node: dependency, Context: true})
	}
	return view.Len() + 1
}

// Navigation estimates and delivered evidence use one measure: the whole
// document estimate equals the model view of a whole-source read.
func TestModelViewEstimateMatchesDeliveredRead(t *testing.T) {
	units := []Unit{
		{Block: "document.p[1]", Text: "Preface."},
		{Block: "document.p[2]", Text: "Part", HeadingLevel: 1, StructureSource: "native_style"},
		{Block: "document.p[3]", Text: "First explanation."},
		{Block: "document.p[4]", Text: "Checks", HeadingLevel: 2, StructureSource: "native_style"},
		{Block: "document.p[5]", Text: "Verify authorization."},
		{Block: "document.p[6]", Text: "Detail", HeadingLevel: 3, StructureSource: "native_style"},
		{Block: "document.p[7]", Text: "Nested body."},
		{Block: "document.p[8]", Text: "Appendix", HeadingLevel: 1, StructureSource: "native_style"},
		{Block: "document.p[9]", Text: "Last explanation."},
	}
	_, _, index := treeFixture(t, "docx", units, SourceInfo{})
	var root, checks document.StructureNode
	for _, node := range index.Nodes {
		if node.Kind == "document" {
			root = node
		}
		if node.Kind == "section" && node.Title == "Checks" {
			checks = node
		}
	}
	estimate, ok := index.ModelViewBytes(root)
	if !ok {
		t.Fatal("missing whole-document estimate")
	}
	delivered := renderedRead(t, index, RangeRequest{All: true, IncludeDescendants: true})
	if estimate != delivered {
		t.Fatalf("whole-source estimate %d != delivered %d", estimate, delivered)
	}
	sectionEstimate, ok := index.ModelViewBytes(checks)
	if !ok || checks.ID == "" {
		t.Fatal("missing section estimate")
	}
	withContext := renderedRead(t, index, RangeRequest{NodeIDs: []string{checks.ID}, IncludeDescendants: true})
	ancestor := len("# Part [document.p[2]]") + 1
	if sectionEstimate+ancestor != withContext {
		t.Fatalf("section estimate %d + ancestor heading %d != delivered %d", sectionEstimate, ancestor, withContext)
	}
	if document.ModelViewTokens(estimate) <= 0 || strings.Contains(document.RenderModelView(nil), "d") {
		t.Fatal("token conversion or empty render broken")
	}
}
