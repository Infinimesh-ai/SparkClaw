package emaildiagnostics

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"time"
)

type ledgerOperation struct {
	Invocation     string                `json:"invocation,omitempty"`
	RequestKey     string                `json:"request_key,omitempty"`
	App            string                `json:"app,omitempty"`
	Command        string                `json:"command,omitempty"`
	TaskID         string                `json:"task_id,omitempty"`
	ReceiptBinding *ledgerReceiptBinding `json:"receipt_binding,omitempty"`
}
type ledgerReceiptBinding struct {
	TaskID           string `json:"task_id"`
	TerminalRevision int64  `json:"terminal_revision"`
	ResultDigest     string `json:"result_digest"`
}
type ledgerTask struct {
	ID               string `json:"id"`
	RequestKey       string `json:"request_key"`
	Invocation       string `json:"invocation"`
	Status           string `json:"status"`
	Reason           string `json:"reason"`
	Effect           int    `json:"effect"`
	Attempt          int    `json:"attempt"`
	Dispatch         string `json:"dispatch"`
	TerminalRevision int64  `json:"terminal_revision"`
	ResultDigest     string `json:"result_digest"`
	ACK              string `json:"ack"`
	Sequence         int64  `json:"sequence"`
	OwnerScope       string `json:"owner_scope"`
	OwnerBinding     string `json:"owner_binding"`
	App              string `json:"app"`
	Command          string `json:"command"`
	Events           []struct {
		Sequence int64  `json:"sequence"`
		Type     string `json:"type"`
		Reason   string `json:"reason"`
		Status   string `json:"status"`
	} `json:"events"`
}
type ledgerResult struct {
	SchemaVersion int          `json:"schema_version"`
	Tasks         []ledgerTask `json:"tasks"`
	Gaps          []string     `json:"gaps"`
	RowsRead      int          `json:"rows_read"`
	BytesRead     int64        `json:"bytes_read"`
}
type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(raw []byte) (int, error) {
	if len(raw) > b.limit-b.Len() {
		return 0, errors.New("diagnostic output limit")
	}
	return b.Buffer.Write(raw)
}

//go:embed ledger_reader.mjs
var ledgerReaderSource string

func readLedger(ctx context.Context, r *Report, q Selection, o Options) {
	if o.Ledger == "" {
		r.gap("ledger_not_supplied")
		return
	}
	rows := remainingRows(r)
	if rows <= 0 {
		r.gap("row_limit")
		return
	}
	operations := []ledgerOperation{}
	for _, a := range r.Attempts {
		if a.invocation == "" && a.requestKey == "" {
			continue
		}
		if len(a.invocation) > 128 || len(a.requestKey) > 128 {
			r.gap("ledger_identity_invalid")
			continue
		}
		operation := ledgerOperation{Invocation: a.invocation, RequestKey: a.requestKey, TaskID: a.taskID}
		if a.receiptRevision > 0 && hex64.MatchString(a.receiptDigest) && !a.boundaryConflict {
			operation.ReceiptBinding = &ledgerReceiptBinding{a.taskID, a.receiptRevision, a.receiptDigest}
		}
		switch a.provider {
		case "gmail":
			operation.App = "mail-gmail"
		case "outlook":
			operation.App = "mail-outlook"
		case "qq_mail":
			operation.App = "mail-qq-mail"
		}
		if a.draftID != "" {
			operation.Command = "send"
		}
		operations = append(operations, operation)
	}
	if len(operations) == 0 {
		r.gap("ledger_identity_unavailable")
		return
	}
	source := Source{Ref: "ledger-1", Kind: "app_cli_ledger", Schema: "ledger_v2", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	defer func() {
		source.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		r.Sources = append(r.Sources, source)
	}()
	gap := func(code string) { code = safeGap(code); source.Missing = append(source.Missing, code); r.gap(code) }
	input := struct {
		Path       string            `json:"path"`
		Principal  string            `json:"principal"`
		Owner      string            `json:"owner"`
		OwnerScope string            `json:"owner_scope"`
		Index      string            `json:"index_directory,omitempty"`
		Operations []ledgerOperation `json:"operations"`
		MaxRows    int               `json:"max_rows"`
		MaxRecord  int               `json:"max_record_bytes"`
	}{o.Ledger, o.LedgerPrincipal, o.LedgerOwner, digestString(q.Owner), o.IndexDirectory, operations, rows, r.Limits.RecordBytes}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > 1<<20 {
		gap("ledger_query_invalid")
		return
	}
	node := o.Node
	if node == "" {
		node = "node"
	}
	cmd := exec.CommandContext(ctx, node, "--no-warnings", "--input-type=module", "--eval", ledgerReaderSource+"\nawait runCLI();")
	// Do not inherit preload, coverage or cache settings before the OS guard.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.Stdin = bytes.NewReader(raw)
	out := &boundedBuffer{limit: r.Limits.OutputBytes}
	cmd.Stdout = out
	cmd.Stderr = &boundedBuffer{limit: 4096}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			gap("query_canceled")
		} else {
			gap("ledger_reader_failed")
		}
		return
	}
	var result ledgerResult
	if json.Unmarshal(out.Bytes(), &result) != nil || result.SchemaVersion != 1 || result.RowsRead < 0 || result.RowsRead > rows || result.BytesRead < 0 {
		gap("ledger_response_invalid")
		return
	}
	source.RowsRead = result.RowsRead
	source.BytesRead = result.BytesRead
	for _, code := range result.Gaps {
		gap(code)
	}
	applyLedger(r, q, result, source.Ref)
}

