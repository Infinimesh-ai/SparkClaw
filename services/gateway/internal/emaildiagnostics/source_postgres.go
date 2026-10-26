package emaildiagnostics

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func ReadPostgres(ctx context.Context, dsn string, q Selection, limits ReadLimits) (result ReadResult) {
	result.Schema = "gateway_postgres_email_records_v1"
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
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		result.gap("source_unavailable")
		return
	}
	// Read-only at connection startup as well as inside the transaction. No
	// Store constructor, schema migrator, pool hook or application bootstrap.
	config.RuntimeParams["default_transaction_read_only"] = "on"
	config.RuntimeParams["statement_timeout"] = "10000"
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		result.gap(sourcePostgresGap(ctx, err))
		return
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = conn.Close(closeCtx)
	}()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		result.gap(sourcePostgresGap(ctx, err))
		return
	}
	defer func() {
		rollbackCtx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = tx.Rollback(rollbackCtx)
	}()
	if ok, checkErr := sourcePostgresSchema(ctx, tx); checkErr != nil {
		result.gap(sourcePostgresGap(ctx, checkErr))
		return
	} else if !ok {
		result.gap("source_schema_unsupported")
		return
	}
	c := sourceCollector{q: q, limits: limits, result: &result, selected: map[string]SourceRecord{}}
	read := func(kind, predicate string, ids []string, operation bool) bool {
		projection, valid := sourceSQLProjection("payload", sourceRecordFields[kind], 0)
		// Count source bytes, including rejected oversized rows, without sending
		// unbounded private payloads or metadata over the database connection.
		size := `octet_length(payload::text)+octet_length(owner_id)+octet_length(kind)+octet_length(id)+octet_length(parent)+octet_length(related)+octet_length(state)`
		// Sort exactly like the existing parent/order indexes. Both the byte
		// cutoff and extra row-limit sentinel are checked server-side, before
		// projected values can enter the client/network buffers.
		order := `sort_key COLLATE "C" DESC,id COLLATE "C" DESC`
		admit := `source_size <= $4 AND cumulative_bytes <= $6 AND read_ordinal < $5`
		query := `SELECT source_size,CASE WHEN ` + admit + ` THEN owner_id ELSE '' END,CASE WHEN ` + admit + ` THEN kind ELSE '' END,CASE WHEN ` + admit + ` THEN id ELSE '' END,CASE WHEN ` + admit + ` THEN parent ELSE '' END,CASE WHEN ` + admit + ` THEN related ELSE '' END,CASE WHEN ` + admit + ` THEN state ELSE '' END,CASE WHEN ` + admit + ` THEN ` + projection + ` ELSE NULL END,CASE WHEN ` + admit + ` THEN ` + valid + ` ELSE false END FROM (SELECT *,SUM(source_size) OVER(ORDER BY ` + order + `) AS cumulative_bytes,ROW_NUMBER() OVER(ORDER BY ` + order + `) AS read_ordinal FROM (SELECT owner_id,kind,id,parent,related,state,sort_key,payload,` + size + ` AS source_size FROM email_management_records WHERE owner_id=$1 AND kind=$2 AND ` + predicate + ` ORDER BY ` + order + ` LIMIT $5) AS bounded_source) AS bounded ORDER BY ` + order
		rows, e := tx.Query(ctx, query, q.Owner, kind, ids, limits.RecordBytes, limits.Rows-result.Rows+1, limits.Bytes-result.Bytes)
		if e != nil {
			result.gap(sourcePostgresGap(ctx, e))
			return false
		}
		defer rows.Close()
		for rows.Next() {
			if result.Rows >= limits.Rows {
				result.gap("source_row_limit")
				return false
			}
			var r SourceRecord
			var size int64
			var valid bool
			if e = rows.Scan(&size, &r.Owner, &r.Kind, &r.ID, &r.Parent, &r.Related, &r.State, &r.Data, &valid); e != nil {
				result.gap(sourcePostgresGap(ctx, e))
				return false
			}
			if size > limits.Bytes-result.Bytes {
				result.Bytes = limits.Bytes
				result.gap("source_byte_limit")
				return false
			}
			result.Bytes += size
			if size > int64(limits.RecordBytes) {
				result.Rows++
				result.gap("source_record_limit")
				continue
			}
			if !valid {
				result.Rows++
				result.gap("source_record_invalid")
				continue
			}
			if !c.consider(r, operation) {
				return false
			}
		}
		if e = rows.Err(); e != nil {
			result.gap(sourcePostgresGap(ctx, e))
			return false
		}
		return true
	}
	ok := true
	switch q.Kind {
	case "draft":
		ok = read("draft", `id=ANY($3::text[])`, []string{q.ID}, false)
		if ok {
			ok = read("send_snapshot", `parent=ANY($3::text[])`, []string{q.ID}, false)
		}
	case "job":
		ok = read("job", `id=ANY($3::text[])`, []string{q.ID}, false)
	case "operation":
		result.Scan = true
		ok = read("send_snapshot", `$3::text[] IS NOT NULL`, []string{}, true)
		if ok {
			ok = read("sync_failure", `$3::text[] IS NOT NULL`, []string{}, true)
		}
	}
	if ok && q.Kind == "operation" {
		ids := sourceSortedIDs(c.relations("draft"))
		if len(ids) > 0 {
			ok = read("draft", `id=ANY($3::text[])`, ids, false)
		}
	}
	if ok {
		ids := sourceSortedIDs(c.relations("mailbox"))
		if len(ids) > 0 {
			read("mailbox", `id=ANY($3::text[])`, ids, false)
		}
	}
	return c.finish()
}
func sourceSortedIDs(ids map[string]bool) []string {
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
func sourcePostgresGap(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return sourceErrorGap(ctx.Err())
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		switch pgerr.Code {
		case "42P01", "42703", "42883", "42804":
			return "source_schema_unsupported"
		case "57014":
			return "source_timeout"
		}
	}
	return "source_unavailable"
}

