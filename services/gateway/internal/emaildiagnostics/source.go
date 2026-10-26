package emaildiagnostics

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type Selection struct{ Owner, Kind, ID, IdentityKind string }
type ReadLimits struct {
	Rows        int
	Bytes       int64
	RecordBytes int
}
type SourceRecord struct {
	Owner   string          `json:"owner"`
	Kind    string          `json:"kind"`
	ID      string          `json:"id"`
	Parent  string          `json:"parent,omitempty"`
	Related string          `json:"related,omitempty"`
	State   string          `json:"state,omitempty"`
	Data    json.RawMessage `json:"data"`
}
type ReadResult struct {
	Records []SourceRecord
	Gaps    []string
	Rows    int
	Bytes   int64
	Schema  string
	Scan    bool
}

func sourceLimits(l ReadLimits) ReadLimits {
	if l.Rows <= 0 || l.Rows > 2000 {
		l.Rows = 2000
	}
	if l.Bytes <= 0 || l.Bytes > 256<<20 {
		l.Bytes = 256 << 20
	}
	if l.RecordBytes <= 0 || l.RecordBytes > 1<<20 {
		l.RecordBytes = 1 << 20
	}
	return l
}

func (r *ReadResult) gap(s string) {
	if !slices.Contains(r.Gaps, s) {
		r.Gaps = append(r.Gaps, s)
	}
}

func sourceSelection(q Selection) string {
	if q.Owner == "" || q.ID == "" || len(q.Owner) > 1024 || len(q.ID) > 1024 || strings.ContainsAny(q.Owner+q.ID, "\x00\r\n") {
		return "source_selection_invalid"
	}
	switch q.Kind {
	case "draft", "job":
		return ""
	case "operation":
		if q.IdentityKind == "invocation" {
			return ""
		}
		if q.IdentityKind == "request-key" {
			if _, _, ok := app.OperationEpoch(q.ID); ok {
				return ""
			}
			return "gateway_identity_mapping_required"
		}
	}
	return "source_selection_invalid"
}

func sourceString(r SourceRecord, key string) string {
	var data map[string]json.RawMessage
	var s string
	if json.Unmarshal(r.Data, &data) == nil {
		_ = json.Unmarshal(data[key], &s)
	}
	return s
}

type sourceCollector struct {
	q         Selection
	limits    ReadLimits
	result    *ReadResult
	selected  map[string]SourceRecord
	conflicts map[string]bool
}

func (c *sourceCollector) consider(r SourceRecord, operationScan bool) bool {
	if r.Owner != c.q.Owner {
		return true
	}
	key := r.Kind + "\x00" + r.ID
	if c.conflicts[key] {
		return true
	}
	if old, ok := c.selected[key]; ok {
		projected, valid := ProjectRecord(r)
		if !valid || string(projected.Data) != string(old.Data) || projected.Parent != old.Parent || projected.Related != old.Related || projected.State != old.State {
			c.result.gap("source_record_conflict")
			delete(c.selected, key)
			if c.conflicts == nil {
				c.conflicts = map[string]bool{}
			}
			c.conflicts[key] = true
		}
		return true
	}
	if c.result.Rows >= c.limits.Rows {
		c.result.gap("source_row_limit")
		return false
	}
	c.result.Rows++
	projected, ok := ProjectRecord(r)
	if !ok {
		c.result.gap("source_record_invalid")
		return true
	}
	if operationScan && !sourceInvocationMatch(projected, c.q.ID) {
		return true
	}
	c.selected[key] = projected
	return true
}
func (c *sourceCollector) finish() ReadResult {
	if len(c.relations("mailbox")) > 0 || len(c.relations("draft")) > 0 {
		c.result.gap("source_related_missing")
	}
	for _, r := range c.selected {
		c.result.Records = append(c.result.Records, r)
	}
	sort.Slice(c.result.Records, func(i, j int) bool {
		a, b := c.result.Records[i], c.result.Records[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	if c.q.Kind == "job" {
		c.result.gap("job_attempt_mapping_missing")
	}
	if len(c.result.Records) == 0 {
		c.result.gap("source_record_missing")
	}
	return *c.result
}

func sourceInitialMatch(q Selection, r SourceRecord) bool {
	if r.Owner != q.Owner {
		return false
	}
	switch q.Kind {
	case "draft":
		return (r.Kind == "draft" && r.ID == q.ID) || (r.Kind == "send_snapshot" && r.Parent == q.ID)
	case "operation":
		return r.Kind == "send_snapshot" || r.Kind == "sync_failure"
	case "job":
		return r.Kind == "job" && r.ID == q.ID
	}
	return false
}

func sourceInvocationMatch(r SourceRecord, id string) bool {
	if r.Kind == "send_snapshot" {
		return sourceString(r, "invocation_id") == id
	}
	if r.Kind != "sync_failure" {
		return false
	}
	var data struct {
		Attempts []struct {
			InvocationID string `json:"invocation_id"`
		} `json:"attempts"`
	}
	if json.Unmarshal(r.Data, &data) != nil {
		return false
	}
	for _, a := range data.Attempts {
		if a.InvocationID == id {
			return true
		}
	}
	return false
}

func (c *sourceCollector) relations(kind string) map[string]bool {
	ids := map[string]bool{}
	for _, r := range c.selected {
		if kind == "draft" && r.Kind == "send_snapshot" && r.Parent != "" {
			ids[r.Parent] = true
		}
		if kind == "mailbox" {
			if id := sourceString(r, "mailbox_id"); id != "" {
				ids[id] = true
			}
		}
	}
	for id := range ids {
		if _, exists := c.selected[kind+"\x00"+id]; exists {
			delete(ids, id)
		}
	}
	return ids
}
