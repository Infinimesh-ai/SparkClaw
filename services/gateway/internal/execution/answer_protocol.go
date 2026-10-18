package execution

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
)

const AnswerMaxInteger = uint64(1<<53 - 1)

var answerDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// AnswerCursor is meaningful only with the corresponding verified text in
// client memory. It is neither durable state nor a delivery acknowledgement.
type AnswerCursor struct {
	Epoch         string `json:"epoch"`
	Generation    string `json:"generation"`
	Revision      uint64 `json:"revision"`
	Offset        uint64 `json:"offset"`
	ContentDigest string `json:"content_digest"`
}

func (c AnswerCursor) Valid() bool {
	return UUID(c.Epoch) && UUID(c.Generation) && c.Revision > 0 && c.Revision <= AnswerMaxInteger && c.Offset <= ResultBytes && answerDigest.MatchString(c.ContentDigest)
}

type AnswerStreamInput struct {
	SchemaVersion int           `json:"schema_version"`
	InputDigest   string        `json:"input_digest"`
	LatestOpenAt  time.Time     `json:"latest_open_at"`
	Cursor        *AnswerCursor `json:"cursor,omitempty"`
}

func (v AnswerStreamInput) Valid() bool {
	return v.SchemaVersion == 1 && answerDigest.MatchString(v.InputDigest) && !v.LatestOpenAt.IsZero() && (v.Cursor == nil || v.Cursor.Valid())
}

func ParseAnswerStreamInput(raw []byte) (AnswerStreamInput, error) {
	var v AnswerStreamInput
	fields, err := strictAnswerObject(raw)
	if err != nil || (len(fields) != 3 && len(fields) != 4) || appstream.DecodeStrict(raw, &v) != nil || !v.Valid() {
		return v, ErrConflict
	}
	for _, key := range []string{"schema_version", "input_digest", "latest_open_at"} {
		if _, ok := fields[key]; !ok {
			return v, ErrConflict
		}
	}
	if rawCursor, ok := fields["cursor"]; ok {
		cursor, err := strictAnswerObject(rawCursor)
		if err != nil || len(cursor) != 5 {
			return v, ErrConflict
		}
		for _, key := range []string{"epoch", "generation", "revision", "offset", "content_digest"} {
			if _, ok := cursor[key]; !ok {
				return v, ErrConflict
			}
		}
	}
	return v, nil
}

// AnswerEvent is the SparkClaw binding inside an opaque application frame.
// MarshalJSON emits exactly the fields required for its kind, including zero
// offsets. Controls are sent outside the application text sequence.
type AnswerEvent struct {
	Kind, RequestID, InputDigest, Epoch, Generation string
	Revision                                        uint64
	FromOffset, ToOffset, Offset, TotalBytes        uint64
	Text, ContentDigest, SnapshotID, State, Reason  string
}

func (e AnswerEvent) fields() map[string]any {
	v := map[string]any{"kind": e.Kind, "request_id": e.RequestID, "input_digest": e.InputDigest, "epoch": e.Epoch, "generation": e.Generation, "revision": e.Revision}
	switch e.Kind {
	case "delta":
		v["from_offset"], v["to_offset"], v["text"], v["content_digest"] = e.FromOffset, e.ToOffset, e.Text, e.ContentDigest
	case "snapshot_begin", "snapshot_end":
		v["snapshot_id"], v["total_bytes"], v["content_digest"] = e.SnapshotID, e.TotalBytes, e.ContentDigest
	case "snapshot_chunk":
		v["snapshot_id"], v["offset"], v["text"] = e.SnapshotID, e.Offset, e.Text
	case "state":
		v["state"] = e.State
	case "invalidate":
		v["reason"] = e.Reason
	}
	return v
}

func (e AnswerEvent) MarshalJSON() ([]byte, error) {
	if !e.Valid() {
		return nil, errors.New("invalid answer event")
	}
	return json.Marshal(e.fields())
}

func (e AnswerEvent) Valid() bool {
	if !UUID(e.RequestID) || !UUID(e.Epoch) || !UUID(e.Generation) || !answerDigest.MatchString(e.InputDigest) || e.Revision == 0 || e.Revision > AnswerMaxInteger {
		return false
	}
	switch e.Kind {
	case "delta":
		return e.Text != "" && utf8.ValidString(e.Text) && !strings.ContainsRune(e.Text, 0) && e.FromOffset <= e.ToOffset && e.ToOffset <= ResultBytes && uint64(len(e.Text)) == e.ToOffset-e.FromOffset && answerDigest.MatchString(e.ContentDigest)
	case "snapshot_begin", "snapshot_end":
		return UUID(e.SnapshotID) && e.TotalBytes <= ResultBytes && answerDigest.MatchString(e.ContentDigest)
	case "snapshot_chunk":
		return UUID(e.SnapshotID) && e.Text != "" && utf8.ValidString(e.Text) && !strings.ContainsRune(e.Text, 0) && e.Offset <= ResultBytes && uint64(len(e.Text)) <= ResultBytes-e.Offset
	case "state":
		switch e.State {
		case "accepted", "running", "approval_pending", "browser_login_blocked", "completed", "failed", "unknown", "delivered", "delivery_expired":
			return true
		}
	case "invalidate":
		switch e.Reason {
		case "reset", "failed", "unknown", "delivered", "delivery_expired", "revoked", "closed", "preview_unavailable":
			return true
		}
	}
	return false
}

// ParseAnswerEvent rejects unknown, missing and duplicate keys and invalid
// UTF-8 before decoding. Go's default JSON decoder alone accepts duplicates and
// replaces malformed UTF-8, so neither is sufficient for wire validation.
func ParseAnswerEvent(raw []byte) (AnswerEvent, error) {
	var e AnswerEvent
	if len(raw) > appstream.MaxBytes {
		return e, ErrConflict
	}
	v, err := strictAnswerObject(raw)
	if err != nil {
		return e, err
	}
	read := func(key string, target any) {
		if err == nil {
			value, ok := v[key]
			if !ok {
				err = ErrConflict
			} else {
				err = json.Unmarshal(value, target)
			}
		}
	}
	read("kind", &e.Kind)
	read("request_id", &e.RequestID)
	read("input_digest", &e.InputDigest)
	read("epoch", &e.Epoch)
	read("generation", &e.Generation)
	read("revision", &e.Revision)
	switch e.Kind {
	case "delta":
		read("from_offset", &e.FromOffset)
		read("to_offset", &e.ToOffset)
		read("text", &e.Text)
		read("content_digest", &e.ContentDigest)
	case "snapshot_begin", "snapshot_end":
		read("snapshot_id", &e.SnapshotID)
		read("total_bytes", &e.TotalBytes)
		read("content_digest", &e.ContentDigest)
	case "snapshot_chunk":
		read("snapshot_id", &e.SnapshotID)
		read("offset", &e.Offset)
		read("text", &e.Text)
	case "state":
		read("state", &e.State)
	case "invalidate":
		read("reason", &e.Reason)
	}
	if err != nil || !e.Valid() || len(v) != len(e.fields()) {
		return AnswerEvent{}, ErrConflict
	}
	for k := range v {
		if _, ok := e.fields()[k]; !ok {
			return AnswerEvent{}, ErrConflict
		}
	}
	return e, nil
}

func strictAnswerObject(raw []byte) (map[string]json.RawMessage, error) {
	v := map[string]json.RawMessage{}
	if appstream.DecodeStrict(raw, &v) != nil || v == nil {
		return nil, ErrConflict
	}
	for _, value := range v {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrConflict
		}
	}
	return v, nil
}