// All names are compile-time whitelist entries. User input only reaches query
// parameters. Nested receipt/proof/attempt projections never select raw payload.
func sourceSQLProjection(path string, fields map[string]sourceField, depth int) (string, string) {
	keys := make([]string, 0, len(fields))
	for name := range fields {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	pairs := []string{}
	checks := []string{`jsonb_typeof(` + path + `)='object'`}
	for _, name := range keys {
		rule := fields[name]
		field := `(` + path + `->'` + name + `')`
		value := field
		present := field + ` IS NULL OR ` + field + `='null'::jsonb`
		valid := `jsonb_typeof(` + field + `)='` + rule.scalar + `'`
		if rule.fields != nil {
			if rule.array {
				alias := "element" + strconv.Itoa(depth)
				inner, good := sourceSQLProjection(alias+".value", rule.fields, depth+1)
				array := `jsonb_array_elements(CASE WHEN jsonb_typeof(` + field + `)='array' THEN ` + field + ` ELSE '[]'::jsonb END) WITH ORDINALITY AS ` + alias + `(value,position)`
				value = `(SELECT COALESCE(jsonb_agg(` + inner + ` ORDER BY ` + alias + `.position),'[]'::jsonb) FROM ` + array + `)`
				valid = `jsonb_typeof(` + field + `)='array' AND NOT EXISTS(SELECT 1 FROM ` + array + ` WHERE NOT COALESCE((` + good + `),false))`
			} else {
				value, valid = sourceSQLProjection(field, rule.fields, depth+1)
			}
		}
		if name == "receipt" {
			empty := []string{}
			for _, id := range []string{"provider_message_id", "provider_thread_id"} {
				p := `(` + field + `->'` + id + `')`
				empty = append(empty, `(`+p+` IS NULL OR `+p+`='null'::jsonb OR `+p+`='""'::jsonb)`)
			}
			valid = `(` + valid + `) AND (COALESCE(` + field + `->>'status','')<>'not_sent' OR (` + strings.Join(empty, " AND ") + `))`
		}
		pairs = append(pairs, `'`+name+`'`, `CASE WHEN `+present+` THEN NULL WHEN (`+valid+`) THEN `+value+` ELSE NULL END`)
		checks = append(checks, `(`+present+` OR (`+valid+`))`)
	}
	return `jsonb_build_object(` + strings.Join(pairs, ",") + `)`, `(` + strings.Join(checks, " AND ") + `)`
}
