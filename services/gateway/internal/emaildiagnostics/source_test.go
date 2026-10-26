package emaildiagnostics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func sourceFixture() []SourceRecord {
	return []SourceRecord{
		{Owner: "owner-a", Kind: "mailbox", ID: "box-a", Data: json.RawMessage(`{"id":"box-a","provider":"qqmail","binding_generation":3,"address":"secret@example.test","page_acks":{"private":"secret"}}`)},
		{Owner: "owner-a", Kind: "draft", ID: "draft-a", Parent: "box-a", State: "failed", Data: json.RawMessage(`{"id":"draft-a","owner_id":"owner-a","mailbox_id":"box-a","state":"failed","error_code":"email_send_rejected_temporarily","version":4,"body":"secret body","subject":"secret subject","snapshot":{"invocation_id":"operation-a","mailbox_id":"box-a","state":"failed","body":"secret snapshot"},"receipt":{"provider":"qqmail","status":"not_sent","provider_payload":"secret provider payload","recipient_digest":"secret recipient","lifecycle":{"operation_id":"operation-a","task_id":"task-a","terminal_revision":3,"result_digest":"digest","acknowledged":false,"private":"secret"},"not_sent":{"schema_version":1,"kind":"provider_rejected","invocation_id":"operation-a","task_id":"task-a","intent_digest":"intent","resource_digest":"resource","binding_digest":"binding","ledger_epoch":3,"reason":"PROVIDER_TEMPORARILY_REJECTED","provider_code":-20003,"private":"secret"}}}`)},
		{Owner: "owner-a", Kind: "send_snapshot", ID: "snap-a", Parent: "draft-a", State: "failed", Data: json.RawMessage(`{"invocation_id":"operation-a","state":"failed","mailbox_id":"box-a","body":"secret attempt"}`)},
		{Owner: "owner-b", Kind: "send_snapshot", ID: "snap-b", Parent: "draft-a", Data: json.RawMessage(`{"invocation_id":"operation-a","mailbox_id":"box-b","body":"secret foreign"}`)},
		{Owner: "owner-a", Kind: "job", ID: "job-a", Parent: "box-a", Data: json.RawMessage(`{"id":"job-a","owner_id":"owner-a","kind":"discover","mailbox_id":"box-a","target_id":"box-a","generation":3,"attempt":2,"state":"failed","error_code":"email_read_failed","lease_token":"secret token"}`)},
		{Owner: "owner-a", Kind: "sync_failure", ID: "failure-a", Parent: "box-a", Data: json.RawMessage(`{"id":"failure-a","mailbox_id":"box-a","attempts":[{"invocation_id":"collect-a","outcome":"failed","actor":"secret actor"}]}`)},
	}
}

func writeSourceFixture(t *testing.T, records []SourceRecord, extra string) string {
	t.Helper()
	rows := map[string]SourceRecord{}
	for _, r := range records {
		rows[r.Owner+"/"+r.Kind+"/"+r.ID] = r
	}
	raw, err := json.Marshal(map[string]any{"email_management_records": rows, "sessions": map[string]any{}, "messages": extra})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, raw, 0400); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSourceProjectionPrivacyAndProof(t *testing.T) {
	for _, r := range sourceFixture() {
		got, ok := ProjectRecord(r)
		if !ok {
			t.Fatalf("projection rejected %s", r.Kind)
		}
		if strings.Contains(string(got.Data), "secret") {
			t.Fatalf("private field retained for %s: %s", r.Kind, got.Data)
		}
		if r.Kind == "draft" && (!strings.Contains(string(got.Data), `"provider_code":-20003`) || !strings.Contains(string(got.Data), `"acknowledged":false`)) {
			t.Fatalf("proof missing: %s", got.Data)
		}
	}
	for _, r := range []SourceRecord{{Kind: "mail", Data: json.RawMessage(`{}`)}, {Kind: "draft", Data: json.RawMessage(`[]`)}, {Kind: "draft", Data: json.RawMessage(`{"state":{"body":"secret"}}`)}} {
		if _, ok := ProjectRecord(r); ok {
			t.Fatalf("accepted unsupported record: %+v", r)
		}
	}
}

