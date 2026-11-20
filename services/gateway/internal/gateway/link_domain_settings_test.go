package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/linkclient"
)

type settingsValue struct {
	Revision string          `json:"revision"`
	Value    json.RawMessage `json:"value"`
}

// linkDomainReply calls a method answered with the domain result and
// decodes its body.
func linkDomainReply(t *testing.T, ctx context.Context, c *linkclient.Client, method string, args any, key string, body any) int {
	t.Helper()
	var result linkDomainResult
	if err := linkDomainCall(ctx, c, method, args, key, &result); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	if body != nil && len(result.Body) > 0 {
		if err := json.Unmarshal(result.Body, body); err != nil {
			t.Fatalf("%s body %s: %v", method, result.Body, err)
		}
	}
	return result.Status
}

func TestLinkSettingsOwnerRevisionReplayAndConflict(t *testing.T) {
	b, desk, ctx := linkDomainTestDevice(t)
	var read settingsValue
	if status := linkDomainReply(t, ctx, desk, "settings.owner.get", map[string]any{}, "", &read); status != 200 || read.Revision == "" {
		t.Fatalf("read %d %+v", status, read)
	}
	key := newLinkUUID()
	patch := map[string]any{"display_name": "Link owner", "preferences": map[string]string{"language": "en"}, "expected_revision": read.Revision}
	var changed settingsValue
	if status := linkDomainReply(t, ctx, desk, "settings.owner.patch", patch, key, &changed); status != 200 || changed.Revision == read.Revision || !bytes.Contains(changed.Value, []byte("Link owner")) {
		t.Fatalf("patch %d %+v", status, changed)
	}
	// The same key answers the original change without running it again.
	var replayed settingsValue
	if status := linkDomainReply(t, ctx, desk, "settings.owner.patch", patch, key, &replayed); status != 200 || replayed.Revision != changed.Revision {
		t.Fatalf("replay %d %+v", status, replayed)
	}
	// A change made against a revision the owner has moved past is refused.
	var stale map[string]any
	if status := linkDomainReply(t, ctx, desk, "settings.owner.patch", map[string]any{"display_name": "stale", "expected_revision": read.Revision}, newLinkUUID(), &stale); status != 409 || stale["code"] != "revision_conflict" {
		t.Fatalf("stale %d %+v", status, stale)
	}
	profile, found, err := b.server.store.GetOwnerProfileByID(ctx, read.ownerID(t))
	if err != nil || !found || profile.DisplayName != "Link owner" {
		t.Fatalf("stored %+v %v %v", profile, found, err)
	}
	// The same key with other content is a conflict, not a second change.
	other := map[string]any{"display_name": "Other", "expected_revision": changed.Revision}
	var result linkDomainResult
	if err := linkDomainCall(ctx, desk, "settings.owner.patch", other, key, &result); !linkErrorCode(err, contract.CodeConflict) {
		t.Fatalf("reused key %v %+v", err, result)
	}
}

func (v settingsValue) ownerID(t *testing.T) string {
	var profile struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(v.Value, &profile); err != nil || profile.ID == "" {
		t.Fatalf("owner %s", v.Value)
	}
	return profile.ID
}

func linkErrorCode(err error, code string) bool {
	var cerr *contract.Error
	return errors.As(err, &cerr) && cerr.Code == code
}

func TestLinkSettingsMutationsNeedAnIdempotencyKey(t *testing.T) {
	_, desk, ctx := linkDomainTestDevice(t)
	for _, method := range []string{"settings.owner.patch", "settings.connectors.patch", "settings.bindings.start", "settings.mcp.tickets.issue", "settings.browser.save"} {
		if err := linkDomainCall(ctx, desk, method, map[string]any{}, "", nil); !linkErrorCode(err, contract.CodeInvalidRequest) {
			t.Fatalf("%s without a key: %v", method, err)
		}
	}
}

