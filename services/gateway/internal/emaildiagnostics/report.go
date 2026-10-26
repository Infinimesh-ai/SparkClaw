// Package emaildiagnostics reads retained evidence without constructing any
// business service. Unknown and absent evidence never authorize a write.
package emaildiagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type Limits struct {
	WallMS        int   `json:"wall_ms"`
	Rows          int   `json:"rows"`
	SnapshotBytes int64 `json:"snapshot_bytes"`
	LogBytes      int64 `json:"log_bytes"`
	RecordBytes   int   `json:"record_bytes"`
	OutputBytes   int   `json:"output_bytes"`
}
type Options struct {
	Owner, Draft, Operation, Job, IdentityKind                                  string
	Snapshot, PostgresDSN, Ledger, LedgerPrincipal, LedgerOwner, IndexDirectory string
	Node, ReleaseManifest                                                       string
	Logs                                                                        []string
	Limits                                                                      Limits
}
type Source struct {
	Ref        string   `json:"ref"`
	Kind       string   `json:"kind"`
	Schema     string   `json:"schema,omitempty"`
	StartedAt  string   `json:"started_at"`
	FinishedAt string   `json:"finished_at"`
	BytesRead  int64    `json:"bytes_read"`
	RowsRead   int      `json:"rows_read"`
	Scan       bool     `json:"scan"`
	Missing    []string `json:"missing,omitempty"`
}
type Version struct {
	Release       string `json:"release,omitempty"`
	SourceCommit  string `json:"source_commit,omitempty"`
	RuntimeDigest string `json:"runtime_digest,omitempty"`
	EvidenceRef   string `json:"evidence_ref,omitempty"`
}
type Versions struct {
	Expected       Version `json:"expected"`
	ObservedLoaded string  `json:"observed_loaded"`
}
type Business struct {
	State     string `json:"state"`
	Persisted bool   `json:"persisted"`
	Result    string `json:"result"`
	ErrorCode string `json:"error_code,omitempty"`
}
type Cause struct {
	Code         string   `json:"code"`
	Stage        string   `json:"stage"`
	Source       string   `json:"source"`
	Boundary     string   `json:"boundary"`
	EvidenceRefs []string `json:"evidence_refs"`
}
type Attempt struct {
	Ref                                                      string   `json:"ref"`
	InvocationRef                                            string   `json:"invocation_ref,omitempty"`
	RequestKeyRef                                            string   `json:"request_key_ref,omitempty"`
	TaskRef                                                  string   `json:"task_ref,omitempty"`
	Execution                                                string   `json:"execution"`
	Business                                                 Business `json:"business"`
	ObservedBusiness                                         string   `json:"observed_business"`
	Effect                                                   string   `json:"effect"`
	ACK                                                      string   `json:"ack"`
	Cleanup                                                  string   `json:"cleanup"`
	LastProvedStage                                          string   `json:"last_proved_stage"`
	FirstSafeCause                                           *Cause   `json:"first_safe_cause,omitempty"`
	CauseCandidates                                          []Cause  `json:"cause_candidates,omitempty"`
	Recovery                                                 string   `json:"recovery"`
	IdentityMode                                             string   `json:"identity_mode,omitempty"`
	OwnerBinding                                             string   `json:"owner_binding,omitempty"`
	EvidenceRefs                                             []string `json:"evidence_refs"`
	invocation, requestKey, taskID, draftID, jobID, provider string
	jobAttempt                                               int
	jobGeneration                                            int64
	observations                                             map[string]string
	boundaryConflict                                         bool
	ackConflict                                              bool
	receiptRevision                                          int64
	receiptDigest                                            string
}
type HealthSnapshot struct {
	Component     string            `json:"component"`
	State         string            `json:"state"`
	Reason        string            `json:"reason"`
	EvidenceRef   string            `json:"evidence_ref"`
	Coverage      string            `json:"coverage"`
	Counters      map[string]uint64 `json:"counters,omitempty"`
	LastWrittenAt string            `json:"last_written_at,omitempty"`
}
type Report struct {
	SchemaVersion     int                 `json:"schema_version"`
	ReadOnly          bool                `json:"read_only"`
	QueryKind         string              `json:"query_kind"`
	QueryRef          string              `json:"query_ref"`
	Evidence          string              `json:"evidence"`
	Coverage          string              `json:"coverage"`
	Limits            Limits              `json:"limits"`
	Sources           []Source            `json:"sources"`
	Versions          Versions            `json:"versions"`
	Attempts          []Attempt           `json:"attempts"`
	Health            []HealthSnapshot    `json:"health,omitempty"`
	Missing           []string            `json:"missing"`
	OmittedAttempts   int                 `json:"omitted_attempts,omitempty"`
	UnstartedEvidence []UnstartedEvidence `json:"unstarted_evidence,omitempty"`
}

