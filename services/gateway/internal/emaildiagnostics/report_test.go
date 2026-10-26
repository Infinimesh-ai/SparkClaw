package emaildiagnostics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func record(owner, kind, id, parent, data string) SourceRecord {
	return SourceRecord{Owner: owner, Kind: kind, ID: id, Parent: parent, Data: json.RawMessage(data)}
}
func TestAggregateNeverTreatsWorkerSuccessAsMailSuccess(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "job", ID: strings.Repeat("a", 64)}
	rows := []SourceRecord{record(q.Owner, "job", q.ID, "", `{"id":"`+q.ID+`","owner_id":"owner","state":"succeeded","kind":"discover","attempt":2,"generation":3}`)}
	r := aggregate(q, rows, "fixture")
	if len(r.Attempts) != 1 || r.Attempts[0].Execution != "succeeded" || r.Attempts[0].Business.State != "incomplete" || r.Attempts[0].Effect != "incomplete" {
		t.Fatalf("worker success conflated: %+v", r)
	}
	if !contains(r.Missing, "analysis_attempt_index_unavailable") {
		t.Fatal("missing D09 evidence hidden")
	}
}

func TestAggregateOwnerIsolationHistoryAndUnknownFence(t *testing.T) {
	op := "op3.1.4." + strings.Repeat("a", 32)
	old := "op3.1.4." + strings.Repeat("b", 32)
	rows := []SourceRecord{
		record("owner", "draft", "draft", "", `{"owner_id":"owner","state":"unknown","snapshot":{"invocation_id":"`+op+`","state":"unknown"},"subject":"private@example.test"}`),
		record("owner", "send_snapshot", "history", "draft", `{"invocation_id":"`+old+`","state":"failed"}`),
		record("other", "send_snapshot", "bad", "draft", `{"invocation_id":"stolen","state":"sent"}`),
	}
	r := aggregate(Selection{Owner: "owner", Kind: "draft", ID: "draft"}, rows, "fixture")
	if len(r.Attempts) != 2 {
		t.Fatalf("history merged or cross-owner included: %+v", r)
	}
	found := false
	for _, a := range r.Attempts {
		if a.Business.State == "unknown" {
			found = true
			if a.Recovery != "inspect_without_resend" || a.Effect != "unknown" {
				t.Fatalf("unknown lost: %+v", a)
			}
		}
	}
	if !found {
		t.Fatal("current unknown disappeared")
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "private@example.test") || strings.Contains(string(raw), "stolen") || strings.Contains(string(raw), op) {
		t.Fatalf("private output: %s", raw)
	}
}

func TestBoundaryLogsRequireExactAttemptAndDoNotRewritePersistence(t *testing.T) {
	op := "op3.1.4." + strings.Repeat("a", 32)
	q := Selection{Owner: "owner", Kind: "draft", ID: "draft"}
	r := aggregate(q, []SourceRecord{record("owner", "draft", "draft", "", `{"owner_id":"owner","state":"sending","snapshot":{"invocation_id":"`+op+`","state":"sending"}}`)}, "fixture")
	lines := []string{
		`{"msg":"email send boundary","schema_version":1,"draft_scope_sha256":"` + scopeHash("owner", "draft") + `","operation_id":"` + op + `","stage":"send_result","business_outcome":"sent","business_persisted":false}`,
		`{"msg":"email send boundary","schema_version":1,"draft_scope_sha256":"` + scopeHash("other", "draft") + `","operation_id":"` + op + `","stage":"ack","cause_code":"browser_extension_unavailable"}`,
		`{"msg":"email send boundary","schema_version":1,"draft_scope_sha256":"` + scopeHash("owner", "draft") + `","operation_id":"` + op + `","stage":"persist_result","cause_code":"store_durability_failed","cause_stage":"persist_result","cause_source":"store"}`,
	}
	applyLogLines(&r, q, lines, "logs")
	a := r.Attempts[0]
	if a.Business.State != "sending" || a.ObservedBusiness != "sent" || a.FirstSafeCause == nil || a.FirstSafeCause.Code != "store_durability_failed" || a.FirstSafeCause.Stage != "persist_result" {
		t.Fatalf("bad evidence: %+v", a)
	}
	if a.ACK != "incomplete" {
		t.Fatal("another owner's ACK cause joined")
	}
	if a.LastProvedStage != "persist_result" {
		t.Fatalf("last observed boundary lost: %s", a.LastProvedStage)
	}
}

