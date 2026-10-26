package emaildiagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

var ErrInvalidQuery = errors.New("invalid diagnostic query")

func normalizedLimits(in Limits) Limits {
	out := Limits{WallMS: 10000, Rows: 2000, SnapshotBytes: 256 << 20, LogBytes: 32 << 20, RecordBytes: 1 << 20, OutputBytes: 1 << 20}
	if in.WallMS > 0 && in.WallMS < out.WallMS {
		out.WallMS = in.WallMS
	}
	if in.Rows > 0 && in.Rows < out.Rows {
		out.Rows = in.Rows
	}
	if in.SnapshotBytes > 0 && in.SnapshotBytes < out.SnapshotBytes {
		out.SnapshotBytes = in.SnapshotBytes
	}
	if in.LogBytes > 0 && in.LogBytes < out.LogBytes {
		out.LogBytes = in.LogBytes
	}
	if in.RecordBytes > 0 && in.RecordBytes < out.RecordBytes {
		out.RecordBytes = in.RecordBytes
	}
	if in.OutputBytes >= 256 && in.OutputBytes < out.OutputBytes {
		out.OutputBytes = in.OutputBytes
	}
	return out
}
func selection(o Options) (Selection, error) {
	q := Selection{Owner: o.Owner, IdentityKind: o.IdentityKind}
	n := 0
	for _, v := range []struct{ kind, id string }{{"draft", o.Draft}, {"operation", o.Operation}, {"job", o.Job}} {
		if v.id != "" {
			n++
			q.Kind = v.kind
			q.ID = v.id
		}
	}
	if n != 1 || strings.TrimSpace(o.Owner) == "" || len(o.Owner) > 1024 || len(q.ID) > 1024 || strings.ContainsAny(q.ID+q.Owner, "\x00\r\n") {
		return q, ErrInvalidQuery
	}
	if q.Kind == "operation" && o.IdentityKind != "invocation" && o.IdentityKind != "request-key" {
		return q, ErrInvalidQuery
	}
	if q.Kind != "operation" && o.IdentityKind != "" {
		return q, ErrInvalidQuery
	}
	if o.Snapshot != "" && o.PostgresDSN != "" || o.Snapshot == "" && o.PostgresDSN == "" && (q.Kind != "operation" || o.Ledger == "") {
		return q, ErrInvalidQuery
	}
	if o.Ledger != "" && (o.LedgerOwner == "" || o.LedgerPrincipal == "") {
		return q, ErrInvalidQuery
	}
	if len(o.Logs) > 32 {
		return q, ErrInvalidQuery
	}
	return q, nil
}
func remainingRows(r *Report) int {
	remaining := r.Limits.Rows
	for _, s := range r.Sources {
		remaining -= s.RowsRead
	}
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Build consumes already retained sources. It has no runtime/service callbacks.
func Build(parent context.Context, o Options) (Report, error) {
	q, err := selection(o)
	if err != nil {
		return Report{}, err
	}
	limits := normalizedLimits(o.Limits)
	ctx, cancel := context.WithTimeout(parent, time.Duration(limits.WallMS)*time.Millisecond)
	defer cancel()
	r := emptyReport(q)
	r.Limits = limits
	if ctx.Err() != nil {
		r.gap("query_canceled")
		return r, nil
	}
	if o.Snapshot != "" || o.PostgresDSN != "" {
		source := Source{Ref: "gateway-1", Kind: "file_snapshot", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		readLimits := ReadLimits{Rows: limits.Rows, Bytes: limits.SnapshotBytes, RecordBytes: limits.RecordBytes}
		var result ReadResult
		if o.Snapshot != "" {
			result = ReadSnapshot(ctx, o.Snapshot, q, readLimits)
		} else {
			source.Kind = "postgres"
			result = ReadPostgres(ctx, o.PostgresDSN, q, readLimits)
		}
		r = aggregateSource(q, result, source.Ref)
		r.Limits = limits
		source.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		source.Schema = result.Schema
		source.RowsRead = result.Rows
		source.BytesRead = result.Bytes
		source.Scan = result.Scan
		for _, gap := range result.Gaps {
			gap = safeGap(gap)
			source.Missing = append(source.Missing, gap)
			r.gap(gap)
		}
		r.Sources = append(r.Sources, source)
	}
	if len(r.Attempts) == 0 && q.Kind == "operation" {
		a := newAttempt(q, q.ID)
		if q.IdentityKind == "request-key" {
			a.requestKey = q.ID
			a.RequestKeyRef = scopeHash(q.Owner, q.ID)
		} else {
			a.invocation = q.ID
			a.InvocationRef = scopeHash(q.Owner, q.ID)
		}
		r.Attempts = append(r.Attempts, a)
	}
	logs := readLogs(ctx, &r, o.Logs)
	readLedger(ctx, &r, q, o)
	applyEvents(&r, q, logs)
	readExpectedVersion(ctx, &r, o.ReleaseManifest)
	if ctx.Err() != nil {
		r.gap("query_canceled")
	}
	if len(r.Health) == 0 {
		r.gap("sink_health_not_observed")
	}
	r.gap("trace_link_unavailable")
	return r, nil
}

func readExpectedVersion(ctx context.Context, r *Report, path string) {
	if path == "" {
		r.gap("expected_version_not_supplied")
		return
	}
	source := Source{Ref: "release-1", Kind: "release_manifest", Schema: "release_v1", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	defer func() {
		source.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		r.Sources = append(r.Sources, source)
	}()
	if ctx.Err() != nil {
		r.gap("query_canceled")
		return
	}
	info, statErr := os.Stat(path)
	if statErr != nil || !info.Mode().IsRegular() {
		r.gap("release_manifest_unavailable")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		r.gap("release_manifest_unavailable")
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		r.gap("release_manifest_unavailable")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	source.BytesRead = int64(len(raw))
	source.RowsRead = 1
	if err != nil || len(raw) > 65536 {
		r.gap("release_manifest_invalid")
		return
	}
	var value struct {
		ID     string `json:"id"`
		Commit string `json:"source_commit"`
		Digest string `json:"runtime_digest"`
	}
	if !sourceUniqueJSON(raw) || json.Unmarshal(raw, &value) != nil || !releaseName.MatchString(value.ID) || !hex40.MatchString(value.Commit) || !hex64.MatchString(value.Digest) {
		r.gap("release_manifest_invalid")
		return
	}
	r.Versions.Expected = Version{Release: value.ID, SourceCommit: value.Commit, RuntimeDigest: value.Digest, EvidenceRef: source.Ref}
}

// Encode bounds output too. Omitting detail is explicitly incomplete, never a
// compact success claim. JSON and text use the same filtered report data.
func Encode(r Report, format string) ([]byte, error) {
	if format != "json" && format != "text" {
		return nil, ErrInvalidQuery
	}
	limit := r.Limits.OutputBytes
	if limit == 0 {
		limit = 1 << 20
	}
	for {
		raw, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return nil, errors.New("report encoding failed")
		}
		if format == "text" {
			raw = []byte("Read-only mail evidence (incomplete)\n" + string(raw))
		}
		if len(raw)+1 <= limit {
			return append(raw, '\n'), nil
		}
		r.gap("output_limit")
		if len(r.Attempts) > 0 {
			keep := len(r.Attempts) / 2
			r.OmittedAttempts += len(r.Attempts) - keep
			r.Attempts = r.Attempts[:keep]
			continue
		}
		if len(r.Health) > 0 {
			r.Health = nil
			continue
		}
		if len(r.UnstartedEvidence) > 0 {
			r.UnstartedEvidence = nil
			continue
		}
		if len(r.Sources) > 0 {
			r.Sources = nil
			continue
		}
		return []byte(fmt.Sprintf("{\"schema_version\":1,\"read_only\":true,\"evidence\":\"incomplete\",\"missing\":[\"output_limit\"]}\n")), nil
	}
}

// Structural changes invalidate the source as a snapshot. Bounded partial reads
// remain visible, but cannot supply an owner binding or recovery eligibility.
func aggregateSource(q Selection, source ReadResult, ref string) Report {
	for _, gap := range source.Gaps {
		switch gap {
		case "source_changed", "source_malformed", "source_schema_unsupported", "encrypted_source_unsupported":
			return emptyReport(q)
		}
	}
	r := aggregate(q, source.Records, ref)
	for _, gap := range source.Gaps {
		if strings.HasPrefix(gap, "source_") && gap != "source_related_missing" {
			for i := range r.Attempts {
				disableReceiptProof(&r.Attempts[i])
			}
			break
		}
	}
	return r
}
