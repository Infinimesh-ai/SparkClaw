package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
)

type acquisitionFixture struct {
	args     map[string]any
	tool     string
	released map[string]any
	closed   bool
}

func (f *acquisitionFixture) Call(ctx context.Context, tool string, args map[string]any) (browserautomation.Result, error) {
	f.tool = tool
	f.args = args
	return browserautomation.Result{Tool: tool, Provider: "incumbent", SessionGeneration: 7, Output: map[string]any{"pages": []any{map[string]any{"page_id": "page_37", "selected": true}}, "page_id": "page_37", "text": "backend fixture"}}, nil
}
func (f *acquisitionFixture) Health(ctx context.Context, args map[string]any) (browserautomation.Result, error) {
	return browserautomation.Result{Output: map[string]any{"ok": true}}, nil
}
func (f *acquisitionFixture) ReadPage(ctx context.Context, url string, args map[string]any) (browserautomation.PageReadResult, error) {
	f.args = args
	return browserautomation.PageReadResult{URL: url, Text: "backend fixture", Rendered: true}, nil
}
func (f *acquisitionFixture) ReleaseSession(args map[string]any) error { f.released = args; return nil }
func (f *acquisitionFixture) Close() error                             { f.closed = true; return nil }
func TestAcquisitionProductionWrapperPreservesPublicArgumentsAndResults(t *testing.T) {
	fixture := &acquisitionFixture{}
	adapter := NewAcquisitionAdapter(fixture)
	defer adapter.Close()
	if adapter.(*AcquisitionAdapter).Capabilities().Role != BackendAcquisition {
		t.Fatal("wrong role")
	}
	args := map[string]any{"owner_id": "owner", "browser_profile_id": "existing", "url": "https://example.test", "presentation": "hidden", "custom_existing_metadata": "preserved"}
	result, err := adapter.Call(context.Background(), "browser.open", args)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.tool != "browser.open" || !reflect.DeepEqual(fixture.args, args) || result.Provider != "incumbent" || result.SessionGeneration != 7 {
		t.Fatalf("public protocol changed: %#v %#v", fixture.args, result)
	}
	read, err := adapter.ReadPage(context.Background(), "https://example.test", args)
	if err != nil || read.Text != "backend fixture" || !reflect.DeepEqual(fixture.args, args) {
		t.Fatal(read, err)
	}
	if err := adapter.(browserautomation.SessionReleaser).ReleaseSession(args); err != nil || !reflect.DeepEqual(fixture.released, args) {
		t.Fatal(err, fixture.released)
	}
}
func TestCommonBackendRoleUsesBoundActualPageAndEquivalentReadNavigate(t *testing.T) {
	fixture := &acquisitionFixture{}
	host := NewBackendHost(fixture)
	ctx := context.Background()
	binding, err := host.Acquire(ctx, Scope{Identity: testIdentity, ConversationID: "backend", TaskID: "task"})
	if err != nil {
		t.Fatal(err)
	}
	output, err := host.Dispatch(ctx, binding, "backend_navigate", "navigate", map[string]any{"url": "https://example.test"})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	json.Unmarshal(output, &result)
	if result["page_id"] != binding.PageID {
		t.Fatal(result)
	}
	if _, err := host.Dispatch(ctx, binding, "backend_read", "read", map[string]any{"max_chars": 1024}); err != nil {
		t.Fatal(err)
	}
	if fixture.args["page_id"] != "page_37" || fixture.args["reuse_active_page"] != true || fixture.args["owner_id"] != "owner" {
		t.Fatal(fixture.args)
	}
	forged := binding
	forged.PageGeneration++
	if _, err := host.Dispatch(ctx, forged, "backend_forged", "snapshot", map[string]any{}); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
	other := binding.Scope
	other.ConversationID = "other"
	if _, err := host.Acquire(ctx, other); !errors.Is(err, ErrFence) {
		t.Fatal("duplicate controller", err)
	}
	renewed, err := host.Renew(ctx, binding)
	if err != nil || !renewed.LeaseExpiresAt.After(binding.LeaseExpiresAt) {
		t.Fatal(renewed, err)
	}
	if err := host.Release(ctx, renewed); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Dispatch(ctx, binding, "backend_stale", "read", map[string]any{}); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
}