func TestLogParsingPrivacyConflictsAndUnorderedCauses(t *testing.T) {
	op := "op3.1.4." + strings.Repeat("a", 32)
	q := Selection{Owner: "owner", Kind: "draft", ID: "draft"}
	r := aggregate(q, []SourceRecord{record("owner", "draft", "draft", "", `{"owner_id":"owner","state":"unknown","snapshot":{"invocation_id":"`+op+`","state":"unknown"}}`)}, "fixture")
	prefix := `2026/10/26 12:00:00 INFO email send boundary schema_version=1 draft_scope_sha256=` + scopeHash("owner", "draft") + ` operation_id=` + op + ` `
	applyLogLines(&r, q, []string{prefix + `stage=send_result business_outcome=unknown cause_code="private@example.test" cause_stage=controller_request cause_source=browser_control`, prefix + `stage=send_result business_outcome=sent cause_code=browser_extension_unavailable cause_stage=controller_request cause_source=browser_control`}, "logs")
	if !contains(r.Missing, "conflicting_boundary_evidence") || r.Attempts[0].FirstSafeCause != nil {
		t.Fatalf("conflict invented first cause: %+v", r)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "private@example.test") {
		t.Fatalf("payload leaked: %s", raw)
	}
}

func TestBuildBoundsVersionsAndNoSourceInitialization(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	manifest := filepath.Join(dir, "release.json")
	logs := filepath.Join(dir, "events.log")
	os.WriteFile(state, []byte(`{"email_management_records":{}}`), 0400)
	os.WriteFile(manifest, []byte(`{"id":"0.3.0-sparkclaw.41","source_commit":"bcc55506cc933f981668e3b2d1ed1eba1bf4c6a3","runtime_digest":"`+strings.Repeat("a", 64)+`"}`), 0400)
	os.WriteFile(logs, []byte(strings.Repeat("x", 2048)), 0400)
	r, err := Build(t.Context(), Options{Owner: "owner", Draft: "draft", Snapshot: state, ReleaseManifest: manifest, Logs: []string{logs}, Limits: Limits{LogBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(r.Missing, "log_byte_limit") || r.Versions.Expected.Release != "0.3.0-sparkclaw.41" || r.Versions.ObservedLoaded != "incomplete" {
		t.Fatalf("false coverage/version: %+v", r)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Fatal("report initialized new files")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err = Build(ctx, Options{Owner: "owner", Draft: "draft", Snapshot: state})
	if err != nil || !contains(r.Missing, "query_canceled") {
		t.Fatalf("cancellation missing: %+v %v", r, err)
	}
}

func TestCollectionOperationUsesOnlyItsPersistedAttempt(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "operation", ID: "email_changes_a_r2", IdentityKind: "invocation"}
	rows := []SourceRecord{record("owner", "sync_failure", "failure", "", `{"owner_id":"owner","mailbox_id":"box","state":"resolved","attempts":[{"invocation_id":"other","outcome":"succeeded"},{"invocation_id":"email_changes_a_r2","outcome":"failed","error_code":"email_source_missing"}]}`)}
	r := aggregate(q, rows, "fixture")
	if len(r.Attempts) != 1 || r.Attempts[0].Business.State != "failed" || r.Attempts[0].Execution != "incomplete" || r.Attempts[0].Business.ErrorCode != "email_source_missing" {
		t.Fatalf("collection evidence lost/misattributed: %+v", r)
	}
}

func TestJobCollectionLogWithoutGenerationCannotBorrowCurrentAttempt(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "job", ID: strings.Repeat("a", 64)}
	r := aggregate(q, []SourceRecord{record("owner", "job", q.ID, "", `{"owner_id":"owner","state":"failed","attempt":1,"generation":8}`)}, "fixture")
	applyLogLines(&r, q, []string{`{"msg":"email collection failed","schema_version":1,"job_id":"` + q.ID + `","attempt":1,"invocation_id":"historical","stage":"collection","cause_code":"email_source_missing","cause_source":"source"}`}, "logs")
	if r.Attempts[0].FirstSafeCause != nil || r.Attempts[0].InvocationRef != "" || !contains(r.Missing, "collection_generation_missing") {
		t.Fatalf("borrowed historical generation: %+v", r)
	}
}

func TestHostRequiresActualLedgerKeyAndTaskNotNearbyTimestamp(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "operation", ID: "legacy-invocation", IdentityKind: "invocation"}
	r := emptyReport(q)
	a := newAttempt(q, q.ID)
	a.invocation = q.ID
	a.taskID = "task-1"
	a.requestKey = "actual-ledger-key"
	r.Attempts = []Attempt{a}
	good := `{"event":"application_host_failed","task_id":"task-1","operation_key_sha256":"` + digestString(a.requestKey) + `","correlation":"complete","code":"browser_extension_unavailable","phase":"read"}`
	wrong := strings.ReplaceAll(good, digestString(a.requestKey), digestString(q.ID))
	applyLogLines(&r, q, []string{wrong}, "wrong")
	if r.Attempts[0].FirstSafeCause != nil {
		t.Fatal("unmapped invocation used as request key")
	}
	applyLogLines(&r, q, []string{good}, "good")
	if r.Attempts[0].FirstSafeCause == nil || r.Attempts[0].FirstSafeCause.Source != "host" {
		t.Fatal("exact Host failure missing")
	}
	if r.Attempts[0].FirstSafeCause.Stage != "read" {
		t.Fatal("real Host phase lost")
	}
}

func TestPausedJobAndUnstartedEvidenceAreNotInventedExecutions(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "job", ID: "job"}
	r := aggregate(q, []SourceRecord{record("owner", "job", "job", "", `{"owner_id":"owner","state":"paused","attempt":1,"generation":2}`)}, "fixture")
	if r.Attempts[0].Execution != "paused" {
		t.Fatal("paused worker lost")
	}
	q = Selection{Owner: "owner", Kind: "draft", ID: "draft"}
	r = aggregate(q, []SourceRecord{record("owner", "draft", "draft", "", `{"owner_id":"owner","state":"failed","snapshot":{"invocation_id":"previous-operation","state":"failed"}}`)}, "fixture")
	applyLogLines(&r, q, []string{`{"msg":"email send boundary","schema_version":1,"draft_scope_sha256":"` + scopeHash(q.Owner, q.ID) + `","operation_id":"","stage":"identity","business_outcome":"not_started","identity_mode":"legacy_epoch_failed","cause_code":"browser_controller_unavailable","cause_stage":"controller_request","cause_source":"browser_control"}`}, "logs")
	raw, _ := json.Marshal(r)
	if !strings.Contains(string(raw), "unstarted_evidence") || !strings.Contains(string(raw), "browser_controller_unavailable") || r.Attempts[0].FirstSafeCause != nil {
		t.Fatalf("unstarted evidence lost or borrowed prior task: %s", raw)
	}
}

