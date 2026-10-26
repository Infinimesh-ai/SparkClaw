package emaildiagnostics

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type logEvent struct {
	fields map[string]any
	ref    string
}

var logPair = regexp.MustCompile(`(?:^| )([a-z_][a-z0-9_]*)=("(?:[^"\\]|\\.)*"|[^ ]+)`)
var acceptedLogFields = map[string]bool{"attempted": true, "written": true, "dropped": true, "lost": true, "metadata_rejected": true, "last_written_at": true, "event": true, "msg": true, "schema_version": true, "draft_scope_sha256": true, "operation_id": true, "invocation_id": true, "stage": true, "phase": true, "cause_code": true, "cause_stage": true, "cause_source": true, "code": true, "business_outcome": true, "business_persisted": true, "identity_mode": true, "job_id": true, "attempt": true, "generation": true, "task_id": true, "operation_key_sha256": true, "correlation": true, "component": true, "state": true, "reason": true, "coverage": true, "evidence": true}

func parseLog(line string, ref string) (logEvent, bool) {
	fields := map[string]any{}
	if strings.HasPrefix(strings.TrimSpace(line), "{") && !sourceUniqueJSON([]byte(line)) {
		return logEvent{}, false
	}
	if json.Unmarshal([]byte(line), &fields) != nil {
		fields = map[string]any{}
		for _, msg := range []string{"email send boundary", "email collection failed", "email job failed", "latency trace health"} {
			if strings.Contains(line, " INFO "+msg+" ") || strings.Contains(line, " WARN "+msg+" ") {
				fields["msg"] = msg
				break
			}
		}
		if len(fields) == 0 {
			return logEvent{}, false
		}
		for _, pair := range logPair.FindAllStringSubmatch(line, -1) {
			if !acceptedLogFields[pair[1]] {
				continue
			}
			if _, duplicate := fields[pair[1]]; duplicate {
				return logEvent{}, false
			}
			value := pair[2]
			if strings.HasPrefix(value, `"`) {
				if unquoted, err := strconv.Unquote(value); err == nil {
					fields[pair[1]] = unquoted
				}
				continue
			}
			switch value {
			case "true":
				fields[pair[1]] = true
			case "false":
				fields[pair[1]] = false
			default:
				if n, err := strconv.ParseFloat(value, 64); err == nil {
					fields[pair[1]] = n
				} else {
					fields[pair[1]] = value
				}
			}
		}
	}
	for key, value := range fields {
		if !acceptedLogFields[key] {
			delete(fields, key)
			continue
		}
		if s, ok := value.(string); ok && len(s) > 256 {
			delete(fields, key)
		}
	}
	name := str(fields, "event")
	if name == "" {
		name = str(fields, "msg")
	}
	switch name {
	case "email send boundary", "email collection failed", "email job failed", "latency_trace_health", "latency trace health", "application_host_failed":
		return logEvent{fields: fields, ref: ref}, true
	default:
		return logEvent{}, false
	}
}
func str(m map[string]any, k string) string { v, _ := m[k].(string); return v }
func number(m map[string]any, k string) int64 {
	v, ok := m[k].(float64)
	if !ok {
		return -1
	}
	if v < 0 || v > 9007199254740991 || v != float64(int64(v)) {
		return -1
	}
	return int64(v)
}

