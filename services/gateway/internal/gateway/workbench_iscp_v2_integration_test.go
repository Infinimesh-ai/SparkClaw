package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpobjects"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// This fixture runs the production Gateway domain adapters and default file
// store through the same chain the link layer's domain.call uses.
type v2EncryptedFixture struct {
	handler  wb.Handler
	server   *Server
	ctx      context.Context
	revision uint64
	configs  []wb.Config
}

func newV2EncryptedFixture(t *testing.T, executor execution.Executor) *v2EncryptedFixture {
	t.Helper()
	server, _, local, _ := workbenchISCPFixture(t, executor)
	return startV2EncryptedFixture(t, server, local, nil, nil)
}
func startV2EncryptedFixture(t *testing.T, server *Server, local wb.Config, extraScopes, extraQualified []string, grantTTL ...time.Duration) *v2EncryptedFixture {
	t.Helper()
	// The business domains now run behind the link layer; the fixture calls
	// the same domain chain the link bridge uses, without a transport.
	_ = extraScopes
	_ = extraQualified
	_ = grantTTL
	return &v2EncryptedFixture{server: server, handler: linkDomainTestChain(t, server, local), ctx: t.Context(), revision: 1, configs: []wb.Config{local}}
}
func (f *v2EncryptedFixture) call(t *testing.T, q wb.Request) wb.Response {
	t.Helper()
	return f.handler(f.ctx, q)
}
func v2Request(operation string, body []byte) wb.Request {
	r := workbenchISCPRequest(operation, body)
	r.Profile = wb.ProfileV2
	r.OperationID = r.ID
	return r
}
func (f *v2EncryptedFixture) jsonCall(t *testing.T, operation string, value any) wb.Response {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return f.call(t, v2Request(operation, raw))
}
func requireV2Status(t *testing.T, r wb.Response, status int) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("status=%d want=%d error=%s code=%s body=%s", r.Status, status, r.Error, r.Code, r.Body)
	}
}
func (f *v2EncryptedFixture) bind(t *testing.T) {
	t.Helper()
	r := f.jsonCall(t, wb.OperationBind, map[string]any{"schema_version": 1, "installation_id": iscpTestInstallation})
	requireV2Status(t, r, 200)
}
func (f *v2EncryptedFixture) upload(t *testing.T, purpose, name, mime string, raw []byte) wb.ObjectReference {
	t.Helper()
	id := fmt.Sprintf("eeeeeeee-eeee-4eee-8eee-%012x", iscpInvocationSequence.Add(1))
	open := iscpobjects.OpenRequest{TransferID: id, Purpose: purpose, Name: name, MediaType: mime, Size: int64(len(raw)), SHA256: execution.Digest(raw)}
	requireV2Status(t, f.jsonCall(t, wb.OperationTransferOpen, open), 200)
	for offset := 0; offset < len(raw); offset += iscpobjects.ChunkBytes {
		chunk := raw[offset:min(offset+iscpobjects.ChunkBytes, len(raw))]
		q := iscpobjects.ChunkRequest{TransferID: id, Index: offset / iscpobjects.ChunkBytes, Offset: int64(offset), SHA256: execution.Digest(chunk), DataBase64: base64.StdEncoding.EncodeToString(chunk)}
		requireV2Status(t, f.jsonCall(t, wb.OperationTransferChunk, q), 200)
	}
	response := f.jsonCall(t, wb.OperationTransferCommit, map[string]string{"transfer_id": id})
	requireV2Status(t, response, 200)
	var committed iscpobjects.Checkpoint
	if json.Unmarshal(response.Body, &committed) != nil || committed.State != "committed" || committed.AcknowledgedBytes != int64(len(raw)) {
		t.Fatalf("invalid durable checkpoint %s", response.Body)
	}
	return committed.Object
}
func (f *v2EncryptedFixture) download(t *testing.T, ref wb.ObjectReference) []byte {
	t.Helper()
	var raw []byte
	for offset := int64(0); offset < ref.Size; {
		response := f.jsonCall(t, wb.OperationObjectRead, map[string]any{"object_id": ref.ObjectID, "version": ref.Version, "offset": offset, "length": iscpobjects.ChunkBytes})
		requireV2Status(t, response, 200)
		var part iscpobjects.ReadResult
		if err := json.Unmarshal(response.Body, &part); err != nil {
			t.Fatal(err)
		}
		chunk, err := base64.StdEncoding.DecodeString(part.DataBase64)
		if err != nil || part.Offset != offset || execution.Digest(chunk) != part.SHA256 || len(chunk) == 0 {
			t.Fatal("object chunk corrupt")
		}
		raw = append(raw, chunk...)
		offset += int64(len(chunk))
	}
	if execution.Digest(raw) != ref.SHA256 {
		t.Fatal("whole downloaded object digest mismatch")
	}
	return raw
}