func TestOneAttemptConflictCannotOverwriteObservedResultOrAnotherCause(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "draft", ID: "draft"}
	r := emptyReport(q)
	a := newAttempt(q, "a")
	a.draftID = "draft"
	a.invocation = "a"
	a.ACK = "pending"
	a.Recovery = "existing_maintenance_ack_eligible"
	b := newAttempt(q, "b")
	b.draftID = "draft"
	b.invocation = "b"
	r.Attempts = []Attempt{a, b}
	prefix := `{"msg":"email send boundary","schema_version":1,"draft_scope_sha256":"` + scopeHash(q.Owner, q.ID) + `",`
	applyLogLines(&r, q, []string{prefix + `"operation_id":"a","stage":"send_result","business_outcome":"sent"}`, prefix + `"operation_id":"a","stage":"send_result","business_outcome":"unknown"}`, prefix + `"operation_id":"b","stage":"ack","cause_code":"browser_extension_unavailable","cause_stage":"controller_request","cause_source":"browser_control"}`}, "logs")
	if r.Attempts[0].ObservedBusiness != "incomplete" || r.Attempts[0].Recovery != "inspect_without_resend" || r.Attempts[1].FirstSafeCause == nil {
		t.Fatalf("cross-attempt contamination: %+v", r.Attempts)
	}
}

func TestEncodeBoundsAndMalformedLogDoesNotManufactureEvidence(t *testing.T) {
	r := emptyReport(Selection{Owner: "owner", Kind: "draft", ID: "draft"})
	r.Limits = Limits{OutputBytes: 512}
	for range 50 {
		r.Attempts = append(r.Attempts, newAttempt(Selection{Owner: "owner"}, "long"))
	}
	raw, err := Encode(r, "json")
	if err != nil || len(raw) > 512 || !strings.Contains(string(raw), "output_limit") {
		t.Fatalf("unbounded output %d: %v", len(raw), err)
	}
	if _, ok := parseLog(`{"event":"application_host_failed","task_id":"other","task_id":"correct"}`, "fixture"); ok {
		t.Fatal("duplicate identity accepted")
	}
}

