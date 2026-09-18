package store

import (
	"regexp"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var emailDatePathPattern = regexp.MustCompile(`^\d{4}/(?:0[1-9]|1[0-2])/(?:0[1-9]|[12]\d|3[01])$`)

// emailCaptureDatePath extracts the day directory a capture was committed under
// so it can be indexed for date-scoped cleanup. Legacy pointers predate the date
// layout and index as empty, which is also how the cutover sweep finds them.
func emailCaptureDatePath(manifestPath string) string {
	parts := strings.Split(manifestPath, "/")
	if len(parts) != 10 || parts[0] != "email" || parts[7] != "source" || parts[9] != "capture.json" {
		return ""
	}
	date := strings.Join(parts[1:4], "/")
	if !emailDatePathPattern.MatchString(date) {
		return ""
	}
	return date
}

// emailPurgeCaptures tombstones the selected capture versions. It deliberately
// keeps ManifestPath, OriginalPath and both hashes: they are the unlink target
// and, if the process dies before the bytes are gone, the only evidence the
// recovery sweep has. Bodies and mail metadata are never touched.
func emailPurgeCaptures(e *emailEngine, c EmailCapturePurgeCommand) (EmailCapturePurgeResult, error) {
	var out EmailCapturePurgeResult
	if c.At.IsZero() || !slices.Contains([]string{app.EmailPurgeManual, app.EmailPurgeLegacy}, c.Reason) {
		return out, errEmailInvalid
	}
	limit := emailLimit(c.Limit)
	candidates, cursor, more, err := emailPurgeSelect(e, c, limit)
	if err != nil {
		return out, err
	}
	for _, v := range candidates {
		if c.Finalize {
			// The bytes are gone; drop the row out of the reaper's index. The
			// tombstone itself stays so the window keeps reporting "cleaned up".
			if v.PurgedAt == nil {
				continue
			}
			emailPut(e, "capture", v.ID, v.MailID, emailCaptureDatePath(v.ManifestPath), "purged_clean", "", v.ID, v)
			out.Captures = append(out.Captures, v)
			continue
		}
		if v.PurgedAt != nil {
			continue
		}
		at := c.At.UTC()
		v.PurgedAt, v.PurgeReason = &at, c.Reason
		// State "purged" is what makes the recovery sweep an indexed lookup
		// instead of a walk of every capture the owner has ever taken.
		emailPut(e, "capture", v.ID, v.MailID, emailCaptureDatePath(v.ManifestPath), "purged", "", v.ID, v)
		m, err := emailMail(e, v.MailID)
		if err != nil {
			return out, err
		}
		if m.CaptureID == v.ID && m.CaptureState == app.EmailCaptureSourceMissing {
			if err := emailCancelSourceRepair(e, &m, c.Reason); err != nil {
				return out, err
			}
		}
		emailPurgeAttachments(e, m)
		// Bump the owner revision through the mail record so the window sees the
		// change. Never emailChangedMail: a purge must not re-trigger analysis.
		emailSaveMail(e, m)
		out.Captures = append(out.Captures, v)
	}
	if e.err != nil {
		return EmailCapturePurgeResult{}, e.err
	}
	out.NextCursor, out.Remaining = cursor, more
	return out, nil
}

// emailPurgeAttachments marks the representation's parts unavailable. The parsed
// body and the attachment paths survive, so the mail stays readable and the
// record still shows what used to be on disk.
func emailPurgeAttachments(e *emailEngine, m app.EmailMail) {
	if m.RepresentationID == "" {
		return
	}
	r, ok := emailGet[app.EmailRepresentation](e, "representation", m.RepresentationID)
	if !ok {
		return
	}
	changed := false
	for i := range r.Attachments {
		if r.Attachments[i].State != app.EmailAttachmentPurged {
			r.Attachments[i].State, changed = app.EmailAttachmentPurged, true
		}
	}
	if changed {
		emailPut(e, "representation", r.ID, m.ID, "", "", "", r.ID, r)
	}
}

// emailPurgeSelect resolves a scope to capture versions plus the cursor to
// resume from. The cursor belongs to whichever record kind the scope paginates
// over — captures sort by ID, mails by emailOrder — so each scope returns its
// own rather than assuming a shared one.
func emailPurgeSelect(e *emailEngine, c EmailCapturePurgeCommand, limit int) (out []app.EmailCaptureVersion, cursor string, more bool, err error) {
	capture := func(id string) []app.EmailCaptureVersion {
		if v, ok := emailGet[app.EmailCaptureVersion](e, "capture", id); ok {
			return []app.EmailCaptureVersion{v}
		}
		return nil
	}
	switch c.Scope {
	case EmailPurgeScopeMail:
		m, err := emailMail(e, c.MailID)
		if err != nil {
			return nil, "", false, err
		}
		if m.CaptureID == "" {
			return nil, "", false, nil
		}
		return capture(m.CaptureID), "", false, nil
	case EmailPurgeScopeCaptureIDs:
		if len(c.CaptureIDs) == 0 || len(c.CaptureIDs) > limit {
			return nil, "", false, errEmailInvalid
		}
		for _, id := range c.CaptureIDs {
			out = append(out, capture(id)...)
		}
		return out, "", false, nil
	case EmailPurgeScopeDate, EmailPurgeScopeAll:
		q := emailRowsQuery{Kind: "capture", After: c.After, Limit: limit + 1}
		if c.Scope == EmailPurgeScopeDate {
			if !emailDatePathPattern.MatchString(c.DatePath) {
				return nil, "", false, errEmailInvalid
			}
			q.Related = c.DatePath
		}
		rows := emailList[app.EmailCaptureVersion](e, q)
		if len(rows) > limit {
			rows = rows[:limit]
			cursor, more = rows[len(rows)-1].ID, true
		}
		return rows, cursor, more, nil
	case EmailPurgeScopeMailbox:
		if c.MailboxID == "" {
			return nil, "", false, errEmailInvalid
		}
		// MailboxID indexes onto the mail record's Parent, matching ListEmailMails.
		rows := emailList[app.EmailMail](e, emailRowsQuery{Kind: "mail", Parent: c.MailboxID, After: c.After, Limit: limit + 1})
		if len(rows) > limit {
			rows = rows[:limit]
			last := rows[len(rows)-1]
			cursor, more = emailOrder(last.SourceTime, last.ID), true
		}
		for _, m := range rows {
			if m.CaptureID != "" {
				out = append(out, capture(m.CaptureID)...)
			}
		}
		return out, cursor, more, nil
	case EmailPurgeScopeConversation:
		if c.ConversationID == "" {
			return nil, "", false, errEmailInvalid
		}
		rows := emailList[app.EmailMail](e, emailRowsQuery{Kind: "mail", Related: c.ConversationID, After: c.After, Limit: limit + 1, IncludeSuperseded: true})
		if len(rows) > limit {
			rows = rows[:limit]
			last := rows[len(rows)-1]
			cursor, more = emailOrder(last.SourceTime, last.ID), true
		}
		for _, m := range rows {
			if m.CaptureID != "" {
				out = append(out, capture(m.CaptureID)...)
			}
		}
		return out, cursor, more, nil
	}
	return nil, "", false, errEmailInvalid
}

func emailScanCaptures(e *emailEngine, c EmailCaptureScan) ([]app.EmailCaptureVersion, error) {
	return emailList[app.EmailCaptureVersion](e, emailRowsQuery{Kind: "capture", State: c.State, After: c.After, Limit: emailLimit(c.Limit)}), e.err
}

// emailAdoptCapture registers bytes that landed before their transaction
// committed. It carries no job lease, so it is deliberately restricted to
// filling a hole: a mail that already has a capture is left untouched, and a
// capture ID that already exists is a conflict. That makes a race with a live
// capture resolve in the live capture's favour.
func emailAdoptCapture(e *emailEngine, c EmailCaptureAdoption) (app.EmailMail, error) {
	// The legacy source-recovery worker has no browser lease. Fence its late
	// adoption explicitly once timeline journals own the intake transaction.
	if emailTimelineActive(e) {
		return app.EmailMail{}, errEmailConflict
	}
	m, err := emailMail(e, c.Capture.MailID)
	if err != nil {
		return m, err
	}
	if m.MailboxID != c.MailboxID {
		return m, errEmailInvalid
	}
	if _, err = emailBound(e, c.MailboxID, c.BindingGeneration); err != nil {
		return m, err
	}
	if m.CaptureID != "" {
		return m, e.err
	}
	v := c.Capture
	if v.ID == "" || !emailSafePath(v.ManifestPath) || !emailHashValid(v.ManifestSHA256) || !emailSafePath(v.OriginalPath) ||
		!emailHashValid(v.OriginalSHA256) || !slices.Contains([]string{app.EmailCaptureComplete, app.EmailCapturePartial}, v.State) ||
		v.PurgedAt != nil || v.PurgeReason != "" {
		return m, errEmailInvalid
	}
	if _, exists := emailGet[app.EmailCaptureVersion](e, "capture", v.ID); exists {
		return m, errEmailConflict
	}
	v.CreatedAt = e.now
	emailPut(e, "capture", v.ID, m.ID, emailCaptureDatePath(v.ManifestPath), "", "", v.ID, v)
	m.CaptureID, m.CaptureState = v.ID, v.State
	m.InputVersion++
	emailSaveMail(e, m)
	emailChangedMail(e, m)
	_, err = emailRequest(e, EmailJobRequest{Kind: app.EmailJobParse, TargetID: m.ID})
	return m, err
}
