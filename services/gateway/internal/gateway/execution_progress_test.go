package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func progressRequest(digest string, revision uint64, wait int) wb.Request {
	body, _ := json.Marshal(execution.ProgressInput{InputDigest: digest, AfterRevision: revision, WaitMS: wait})
	r := v2Request(wb.OperationExecutionProgress, body)
	r.Params = map[string]string{"request_id": iscpTestRequest}
	return r
}
func decodeProgress(t *testing.T, r wb.Response) execution.Progress {
	t.Helper()
	requireV2Status(t, r, 200)
	var p execution.Progress
	if json.Unmarshal(r.Body, &p) != nil || p.SchemaVersion != 1 || !execution.UUID(p.Epoch) || p.RequestID != iscpTestRequest || p.Digest != execution.Digest([]byte(p.Content)) {
		t.Fatalf("bad progress snapshot: %s", r.Body)
	}
	return p
}

func TestExecutionProgressEncryptedReadWakeupAndDurableAck(t *testing.T) {
	started := make(chan context.Context, 1)
	finish := make(chan struct{})
	var once sync.Once
	end := func() { once.Do(func() { close(finish) }) }
	var calls atomic.Int32
	s, _, cfg, _ := workbenchISCPFixture(t, func(ctx context.Context, e execution.Envelope, _ map[string][]byte) (execution.Output, error) {
		calls.Add(1)
		started <- ctx
		select {
		case <-finish:
			return execution.Output{Content: "complete reliable answer"}, nil
		case <-ctx.Done():
			return execution.Output{}, ctx.Err()
		}
	})
	t.Cleanup(end)
	f := startV2EncryptedFixture(t, s, cfg, nil, []string{wb.OperationExecutionProgress})
	requireV2Status(t, f.jsonCall(t, wb.OperationBind, map[string]any{"schema_version": 1, "installation_id": iscpTestInstallation}), 200)
	raw, _ := json.Marshal(workbenchISCPEnvelope())
	digest := execution.Digest(raw)
	requireV2Status(t, f.call(t, v2Request(wb.OperationSubmit, raw)), 202)
	runCtx := <-started
	p := decodeProgress(t, f.call(t, progressRequest(digest, 0, 0)))
	if p.Content != "" || p.State != "running" {
		t.Fatalf("initial: %+v", p)
	}
	result := make(chan wb.Response, 1)
	go func() {
		r, err := f.client.Call(f.ctx, progressRequest(digest, p.Revision, 10000))
		if err != nil {
			r.Status = 599
		}
		result <- r
	}()
	execution.PublishProgress(runCtx, "增量文字")
	select {
	case r := <-result:
		p = decodeProgress(t, r)
	case <-time.After(2 * time.Second):
		t.Fatal("encrypted progress did not wake")
	}
	if p.Content != "增量文字" || p.InputDigest != digest {
		t.Fatalf("preview: %+v", p)
	}
	lookup := f.call(t, v2Request(wb.OperationLookup, nil))
	requireV2Status(t, lookup, 200)
	if bytes.Contains(lookup.Body, []byte(`"result"`)) {
		t.Fatal("preview counted as delivery")
	}
	for _, bad := range []wb.Request{
		progressRequest(strings.Repeat("0", 64), 0, 0),
		func() wb.Request {
			r := progressRequest(digest, 0, 0)
			r.InstallationID = "99999999-9999-4999-8999-999999999999"
			return r
		}(),
		func() wb.Request {
			r := progressRequest(digest, 0, 0)
			r.RequestID = "99999999-9999-4999-8999-999999999999"
			return r
		}(),
		progressRequest(digest, 0, 10001),
		func() wb.Request {
			r := progressRequest(digest, 0, 0)
			r.Body = json.RawMessage(`{"input_digest":"` + digest + `","after_revision":0,"wait_ms":0,"extra":true}`)
			return r
		}(),
	} {
		if r := f.call(t, bad); r.Status < 400 {
			t.Fatalf("foreign/invalid progress admitted: %+v", r)
		}
	}
	go func() {
		r, err := f.client.Call(f.ctx, progressRequest(digest, p.Revision, 10000))
		if err != nil {
			r.Status = 599
		}
		result <- r
	}()
	end()
	select {
	case r := <-result:
		p = decodeProgress(t, r)
	case <-time.After(2 * time.Second):
		t.Fatal("terminal encrypted progress did not wake")
	}
	if p.State != "completed" {
		t.Fatalf("terminal: %+v", p)
	}
	lookup = f.call(t, v2Request(wb.OperationLookup, nil))
	requireV2Status(t, lookup, 200)
	var status execution.Status
	if json.Unmarshal(lookup.Body, &status) != nil || status.Result == nil || execution.Digest([]byte(status.Result.Payload)) != status.Result.Digest {
		t.Fatal("reliable lookup changed")
	}
	requireV2Status(t, f.jsonCall(t, wb.OperationAck, map[string]any{"sequence": 1, "digest": p.Digest, "durable": true}), 409)
	requireV2Status(t, f.jsonCall(t, wb.OperationAck, map[string]any{"sequence": 1, "digest": status.Result.Digest, "durable": true}), 200)
	acked := decodeProgress(t, f.call(t, progressRequest(digest, p.Revision, 10000)))
	if acked.State != "delivered" || acked.Content != "" || acked.Revision <= p.Revision || calls.Load() != 1 {
		t.Fatalf("ack/replay: %+v calls=%d", acked, calls.Load())
	}
}