func TestLinkSettingsRefusesRecordIDsThatAreNotIDs(t *testing.T) {
	_, desk, ctx := linkDomainTestDevice(t)
	for _, call := range []struct {
		method string
		args   map[string]any
		key    bool
	}{
		{"settings.bindings.get", map[string]any{"binding_id": "../x"}, false},
		{"settings.bindings.login", map[string]any{"binding_id": ""}, false},
		{"settings.bindings.revoke", map[string]any{"binding_id": "a/b"}, true},
		{"settings.mcp.tickets.revoke", map[string]any{"ticket_id": "a b"}, true},
		{"settings.mcp.bindings.delete", map[string]any{"binding_id": strings.Repeat("x", 300)}, true},
		{"settings.bindings.list", map[string]any{"channel": "email"}, false},
		{"settings.bindings.start", map[string]any{"channel": "weixin", "bot_token": "secret"}, true},
		{"settings.mcp.tickets.issue", map[string]any{"ttl_seconds": 5}, true},
		{"settings.mcp.pairing.start", map[string]any{"display_name": " "}, true},
		{"settings.browser.save", map[string]any{"token": ""}, true},
	} {
		key := ""
		if call.key {
			key = newLinkUUID()
		}
		var body map[string]any
		if status := linkDomainReply(t, ctx, desk, call.method, call.args, key, &body); status != 400 || body["code"] != "invalid_input" {
			t.Fatalf("%s %v: %d %+v", call.method, call.args, status, body)
		}
	}
}

func TestLinkSettingsReadsAnswerTheirScreens(t *testing.T) {
	_, desk, ctx := linkDomainTestDevice(t)
	// This backend has no connector control: the screen is told so.
	var connectors map[string]any
	if status := linkDomainReply(t, ctx, desk, "settings.connectors.list", map[string]any{}, "", &connectors); status != 503 || connectors["code"] != "connector_unavailable" {
		t.Fatalf("connectors %d %+v", status, connectors)
	}
	var bindings map[string]any
	if status := linkDomainReply(t, ctx, desk, "settings.bindings.list", map[string]any{"channel": "weixin"}, "", &bindings); status != 200 || bindings["bindings"] == nil {
		t.Fatalf("bindings %d %+v", status, bindings)
	}
	var missing map[string]any
	if status := linkDomainReply(t, ctx, desk, "settings.bindings.get", map[string]any{"binding_id": "binding-unknown"}, "", &missing); status != 404 {
		t.Fatalf("missing binding %d %+v", status, missing)
	}
	var tickets map[string]any
	if status := linkDomainReply(t, ctx, desk, "settings.mcp.tickets.list", map[string]any{}, "", &tickets); status != 200 || tickets["tickets"] == nil {
		t.Fatalf("tickets %d %+v", status, tickets)
	}
	var pairing map[string]any
	if status := linkDomainReply(t, ctx, desk, "settings.mcp.pairing.status", map[string]any{}, "", &pairing); status != 200 || pairing["state"] == nil {
		t.Fatalf("pairing %d %+v", status, pairing)
	}
	var browser map[string]any
	if status := linkDomainReply(t, ctx, desk, "settings.browser.get", map[string]any{}, "", &browser); status != 200 && status != 503 {
		t.Fatalf("browser %d %+v", status, browser)
	}
}

func TestLinkDomainArgsObject(t *testing.T) {
	b, desk, ctx := linkDomainTestDevice(t)
	var read settingsValue
	linkDomainReply(t, ctx, desk, "settings.owner.get", map[string]any{}, "", &read)
	args, _ := json.Marshal(map[string]any{"display_name": "From object", "expected_revision": read.Revision})
	device := b.linkDeviceID(t)
	meta, err := b.server.link.domainObj.put(device, "domain_args", "args.json", "application/json", args)
	if err != nil {
		t.Fatal(err)
	}
	var out contract.DomainCallResult
	if err := desk.Request(ctx, contract.OpDomainCall, contract.DomainCallParams{Domain: "settings", Method: "settings.owner.patch", ArgsObject: meta.ID, IdempotencyKey: newLinkUUID()}, &out); err != nil {
		t.Fatal(err)
	}
	var result linkDomainResult
	if json.Unmarshal(out.Result, &result) != nil || result.Status != 200 || !bytes.Contains(result.Body, []byte("From object")) {
		t.Fatalf("args object %s", out.Result)
	}
	if err := desk.Request(ctx, contract.OpDomainCall, contract.DomainCallParams{Domain: "settings", Method: "settings.owner.get", ArgsObject: newLinkUUID()}, &out); !linkErrorCode(err, contract.CodeNotFound) {
		t.Fatalf("missing args object %v", err)
	}
}

// linkDeviceID is the one device in the backend's directory.
func (b *linkTestBackend) linkDeviceID(t *testing.T) string {
	t.Helper()
	var directory consoleapi.Directory
	raw, err := os.ReadFile(filepath.Join(b.stateDir, link.DirectoryFile))
	if err != nil || json.Unmarshal(raw, &directory) != nil || len(directory.Devices) != 1 {
		t.Fatalf("directory %v", err)
	}
	return directory.Devices[0].DeviceID
}