// No operation identity exists for these failures. Keep them separate from
// every historical attempt, even if they refer to the same draft.
type UnstartedEvidence struct {
	Boundary     string `json:"boundary"`
	IdentityMode string `json:"identity_mode"`
	Cause        Cause  `json:"cause"`
	EvidenceRef  string `json:"evidence_ref"`
	Correlation  string `json:"correlation"`
}

func scopeHash(owner, id string) string {
	sum := sha256.Sum256([]byte(owner + "\x00" + id))
	return hex.EncodeToString(sum[:])
}
func digestString(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}
func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func (r *Report) gap(s string) {
	if !contains(r.Missing, s) {
		r.Missing = append(r.Missing, s)
	}
}
func pick(value string, values ...string) string {
	for _, s := range values {
		if value == s {
			return value
		}
	}
	return "incomplete"
}
func emptyReport(q Selection) Report {
	return Report{SchemaVersion: 1, ReadOnly: true, QueryKind: q.Kind, QueryRef: scopeHash(q.Owner, q.Kind+":"+q.ID), Evidence: "incomplete", Coverage: "instrumented_boundaries_only", Versions: Versions{ObservedLoaded: "incomplete"}, Attempts: []Attempt{}, Sources: []Source{}, Missing: []string{"observed_loaded_version_missing", "cleanup_evidence_missing"}}
}
func newAttempt(q Selection, id string) Attempt {
	return Attempt{Ref: scopeHash(q.Owner, id), Execution: "incomplete", Business: Business{State: "incomplete", Result: "incomplete"}, ObservedBusiness: "incomplete", Effect: "incomplete", ACK: "incomplete", Cleanup: "incomplete", LastProvedStage: "incomplete", Recovery: "inspect_without_resend", EvidenceRefs: []string{}, observations: map[string]string{}}
}

