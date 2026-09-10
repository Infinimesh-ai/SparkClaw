package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type emailPostgresRecords struct {
	dirty bool
	ctx   context.Context
	tx    pgx.Tx
	owner string
}

func (p *emailPostgresRecords) get(kind, id string) (EmailRecord, bool, error) {
	var r EmailRecord
	err := p.tx.QueryRow(p.ctx, `SELECT owner_id,kind,id,parent,related,state,search_text,sort_key,payload FROM email_management_records WHERE owner_id=$1 AND kind=$2 AND id=$3`, p.owner, kind, id).Scan(&r.Owner, &r.Kind, &r.ID, &r.Parent, &r.Related, &r.State, &r.Search, &r.Sort, &r.Data)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}
func (p *emailPostgresRecords) put(r EmailRecord) error {
	_, err := p.tx.Exec(p.ctx, `INSERT INTO email_management_records(owner_id,kind,id,parent,related,state,search_text,sort_key,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(owner_id,kind,id) DO UPDATE SET parent=EXCLUDED.parent,related=EXCLUDED.related,state=EXCLUDED.state,search_text=EXCLUDED.search_text,sort_key=EXCLUDED.sort_key,payload=EXCLUDED.payload`, r.Owner, r.Kind, r.ID, r.Parent, r.Related, r.State, r.Search, r.Sort, r.Data)
	if err == nil {
		p.dirty = true
	}
	return err
}
func emailPostgresQuery(owner string, q emailRowsQuery, ordered bool) (string, []any) {
	sql := `SELECT owner_id,kind,id,parent,related,state,search_text,sort_key,payload FROM email_management_records WHERE owner_id=$1 AND kind=$2`
	args := []any{owner, q.Kind}
	if q.Kind == "mail" {
		sql += ` AND COALESCE(payload->>'superseded_by_mail_id','')=''`
	}
	add := func(clause string, value any) { args = append(args, value); sql += fmt.Sprintf(clause, len(args)) }
	if q.HistoryOnly {
		sql += ` AND search_text='history'`
	}
	if q.ExcludeHistory {
		sql += ` AND search_text<>'history'`
	}
	if q.ExcludePageBatchSuperseded {
		add(` AND NOT(state='paused' AND COALESCE(payload->>'error_code','')=$%d)`, emailPageBatchSuperseded)
	}
	if q.Entry != "" {
		add(` AND COALESCE(payload->>'representation_id','')<>'' AND COALESCE(payload->'classification'->>'effective_entry','interaction')=$%d`, q.Entry)
	}
	if q.Validity != "" {
		sql += ` AND payload->'classification'->>'notification_subtype'='verification'`
		if q.Validity == "validity_unknown" {
			sql += ` AND COALESCE(payload->'verification'->>'expires_at','')=''`
		} else {
			sql += ` AND COALESCE(payload->'verification'->>'expires_at','')<>''`
			if q.Validity == "expired" {
				add(` AND (payload->'verification'->>'expires_at')::timestamptz <= $%d`, q.AsOf)
			} else {
				add(` AND (payload->'verification'->>'expires_at')::timestamptz > $%d`, q.AsOf)
			}
		}
	}
	if q.NotificationSubtype != "" {
		add(` AND payload->'classification'->>'notification_subtype'=$%d`, q.NotificationSubtype)
	}
	if q.PendingOnly {
		sql += ` AND COALESCE(payload->>'representation_id','')=''`
	}
	if q.UnassignedOnly {
		sql += ` AND related=''`
	}
	if q.InteractionConversation {
		sql += ` AND EXISTS(SELECT 1 FROM email_management_records member WHERE member.owner_id=email_management_records.owner_id AND member.kind='mail' AND member.related=email_management_records.id AND COALESCE(member.payload->'classification'->>'effective_entry','interaction')='interaction')`
	}
	if q.Direction != "" {
		add(` AND payload->>'direction'=$%d`, q.Direction)
	}
	if q.RequireNativeCapture {
		sql += ` AND COALESCE(payload->>'capture_id','')<>'' AND payload->>'capture_state'='complete'`
	}
	if q.CapturedOnly {
		sql += ` AND (COALESCE(payload->>'capture_id','')<>'' OR COALESCE(payload->>'local_send_id','')<>'')`
	}
	if q.MailMessageID != "" {
		add(` AND payload->>'message_id'=$%d`, q.MailMessageID)
	}
	if q.MailThreadID != "" {
		add(` AND payload->>'provider_thread_id'=$%d`, q.MailThreadID)
	}
	if q.Parent != "" {
		add(` AND parent=$%d`, q.Parent)
	}
	if q.Related != "" {
		add(` AND related=$%d`, q.Related)
	}
	if q.State != "" {
		add(` AND state=$%d`, q.State)
	}
	if len(q.States) > 0 {
		add(` AND state=ANY($%d)`, q.States)
	}
	if q.ConversationMailbox != "" {
		add(` AND EXISTS(SELECT 1 FROM email_management_records member WHERE member.owner_id=email_management_records.owner_id AND member.kind='mail' AND member.related=email_management_records.id AND member.parent=$%d)`, q.ConversationMailbox)
	}
	if q.Search != "" {
		if q.ConversationSearch {
			args = append(args, q.Search)
			n := len(args)
			sql += fmt.Sprintf(` AND (strpos(search_text,lower($%d))>0 OR EXISTS(SELECT 1 FROM email_management_records member WHERE member.owner_id=email_management_records.owner_id AND member.kind='mail' AND member.related=email_management_records.id AND strpos(member.search_text,lower($%d))>0))`, n, n)
		} else {
			add(` AND strpos(search_text,lower($%d))>0`, q.Search)
		}
	}
	if q.After != "" {
		if q.Asc {
			add(` AND sort_key COLLATE "C">$%d`, q.After)
		} else {
			add(` AND sort_key COLLATE "C"<$%d`, q.After)
		}
	}
	if q.Due != "" {
		add(` AND sort_key COLLATE "C"<=$%d`, q.Due)
	}
	if ordered {

		// These keys encode bytewise timestamps/IDs and use '~' as an inclusive
		// ID sentinel. Database locale ordering (e.g. en_US) sorts that sentinel
		// before letters, breaking exact deadlines and cross-backend cursors.
		if q.Asc {
			add(` ORDER BY sort_key COLLATE "C" ASC,id COLLATE "C" ASC LIMIT $%d`, q.Limit)
		} else {
			add(` ORDER BY sort_key COLLATE "C" DESC,id COLLATE "C" DESC LIMIT $%d`, q.Limit)
		}
	}
	return sql, args
}
func (p *emailPostgresRecords) list(q emailRowsQuery) ([]EmailRecord, error) {
	sql, args := emailPostgresQuery(p.owner, q, true)
	rows, err := p.tx.Query(p.ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EmailRecord{}
	for rows.Next() {
		var r EmailRecord
		if err = rows.Scan(&r.Owner, &r.Kind, &r.ID, &r.Parent, &r.Related, &r.State, &r.Search, &r.Sort, &r.Data); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func emailPostgresRun[T any](s *PostgresStore, ctx context.Context, op StoreOperation, owner, key string, input any, write bool, fn func(*emailEngine) (T, error)) (out T, err error) {
	ctx, cancel := operationContext(ctx, op, s.operationTimeouts)
	defer cancel()
	if err = operationContextError(op, ctx); err != nil {
		return
	}
	isolation := pgx.RepeatableRead
	if write {
		isolation = pgx.ReadCommitted
	}
	connection, err := s.db.Acquire(ctx)
	if err != nil {
		return out, classifyPostgresReadError(op, ctx, err)
	}
	released := false
	defer func() {
		if !released {
			connection.Release()
		}
	}()
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: isolation})
	if err != nil {
		return out, classifyPostgresReadError(op, ctx, err)
	}
	defer func() {
		cleanup, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer c()
		_ = tx.Rollback(cleanup)
	}()
	owner = normalizeConnectorOwner(owner)
	// Serialize short owner aggregate commands before their first data read. Model
	// and browser work never run while this lock or a transaction is held.
	if write {
		_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, connectorOwnerChannelAdvisoryKey(owner, "email_management"))
		if err != nil {
			return out, classifyPostgresReadError(op, ctx, err)
		}
	}
	e := &emailEngine{db: &emailPostgresRecords{ctx: ctx, tx: tx, owner: owner}, owner: owner, now: postgresTime(time.Now())}
	out, err = emailRun(e, op, key, input, fn)
	if err != nil {
		if errors.Is(err, errEmailCorrupt) || errors.Is(err, errEmailInvalid) || errors.Is(err, errEmailConflict) || errors.Is(err, errEmailNotFound) {
			return out, emailClassify(ctx, op, err)
		}
		return out, classifyPostgresReadError(op, ctx, err)
	}
	if err = tx.Commit(ctx); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		raw := connection.Hijack()
		released = true
		_ = raw.Close(cleanup)
		return out, storeError(ctx, op, StoreErrorUnknownOutcome, err)
	}
	return out, nil
}

func (p *emailPostgresRecords) changed() bool { return p.dirty }

func (p *emailPostgresRecords) count(q emailRowsQuery) (EmailScopeCounts, error) {
	q.After = ""
	sql, args := emailPostgresQuery(p.owner, q, false)
	sql = `SELECT count(*),count(*) FILTER(WHERE NOT EXISTS(SELECT 1 FROM email_management_records viewed WHERE viewed.owner_id=selected.owner_id AND viewed.kind='view' AND viewed.id=selected.id)) FROM (` + sql + `) selected`
	var out EmailScopeCounts
	err := p.tx.QueryRow(p.ctx, sql, args...).Scan(&out.Total, &out.Unseen)
	return out, err
}

func (p *emailPostgresRecords) exists(q emailRowsQuery) (bool, error) {
	sql, args := emailPostgresQuery(p.owner, q, false)
	var found bool
	err := p.tx.QueryRow(p.ctx, `SELECT EXISTS(`+sql+` LIMIT 1)`, args...).Scan(&found)
	return found, err
}
