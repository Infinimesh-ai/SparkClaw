package emaildiagnostics

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"time"
)

func ReadSnapshot(ctx context.Context, path string, q Selection, limits ReadLimits) (result ReadResult) {
	result.Scan = true
	result.Schema = "gateway_snapshot_email_records_v1"
	if gap := sourceSelection(q); gap != "" {
		result.gap(gap)
		return
	}
	limits = sourceLimits(limits)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		result.gap(sourceErrorGap(ctx.Err()))
		return
	}
	// Stat before Open avoids blocking on named pipes or devices. Open never
	// requests creation, migration, permissions changes, or a writable handle.
	before, err := os.Stat(path)
	if err != nil || !before.Mode().IsRegular() {
		result.gap("source_unavailable")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		result.gap("source_unavailable")
		return
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		result.gap("source_changed")
		return
	}
	defer func() {
		after, e := os.Stat(path)
		last, fe := f.Stat()
		if e != nil || fe != nil || !os.SameFile(opened, after) || !os.SameFile(opened, last) || opened.Size() != after.Size() || opened.Size() != last.Size() || !opened.ModTime().Equal(after.ModTime()) || !opened.ModTime().Equal(last.ModTime()) || opened.Mode() != after.Mode() {
			result.gap("source_changed")
		}
	}()
	c := sourceCollector{q: q, limits: limits, result: &result, selected: map[string]SourceRecord{}}
	scan := func(match func(SourceRecord) bool, operation bool) bool {
		if _, e := f.Seek(0, io.SeekStart); e != nil {
			result.gap("source_unavailable")
			return false
		}
		input := &sourceInput{ctx: ctx, r: f, result: &result, max: limits.Bytes}
		s := &sourceStream{r: bufio.NewReaderSize(input, 32768)}
		e := scanSourceSnapshot(s, limits.RecordBytes, &result, func(r SourceRecord) bool {
			if match(r) {
				return c.consider(r, operation)
			}
			return true
		})
		if e != nil {
			result.gap(sourceErrorGap(e))
			return false
		}
		return true
	}
	if scan(func(r SourceRecord) bool { return sourceInitialMatch(q, r) }, q.Kind == "operation") {
		if q.Kind == "operation" {
			ids := c.relations("draft")
			if len(ids) > 0 {
				scan(func(r SourceRecord) bool { return r.Owner == q.Owner && r.Kind == "draft" && ids[r.ID] }, false)
			}
		}
		ids := c.relations("mailbox")
		if len(ids) > 0 && !hasSourceLimit(result) {
			scan(func(r SourceRecord) bool { return r.Owner == q.Owner && r.Kind == "mailbox" && ids[r.ID] }, false)
		}
	}
	return c.finish()
}

func hasSourceLimit(result ReadResult) bool {
	for _, g := range result.Gaps {
		if g == "source_byte_limit" || g == "source_row_limit" || g == "source_canceled" || g == "source_timeout" {
			return true
		}
	}
	return false
}

func scanSourceSnapshot(s *sourceStream, recordLimit int, result *ReadResult, consume func(SourceRecord) bool) error {
	if e := s.space(nil); e != nil {
		return e
	}
	if e := s.expect('{', nil); e != nil {
		return errSourceSchema
	}
	seen, known := false, false
	if e := s.space(nil); e != nil {
		return e
	}
	b, e := s.peek()
	if e != nil {
		return e
	}
	if b == '}' {
		return errSourceSchema
	}
	for {
		key, e := s.key()
		if e != nil {
			return e
		}
		if e = s.space(nil); e != nil {
			return e
		}
		if e = s.expect(':', nil); e != nil {
			return e
		}
		if e = s.space(nil); e != nil {
			return e
		}
		switch key {
		case "ciphertext":
			result.gap("encrypted_source_unsupported")
			if e = s.value(nil, 0); e != nil {
				return e
			}
		case "email_management_records":
			if seen {
				return errSourceSchema
			}
			seen = true
			if e = scanSourceRecords(s, recordLimit, result, consume); e != nil {
				return e
			}
		case "sessions", "owner_profile", "clients":
			known = true
			fallthrough
		default:
			if e = s.value(nil, 0); e != nil {
				return e
			}
		}
		if e = s.space(nil); e != nil {
			return e
		}
		b, e = s.take(nil)
		if e != nil {
			return e
		}
		if b == '}' {
			break
		}
		if b != ',' {
			return errSourceJSON
		}
		if e = s.space(nil); e != nil {
			return e
		}
	}
	if e = s.space(nil); e != io.EOF {
		if e != nil {
			return e
		}
		return errSourceJSON
	}
	if !seen && !known {
		if len(result.Gaps) == 0 {
			return errSourceSchema
		}
	}
	return nil
}

func scanSourceRecords(s *sourceStream, recordLimit int, result *ReadResult, consume func(SourceRecord) bool) error {
	b, e := s.peek()
	if e != nil {
		return e
	}
	if b == 'n' {
		return s.value(nil, 0)
	}
	if b != '{' {
		return errSourceSchema
	}
	_, _ = s.take(nil)
	if e = s.space(nil); e != nil {
		return e
	}
	b, e = s.peek()
	if e != nil {
		return e
	}
	if b == '}' {
		_, e = s.take(nil)
		return e
	}
	for {
		if _, e = s.key(); e != nil {
			return e
		}
		if e = s.space(nil); e != nil {
			return e
		}
		if e = s.expect(':', nil); e != nil {
			return e
		}
		capture := &sourceCapture{limit: recordLimit}
		if e = s.value(capture, 0); e != nil {
			return e
		}
		if capture.large {
			result.gap("source_record_limit")
		} else {
			row, ok := decodeSourceRecord(capture.raw)
			if !ok {
				result.gap("source_record_invalid")
			} else if !consume(row) {
				return errSourceRows
			}
		}
		if e = s.space(nil); e != nil {
			return e
		}
		b, e = s.take(nil)
		if e != nil {
			return e
		}
		if b == '}' {
			return nil
		}
		if b != ',' {
			return errSourceJSON
		}
		if e = s.space(nil); e != nil {
			return e
		}
	}
}

func decodeSourceRecord(raw []byte) (SourceRecord, bool) {
	var row SourceRecord
	if !sourceUniqueJSON(raw) {
		return row, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return row, false
	}
	// encoding/json's struct decoder accepts case-insensitive aliases. Owner
	// and identity fields must instead use exactly the persisted schema keys.
	for name, target := range map[string]*string{"owner": &row.Owner, "kind": &row.Kind, "id": &row.ID, "parent": &row.Parent, "related": &row.Related, "state": &row.State} {
		if value, exists := fields[name]; exists {
			if json.Unmarshal(value, target) != nil {
				return SourceRecord{}, false
			}
		}
	}
	row.Data = fields["data"]
	return row, row.Owner != "" && row.Kind != "" && row.ID != "" && len(row.Data) > 0
}
