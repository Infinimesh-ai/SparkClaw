package docindex

import (
	"reflect"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func TestSelectedVisualPagesKeepNativeScopeAndVersion(t *testing.T) {
	index := &Index{Nodes: []document.StructureNode{
		{ID: "slide1", Locator: document.StructureLocator{Page: 1}},
		{ID: "arrow", ParentID: "slide1", Locator: document.StructureLocator{Page: 1}},
		{ID: "slide2", Locator: document.StructureLocator{Page: 2}},
		{ID: "chart", ParentID: "slide2", Locator: document.StructureLocator{Page: 2}},
		{ID: "decoy", Locator: document.StructureLocator{Page: 3}},
	}}
	for _, request := range []RangeRequest{{PageStart: 2, PageEnd: 2}, {NodeIDs: []string{"slide2"}, IncludeDescendants: true}, {NodeIDs: []string{"chart"}}} {
		pages, err := index.SelectedPages(request)
		if err != nil || !reflect.DeepEqual(pages, []int{2}) {
			t.Fatal("page scope leaked", pages, err)
		}
	}
	for _, request := range []RangeRequest{{}, {All: true, PageStart: 2}, {Cursor: "cursor"}, {NodeIDs: []string{"invented"}}, {All: true, SourceSHA256: "stale"}} {
		if _, err := index.SelectedPages(request); err == nil {
			t.Fatal("invalid visual scope accepted", request)
		}
	}
}
