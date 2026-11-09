package browserautomation

import (
	"reflect"
	"testing"
)

func TestClientResultPresentationPreservesExistingTaskPage(t *testing.T) {
	controller := newFakePlaywrightController()
	adapter := NewClientPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller)
	t.Cleanup(func() { _ = adapter.Close() })
	const target = "https://example.com/form"
	opened, err := adapter.Call(t.Context(), "browser.open", map[string]any{"url": target})
	if err != nil {
		t.Fatal(err)
	}
	pageID := selectedPageID(mapValue(opened.Output))
	session := controller.lastSession()
	offset := len(session.calls)
	shown, err := adapter.Call(t.Context(), "browser.open", map[string]any{
		"url": target, "page_id": pageID, "presentation": "visible", "reason": "workflow_result_presentation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if selectedPageID(mapValue(shown.Output)) != pageID || len(session.pages) != 1 {
		t.Fatalf("presenting the result replaced the task page: %#v", shown.Output)
	}
	var operations []string
	for _, call := range session.calls[offset:] {
		operations = append(operations, call.Operation)
	}
	if !reflect.DeepEqual(operations, []string{"page.info"}) {
		t.Fatalf("presentation must only observe/select the original page, got %v", operations)
	}
}

func TestClientResultPresentationRefusesChangedPage(t *testing.T) {
	for _, target := range []string{"https://example.com/other", "https://other.test/form", "https://example.com/Form", "https://example.com/form/", "https://example.com/form?changed=1", "https://example.com/form#/changed"} {
		t.Run(target, func(t *testing.T) {
			controller := newFakePlaywrightController()
			adapter := NewClientPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller)
			t.Cleanup(func() { _ = adapter.Close() })
			if _, err := adapter.Call(t.Context(), "browser.open", map[string]any{"url": "https://example.com/form"}); err != nil {
				t.Fatal(err)
			}
			session := controller.lastSession()
			offset := len(session.calls)
			if _, err := adapter.Call(t.Context(), "browser.open", map[string]any{"url": target, "reason": "workflow_result_presentation"}); err == nil {
				t.Fatal("changed target must not silently create a replacement page")
			}
			if len(session.pages) != 1 || len(session.calls[offset:]) != 1 || session.lastCall().Operation != "page.info" {
				t.Fatal("refused presentation navigated or created a page")
			}
		})
	}
}

func TestClientResultPresentationDoesNotRecreateMissingPage(t *testing.T) {
	controller := newFakePlaywrightController()
	adapter := NewClientPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller)
	t.Cleanup(func() { _ = adapter.Close() })
	if _, err := adapter.Call(t.Context(), "browser.open", map[string]any{"url": "https://example.com/form", "page_id": "page_99", "reason": "workflow_result_presentation"}); err == nil {
		t.Fatal("absent result page must be refused")
	}
	session := controller.lastSession()
	if len(session.pages) != 1 || len(session.calls) != 1 || session.lastCall().Operation != "page.info" {
		t.Fatal("missing result presentation created or navigated a page")
	}
}

func TestClientResultPresentationUsesCurrentPageAndNormalizesOriginOnly(t *testing.T) {
	controller := newFakePlaywrightController()
	adapter := NewClientPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller)
	t.Cleanup(func() { _ = adapter.Close() })
	if _, err := adapter.Call(t.Context(), "browser.open", map[string]any{"url": "https://example.com/"}); err != nil {
		t.Fatal(err)
	}
	shown, err := adapter.Call(t.Context(), "browser.open", map[string]any{"url": "https://EXAMPLE.com", "reason": "workflow_result_presentation"})
	if err != nil {
		t.Fatal(err)
	}
	if selectedPageID(mapValue(shown.Output)) != "page_1" || len(controller.lastSession().pages) != 1 {
		t.Fatal("current origin page was not preserved")
	}
}

func TestExplicitClientOpenAndBackendPresentationRetainNewTabSemantics(t *testing.T) {
	for _, client := range []bool{true, false} {
		controller := newFakePlaywrightController()
		adapter := NewPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller).(*PlaywrightExtensionAdapter)
		adapter.clientEmbedded = client
		t.Cleanup(func() { _ = adapter.Close() })
		args := map[string]any{"url": "https://example.com/form"}
		if _, err := adapter.Call(t.Context(), "browser.open", args); err != nil {
			t.Fatal(err)
		}
		if !client {
			args["reason"] = "workflow_result_presentation"
		}
		if _, err := adapter.Call(t.Context(), "browser.open", args); err != nil {
			t.Fatal(err)
		}
		if len(controller.lastSession().pages) != 2 {
			t.Fatal("explicit open/backend semantics changed")
		}
	}
}