func TestLogRecordLimitIsHonoredBelowScannerBufferSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 512)+"\n"), 0400); err != nil {
		t.Fatal(err)
	}
	r := emptyReport(Selection{Owner: "owner", Kind: "draft", ID: "draft"})
	r.Limits = normalizedLimits(Limits{RecordBytes: 128})
	readLogs(t.Context(), &r, []string{path})
	if !contains(r.Missing, "log_record_invalid_or_oversized") {
		t.Fatalf("record bound ignored: %+v", r)
	}
}

func TestConflictingPersistentResultsRemainIncompleteInEitherOrder(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "draft", ID: "draft"}
	rows := []SourceRecord{record("owner", "draft", "draft", "", `{"owner_id":"owner","state":"sent","snapshot":{"invocation_id":"same","state":"sent"}}`), record("owner", "send_snapshot", "snapshot", "draft", `{"invocation_id":"same","state":"unknown"}`)}
	for range 2 {
		r := aggregate(q, rows, "fixture")
		if len(r.Attempts) != 1 || r.Attempts[0].Business.State != "incomplete" || r.Attempts[0].Recovery != "inspect_without_resend" {
			t.Fatalf("selected a conflicting business state: %+v", r)
		}
		rows[0], rows[1] = rows[1], rows[0]
	}
	q = Selection{Owner: "owner", Kind: "operation", ID: "same", IdentityKind: "invocation"}
	rows = []SourceRecord{record("owner", "sync_failure", "failure", "", `{"owner_id":"owner","attempts":[{"invocation_id":"same","outcome":"failed","error_code":"email_source_missing"},{"invocation_id":"same","outcome":"succeeded"}]}`)}
	r := aggregate(q, rows, "fixture")
	selectFirstCauses(&r)
	if r.Attempts[0].Business.State != "incomplete" || r.Attempts[0].FirstSafeCause != nil {
		t.Fatalf("collection conflict selected a winner: %+v", r)
	}
}

func TestACKConflictCannotAuthorizeRecoveryOrBeOverwrittenByDuplicate(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "operation", ID: "op", IdentityKind: "invocation"}
	r := emptyReport(q)
	a := newAttempt(q, "op")
	a.invocation = "op"
	a.ACK = "pending"
	a.Recovery = "existing_maintenance_ack_eligible"
	r.Attempts = []Attempt{a}
	task := ledgerTask{ID: "task", Invocation: "op", RequestKey: "key", OwnerScope: digestString("owner"), Status: "failed", ACK: "acknowledged"}
	applyLedger(&r, q, ledgerResult{Tasks: []ledgerTask{task, task}}, "ledger")
	if r.Attempts[0].ACK != "incomplete" || r.Attempts[0].Recovery != "inspect_without_resend" {
		t.Fatalf("conflicting ACK still eligible: %+v", r)
	}
}

func TestReceiptRevisionConflictCannotOfferACKRecovery(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "operation", ID: "op", IdentityKind: "invocation"}
	r := emptyReport(q)
	a := newAttempt(q, "op")
	a.invocation = "op"
	a.taskID = "task"
	a.ACK = "pending"
	a.Recovery = "existing_maintenance_ack_eligible"
	a.receiptRevision = 1
	a.receiptDigest = strings.Repeat("a", 64)
	r.Attempts = []Attempt{a}
	applyLedger(&r, q, ledgerResult{Tasks: []ledgerTask{{ID: "task", Invocation: "op", RequestKey: "key", OwnerScope: digestString(q.Owner), Status: "failed", ACK: "pending", TerminalRevision: 2, ResultDigest: strings.Repeat("b", 64)}}}, "ledger")
	if r.Attempts[0].Recovery != "inspect_without_resend" || !contains(r.Missing, "receipt_revision_conflict") {
		t.Fatalf("obsolete receipt treated as current: %+v", r)
	}
}

