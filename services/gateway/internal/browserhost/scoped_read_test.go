package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestScopedReadReusesOnlyTheBoundLivePage(t *testing.T) {
	for _, change := range []string{"none", "different_target", "different_path_case", "different_query", "page_changed", "ended_session", "released"} {
		t.Run(change, func(t *testing.T) {
			const target = "https://example.test/Article?q=1"
			desktop := newFakeDesktop("11111111-1111-4111-8111-111111111111")
			desktop.answer = func(command Command) (json.RawMessage, error) {
				current := target
				if change == "page_changed" && command.Operation == "read" {
					current = "https://example.test/other"
				}
				output, _ := json.Marshal(map[string]any{"page_id": command.Binding.PageID, "url": current, "final_url": current, "title": "Article", "text": "Read the existing document."})
				return reply(command, "completed", string(output)), nil
			}
			broker, _ := reverseBroker(t, desktop)
			adapter := NewScopedAdapter(broker, reverseScope)
			if _, err := adapter.Call(t.Context(), "browser.open", map[string]any{"url": target, "presentation": "hidden"}); err != nil {
				t.Fatal(err)
			}
			requested := target
			switch change {
			case "different_target":
				requested = "https://other.test/Article?q=1"
			case "different_path_case":
				requested = "https://example.test/article?q=1"
			case "different_query":
				requested = "https://example.test/Article?q=2"
			case "ended_session":
				desktop.end()
			case "released":
				if err := adapter.ReleaseSession(nil); err != nil {
					t.Fatal(err)
				}
			}
			read, err := adapter.ReadPage(t.Context(), requested, map[string]any{"reuse_active_page": true, "presentation": "hidden"})
			if change == "none" {
				if err != nil || read.Text != "Read the existing document." || read.ReadSource != "client_embedded_webcontents" {
					t.Fatalf("read=%+v err=%v", read, err)
				}
			} else if !errors.Is(err, ErrFence) || read.Text != "" {
				t.Fatalf("changed binding returned content: read=%+v err=%v", read, err)
			}
			if change == "page_changed" {
				tabs, err := adapter.Call(t.Context(), "browser.list_tabs", nil)
				if err != nil || len(tabs.Pages) != 1 || tabs.Pages[0].(map[string]any)["url"] != "https://example.test/other" {
					t.Fatalf("stale page URL retained: %+v %v", tabs, err)
				}
				if _, err := adapter.ReadPage(t.Context(), target, map[string]any{"reuse_active_page": true}); !errors.Is(err, ErrFence) {
					t.Fatal("changed document was reused", err)
				}
			}
			var operations []string
			for _, command := range desktop.commands {
				operations = append(operations, command.Operation)
			}
			want := []string{"acquire", "navigate"}
			if change == "none" || change == "page_changed" {
				want = append(want, "read")
			}
			if change == "released" {
				want = append(want, "release")
			}
			if !reflect.DeepEqual(operations, want) {
				t.Fatalf("operations=%v want=%v", operations, want)
			}
		})
	}
}

func TestScopedReadReuseDoesNotAcquireAnAbsentPage(t *testing.T) {
	desktop := newFakeDesktop("11111111-1111-4111-8111-111111111111")
	broker, _ := reverseBroker(t, desktop)
	adapter := NewScopedAdapter(broker, reverseScope)
	if _, err := adapter.ReadPage(context.Background(), "https://example.test/", map[string]any{"reuse_active_page": true}); !errors.Is(err, ErrFence) || desktop.count() != 0 {
		t.Fatalf("reuse acquired or navigated an absent page: %v commands=%d", err, desktop.count())
	}
}