func TestExecutionProgressRequiresQualificationAndV2(t *testing.T) {
	f := newV2EncryptedFixture(t, func(context.Context, execution.Envelope, map[string][]byte) (execution.Output, error) {
		t.Fatal("read executed work")
		return execution.Output{}, nil
	})
	if caps, ok := f.client.Negotiated(); !ok || slices.Contains(caps.Operations, wb.OperationExecutionProgress) {
		t.Fatal("unqualified progress advertised")
	}
	requireV2Status(t, f.call(t, progressRequest(strings.Repeat("a", 64), 0, 0)), 501)
	r := progressRequest(strings.Repeat("a", 64), 0, 0)
	r.Profile = wb.Profile
	if r.Validate() == nil {
		t.Fatal("v1 accepted new progress operation")
	}
}

func TestExecutionProgressWorstEscapedTextFitsEncryptedResponse(t *testing.T) {
	s, _, cfg, _ := workbenchISCPFixture(t, func(ctx context.Context, _ execution.Envelope, _ map[string][]byte) (execution.Output, error) {
		execution.PublishProgress(ctx, strings.Repeat("\x00", execution.ProgressBytes+1))
		return execution.Output{Content: "complete"}, nil
	})
	f := startV2EncryptedFixture(t, s, cfg, nil, []string{wb.OperationExecutionProgress})
	requireV2Status(t, f.jsonCall(t, wb.OperationBind, map[string]any{"schema_version": 1, "installation_id": iscpTestInstallation}), 200)
	raw, _ := json.Marshal(workbenchISCPEnvelope())
	requireV2Status(t, f.call(t, v2Request(wb.OperationSubmit, raw)), 202)
	s.executions.Wait()
	p := decodeProgress(t, f.call(t, progressRequest(execution.Digest(raw), 0, 0)))
	if len(p.Content) != execution.ProgressBytes || !p.Truncated {
		t.Fatal("worst-case escaped encrypted response lost preview")
	}
}

func TestExecutionProgressRealFinalStreamAndHTTPIdentity(t *testing.T) {
	s, _, _, handler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, handler)
	e := workbenchISCPEnvelope()
	e.Messages = []execution.Message{{Role: "user", Content: "法国的首都是什么？\nMOCK_CONVERSATION_RESPONSE:巴黎。"}}
	raw, _ := json.Marshal(e)
	submit := handler(t.Context(), workbenchISCPRequest(wb.OperationSubmit, raw))
	if submit.Status != 202 {
		t.Fatalf("submit=%+v", submit)
	}
	s.executions.Wait()
	input := execution.ProgressInput{InputDigest: execution.Digest(raw)}
	p, err := s.executions.ReadProgress(t.Context(), e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, input)
	if err != nil || p.Content != "巴黎。" || p.State != "completed" {
		t.Fatalf("runtime final stream did not publish answer: %+v %v", p, err)
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	inputRaw, _ := json.Marshal(input)
	for _, test := range []struct {
		token, install string
		want           int
	}{{"synthetic-issued-client-credential-never-used-by-iscp", e.InstallationID, 200}, {"wrong", e.InstallationID, 401}, {"synthetic-issued-client-credential-never-used-by-iscp", "99999999-9999-4999-8999-999999999999", 403}} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/v1/executions/"+e.RequestID+"/progress", bytes.NewReader(inputRaw))
		req.Header.Set("Authorization", "Bearer "+test.token)
		req.Header.Set("X-SparkClaw-Installation", test.install)
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != test.want {
			t.Fatalf("http identity=%d want=%d", response.StatusCode, test.want)
		}
		if test.want == 200 {
			var snapshot execution.Progress
			if json.NewDecoder(response.Body).Decode(&snapshot) != nil || snapshot != p || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("HTTP preview contract changed")
			}
		}
		response.Body.Close()
	}
	control, err := os.ReadFile(filepath.Join(s.executions.Root(), "control.json"))
	if err != nil || bytes.Contains(control, []byte("巴黎")) {
		t.Fatal("preview leaked into control")
	}
}
