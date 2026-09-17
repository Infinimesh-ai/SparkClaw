package store

import (
	"context"
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"slices"
	"strings"
	"time"
)

type emailMemoryRecords struct {
	owner   string
	records map[string]EmailRecord
	writes  map[string]EmailRecord
}

func (m *emailMemoryRecords) get(kind, id string) (EmailRecord, bool, error) {
	key := emailRecordKey(m.owner, kind, id)
	r, ok := m.writes[key]
	if !ok {
		r, ok = m.records[key]
	}
	return r, ok, nil
}
func (m *emailMemoryRecords) put(r EmailRecord) error {
	m.writes[emailRecordKey(r.Owner, r.Kind, r.ID)] = r
	return nil
}
func emailRowMatches(r EmailRecord, owner string, q emailRowsQuery) bool {

	matches := (!q.HistoryOnly || r.Search == "history") && (!q.ExcludeHistory || r.Search != "history") && r.Owner == owner && r.Kind == q.Kind && (q.Parent == "" || r.Parent == q.Parent) && (q.Related == "" || r.Related == q.Related) && (q.State == "" || r.State == q.State) && (len(q.States) == 0 || slices.Contains(q.States, r.State)) && (q.Search == "" || strings.Contains(r.Search, strings.ToLower(q.Search))) && (q.After == "" || (!q.Asc && r.Sort < q.After) || (q.Asc && r.Sort > q.After)) && (q.Due == "" || r.Sort <= q.Due)
	if !matches {
		return false
	}
	if r.Kind == "mail" {
		var m struct {
			SupersededByMailID string `json:"superseded_by_mail_id"`
		}
		if json.Unmarshal(r.Data, &m) != nil || m.SupersededByMailID != "" {
			return false
		}
	}
	if q.ExcludePageBatchSuperseded && r.State == "paused" {
		var job struct {
			ErrorCode string `json:"error_code"`
		}
		if json.Unmarshal(r.Data, &job) != nil || job.ErrorCode == emailPageBatchSuperseded {
			return false
		}
	}
	if q.Entry != "" || q.NotificationSubtype != "" || q.PendingOnly || q.UnassignedOnly || q.Validity != "" {
		var m app.EmailMail
		if json.Unmarshal(r.Data, &m) != nil {
			return false
		}
		if q.Entry != "" && (m.RepresentationID == "" || emailEffectiveEntry(m) != q.Entry) {
			return false
		}
		if q.Validity != "" && emailVerificationValidity(m, q.AsOf) != q.Validity {
			return false
		}
		if q.NotificationSubtype != "" && (m.Classification == nil || m.Classification.NotificationSubtype != q.NotificationSubtype) {
			return false
		}
		if q.PendingOnly && m.RepresentationID != "" {
			return false
		}
		if q.UnassignedOnly && m.ConversationID != "" {
			return false
		}
	}
	if q.RequireNativeCapture || q.Direction != "" || q.CapturedOnly || q.UncapturedOnly || q.MailMessageID != "" || q.MailThreadID != "" {
		var mail struct {
			CaptureID        string `json:"capture_id"`
			CaptureState     string `json:"capture_state"`
			SyncState        string `json:"sync_state"`
			LocalSendID      string `json:"local_send_id"`
			Direction        string `json:"direction"`
			MessageID        string `json:"message_id"`
			ProviderThreadID string `json:"provider_thread_id"`
		}
		if json.Unmarshal(r.Data, &mail) != nil || (q.RequireNativeCapture && (mail.CaptureID == "" || mail.CaptureState != app.EmailCaptureComplete)) || (q.Direction != "" && mail.Direction != q.Direction) || (q.CapturedOnly && mail.CaptureID == "" && mail.LocalSendID == "") || (q.UncapturedOnly && (mail.CaptureID != "" || mail.LocalSendID != "" || mail.SyncState == app.EmailMailSyncSuppressed)) || (q.MailMessageID != "" && mail.MessageID != q.MailMessageID) || (q.MailThreadID != "" && mail.ProviderThreadID != q.MailThreadID) {
			return false
		}
	}
	return true
}
func (m *emailMemoryRecords) list(q emailRowsQuery) ([]EmailRecord, error) {
	out := []EmailRecord{}
	mailboxMatches := map[string]bool{}
	searchMatches := map[string]bool{}
	eventTitleMatches := map[string]bool{}
	interactionMatches := map[string]bool{}
	memberMatches := map[string]bool{}
	each := func(visit func(EmailRecord)) {
		for key, r := range m.records {
			if current, ok := m.writes[key]; ok {
				r = current
			}
			visit(r)
		}
		for key, r := range m.writes {
			if _, ok := m.records[key]; !ok {
				visit(r)
			}
		}
	}
	if q.EventSearch || q.EventEntry != "" || q.NonemptyEvents || q.InteractionConversation || q.ConversationMailbox != "" || (q.ConversationSearch && q.Search != "") {
		each(func(r EmailRecord) {
			if r.Owner == m.owner && r.Kind == "conversation" && strings.Contains(r.Search, strings.ToLower(q.Search)) {
				eventTitleMatches[r.ID] = true
			}
			if r.Owner == m.owner && r.Kind == "mail" && r.Related != "" {
				var member app.EmailMail
				if json.Unmarshal(r.Data, &member) != nil || member.SupersededByMailID != "" {
					return
				}
				memberMatches[r.Related] = true
				if json.Unmarshal(r.Data, &member) == nil && emailEffectiveEntry(member) == "interaction" {
					interactionMatches[r.Related] = true
				}
				if r.Parent == q.ConversationMailbox {
					mailboxMatches[r.Related] = true
				}
				if strings.Contains(r.Search, strings.ToLower(q.Search)) {
					searchMatches[r.Related] = true
				}
			}
		})
	}
	base := q
	if q.ConversationSearch || q.EventSearch {
		base.Search = ""
	}
	each(func(r EmailRecord) {
		if !emailRowMatches(r, m.owner, base) {
			return
		}
		if q.NonemptyEvents && !memberMatches[r.ID] {
			return
		}
		if q.EventEntry != "" {
			entry := "notification"
			id := r.ID
			if r.Kind == "mail" {
				id = r.Related
				if id == "" {
					var mail app.EmailMail
					if json.Unmarshal(r.Data, &mail) != nil {
						return
					}
					entry = emailEffectiveEntry(mail)
				}
			}
			if id != "" && interactionMatches[id] {
				entry = "interaction"
			}
			if entry != q.EventEntry {
				return
			}
		}
		if q.InteractionConversation && !interactionMatches[r.ID] {
			return
		}
		if q.ConversationMailbox != "" && !mailboxMatches[r.ID] {
			return
		}
		if q.EventSearch && q.Search != "" && !strings.Contains(r.Search, strings.ToLower(q.Search)) && !eventTitleMatches[r.Related] && !searchMatches[r.Related] {
			return
		}
		if q.ConversationSearch && q.Search != "" && !strings.Contains(r.Search, strings.ToLower(q.Search)) && !searchMatches[r.ID] {
			return
		}
		out = append(out, r)
	})
	slices.SortFunc(out, func(a, b EmailRecord) int {
		if q.Asc {
			return strings.Compare(a.Sort, b.Sort)
		}
		return strings.Compare(b.Sort, a.Sort)
	})
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}
func emailMemoryRun[T any](s *MemoryStore, ctx context.Context, op StoreOperation, owner, key string, input any, write bool, fn func(*emailEngine) (T, error)) (out T, err error) {
	ctx, cancel := operationContext(ctx, op, s.operationTimeouts)
	defer cancel()
	if err = operationContextError(op, ctx); err != nil {
		return
	}
	if write {
		s.mu.Lock()
		defer s.mu.Unlock()
	} else {
		s.mu.RLock()
		defer s.mu.RUnlock()
	}
	db := &emailMemoryRecords{owner: normalizeConnectorOwner(owner), records: s.emailRecords, writes: map[string]EmailRecord{}}
	e := &emailEngine{db: db, owner: db.owner, now: postgresTime(time.Now())}
	out, err = emailRun(e, op, key, input, fn)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && write {
		for k, v := range db.writes {
			s.emailRecords[k] = v
		}
	}
	return out, emailClassify(ctx, op, err)
}
func cloneEmailRecords(in map[string]EmailRecord) map[string]EmailRecord {
	out := make(map[string]EmailRecord, len(in))
	for k, v := range in {
		v.Data = slices.Clone(v.Data)
		out[k] = v
	}
	return out
}