func TestUntrustedCWDHelperCannotRunBeforeReadOnlyGuard(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	helper := filepath.Join(dir, "tools/browser-controller/scripts/email-diagnostics-ledger.mjs")
	if err := os.MkdirAll(filepath.Dir(helper), 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "unauthorized-write")
	quoted, _ := json.Marshal(marker)
	if err := os.WriteFile(helper, []byte(`import fs from 'node:fs'; fs.writeFileSync(`+string(quoted)+`, 'changed'); console.log(JSON.stringify({schema_version:1,tasks:[],gaps:[],rows_read:0,bytes_read:0}));`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Build(t.Context(), Options{Owner: "owner", Operation: "op3.1.4." + strings.Repeat("a", 32), IdentityKind: "invocation", Ledger: filepath.Join(dir, "missing.sqlite"), LedgerOwner: "ledger-owner", LedgerPrincipal: "product-owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("untrusted CWD helper executed before guard")
	}
}

func TestJobLogWithoutOwnerBindingCannotBorrowAnotherOwnerCause(t *testing.T) {
	q := Selection{Owner: "owner-a", Kind: "job", ID: strings.Repeat("a", 64)}
	r := aggregate(q, []SourceRecord{record(q.Owner, "job", q.ID, "", `{"owner_id":"owner-a","state":"failed","attempt":1,"generation":1}`)}, "fixture")
	applyLogLines(&r, q, []string{`{"msg":"email job failed","schema_version":1,"job_id":"` + q.ID + `","attempt":1,"generation":1,"stage":"lease_renewal","cause_code":"store_unavailable","cause_source":"store"}`}, "other-owner-log")
	if r.Attempts[0].FirstSafeCause != nil || !contains(r.Missing, "job_log_owner_binding_missing") {
		t.Fatalf("unscoped job cause borrowed: %+v", r)
	}
}

func TestSinkHealthCountsAreSnapshotsWithIncompleteCoverage(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "draft", ID: "draft"}
	r := emptyReport(q)
	applyLogLines(&r, q, []string{`{"event":"latency_trace_health","schema_version":1,"component":"go_latencytrace","state":"degraded","reason":"queue_full","attempted":10,"written":6,"dropped":3,"lost":3,"metadata_rejected":0,"last_written_at":"2026-10-26T01:00:00Z"}`}, "log")
	raw, _ := json.Marshal(r)
	if !strings.Contains(string(raw), `"written":6`) || !contains(r.Missing, "sink_history_incomplete") || r.Evidence != "incomplete" {
		t.Fatalf("sink snapshot lost or overstated: %s", raw)
	}
}

func TestHostBoundaryDoesNotRegressOnUnorderedIdentityLog(t *testing.T) {
	a := newAttempt(Selection{Owner: "owner"}, "op")
	advanceBoundary(&a, "host")
	advanceBoundary(&a, "identity")
	advanceBoundary(&a, "begin")
	if a.LastProvedStage != "host" {
		t.Fatalf("host boundary regressed: %s", a.LastProvedStage)
	}
	advanceBoundary(&a, "send_result")
	if a.LastProvedStage != "send_result" {
		t.Fatal("later result boundary did not advance")
	}
}

func TestTruncatedSnapshotCannotProvePersistedResult(t *testing.T) {
	path := writeSourceFixture(t, []SourceRecord{record("owner-a", "draft", "draft-a", "", `{"owner_id":"owner-a","state":"sent","snapshot":{"invocation_id":"op","state":"sent"}}`)}, "")
	raw, _ := os.ReadFile(path)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw[:len(raw)-1], 0400); err != nil {
		t.Fatal(err)
	}
	r, err := Build(t.Context(), Options{Owner: "owner-a", Draft: "draft-a", Snapshot: path})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(r.Missing, "source_malformed") {
		t.Fatal("truncation was not reported")
	}
	for _, a := range r.Attempts {
		if a.Business.Persisted {
			t.Fatalf("truncated source still proves business state: %+v", a)
		}
	}
}

func TestChangedLedgerCannotSupplyACKOrIdentity(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "operation", ID: "op", IdentityKind: "invocation"}
	r := emptyReport(q)
	a := newAttempt(q, "op")
	a.invocation = "op"
	a.ACK = "pending"
	a.Recovery = "existing_maintenance_ack_eligible"
	r.Attempts = []Attempt{a}
	applyLedger(&r, q, ledgerResult{Gaps: []string{"ledger_source_changed"}, Tasks: []ledgerTask{{ID: "task", Invocation: "op", RequestKey: "key", OwnerScope: digestString(q.Owner), Status: "failed", ACK: "pending"}}}, "ledger")
	if r.Attempts[0].TaskRef != "" || r.Attempts[0].Recovery != "inspect_without_resend" || r.Attempts[0].ACK != "incomplete" {
		t.Fatalf("changed ledger still proves linkage: %+v", r)
	}
}