func aggregate(q Selection, records []SourceRecord, ref string) Report {
	r := emptyReport(q)
	providers := map[string]string{}
	for _, row := range records {
		if row.Owner == q.Owner && row.Kind == "mailbox" {
			var box struct {
				Provider string `json:"provider"`
			}
			if json.Unmarshal(row.Data, &box) == nil {
				providers[row.ID] = pick(box.Provider, app.EmailProviderQQMail, app.EmailProviderGmail, app.EmailProviderOutlook)
			}
		}
	}
	for _, row := range records {
		if row.Owner != q.Owner {
			continue
		}
		evidence := ref + ":" + scopeHash(q.Owner, row.Kind+":"+row.ID)
		if q.Kind == "operation" && row.Kind == "sync_failure" {
			var failure app.EmailSyncFailure
			if json.Unmarshal(row.Data, &failure) != nil || failure.OwnerID != q.Owner {
				r.gap("record_scope_invalid")
				continue
			}
			for _, attempt := range failure.Attempts {
				if attempt.InvocationID != q.ID {
					continue
				}
				a := newAttempt(q, attempt.InvocationID)
				a.invocation = attempt.InvocationID
				a.InvocationRef = scopeHash(q.Owner, a.invocation)
				a.provider = providers[failure.MailboxID]
				a.Business = Business{State: pick(attempt.Outcome, "succeeded", "failed", "retrying"), Persisted: true, Result: "incomplete", ErrorCode: reportSafeCode(attempt.ErrorCode)}
				a.LastProvedStage = "collection_result"
				a.EvidenceRefs = append(a.EvidenceRefs, evidence)
				if attempt.ErrorCode != "" {
					a.addCause(Cause{Code: reportSafeCode(attempt.ErrorCode), Stage: "collection", Source: "application", Boundary: "collection", EvidenceRefs: []string{evidence}})
				}
				duplicate := false
				for i := range r.Attempts {
					old := &r.Attempts[i]
					if old.invocation == a.invocation {
						duplicate = true
						if old.Business != a.Business {
							r.gap("conflicting_collection_evidence")
							invalidateBusiness(old)
						}
					}
				}
				if !duplicate {
					r.Attempts = append(r.Attempts, a)
				}
			}
			continue
		}
		if q.Kind == "job" && row.Kind == "job" && row.ID == q.ID {
			var job app.EmailJob
			if json.Unmarshal(row.Data, &job) != nil || job.OwnerID != q.Owner {
				r.gap("record_scope_invalid")
				continue
			}
			a := newAttempt(q, "job:"+row.ID)
			a.jobID = row.ID
			a.jobAttempt = job.Attempt
			a.jobGeneration = job.Generation
			a.Execution = pick(job.State, app.EmailJobQueued, app.EmailJobRunning, app.EmailJobRetryWait, app.EmailJobSucceeded, app.EmailJobFailed, app.EmailJobPaused, app.EmailJobRetired)
			a.LastProvedStage = "worker_state"
			a.EvidenceRefs = append(a.EvidenceRefs, evidence)
			a.Business.ErrorCode = reportSafeCode(job.ErrorCode)
			r.Attempts = append(r.Attempts, a)
			r.gap("analysis_attempt_index_unavailable")
			continue
		}
		if row.Kind != "draft" && row.Kind != "send_snapshot" {
			continue
		}
		if q.Kind == "draft" && !((row.Kind == "draft" && row.ID == q.ID) || (row.Kind == "send_snapshot" && row.Parent == q.ID)) {
			continue
		}
		var snapshot store.EmailDraftSnapshot
		var d store.EmailDraft
		if row.Kind == "draft" {
			if json.Unmarshal(row.Data, &d) != nil || d.OwnerID != q.Owner {
				r.gap("record_scope_invalid")
				continue
			}
			if d.Snapshot != nil {
				snapshot = *d.Snapshot
			}
			if snapshot.Receipt == nil {
				snapshot.Receipt = d.Receipt
			}
			if snapshot.State == "" {
				snapshot.State = d.State
			}
		} else if json.Unmarshal(row.Data, &snapshot) != nil {
			r.gap("record_invalid")
			continue
		}
		if q.Kind == "operation" && q.ID != snapshot.InvocationID {
			continue
		}
		if q.Kind == "job" {
			continue
		}
		id := snapshot.InvocationID
		if id == "" {
			id = "draft:" + row.ID
		}
		a := newAttempt(q, id)
		a.invocation = snapshot.InvocationID
		if a.invocation != "" {
			a.InvocationRef = scopeHash(q.Owner, a.invocation)
		}
		a.draftID = row.Parent
		if row.Kind == "draft" {
			a.draftID = row.ID
		}
		a.provider = providers[snapshot.MailboxID]
		if d.MailboxID != "" {
			a.provider = providers[d.MailboxID]
		}
		a.Business.State = pick(snapshot.State, "draft", "sending", "sent", "failed", "unknown")
		a.Business.Persisted = true
		a.Business.ErrorCode = reportSafeCode(snapshot.ErrorCode)
		if row.Kind == "draft" {
			a.Business.State = pick(d.State, "draft", "sending", "sent", "failed", "unknown")
			a.Business.ErrorCode = reportSafeCode(d.ErrorCode)
		}
		a.LastProvedStage = "durable_draft"
		if a.invocation != "" {
			a.LastProvedStage = "durable_begin"
		}
		if a.Business.State == "unknown" || a.Business.State == "sending" {
			a.Effect = "unknown"
		}
		receipt := snapshot.Receipt
		if receipt != nil {
			a.Business.Result = pick(receipt.Status, "sent", "not_sent", "unknown")
			lifecycle := receipt.Lifecycle
			if lifecycle != nil && lifecycle.OperationID == a.invocation && lifecycle.TaskID != "" && lifecycle.TerminalRevision > 0 && hex64.MatchString(lifecycle.ResultDigest) {
				a.taskID = lifecycle.TaskID
				a.receiptRevision = lifecycle.TerminalRevision
				a.receiptDigest = lifecycle.ResultDigest
				a.TaskRef = scopeHash(q.Owner, lifecycle.TaskID)
				a.ACK = "pending"
				if lifecycle.Acknowledged {
					a.ACK = "acknowledged"
				}
			} else if lifecycle != nil {
				r.gap("receipt_identity_invalid")
			}
			proof := receipt.NotSent
			if a.Business.State == "failed" && receipt.Status == "not_sent" && proof.ValidFor(a.invocation, a.provider) && receipt.Provider == a.provider && lifecycle != nil && lifecycle.OperationID == a.invocation && lifecycle.TaskID == proof.TaskID {
				a.Effect = "not_sent"
				a.LastProvedStage = "business_result"
				if a.ACK == "pending" && proof.RejectionErrorCode(a.provider) != "" {
					a.Recovery = "existing_maintenance_ack_eligible"
				}
			} else if a.Business.State == "sent" && receipt.Status == "sent" {
				a.Effect = "sent"
				a.LastProvedStage = "business_result"
			}
		}
		a.EvidenceRefs = append(a.EvidenceRefs, evidence)
		duplicate := -1
		for i := range r.Attempts {
			if r.Attempts[i].invocation == a.invocation && (a.invocation != "" || r.Attempts[i].draftID == a.draftID) {
				duplicate = i
				break
			}
		}
		if duplicate >= 0 {
			old := &r.Attempts[duplicate]
			if old.Business != a.Business || old.ACK != a.ACK || old.Effect != a.Effect || old.taskID != a.taskID || old.receiptRevision != a.receiptRevision || old.receiptDigest != a.receiptDigest || old.draftID != a.draftID {
				r.gap("conflicting_business_evidence")
				invalidateBusiness(old)
			}
			old.EvidenceRefs = append(old.EvidenceRefs, evidence)
		} else {
			r.Attempts = append(r.Attempts, a)
		}
	}
	if len(r.Attempts) == 0 {
		r.gap("gateway_record_not_found")
	}
	sort.Slice(r.Attempts, func(i, j int) bool { return r.Attempts[i].Ref < r.Attempts[j].Ref })
	return r
}

