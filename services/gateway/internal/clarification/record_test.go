package clarification

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func recordFixture() (Binding, Record, Revisions, time.Time) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	binding := Binding{OwnerID: "owner", ClientID: "client", InstallationID: "installation", ConversationID: "conversation"}
	record := Record{SchemaVersion: SchemaVersion, ID: "question", RequestID: "request", SourceMessageID: "owner-message", SourceTextSHA256: TextDigest("private owner request"), ContextSHA256: TextDigest("context"), PromptSHA256: TextDigest("choose"), IssuedAt: now.Format(time.RFC3339), ExpiresAt: now.Add(Lifetime).Format(time.RFC3339), GraphRevision: "graph.1", CatalogRevision: "catalog.1", CalibrationRevision: "calibration.1", Options: []Option{{Number: 1, Kind: "route", CandidateID: "document.edit#edit", FileID: "file", BindingSHA256: TextDigest("target")}, {Number: 2, Kind: "route", CandidateID: "document.read#read", BindingSHA256: TextDigest("target2")}}}
	return binding, record, Revisions{Graph: record.GraphRevision, Catalog: record.CatalogRevision, Calibration: record.CalibrationRevision}, now
}

func TestSignedRecordBindsIdentityOptionsAndRevisions(t *testing.T) {
	binding, record, revisions, now := recordFixture()
	key := make([]byte, 32)
	signed, err := Sign(key, binding, record)
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyAt(key, binding, signed, revisions, now); err != nil {
		t.Fatal(err)
	}
	other := binding
	other.ConversationID = "other"
	if !errors.Is(VerifyAt(key, other, signed, revisions, now), ErrInvalid) {
		t.Fatal("identity not bound")
	}
	mutations := map[string]func(*Record){
		"source id":      func(r *Record) { r.SourceMessageID = "another" },
		"context":        func(r *Record) { r.ContextSHA256 = TextDigest("changed") },
		"prompt":         func(r *Record) { r.PromptSHA256 = TextDigest("changed") },
		"source text":    func(r *Record) { r.SourceTextSHA256 = TextDigest("changed") },
		"file":           func(r *Record) { r.Options[0].FileID = "another" },
		"object binding": func(r *Record) { r.Options[0].BindingSHA256 = TextDigest("changed") },
		"issued":         func(r *Record) { r.IssuedAt = now.Add(time.Second).Format(time.RFC3339) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := signed
			changed.Options = append([]Option(nil), signed.Options...)
			mutate(&changed)
			if !errors.Is(VerifyAt(key, binding, changed, revisions, now), ErrInvalid) {
				t.Fatal("tampered record accepted")
			}
		})
	}
	revisions.Graph = "new"
	if !errors.Is(VerifyAt(key, binding, signed, revisions, now), ErrStale) {
		t.Fatal("stale graph accepted")
	}
	if _, err = Sign(key[:31], binding, record); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid key accepted")
	}
}

func TestRecordTimeAndKindBoundaries(t *testing.T) {
	binding, record, revisions, now := recordFixture()
	signed, err := Sign(make([]byte, 32), binding, record)
	if err != nil {
		t.Fatal(err)
	}
	for _, instant := range []time.Time{now.Add(-time.Nanosecond), now.Add(Lifetime)} {
		if err := VerifyAt(make([]byte, 32), binding, signed, revisions, instant); !errors.Is(err, ErrExpired) {
			t.Fatalf("time boundary: %v", err)
		}
	}
	for name, mutate := range map[string]func(*Record){
		"schema1":  func(r *Record) { r.SchemaVersion = 1 },
		"offset":   func(r *Record) { r.IssuedAt = "2026-10-30T12:00:00+00:00" },
		"fraction": func(r *Record) { r.IssuedAt = "2026-10-30T12:00:00.000Z" },
		"too long": func(r *Record) { r.ExpiresAt = now.Add(Lifetime + time.Second).Format(time.RFC3339) },
		"mixed questions": func(r *Record) {
			r.Options[1] = Option{Number: 2, Kind: "delivery", DeliveryMode: "current_route", BindingSHA256: TextDigest("delivery")}
		},
		"missing binding": func(r *Record) { r.Options[0].BindingSHA256 = "" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := record
			changed.Options = append([]Option(nil), record.Options...)
			mutate(&changed)
			if Valid(changed) {
				t.Fatal("invalid record accepted")
			}
		})
	}
}

func TestCanonicalJSONVectorsAndInvalidForms(t *testing.T) {
	value := map[string]any{"z": "<>&\u2028\u2029\n\t\"\\中", "a": []any{true, 0, "e\u0301"}}
	want := "{\"a\":[true,0,\"e\u0301\"],\"z\":\"<>&\u2028\u2029\\u000a\\u0009\\\"\\\\中\"}"
	got, err := Canonical(value)
	if err != nil || string(got) != want {
		t.Fatalf("canonical=%s want=%s err=%v", got, want, err)
	}
	for _, raw := range []string{`{"a":null}`, `{"a":-1}`, `{"a":1.0}`, `{"a":1e2}`, `{"a":1,"a":2}`, `{"é":1}`, `{"a":"\ud800"}`} {
		if _, err := Canonical(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid raw accepted: %s", raw)
		}
	}
	if _, err := Canonical(map[string]any{"text": string([]byte{0xff})}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if TextDigest("é") == TextDigest("e\u0301") || TextDigest("line\n") == TextDigest("line\r\n") {
		t.Fatal("owner bytes normalized")
	}
}

func TestContextDigestBindsMessagesFilesAndMissingMarker(t *testing.T) {
	files := []SnapshotFile{{ID: "b", Name: "b.txt", Size: 0, SHA256: TextDigest(""), Source: "conversation_attachment"}, {ID: "a", Name: "a.txt", Size: 1, SHA256: TextDigest("a"), Source: "conversation_attachment"}}
	messages := []SnapshotMessage{{ID: "m1", Role: "user", Content: "中文\n", Files: files}}
	original, err := ContextDigest(messages, "web")
	if original != "6e7a95c5cc4b78adc0990cd995fffe4b280b2352ae892eea5f6c8f9278afe1c3" {
		t.Fatalf("shared desktop context vector changed: %s", original)
	}
	if err != nil {
		t.Fatal(err)
	}
	messages[0].Files = []SnapshotFile{files[1], files[0]}
	if got, _ := ContextDigest(messages, "web"); got != original {
		t.Fatal("file order changed digest")
	}
	for _, mutate := range []func(){func() { messages[0].FilesUnavailable = true }, func() { messages[0].ID = "m2" }, func() { messages[0].Content = "中文\r\n" }, func() { messages[0].Files[0].SHA256 = strings.Repeat("0", 64) }} {
		mutate()
		if got, _ := ContextDigest(messages, "web"); got == original {
			t.Fatal("context mutation not bound")
		}
	}
}