func TestGatewayCutoffCannotLendReceiptOwnerBinding(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "draft", ID: "draft"}
	rows := []SourceRecord{record("owner", "draft", "draft", "", `{"owner_id":"owner","state":"failed","receipt":{"status":"not_sent","lifecycle":{"operation_id":"op","task_id":"task","terminal_revision":1,"result_digest":"`+strings.Repeat("a", 64)+`"}},"snapshot":{"invocation_id":"op","state":"failed"}}`)}
	r := aggregateSource(q, ReadResult{Records: rows, Gaps: []string{"source_row_limit"}}, "source")
	if len(r.Attempts) != 1 || r.Attempts[0].receiptRevision != 0 || r.Attempts[0].ACK != "incomplete" {
		t.Fatalf("cutoff allowed receipt binding: %+v", r)
	}
}

func TestChangedLogCannotSupplyCauseOrBoundary(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "draft", ID: "draft"}
	r := aggregate(q, []SourceRecord{record("owner", "draft", "draft", "", `{"owner_id":"owner","state":"unknown","snapshot":{"invocation_id":"op","state":"unknown"}}`)}, "fixture")
	r.Sources = []Source{{Ref: "logs", Missing: []string{"log_changed"}}}
	applyLogLines(&r, q, []string{`{"msg":"email send boundary","schema_version":1,"draft_scope_sha256":"` + scopeHash(q.Owner, q.ID) + `","operation_id":"op","stage":"ack","cause_code":"browser_extension_unavailable"}`}, "logs")
	if r.Attempts[0].FirstSafeCause != nil || r.Attempts[0].LastProvedStage != "durable_begin" {
		t.Fatalf("changed log accepted: %+v", r)
	}
}

func TestSameOutcomeReceiptConflictCannotPickHistoricalBinding(t *testing.T) {
	q := Selection{Owner: "owner", Kind: "draft", ID: "draft"}
	receipt := `"receipt":{"status":"sent","lifecycle":{"operation_id":"op","task_id":"task","terminal_revision":1,"result_digest":"` + strings.Repeat("a", 64) + `"}}`
	draft := record("owner", "draft", "draft", "", `{"owner_id":"owner","state":"sent","snapshot":{"invocation_id":"op","state":"sent",`+receipt+`}}`)
	history := record("owner", "send_snapshot", "history", "draft", `{"invocation_id":"op","state":"sent",`+strings.Replace(receipt, `"terminal_revision":1`, `"terminal_revision":2`, 1)+`}`)
	rows := []SourceRecord{draft, history}
	for range 2 {
		r := aggregate(q, rows, "fixture")
		if len(r.Attempts) != 1 || r.Attempts[0].receiptRevision != 0 || r.Attempts[0].LastProvedStage == "business_result" || !contains(r.Missing, "conflicting_business_evidence") {
			t.Fatalf("selected conflicting receipt: %+v", r)
		}
		rows[0], rows[1] = rows[1], rows[0]
	}
}

func TestNodeEnvironmentCannotRunPreloadOrWriteCoverage(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "side-effect")
	preload := filepath.Join(dir, "preload.cjs")
	coverage := filepath.Join(dir, "coverage")
	cache := filepath.Join(dir, "cache")
	quoted, _ := json.Marshal(marker)
	if err := os.WriteFile(preload, []byte(`require('node:fs').writeFileSync(`+string(quoted)+`,'changed')`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	t.Setenv("NODE_V8_COVERAGE", coverage)
	t.Setenv("NODE_COMPILE_CACHE", cache)
	_, err := Build(t.Context(), Options{Owner: "owner", Operation: "op3.1.4." + strings.Repeat("a", 32), IdentityKind: "invocation", Ledger: filepath.Join(dir, "missing.sqlite"), LedgerOwner: "ledger-owner", LedgerPrincipal: "product-owner"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{marker, coverage, cache} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("Node startup created a side effect: %s", filepath.Base(path))
		}
	}
}