func invalidateBusiness(a *Attempt) {
	a.Business = Business{State: "incomplete", Result: "incomplete"}
	a.Effect = "unknown"
	disableReceiptProof(a)
	a.LastProvedStage = "incomplete"
	a.boundaryConflict = true
	a.ackConflict = true
	a.FirstSafeCause = nil
}

var hex64 = regexp.MustCompile(`^[a-f0-9]{64}$`)
var hex40 = regexp.MustCompile(`^[a-f0-9]{40}$`)
var releaseName = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+-sparkclaw\.[0-9]+$`)

func reportSafeCode(code string) string {
	if code == "" {
		return ""
	}
	if known := emailautomation.DiagnosticCode(&emailautomation.Error{Code: app.ToolErrorCode(code)}); known != "" {
		return known
	}
	switch code {
	case "unclassified", "canceled", "timeout", "browser_invalid_request", "browser_not_configured", "browser_controller_unavailable", "browser_extension_unavailable", "browser_extension_rejected", "browser_busy", "browser_cleanup_pending", "browser_credential_stale", "browser_controller_stale", "browser_session_not_found", "browser_session_stale", "browser_page_stale", "browser_operation_unavailable", "browser_script_unavailable", "browser_script_timeout", "browser_vault_unavailable",
		"store_not_found", "store_conflict", "store_invalid", "store_canceled", "store_timeout", "store_unavailable", "store_durability_failed", "store_unknown_outcome", "store_corrupt", "store_internal",
		app.EmailSendRejectedLoginRequired, app.EmailSendRejectedParameter, app.EmailSendRejectedTemporarily, app.EmailSendTemplateDrift,
		"email_source_missing", "email_source_integrity", "email_source_invalid", "email_source_recovery_pending", "email_model_unavailable", "email_model_mock_unqualified", "email_model_output_invalid", "email_model_evidence_invalid", "email_model_input_limit", "email_representation_missing", "email_mail_not_found", "email_processing_failed", "file_not_found", "file_permission_denied",
		"BROWSER_EXTENSION_UNAVAILABLE", "EXECUTOR_RESTARTED", "PROVIDER_LOGIN_REJECTED", "PROVIDER_PARAMETER_REJECTED", "PROVIDER_TEMPORARILY_REJECTED", "BACKEND_UNAVAILABLE", "BACKEND_EXECUTION_FAILED", "BACKEND_TIMEOUT", "CONTROL_TIMEOUT", "trace_write_failed":
		return code
	default:
		return "unclassified"
	}
}

func reportStage(stage string) string {
	return pick(stage, "identity", "begin", "send_result", "persist_result", "ack", "operation", "controller_request", "script_result", "collection", "split_collection", "admission", "split_admission", "contract", "split_contract", "target", "recovery", "lease_renewal", "cancellation", "deadline", "parse", "analysis", "presentation", "dispatch", "reserve", "create", "call", "host", "trace_write", "artifact_write", "local_write", "local_index_write", "encode", "context", "maintenance", "attach", "create_page", "read", "download", "hookActivate", "navigate", "inspect", "act", "currentURL", "count", "attribute", "value", "text", "lines", "readMany", "visible", "enabled", "evaluate", "click", "runReadCode", "runCodeRequest", "fill", "focus", "press", "waitFor", "waitMilliseconds")
}
func reportCauseSource(source string) string {
	return pick(source, "store", "browser_control", "context", "automation", "application", "filesystem", "source", "model", "presentation", "internal", "host", "trace")
}
func safeIdentityMode(mode string) string {
	if mode == "" {
		return ""
	}
	return pick(mode, "epoch", "legacy_unsupported", "legacy_epoch_failed", "epoch_allocation_failed")
}
func (a *Attempt) addCause(c Cause) {
	for i := range a.CauseCandidates {
		v := &a.CauseCandidates[i]
		if v.Code == c.Code && v.Stage == c.Stage && v.Source == c.Source && v.Boundary == c.Boundary {
			v.EvidenceRefs = append(v.EvidenceRefs, c.EvidenceRefs...)
			return
		}
	}
	a.CauseCandidates = append(a.CauseCandidates, c)
}
func selectFirstCauses(r *Report) {
	ranks := map[string]int{"identity": 1, "begin": 2, "admission": 2, "collection": 3, "send_result": 3, "persist_result": 4, "ack": 5}
	for i := range r.Attempts {
		a := &r.Attempts[i]
		a.FirstSafeCause = nil
		if len(a.CauseCandidates) == 0 {
			continue
		}
		first := -1
		rank := 100
		ambiguous := false
		for n, c := range a.CauseCandidates {
			v, ok := ranks[c.Boundary]
			if !ok {
				ambiguous = true
				continue
			}
			if v < rank {
				rank = v
				first = n
			} else if v == rank {
				ambiguous = true
			}
		}
		if a.boundaryConflict {
			ambiguous = true
		}
		if len(a.CauseCandidates) == 1 {
			first = 0
			ambiguous = false
			if a.boundaryConflict {
				ambiguous = true
			}
		}
		if first >= 0 && !ambiguous {
			c := a.CauseCandidates[first]
			a.FirstSafeCause = &c
		} else {
			r.gap("cause_order_incomplete")
		}
	}
}

func safeGap(s string) string {
	if len(s) > 96 || strings.Trim(s, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" {
		return "source_unavailable"
	}
	return s
}

func disableReceiptProof(a *Attempt) {
	a.ACK = "incomplete"
	a.ackConflict = true
	a.Recovery = "inspect_without_resend"
	a.receiptRevision = 0
	a.receiptDigest = ""
}
