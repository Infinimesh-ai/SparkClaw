package mcpaccess

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestProviderEmbedsOrderedMCPMediaContentWithoutLocalPaths(t *testing.T) {
	st := store.NewMemoryStore()
	root := t.TempDir()
	session := st.CreateSessionWithScope("MCP", app.DefaultOwnerID, root, "mcp", true)
	operation, ref := createProviderMediaOperation(t, st, session.ID, "operation-media")
	parts := []app.MessagePart{{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "Here are the files."}}
	fixtures := []struct {
		name        string
		kind        app.MessagePartKind
		contentType string
		content     string
	}{
		{name: "photo.png", kind: app.MessagePartImage, contentType: "image/png", content: "png bytes"},
		{name: "voice.wav", kind: app.MessagePartAudio, contentType: "audio/wav", content: "wav bytes"},
		{name: "report.pdf", kind: app.MessagePartFile, contentType: "application/pdf", content: "pdf bytes"},
	}
	for _, fixture := range fixtures {
		path := filepath.Join(root, fixture.name)
		if err := os.WriteFile(path, []byte(fixture.content), 0o644); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(fixture.content))
		object := app.ArtifactObject{
			ID: "object-" + fixture.name, Kind: "message_attachment", RunID: "run-media", SessionID: session.ID,
			Backend: "workspace", Key: fixture.name, URI: "workspace://" + fixture.name, Path: path,
			ContentType: fixture.contentType, Bytes: len(fixture.content), CreatedAt: time.Now().UTC(),
		}
		st.SaveArtifactObject(object)
		parts = append(parts, app.MessagePart{
			ID: "part-" + fixture.name, Kind: fixture.kind, Disposition: app.MessageDispositionAttachment,
			ArtifactID: object.ID, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: fixture.name, Provenance: "response_media_frozen"},
			Name: fixture.name, ContentType: fixture.contentType, Bytes: len(fixture.content), SHA256: hex.EncodeToString(digest[:]),
		})
	}
	endpoint := app.MessageEndpoint{ProviderKey: "mcp", BindingRef: operation.BindingID, RequesterDeviceID: ref.RequesterDeviceID, SessionID: session.ID}
	_, err := NewProvider(st).Deliver(t.Context(), endpoint, app.DeliveryRequest{
		ID: "delivery-media", ResultID: "result-media", RunID: "run-media", ResultStatus: app.WorkflowResultSucceeded,
		MCP: &ref, Content: app.MessageContent{Parts: parts},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := st.GetMCPOperation(operation.ID)
	var result CallToolResult
	if stored.State != app.MCPOperationSucceeded || json.Unmarshal(stored.Result, &result) != nil || len(result.Content) != 4 {
		t.Fatalf("MCP media result was not persisted as CallToolResult: operation=%#v result=%#v", stored, result)
	}
	if result.Content[0].Type != "text" || result.Content[0].Text != "Here are the files." ||
		result.Content[1].Type != "image" || result.Content[1].Data != base64.StdEncoding.EncodeToString([]byte("png bytes")) ||
		result.Content[2].Type != "audio" || result.Content[2].Data != base64.StdEncoding.EncodeToString([]byte("wav bytes")) ||
		result.Content[3].Type != "resource" || result.Content[3].Resource == nil || result.Content[3].Resource.Blob != base64.StdEncoding.EncodeToString([]byte("pdf bytes")) ||
		!strings.HasPrefix(result.Content[3].Resource.URI, "sparkclaw://mcp-operation/") {
		t.Fatalf("MCP media blocks do not match the standard ordered mapping: %#v", result.Content)
	}
	raw := string(stored.Result)
	if strings.Contains(raw, root) || strings.Contains(raw, "workspace://") || strings.Contains(raw, `"path"`) || strings.Contains(raw, `"resource"`) && strings.Contains(raw, filepath.Join(root, "report.pdf")) {
		t.Fatalf("MCP result leaked a local workspace path: %s", raw)
	}
}

func TestProviderMediaFailureDoesNotPersistPartialResult(t *testing.T) {
	st := store.NewMemoryStore()
	root := t.TempDir()
	session := st.CreateSessionWithScope("MCP", app.DefaultOwnerID, root, "mcp", true)
	operation, ref := createProviderMediaOperation(t, st, session.ID, "operation-atomic")
	path := filepath.Join(root, "changed.pdf")
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	object := app.ArtifactObject{
		ID: "object-changed", SessionID: session.ID, Backend: "workspace", Key: "changed.pdf", URI: "workspace://changed.pdf",
		Path: path, ContentType: "application/pdf", Bytes: len("original"), CreatedAt: time.Now().UTC(),
	}
	st.SaveArtifactObject(object)
	_, err := NewProvider(st).Deliver(t.Context(), app.MessageEndpoint{
		ProviderKey: "mcp", BindingRef: operation.BindingID, RequesterDeviceID: ref.RequesterDeviceID, SessionID: session.ID,
	}, app.DeliveryRequest{
		ID: "delivery-atomic", ResultStatus: app.WorkflowResultSucceeded, MCP: &ref,
		Content: app.MessageContent{Parts: []app.MessagePart{
			{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "first"},
			{ID: "file", Kind: app.MessagePartFile, Disposition: app.MessageDispositionAttachment, ArtifactID: object.ID,
				Resource: &app.ResourceRef{Kind: "workspace_file", Ref: "changed.pdf"}, Name: "changed.pdf", ContentType: "application/pdf", Bytes: len("original")},
		}},
	})
	if err == nil {
		t.Fatal("changed MCP media was delivered")
	}
	stored, _ := st.GetMCPOperation(operation.ID)
	if stored.State != app.MCPOperationRunning || len(stored.Result) != 0 {
		t.Fatalf("failed multipart delivery persisted a partial result: %#v", stored)
	}
}

func TestProviderRawBinaryLimitReservesEncodedEnvelopeHeadroom(t *testing.T) {
	provider := NewProvider(store.NewMemoryStore())
	capabilities := provider.Capabilities()
	if capabilities.MaxTotalBytes != MaxResultRawBinaryBytes || capabilities.MaxParts != 9 ||
		capabilities.MaxBytesByKind[app.MessagePartFile] != MaxResultRawBinaryBytes {
		t.Fatalf("MCP provider limits do not match the encoded-envelope budget: %#v", capabilities)
	}
	encoded := base64.StdEncoding.EncodedLen(MaxResultRawBinaryBytes)
	if encoded >= MaxResultEnvelopeBytes || MaxResultEnvelopeBytes-encoded < 128<<10 {
		t.Fatalf("raw binary limit leaves insufficient envelope headroom: raw=%d encoded=%d envelope=%d", MaxResultRawBinaryBytes, encoded, MaxResultEnvelopeBytes)
	}
	content := func(size int) app.MessageContent {
		return app.MessageContent{Parts: []app.MessagePart{{
			ID: "file", Kind: app.MessagePartFile, Disposition: app.MessageDispositionAttachment,
			ArtifactID: "object", Name: "result.bin", Bytes: size,
		}}}
	}
	if err := delivery.ValidateCapabilities(capabilities, content(MaxResultRawBinaryBytes)); err != nil {
		t.Fatalf("largest qualified raw MCP result was rejected: %v", err)
	}
	if err := delivery.ValidateCapabilities(capabilities, content(MaxResultRawBinaryBytes+1)); err == nil {
		t.Fatal("first oversized raw MCP result was accepted")
	}
}

func createProviderMediaOperation(t *testing.T, st *store.MemoryStore, sessionID, id string) (app.MCPOperation, app.MCPInvocationRef) {
	t.Helper()
	ref := app.MCPInvocationRef{InvocationID: "inv-" + id, OperationID: id, BindingRef: "binding-media", BindingRevision: 1, RequesterDeviceID: "device-media"}
	operation, created, err := st.CreateMCPOperation(app.MCPOperation{
		ID: id, BindingID: ref.BindingRef, IdempotencyKey: id, Fingerprint: id,
		Invocation: app.MCPInvocationContext{ID: ref.InvocationID, OperationID: id, BindingRef: ref.BindingRef, RequesterDeviceID: ref.RequesterDeviceID, RunID: "run-media"},
		State:      app.MCPOperationRunning,
	})
	if err != nil || !created {
		t.Fatalf("create MCP operation: created=%t err=%v", created, err)
	}
	return operation, ref
}
