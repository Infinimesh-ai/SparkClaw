package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscplocalissuer"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpobjects"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
)

// This fixture uses a real pinned issuer HTTP server and the production Gateway
// adapters/default file store. Only Relay queue delivery is in process; this is
// not native-client, provider or hosted-Relay qualification.
type v2EncryptedFixture struct {
	server   *Server
	client   *wb.Endpoint
	network  *workbenchEncryptedRelayNetwork
	ctx      context.Context
	revision uint64
	configs  []wb.Config
	seed     trust.Grant
}

func newV2EncryptedFixture(t *testing.T, executor execution.Executor) *v2EncryptedFixture {
	t.Helper()
	server, _, local, _ := workbenchISCPFixture(t, executor)
	return startV2EncryptedFixture(t, server, local, nil, nil)
}
func startV2EncryptedFixture(t *testing.T, server *Server, local wb.Config, extraScopes, extraQualified []string, grantTTL ...time.Duration) *v2EncryptedFixture {
	t.Helper()
	clientCfg, serverCfg := workbenchEncryptedConfigurations(t, local.Binding)
	issuerDir := filepath.Join(t.TempDir(), "issuer")
	_, err := iscplocalissuer.Initialize(issuerDir, filepath.Join(clientCfg.IdentityDirectory, iscpbridge.IdentityFileName), filepath.Join(serverCfg.IdentityDirectory, iscpbridge.IdentityFileName), "isolated-relay")
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := iscplocalissuer.Load(filepath.Join(issuerDir, "issuer.json"))
	if err != nil {
		t.Fatal(err)
	}
	ttl := 30 * time.Minute
	if len(grantTTL) != 0 {
		ttl = grantTTL[0]
	}
	grant, err := issuer.Sign(ttl)
	if err != nil {
		t.Fatal(err)
	}
	issuerIdentity, err := os.ReadFile(filepath.Join(issuerDir, "issuer.identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	grantRaw, _ := json.Marshal(grant)
	for _, cfg := range []wb.Config{clientCfg, serverCfg} {
		if os.WriteFile(cfg.IssuerIdentityFile, issuerIdentity, 0600) != nil || os.WriteFile(cfg.GrantFile, grantRaw, 0600) != nil {
			t.Fatal("write issuer fixture")
		}
	}
	scopes := []string{"workbench.text", "workbench.control", "settings.read", "settings.write", "objects.read", "objects.write", "notifications.read", "approvals.read", "approvals.decide"}
	scopes = append(scopes, extraScopes...)
	slices.Sort(scopes)
	if err = issuer.AuthorizePermanentScopes(clientCfg.GrantFile, scopes); err != nil {
		t.Fatal(err)
	}
	issuerHTTP := httptest.NewServer(issuer.Handler())
	t.Cleanup(issuerHTTP.Close)
	qualified := []string{wb.OperationSettingsOwnerGet, wb.OperationSettingsOwnerPatch, wb.OperationSettingsCredentialsAdd, wb.OperationOperationsReceipt, wb.OperationTransferOpen, wb.OperationTransferStatus, wb.OperationTransferChunk, wb.OperationTransferCommit, wb.OperationTransferAbort, wb.OperationObjectDescribe, wb.OperationObjectRead, wb.OperationObjectRelease, wb.OperationExecutionInputPut, wb.OperationExecutionFileGet, wb.OperationApprovalsList, wb.OperationApprovalsGet, wb.OperationApprovalsDecide, wb.OperationExecutionApproval}
	qualified = append(qualified, extraQualified...)
	for _, cfg := range []*wb.Config{&clientCfg, &serverCfg} {
		cfg.ApplicationProfiles = []string{wb.ProfileV2, wb.Profile}
		cfg.GrantRenewal = &wb.GrantRenewalConfig{URL: issuerHTTP.URL, AuthorizationLifetime: "until_revoked", PendingFile: filepath.Join(cfg.IdentityDirectory, "pending.json"), PollIntervalSeconds: 1}
		cfg.QualifiedCapabilities = qualified
	}
	handler, err := server.NewWorkbenchISCPHandler(serverCfg)
	if err != nil {
		t.Fatal(err)
	}
	network := &workbenchEncryptedRelayNetwork{inbox: map[string]chan json.RawMessage{"iscp-desktop-device": make(chan json.RawMessage, 128), "iscp-backend-device": make(chan json.RawMessage, 128)}}
	client, err := wb.NewEndpointWithRelay(clientCfg, &workbenchEncryptedRelay{network, "iscp-desktop-device"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	responder, err := wb.NewEndpointWithRelay(serverCfg, &workbenchEncryptedRelay{network, "iscp-backend-device"}, handler, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	done := make(chan error, 2)
	go func() { done <- client.Run(ctx) }()
	go func() { done <- responder.Run(ctx) }()
	t.Cleanup(func() { cancel(); _ = client.Close(); _ = responder.Close(); <-done; <-done })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cap, ok := client.Negotiated(); ok && cap.Profile == wb.ProfileV2 {
			return &v2EncryptedFixture{server: server, client: client, network: network, ctx: ctx, revision: grant.RevocationEpoch, configs: []wb.Config{clientCfg, serverCfg}, seed: grant}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("v2 signed issuer negotiation timed out")
	return nil
}
func (f *v2EncryptedFixture) call(t *testing.T, q wb.Request) wb.Response {
	t.Helper()
	r, err := f.client.Call(f.ctx, q)
	if err != nil {
		t.Fatalf("%s transport: %v", q.Operation, err)
	}
	return r
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
func TestWorkbenchISCPV2SignedPermissionsCASAndDurableReceipt(t *testing.T) {
	f := newV2EncryptedFixture(t, nil)
	requireV2Status(t, f.call(t, v2Request(wb.OperationSettingsOwnerGet, nil)), 403)
	f.bind(t)
	read := f.call(t, v2Request(wb.OperationSettingsOwnerGet, nil))
	requireV2Status(t, read, 200)
	var original struct {
		Revision string `json:"revision"`
	}
	if json.Unmarshal(read.Body, &original) != nil || original.Revision == "" {
		t.Fatal("missing revision")
	}
	change := v2Request(wb.OperationSettingsOwnerPatch, []byte(`{"display_name":"encrypted owner setting","preferences":{"language":"en"}}`))
	change.ExpectedRevision = original.Revision
	changed := f.call(t, change)
	requireV2Status(t, changed, 200)
	change.ID = v2Request(wb.OperationIdentity, nil).ID
	replay := f.call(t, change)
	requireV2Status(t, replay, 200)
	if !bytes.Equal(changed.Body, replay.Body) {
		t.Fatal("same mutation did not replay exact receipt")
	}
	receipt := v2Request(wb.OperationOperationsReceipt, nil)
	receipt.Params = map[string]string{"operation_id": change.OperationID}
	requireV2Status(t, f.call(t, receipt), 200)
	stale := v2Request(wb.OperationSettingsOwnerPatch, []byte(`{"display_name":"stale"}`))
	stale.ExpectedRevision = original.Revision
	requireV2Status(t, f.call(t, stale), 409)
	badInstall := v2Request(wb.OperationSettingsOwnerGet, nil)
	badInstall.InstallationID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	requireV2Status(t, f.call(t, badInstall), 403)
	requireV2Status(t, f.call(t, v2Request(wb.OperationNotificationsList, nil)), 501)
	requireV2Status(t, f.call(t, v2Request(wb.OperationToolsList, nil)), 403)
	credentials := v2Request(wb.OperationSettingsCredentialsAdd, []byte(`{}`))
	credentials.Params = map[string]string{"integration_id": "test"}
	requireV2Status(t, f.call(t, credentials), 503)
	f.network.mu.Lock()
	for _, wire := range f.network.envelopes {
		if bytes.Contains(wire, []byte("encrypted owner setting")) || bytes.Contains(wire, []byte("iscp-owner")) {
			t.Error("Relay sees plaintext setting")
		}
	}
	f.network.mu.Unlock()
}
func TestWorkbenchISCPV2ChunkedOriginalExecutionAndLargeResult(t *testing.T) {
	var calls atomic.Int32
	input := bytes.Repeat([]byte("input-verified-"), 1300)
	resultFile := bytes.Repeat([]byte("output-verified-"), 1500)
	f := newV2EncryptedFixture(t, func(ctx context.Context, e execution.Envelope, files map[string][]byte) (execution.Output, error) {
		calls.Add(1)
		if !bytes.Equal(files["99999999-9999-4999-8999-999999999999"], input) {
			return execution.Output{}, fmt.Errorf("input file bytes changed")
		}
		return execution.Output{Content: strings.Repeat("large verified result ", 4000), Files: map[string][]byte{"evidence.bin": resultFile}}, nil
	})
	f.bind(t)
	inputRef := f.upload(t, "execution_input", "input.bin", "application/octet-stream", input)
	stage := v2Request(wb.OperationExecutionInputPut, nil)
	stage.Object = &inputRef
	stage.InputDigest = inputRef.SHA256
	stage.Params = map[string]string{"request_id": iscpTestRequest, "file_id": "99999999-9999-4999-8999-999999999999"}
	requireV2Status(t, f.call(t, stage), 200)
	envelope := workbenchISCPEnvelope()
	envelope.Messages[0].Content = strings.Repeat("original context whitespace ", 2000)
	envelope.Messages = append([]execution.Message{{Role: "user", Content: strings.Repeat("earlier context ", 2000)}}, envelope.Messages...)
	envelope.InputFiles = []execution.File{{ID: "99999999-9999-4999-8999-999999999999", Name: "input.bin", Size: len(input), SHA256: inputRef.SHA256}}
	original, _ := json.MarshalIndent(envelope, "", "  ")
	if len(original) <= wb.MaxBodyBytes {
		t.Fatal("fixture is not a large original request")
	}
	bodyRef := f.upload(t, "execution_request", "request.json", "application/json", original)
	submit := v2Request(wb.OperationSubmit, nil)
	submit.InputDigest = execution.Digest(original)
	submit.Object = &bodyRef
	f.network.dropNextResult.Store(true)
	lost, stopLost := context.WithTimeout(f.ctx, 150*time.Millisecond)
	_, lostErr := f.client.Call(lost, submit)
	stopLost()
	if lostErr == nil {
		t.Fatal("injected submit result was not lost")
	}
	f.server.executions.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if caps, ok := f.client.Negotiated(); ok && caps.Profile == wb.ProfileV2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := f.client.Negotiated(); !ok {
		t.Fatal("v2 did not renegotiate after lost acceptance")
	}

	lookup := f.call(t, v2Request(wb.OperationLookup, nil))
	requireV2Status(t, lookup, 200)
	if lookup.Object == nil {
		t.Fatalf("large result lacks object: %s", lookup.Body)
	}
	stored := f.download(t, *lookup.Object)
	var status execution.Status
	if json.Unmarshal(stored, &status) != nil || status.State != "completed" || status.InputDigest != execution.Digest(original) || status.Result == nil {
		t.Fatalf("original input identity lost: %s", stored)
	}
	again := f.call(t, v2Request(wb.OperationLookup, nil))
	requireV2Status(t, again, 200)
	if again.Object == nil || *again.Object != *lookup.Object {
		t.Fatal("repeated lookup duplicated publication")
	}
	var payload execution.Payload
	if json.Unmarshal([]byte(status.Result.Payload), &payload) != nil || len(payload.Files) != 1 {
		t.Fatal("missing actual execution artifact")
	}
	fileRequest := v2Request(wb.OperationExecutionFileGet, nil)
	fileRequest.Params = map[string]string{"request_id": iscpTestRequest, "file_id": payload.Files[0].ID}
	fileResponse := f.call(t, fileRequest)
	requireV2Status(t, fileResponse, 200)
	if fileResponse.Object == nil || !bytes.Equal(f.download(t, *fileResponse.Object), resultFile) {
		t.Fatal("result artifact bytes changed")
	}
	requireV2Status(t, f.jsonCall(t, wb.OperationAck, map[string]any{"sequence": status.Result.Sequence, "digest": status.Result.Digest, "durable": true}), 200)
	requireV2Status(t, f.call(t, v2Request(wb.OperationLookup, nil)), 200)
	if calls.Load() != 1 {
		t.Fatal("object transport duplicated execution")
	}
	receipt, err := f.client.DeleteAuthorization(f.ctx, "integration-delete", f.revision)
	if err != nil || receipt.State != "revoked" {
		t.Fatalf("revocation receipt %+v %v", receipt, err)
	}
	if _, err = f.client.Call(f.ctx, v2Request(wb.OperationLookup, nil)); err == nil {
		t.Fatal("deleted authorization still dispatched")
	}
	againReceipt, err := f.client.AuthorizationDeletionReceipt(f.ctx, "integration-delete", f.revision)
	if err != nil || againReceipt != receipt {
		t.Fatal("revoked endpoint could not reconcile deletion")
	}
}

func TestWorkbenchISCPV2ApprovalDecisionAndRestartTermination(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart_%v", restart), func(t *testing.T) {
			var fixture *v2EncryptedFixture
			var starts, effects atomic.Int32
			approval, err := execution.NewPendingApproval("approval_v2", "files.write", "controlled reversible write", map[string]any{"name": "isolated.txt"})
			if err != nil {
				t.Fatal(err)
			}
			fixture = newV2EncryptedFixture(t, func(ctx context.Context, e execution.Envelope, _ map[string][]byte) (execution.Output, error) {
				starts.Add(1)
				service, err := fixture.server.executionService()
				if err != nil {
					return execution.Output{}, err
				}
				decision, err := service.AwaitApproval(ctx, e, approval)
				if err != nil {
					return execution.Output{}, err
				}
				if decision == "approve" {
					effects.Add(1)
				}
				return execution.Output{Content: "approval resolved"}, nil
			})
			fixture.bind(t)
			original, _ := json.Marshal(workbenchISCPEnvelope())
			requireV2Status(t, fixture.call(t, v2Request(wb.OperationSubmit, original)), 202)
			var state execution.Status
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				r := fixture.call(t, v2Request(wb.OperationLookup, nil))
				requireV2Status(t, r, 200)
				if json.Unmarshal(r.Body, &state) != nil {
					t.Fatal("invalid lookup")
				}
				if len(state.PendingApprovals) == 1 {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if len(state.PendingApprovals) != 1 {
				t.Fatal("approval not published")
			}
			list := v2Request(wb.OperationApprovalsList, nil)
			list.Params = map[string]string{"request_id": iscpTestRequest}
			requireV2Status(t, fixture.call(t, list), 200)
			body, _ := json.Marshal(map[string]string{"digest": approval.Digest, "decision": "approve", "input_digest": state.InputDigest})
			decision := v2Request(wb.OperationExecutionApproval, body)
			decision.Params = map[string]string{"request_id": iscpTestRequest, "approval_id": approval.ApprovalID}
			decision.ExpectedRevision = fmt.Sprint(state.Revision)
			if restart {
				old := fixture.server.executions
				old.Close()
				fixture.server.executionMu.Lock()
				fixture.server.executions = nil
				fixture.server.executionMu.Unlock()
				if _, err := fixture.server.executionService(); err != nil {
					t.Fatal(err)
				}
				requireV2Status(t, fixture.call(t, decision), 409)
				r := fixture.call(t, v2Request(wb.OperationLookup, nil))
				requireV2Status(t, r, 200)
				state = execution.Status{}
				if json.Unmarshal(r.Body, &state) != nil || state.State != "failed" || state.TerminationReason != execution.TerminationGatewayRestartedAwaitingApproval || len(state.PendingApprovals) != 0 {
					t.Fatalf("restart did not terminate waiting task %s", r.Body)
				}
				if starts.Load() != 1 || effects.Load() != 0 {
					t.Fatal("restart resumed or repeated waiting work")
				}
				return
			}
			stale := decision
			stale.ID = v2Request(wb.OperationIdentity, nil).ID
			stale.OperationID = stale.ID
			stale.ExpectedRevision = "0"
			requireV2Status(t, fixture.call(t, stale), 409)
			first := fixture.call(t, decision)
			requireV2Status(t, first, 200)
			fixture.server.executions.Wait()
			decision.ID = v2Request(wb.OperationIdentity, nil).ID
			again := fixture.call(t, decision)
			requireV2Status(t, again, 200)
			if !bytes.Equal(first.Body, again.Body) || starts.Load() != 1 || effects.Load() != 1 {
				t.Fatal("decision receipt repeated work")
			}
		})
	}
}