func TestSourceSnapshotReadOnlySelection(t *testing.T) {
	path := writeSourceFixture(t, sourceFixture(), strings.Repeat("private skipped value", 100000))
	before, _ := os.ReadFile(path)
	stat, _ := os.Stat(path)
	inventory, _ := os.ReadDir(filepath.Dir(path))
	for _, q := range []Selection{{Owner: "owner-a", Kind: "draft", ID: "draft-a"}, {Owner: "owner-a", Kind: "operation", ID: "operation-a", IdentityKind: "invocation"}, {Owner: "owner-a", Kind: "job", ID: "job-a"}} {
		got := ReadSnapshot(t.Context(), path, q, ReadLimits{})
		if len(got.Gaps) != 0 && !(q.Kind == "job" && reflect.DeepEqual(got.Gaps, []string{"job_attempt_mapping_missing"})) {
			t.Fatalf("gaps: %+v", got)
		}
		want := 3
		if q.Kind == "job" {
			want = 2
		}
		if len(got.Records) != want || !got.Scan || got.Bytes == 0 {
			t.Fatalf("selection %s: %+v", q.Kind, got)
		}
		for _, r := range got.Records {
			if r.Owner != "owner-a" || r.Kind == "sync_failure" || strings.Contains(string(r.Data), "secret") {
				t.Fatalf("unsafe join: %+v", r)
			}
		}
	}
	after, _ := os.ReadFile(path)
	end, _ := os.Stat(path)
	endInventory, _ := os.ReadDir(filepath.Dir(path))
	if string(before) != string(after) || stat.Mode() != end.Mode() || !stat.ModTime().Equal(end.ModTime()) || len(inventory) != len(endInventory) {
		t.Fatal("source changed")
	}
}

func TestSourceSnapshotIncompleteBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, raw, gap string }{
		{"encrypted", `{"version":1,"alg":"AES-256-GCM","nonce":"private","ciphertext":"private"}`, "encrypted_source_unsupported"},
		{"unsupported", `{"surprise":{}}`, "source_schema_unsupported"},
		{"truncated", `{"email_management_records":{`, "source_malformed"},
		{"invalid", `{"email_management_records":[],"sessions":{}}`, "source_schema_unsupported"},
		{"trailing", `{"email_management_records":{}} true`, "source_malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private.json")
			_ = os.WriteFile(path, []byte(tc.raw), 0400)
			got := ReadSnapshot(t.Context(), path, Selection{Owner: "owner-a", Kind: "draft", ID: "draft-a"}, ReadLimits{})
			if !slices.Contains(got.Gaps, tc.gap) {
				t.Fatalf("gap: %+v", got)
			}
		})
	}
	path := writeSourceFixture(t, sourceFixture(), "")
	q := Selection{Owner: "owner-a", Kind: "draft", ID: "draft-a"}
	for _, tc := range []struct {
		limits ReadLimits
		gap    string
	}{{ReadLimits{Bytes: 32}, "source_byte_limit"}, {ReadLimits{RecordBytes: 128}, "source_record_limit"}, {ReadLimits{Rows: 1}, "source_row_limit"}} {
		got := ReadSnapshot(t.Context(), path, q, tc.limits)
		if !slices.Contains(got.Gaps, tc.gap) {
			t.Fatalf("bound %+v: %+v", tc.limits, got)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := ReadSnapshot(ctx, path, q, ReadLimits{}); !slices.Contains(got.Gaps, "source_canceled") {
		t.Fatalf("cancellation: %+v", got)
	}
	if got := ReadSnapshot(t.Context(), path+"-missing", q, ReadLimits{}); !slices.Contains(got.Gaps, "source_unavailable") {
		t.Fatalf("missing: %+v", got)
	}
	if _, err := os.Stat(path + "-missing"); !os.IsNotExist(err) {
		t.Fatal("missing source created")
	}
}

func TestSourceAmbiguousIdentityAndMissingRelations(t *testing.T) {
	for _, raw := range []string{`{"state":"failed","state":"sent"}`, `{"snapshot":{"invocation_id":"old","invocation_id":"new"}}`, `{"receipt":{"lifecycle":{"acknowledged":false,"acknowledged":true}}}`} {
		if _, ok := ProjectRecord(SourceRecord{Kind: "draft", Data: json.RawMessage(raw)}); ok {
			t.Fatalf("duplicate evidence accepted: %s", raw)
		}
	}
	rows := sourceFixture()[:3]
	rows = rows[1:]
	got := ReadSnapshot(t.Context(), writeSourceFixture(t, rows, ""), Selection{Owner: "owner-a", Kind: "draft", ID: "draft-a"}, ReadLimits{})
	if !slices.Contains(got.Gaps, "source_related_missing") {
		t.Fatalf("missing mailbox not disclosed: %+v", got)
	}
	path := filepath.Join(t.TempDir(), "duplicate.json")
	_ = os.WriteFile(path, []byte(`{"email_management_records":{"x":{"owner":"owner-b","owner":"owner-a","kind":"draft","id":"draft-a","data":{}}}}`), 0400)
	got = ReadSnapshot(t.Context(), path, Selection{Owner: "owner-a", Kind: "draft", ID: "draft-a"}, ReadLimits{})
	if len(got.Records) != 0 || !slices.Contains(got.Gaps, "source_record_invalid") {
		t.Fatalf("duplicate owner accepted: %+v", got)
	}
}

func TestSourceCollectionInvocationAndRequestKey(t *testing.T) {
	path := writeSourceFixture(t, sourceFixture(), "")
	got := ReadSnapshot(t.Context(), path, Selection{Owner: "owner-a", Kind: "operation", ID: "collect-a", IdentityKind: "invocation"}, ReadLimits{})
	if len(got.Gaps) != 0 || len(got.Records) != 2 || got.Records[1].Kind != "sync_failure" {
		t.Fatalf("collection operation: %+v", got)
	}
	got = ReadSnapshot(t.Context(), path, Selection{Owner: "owner-a", Kind: "operation", ID: "legacy-digest", IdentityKind: "request-key"}, ReadLimits{})
	if len(got.Records) != 0 || !slices.Contains(got.Gaps, "gateway_identity_mapping_required") {
		t.Fatalf("unsafe reverse join: %+v", got)
	}
	operation := "op3.1.2.0123456789abcdef0123456789abcdef"
	rows := sourceFixture()
	rows[2].Data = json.RawMessage(`{"invocation_id":"` + operation + `","mailbox_id":"box-a"}`)
	got = ReadSnapshot(t.Context(), writeSourceFixture(t, rows, ""), Selection{Owner: "owner-a", Kind: "operation", ID: operation, IdentityKind: "request-key"}, ReadLimits{})
	if len(got.Gaps) != 0 || len(got.Records) != 3 {
		t.Fatalf("direct op3 mapping: %+v", got)
	}
}

func TestSourceCancellationDuringLongSkippedValue(t *testing.T) {
	path := writeSourceFixture(t, sourceFixture(), strings.Repeat("private", 1<<20))
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	got := ReadSnapshot(ctx, path, Selection{Owner: "owner-a", Kind: "draft", ID: "draft-a"}, ReadLimits{})
	if !slices.Contains(got.Gaps, "source_timeout") {
		t.Fatalf("unbounded skipped input: %+v", got)
	}
}

func TestSourceRejectsInvalidRequestKeyWithoutRead(t *testing.T) {
	for _, id := range []string{"op3:1:2:nonce", "op3.1.2.short", "op3.01.2.0123456789abcdef0123456789abcdef", "op3.1.2.0123456789ABCDEF0123456789ABCDEF"} {
		got := ReadSnapshot(t.Context(), "does-not-exist", Selection{Owner: "owner-a", Kind: "operation", ID: id, IdentityKind: "request-key"}, ReadLimits{})
		if got.Bytes != 0 || len(got.Records) != 0 || !slices.Contains(got.Gaps, "gateway_identity_mapping_required") {
			t.Fatalf("invalid operation scanned source: %+v", got)
		}
	}
}

func TestSourceRejectsContradictoryNotSentReceipt(t *testing.T) {
	for _, field := range []string{"provider_message_id", "provider_thread_id"} {
		for _, nested := range []bool{false, true} {
			raw := `{"receipt":{"status":"not_sent","` + field + `":"secret provider id"}}`
			if nested {
				raw = `{"snapshot":` + raw + `}`
			}
			r := SourceRecord{Owner: "owner-a", Kind: "draft", ID: "bad", Data: json.RawMessage(raw)}
			if _, ok := ProjectRecord(r); ok {
				t.Fatalf("contradictory receipt accepted: %s nested=%v", field, nested)
			}
			got := ReadSnapshot(t.Context(), writeSourceFixture(t, []SourceRecord{r}, ""), Selection{Owner: "owner-a", Kind: "draft", ID: "bad"}, ReadLimits{})
			if len(got.Records) != 0 || !slices.Contains(got.Gaps, "source_record_invalid") {
				t.Fatalf("file conflict not exposed: %+v", got)
			}
		}
	}
}

func TestSourceReportsConcurrentSnapshotChange(t *testing.T) {
	path := writeSourceFixture(t, sourceFixture(), strings.Repeat("private", 1<<20))
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if e == nil {
					_, _ = f.Write([]byte(" "))
					_ = f.Close()
				}
			}
		}
	}()
	got := ReadSnapshot(t.Context(), path, Selection{Owner: "owner-a", Kind: "draft", ID: "draft-a"}, ReadLimits{})
	cancel()
	<-done
	if !slices.Contains(got.Gaps, "source_changed") {
		t.Fatalf("changed snapshot not disclosed: %+v", got)
	}
}

func TestSourceConflictingRecordDoesNotChooseWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflict.json")
	raw := `{"email_management_records":{"first":{"owner":"owner-a","kind":"draft","id":"draft-a","state":"failed","data":{"state":"failed"}},"second":{"owner":"owner-a","kind":"draft","id":"draft-a","state":"sent","data":{"state":"sent"}},"third":{"owner":"owner-a","kind":"draft","id":"draft-a","state":"sent","data":{"state":"sent"}}}}`
	_ = os.WriteFile(path, []byte(raw), 0400)
	got := ReadSnapshot(t.Context(), path, Selection{Owner: "owner-a", Kind: "draft", ID: "draft-a"}, ReadLimits{})
	if len(got.Records) != 0 || !slices.Contains(got.Gaps, "source_record_conflict") {
		t.Fatalf("conflicting evidence picked winner: %+v", got)
	}
}

func TestSourceEnvelopeUsesExactJSONFieldNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case.json")
	raw := `{"email_management_records":{"x":{"owner":"owner-b","Owner":"owner-a","kind":"send_snapshot","id":"foreign","parent":"draft-a","data":{"invocation_id":"operation-a"}}}}`
	_ = os.WriteFile(path, []byte(raw), 0400)
	got := ReadSnapshot(t.Context(), path, Selection{Owner: "owner-a", Kind: "operation", ID: "operation-a", IdentityKind: "invocation"}, ReadLimits{})
	if len(got.Records) != 0 {
		t.Fatalf("case-folded owner crossed scope: %+v", got)
	}
}