func (m *emailMemoryRecords) changed() bool { return len(m.writes) > 0 }

func (m *emailMemoryRecords) count(q emailRowsQuery) (EmailScopeCounts, error) {
	if q.EventEntry != "" {
		q.After = ""
		q.Limit = int(^uint(0) >> 1)
		rows, err := m.list(q)
		out := EmailScopeCounts{}
		for _, r := range rows {
			out.Total++
			if _, seen, _ := m.get("view", r.ID); !seen {
				out.Unseen++
			}
		}
		return out, err
	}
	q.After = ""
	out := EmailScopeCounts{}
	visit := func(r EmailRecord) {
		if !emailRowMatches(r, m.owner, q) {
			return
		}
		out.Total++
		if _, seen, _ := m.get("view", r.ID); !seen {
			out.Unseen++
		}
	}
	for key, r := range m.records {
		if current, ok := m.writes[key]; ok {
			r = current
		}
		visit(r)
	}
	for key, r := range m.writes {
		if _, ok := m.records[key]; !ok {
			visit(r)
		}
	}
	return out, nil
}

func (m *emailMemoryRecords) exists(q emailRowsQuery) (bool, error) {
	for key, r := range m.records {
		if current, ok := m.writes[key]; ok {
			r = current
		}
		if emailRowMatches(r, m.owner, q) {
			return true, nil
		}
	}
	for key, r := range m.writes {
		if _, ok := m.records[key]; !ok && emailRowMatches(r, m.owner, q) {
			return true, nil
		}
	}
	return false, nil
}