func readLogs(ctx context.Context, r *Report, paths []string) []logEvent {
	events := []logEvent{}
	remaining := r.Limits.LogBytes
	rows := remainingRows(r)
	for index, path := range paths {
		ref := "logs-" + strconv.Itoa(index+1)
		source := Source{Ref: ref, Kind: "retained_log", Schema: "boundary_v1", StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Scan: true}
		gap := func(s string) { source.Missing = append(source.Missing, s); r.gap(s) }
		if ctx.Err() != nil {
			gap("query_canceled")
		} else if remaining <= 0 {
			gap("log_byte_limit")
		} else if rows <= 0 {
			gap("row_limit")
		} else {
			info, statErr := os.Stat(path)
			var file *os.File
			var err error
			if statErr != nil || !info.Mode().IsRegular() {
				err = io.ErrUnexpectedEOF
			} else {
				file, err = os.Open(path)
			}
			if err != nil {
				gap("log_unavailable")
			} else {
				before, statErr := file.Stat()
				if statErr != nil || !before.Mode().IsRegular() {
					gap("log_unavailable")
				} else {
					limited := &io.LimitedReader{R: file, N: remaining}
					scan := bufio.NewScanner(limited)
					scan.Buffer(make([]byte, min(4096, r.Limits.RecordBytes)), r.Limits.RecordBytes)
					line := 0
					for scan.Scan() {
						line++
						if ctx.Err() != nil {
							gap("query_canceled")
							break
						}
						if e, ok := parseLog(scan.Text(), ref+":"+strconv.Itoa(line)); ok {
							if rows <= 0 {
								gap("row_limit")
								break
							}
							events = append(events, e)
							source.RowsRead++
							rows--
						} else if strings.Contains(scan.Text(), "email send boundary") || strings.Contains(scan.Text(), "email collection failed") || strings.Contains(scan.Text(), "email job failed") || strings.Contains(scan.Text(), "application_host_failed") || strings.Contains(scan.Text(), "latency_trace_health") {
							gap("log_record_invalid")
						}
					}
					if scan.Err() != nil {
						gap("log_record_invalid_or_oversized")
					}
					source.BytesRead = remaining - limited.N
					remaining = limited.N
					if limited.N == 0 && before.Size() >= source.BytesRead {
						gap("log_byte_limit")
					}
					after, err := file.Stat()
					current, pathErr := os.Stat(path)
					if err != nil || pathErr != nil || !os.SameFile(before, current) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
						gap("log_changed")
					}
				}
				file.Close()
			}
		}
		source.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		r.Sources = append(r.Sources, source)
	}
	if len(paths) == 0 {
		r.gap("logs_not_supplied")
	}
	return events
}
func applyLogLines(r *Report, q Selection, lines []string, source string) {
	events := []logEvent{}
	for i, line := range lines {
		if e, ok := parseLog(line, source+":"+strconv.Itoa(i+1)); ok {
			events = append(events, e)
		}
	}
	applyEvents(r, q, events)
}
func applyEvents(r *Report, q Selection, events []logEvent) {
	for _, event := range events {
		changed := false
		for _, source := range r.Sources {
			if strings.HasPrefix(event.ref, source.Ref+":") && contains(source.Missing, "log_changed") {
				changed = true
				break
			}
		}
		if changed {
			continue
		}
		m := event.fields
		name := str(m, "event")
		if name == "" {
			name = str(m, "msg")
		}
		if name != "application_host_failed" && number(m, "schema_version") != 1 {
			r.gap("log_schema_unsupported")
			continue
		}
		if name == "email send boundary" && q.Kind == "draft" && str(m, "operation_id") == "" && str(m, "draft_scope_sha256") == scopeHash(q.Owner, q.ID) && str(m, "business_outcome") == "not_started" {
			boundary := pick(str(m, "stage"), "identity", "begin")
			if boundary != "incomplete" {
				r.UnstartedEvidence = append(r.UnstartedEvidence, UnstartedEvidence{Boundary: boundary, IdentityMode: safeIdentityMode(str(m, "identity_mode")), Cause: Cause{Code: reportSafeCode(str(m, "cause_code")), Stage: reportStage(str(m, "cause_stage")), Source: reportCauseSource(str(m, "cause_source")), Boundary: boundary, EvidenceRefs: []string{event.ref}}, EvidenceRef: event.ref, Correlation: "attempt_identity_unavailable"})
				r.gap("unstarted_attempt_identity_missing")
			}
			continue
		}
		if name == "latency_trace_health" || name == "latency trace health" {
			if number(m, "schema_version") != 1 {
				r.gap("log_schema_unsupported")
				continue
			}
			component := pick(str(m, "component"), "go_latencytrace", "desktop_latencytrace")
			if component == "incomplete" {
				continue
			}
			r.Health = append(r.Health, HealthSnapshot{Component: component, State: pick(str(m, "state"), "enabled", "disabled", "degraded"), Reason: pick(str(m, "reason"), "recording", "not_enabled", "initialization_failed", "invalid_metadata", "write_failed", "queue_full", "size_limit", "record_limit", "health_observer_failed", "close_failed"), EvidenceRef: event.ref, Coverage: "instrumented_boundaries_only"})
			health := &r.Health[len(r.Health)-1]
			health.Counters = map[string]uint64{}
			for _, key := range []string{"attempted", "written", "dropped", "lost", "metadata_rejected"} {
				if _, exists := m[key]; exists {
					if value := number(m, key); value >= 0 {
						health.Counters[key] = uint64(value)
					} else {
						r.gap("health_counter_invalid")
					}
				}
			}
			if at, err := time.Parse(time.RFC3339Nano, str(m, "last_written_at")); err == nil {
				health.LastWrittenAt = at.UTC().Format(time.RFC3339Nano)
			}
			r.gap("sink_history_incomplete")
			continue
		}
		for i := range r.Attempts {
			a := &r.Attempts[i]
			boundary := ""
			matched := false
			switch name {
			case "email send boundary":
				if number(m, "schema_version") != 1 {
					continue
				}
				matched = a.draftID != "" && str(m, "draft_scope_sha256") == scopeHash(q.Owner, a.draftID) && str(m, "operation_id") == a.invocation && a.invocation != ""
				boundary = reportStage(str(m, "stage"))
				if matched {
					if mode := safeIdentityMode(str(m, "identity_mode")); mode != "" {
						if a.IdentityMode != "" && a.IdentityMode != mode {
							r.gap("conflicting_identity_evidence")
						} else {
							a.IdentityMode = mode
						}
					}
					observed := pick(str(m, "business_outcome"), "sending", "sent", "failed", "unknown", "uncommitted", "not_started")
					if observed != "incomplete" {
						if prior, ok := a.observations[boundary]; ok && prior != observed {
							r.gap("conflicting_boundary_evidence")
							a.boundaryConflict = true
							disableReceiptProof(a)
							a.observations[boundary] = "incomplete"
						} else {
							a.observations[boundary] = observed
						}
						if boundary == "send_result" {
							a.ObservedBusiness = a.observations[boundary]
						}
					}
				}
			case "email job failed", "email collection failed":
				if number(m, "schema_version") != 1 || a.jobID == "" || str(m, "job_id") != a.jobID || number(m, "attempt") != int64(a.jobAttempt) {
					continue
				}
				if _, exists := m["generation"]; !exists {
					r.gap("collection_generation_missing")
				}
				// Existing v1 job logs carry no owner binding. Job IDs are
				// scoped by the Store; equal IDs alone do not establish owner.
				r.gap("job_log_owner_binding_missing")
				continue
			case "application_host_failed":
				matched = a.taskID != "" && a.requestKey != "" && str(m, "task_id") == a.taskID && str(m, "operation_key_sha256") == digestString(a.requestKey) && str(m, "correlation") == "complete"
				boundary = "host"
			}
			if !matched {
				continue
			}
			advanceBoundary(a, boundary)
			a.EvidenceRefs = append(a.EvidenceRefs, event.ref)
			code := str(m, "cause_code")
			stage := str(m, "cause_stage")
			source := str(m, "cause_source")
			if name == "application_host_failed" {
				code = str(m, "code")
				stage = str(m, "phase")
				source = "host"
			}
			if stage == "" {
				stage = boundary
			}
			if code != "" {
				a.addCause(Cause{Code: reportSafeCode(code), Stage: reportStage(stage), Source: reportCauseSource(source), Boundary: boundary, EvidenceRefs: []string{event.ref}})
			}
		}
	}
	selectFirstCauses(r)
}

func advanceBoundary(a *Attempt, boundary string) {
	if boundary == "host" && (a.LastProvedStage == "incomplete" || a.LastProvedStage == "task_state" || a.LastProvedStage == "durable_begin") {
		a.LastProvedStage = "host"
		return
	}
	ranks := map[string]int{"durable_draft": 1, "identity": 2, "begin": 3, "durable_begin": 3, "host": 3, "send_result": 4, "collection_result": 4, "persist_result": 5, "business_result": 5, "ack": 6}
	next, ok := ranks[boundary]
	if ok && next > ranks[a.LastProvedStage] {
		a.LastProvedStage = boundary
	}
}