func applyLedger(r *Report, q Selection, result ledgerResult, source string) {
	for _, gap := range result.Gaps {
		switch gap {
		case "ledger_source_changed", "ledger_read_failed", "ledger_identity_conflict", "ledger_scope_unverified", "legacy_identity_mapping_conflict":
			for i := range r.Attempts {
				disableReceiptProof(&r.Attempts[i])
			}
			if gap == "ledger_source_changed" || gap == "ledger_read_failed" {
				return
			}
		}
	}
	for _, task := range result.Tasks {
		if task.OwnerScope != digestString(q.Owner) {
			r.gap("ledger_scope_mismatch")
			continue
		}
		for i := range r.Attempts {
			a := &r.Attempts[i]
			if !(a.invocation != "" && task.Invocation == a.invocation || a.requestKey != "" && task.RequestKey == a.requestKey) {
				continue
			}
			if a.taskID != "" && a.taskID != task.ID {
				r.gap("receipt_task_conflict")
				continue
			}
			if a.receiptRevision > 0 && (a.receiptRevision != task.TerminalRevision || a.receiptDigest != task.ResultDigest) {
				r.gap("receipt_revision_conflict")
				a.ACK = "incomplete"
				a.ackConflict = true
				a.Recovery = "inspect_without_resend"
			}
			if a.taskID != "" && a.requestKey != "" && a.requestKey != task.RequestKey {
				r.gap("ambiguous_ledger_mapping")
				continue
			}
			a.taskID = task.ID
			a.requestKey = task.RequestKey
			a.TaskRef = scopeHash(q.Owner, task.ID)
			a.RequestKeyRef = scopeHash(q.Owner, task.RequestKey)
			evidence := source + ":" + a.TaskRef
			a.EvidenceRefs = append(a.EvidenceRefs, evidence)
			a.Execution = pick(task.Status, "pending", "prepared", "running", "completed", "failed", "cancelled", "blocked", "uncertain")
			a.OwnerBinding = pick(task.OwnerBinding, "request_owner_scope", "receipt")
			ack := pick(task.ACK, "pending", "acknowledged", "incomplete")
			if !a.ackConflict && a.ACK != "incomplete" && ack != "incomplete" && a.ACK != ack {
				r.gap("ack_evidence_conflict")
				a.ACK = "incomplete"
				a.ackConflict = true
				a.Recovery = "inspect_without_resend"
			} else if !a.ackConflict && a.ACK == "incomplete" {
				a.ACK = ack
			}
			if a.Business.State == "incomplete" {
				a.LastProvedStage = "task_state"
				if task.Status == "uncertain" || task.Dispatch == "released" {
					a.Effect = "unknown"
				}
			}
			if task.Reason != "" {
				a.addCause(Cause{Code: reportSafeCode(task.Reason), Stage: "dispatch", Source: "application", Boundary: "task", EvidenceRefs: []string{evidence}})
			}
			previous := int64(0)
			seen := map[int64]string{}
			for _, event := range task.Events {
				canonical, _ := json.Marshal(event)
				if prior, ok := seen[event.Sequence]; ok {
					if prior != string(canonical) {
						r.gap("conflicting_task_event")
					}
					continue
				}
				seen[event.Sequence] = string(canonical)
				if event.Sequence != previous+1 {
					r.gap("task_event_gap")
				}
				previous = event.Sequence
				if event.Reason != "" {
					a.addCause(Cause{Code: reportSafeCode(event.Reason), Stage: "dispatch", Source: "application", Boundary: "task", EvidenceRefs: []string{evidence + ":" + strconv.FormatInt(event.Sequence, 10)}})
				}
			}
			if previous != task.Sequence {
				r.gap("task_event_gap")
			}
		}
	}
}
