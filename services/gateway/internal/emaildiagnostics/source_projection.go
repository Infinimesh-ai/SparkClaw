package emaildiagnostics

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// Projection is deliberately independent from Store constructors and domain
// methods. It strips private fields before records reach report aggregation.
type sourceField struct {
	scalar string
	fields map[string]sourceField
	array  bool
}

func sourceFields(stringsList, numbers string) map[string]sourceField {
	r := map[string]sourceField{}
	for _, s := range strings.Fields(stringsList) {
		r[s] = sourceField{scalar: "string"}
	}
	for _, s := range strings.Fields(numbers) {
		r[s] = sourceField{scalar: "number"}
	}
	return r
}

var sourceLifecycleFields = func() map[string]sourceField {
	r := sourceFields("operation_id task_id result_digest", "terminal_revision")
	r["acknowledged"] = sourceField{scalar: "boolean"}
	return r
}()
var sourceProofFields = sourceFields("kind invocation_id task_id intent_digest resource_digest binding_digest reason", "schema_version ledger_epoch provider_code")
var sourceReceiptFields = func() map[string]sourceField {
	r := sourceFields("provider status", "browser_credential_generation script_revision")
	r["lifecycle"] = sourceField{fields: sourceLifecycleFields}
	r["not_sent"] = sourceField{fields: sourceProofFields}
	return r
}()
var sourceSnapshotFields = func() map[string]sourceField {
	r := sourceFields("invocation_id state error_code mailbox_id updated_at user_resolution", "version")
	r["receipt"] = sourceField{fields: sourceReceiptFields}
	return r
}()
var sourceRecordFields = func() map[string]map[string]sourceField {
	draft := sourceFields("id owner_id mailbox_id state error_code updated_at", "version mailbox_generation")
	draft["snapshot"] = sourceField{fields: sourceSnapshotFields}
	draft["receipt"] = sourceField{fields: sourceReceiptFields}
	failure := sourceFields("id owner_id mailbox_id mail_id stage scope error_code state provider_mode first_failed_at last_failed_at last_attempt_at resolved_at suppressed_at acknowledged_at", "binding_generation reader_revision checkpoint_revision consecutive_failures confirmation_count")
	failure["attempts"] = sourceField{fields: sourceFields("invocation_id started_at completed_at outcome error_code trigger", ""), array: true}
	return map[string]map[string]sourceField{
		"draft": draft, "send_snapshot": sourceSnapshotFields,
		"mailbox":      sourceFields("id owner_id provider", "binding_generation"),
		"job":          sourceFields("id owner_id kind target_id mailbox_id state error_code created_at updated_at next_attempt_at lease_expires_at round_started_at round_finished_at retired_at retired_from_state retirement_reason", "binding_generation generation attempt max_attempts"),
		"sync_failure": failure,
	}
}()

func ProjectRecord(r SourceRecord) (SourceRecord, bool) {
	if len(r.Data) > 1<<20 || !sourceUniqueJSON(r.Data) {
		return SourceRecord{}, false
	}
	fields, ok := sourceRecordFields[r.Kind]
	if !ok {
		return SourceRecord{}, false
	}
	data, ok := projectSourceObject(r.Data, fields)
	if !ok {
		return SourceRecord{}, false
	}
	r.Data = data
	if id := sourceString(r, "id"); id != "" && id != r.ID {
		return SourceRecord{}, false
	}
	if owner := sourceString(r, "owner_id"); owner != "" && owner != r.Owner {
		return SourceRecord{}, false
	}
	return r, true
}

// Duplicate keys make retained evidence ambiguous (for example two different
// states or owners). Reject them rather than letting encoding/json pick a winner.
// This runs only after the streaming reader has enforced its record-size bound.
func sourceUniqueJSON(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 128 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		if delim == '{' {
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return false
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return false
				}
				seen[s] = true
				if !value(depth + 1) {
					return false
				}
			}
			end, e := d.Token()
			return e == nil && end == json.Delim('}')
		}
		if delim == '[' {
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
			end, e := d.Token()
			return e == nil && end == json.Delim(']')
		}
		return false
	}
	if !value(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
func projectSourceObject(raw json.RawMessage, fields map[string]sourceField) (json.RawMessage, bool) {
	var src map[string]json.RawMessage
	if json.Unmarshal(raw, &src) != nil || src == nil {
		return nil, false
	}
	dst := map[string]json.RawMessage{}
	for name, rule := range fields {
		value, exists := src[name]
		if !exists || string(value) == "null" {
			continue
		}
		if rule.fields != nil {
			if name == "receipt" && !sourceReceiptConsistent(value) {
				return nil, false
			}
			if rule.array {
				var values []json.RawMessage
				if json.Unmarshal(value, &values) != nil {
					return nil, false
				}
				out := make([]json.RawMessage, 0, len(values))
				for _, item := range values {
					projected, ok := projectSourceObject(item, rule.fields)
					if !ok {
						return nil, false
					}
					out = append(out, projected)
				}
				dst[name], _ = json.Marshal(out)
			} else {
				projected, ok := projectSourceObject(value, rule.fields)
				if !ok {
					return nil, false
				}
				dst[name] = projected
			}
			continue
		}
		var decoded any
		dec := json.NewDecoder(strings.NewReader(string(value)))
		dec.UseNumber()
		if dec.Decode(&decoded) != nil {
			return nil, false
		}
		valid := false
		switch rule.scalar {
		case "string":
			_, valid = decoded.(string)
		case "number":
			_, valid = decoded.(json.Number)
		case "boolean":
			_, valid = decoded.(bool)
		}
		if !valid {
			return nil, false
		}
		dst[name] = value
	}
	result, err := json.Marshal(dst)
	return result, err == nil
}

func sourceReceiptConsistent(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	var status string
	_ = json.Unmarshal(value["status"], &status)
	if status != "not_sent" {
		return true
	}
	for _, name := range []string{"provider_message_id", "provider_thread_id"} {
		field, exists := value[name]
		if !exists || string(field) == "null" {
			continue
		}
		var id string
		if json.Unmarshal(field, &id) != nil || id != "" {
			return false
		}
	}
	return true
}
