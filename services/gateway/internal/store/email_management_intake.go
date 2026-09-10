package store

import (
	"errors"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func emailMailbox(e *emailEngine, id string) (app.EmailMailbox, error) {
	v, ok := emailGet[app.EmailMailbox](e, "mailbox", id)
	if !ok {
		return v, errEmailNotFound
	}
	return v, nil
}
func emailMail(e *emailEngine, id string) (app.EmailMail, error) {
	v, ok := emailGet[app.EmailMail](e, "mail", id)
	if !ok {
		return v, errEmailNotFound
	}
	return v, nil
}
func emailSaveMailbox(e *emailEngine, m app.EmailMailbox) {
	state := "paused"
	if m.Active && m.IntakeEnabled {
		state = "active"
	}
	emailPut(e, "mailbox", m.ID, m.Provider, "", state, "", m.ID, m)
}
func emailSaveMail(e *emailEngine, m app.EmailMail) {
	m.ViewedAt = nil
	m.Summary = nil
	search := strings.Join(append([]string{m.Subject, m.MessageID, m.ProviderThreadID}, m.Participants...), " ")
	if m.RepresentationID != "" {
		v, ok := emailGet[app.EmailRepresentation](e, "representation", m.RepresentationID)
		if ok {
			search += " " + v.BodyText
			for _, a := range v.Attachments {
				if emailEvents(e) {
					continue
				}
				search += " " + a.Name + " " + a.Text
			}
		}
	}
	if summary := emailProjectSummary(e, app.EmailJobMessageSummary, m.ID); summary != nil && !emailEvents(e) {
		search += " " + summary.Text
	}
	if m.Verification != nil || (m.Classification != nil && m.Classification.NotificationSubtype == "verification") {
		search = strings.Join(m.Participants, " ") + " " + m.MessageID + " " + m.ProviderThreadID
		if m.Verification != nil {
			search = strings.ReplaceAll(search, m.Verification.Code, "")
		}
	}
	emailPut(e, "mail", m.ID, m.MailboxID, m.ConversationID, m.AssignmentState, search, emailOrder(m.SourceTime, m.ID), m)
}
func emailCounter(e *emailEngine, name string, bump bool) int64 {
	v, _ := emailGet[struct{ Value int64 }](e, "counter", name)
	if bump {
		v.Value++
		emailPut(e, "counter", name, "", "", "", "", name, v)
	}
	return v.Value
}
func emailBound(e *emailEngine, id string, generation int64) (app.EmailMailbox, error) {
	m, err := emailMailbox(e, id)
	if err != nil {
		return m, err
	}
	if !m.Active || !m.IntakeEnabled || m.BindingGeneration != generation {
		return m, errEmailConflict
	}
	return m, nil
}
func emailBind(e *emailEngine, c EmailBindCommand) (app.EmailMailbox, error) {
	if !app.KnownEmailProvider(c.Provider) || c.ExpectedVersion < 0 {
		return app.EmailMailbox{}, errEmailInvalid
	}
	address, err := emailAddress(c.Address)
	if err != nil {
		return app.EmailMailbox{}, errors.Join(errEmailInvalid, err)
	}
	id := emailID(e.owner, c.Provider, address)
	m, exists := emailGet[app.EmailMailbox](e, "mailbox", id)
	selection, _ := emailGet[emailBindingSelection](e, "counter", "binding:"+c.Provider)
	if selection.Version != c.ExpectedVersion {
		return m, errEmailConflict
	}
	if selection.MailboxID != "" && selection.MailboxID != id {
		old, ok := emailGet[app.EmailMailbox](e, "mailbox", selection.MailboxID)
		if !ok {
			return m, errEmailCorrupt
		}
		old.Active = false
		old.IntakeEnabled = false
		old.BindingGeneration++
		old.Version = selection.Version + 1
		old.UpdatedAt = e.now
		emailSaveMailbox(e, old)
	}
	selection.Version++
	selection.MailboxID = id
	emailPut(e, "counter", "binding:"+c.Provider, "", "", "", "", "binding:"+c.Provider, selection)
	if !exists {
		boundary := c.Boundary
		if boundary.IsZero() {
			boundary = e.now
		}
		m = app.EmailMailbox{ID: id, OwnerID: e.owner, Provider: c.Provider, Address: c.Address, NormalizedAddress: address, Boundary: postgresTime(boundary), ActivatedAt: postgresTime(boundary)}
	}
	m.BindingGeneration++
	m.Version = selection.Version
	m.Active = true
	m.IntakeEnabled = c.Enabled
	m.ErrorCode = ""
	m.UpdatedAt = e.now
	emailSaveMailbox(e, m)
	return m, e.err
}
func emailPause(e *emailEngine, c EmailPauseCommand) (app.EmailMailbox, error) {
	m, err := emailMailbox(e, c.MailboxID)
	if err != nil {
		return m, err
	}
	if m.BindingGeneration != c.BindingGeneration {
		return m, errEmailConflict
	}
	m.Active = false
	m.IntakeEnabled = false
	m.BindingGeneration++
	selection, _ := emailGet[emailBindingSelection](e, "counter", "binding:"+m.Provider)
	if selection.MailboxID == m.ID {
		selection.Version++
		m.Version = selection.Version
		emailPut(e, "counter", "binding:"+m.Provider, "", "", "", "", "binding:"+m.Provider, selection)
	}
	m.ErrorCode = c.ErrorCode
	m.UpdatedAt = e.now
	emailSaveMailbox(e, m)
	return m, e.err
}
func emailAdmit(e *emailEngine, c EmailDiscoveryCommand) (EmailDiscoveryAdmission, error) {
	out := EmailDiscoveryAdmission{Mails: []app.EmailMail{}}
	box, err := emailBound(e, c.MailboxID, c.BindingGeneration)
	if err != nil {
		return out, err
	}
	if c.PageBatch && (c.Lease.JobID == "" || c.ThreadID != "") {
		return out, errEmailInvalid
	}
	if c.AcknowledgedPageID != "" && (!c.PageBatch || len(c.Members) != 0 || !strings.HasPrefix(c.AcknowledgedPageID, "page_") || !emailHashValid(strings.TrimPrefix(c.AcknowledgedPageID, "page_")) || !slices.Contains([]string{"unread", "recent_observation", "recent_inbound"}, c.Trigger)) {
		return out, errEmailInvalid
	}
	if c.Lease.JobID != "" {
		job, ok := emailGet[app.EmailJob](e, "job", c.Lease.JobID)
		if !ok {
			return out, errEmailNotFound
		}
		if !containsEmail([]string{app.EmailJobDiscover, app.EmailJobThreadSync}, job.Kind) {
			return out, errEmailInvalid
		}
		if c.PageBatch && job.Kind != app.EmailJobDiscover {
			return out, errEmailInvalid
		}
		if job.Kind == app.EmailJobDiscover && job.TargetID != box.ID {
			return out, errEmailInvalid
		}
		if job.Kind == app.EmailJobThreadSync && job.TargetID != emailID(box.ID, c.ThreadID) {
			return out, errEmailInvalid
		}
		if err = emailLeaseCheck(e, c.Lease, job.Kind, job.TargetID); err != nil {
			return out, err
		}
	}
	if c.AcknowledgedPageID != "" {
		if box.PageAcks == nil {
			box.PageAcks = map[string]string{}
		}
		box.PageAcks[c.Trigger] = c.AcknowledgedPageID
		box.UpdatedAt = e.now
		emailSaveMailbox(e, box)
		return out, e.err
	}
	if len(c.Members) > 100 || c.ObservedAt.IsZero() {
		return out, errEmailInvalid
	}
	for _, v := range c.Members {
		if strings.TrimSpace(v.ProviderMessageID) == "" || strings.TrimSpace(v.ProviderSelectionID) == "" || !slices.Contains([]string{"inbound", "sent", "unknown"}, v.Direction) {
			return out, errEmailInvalid
		}
	}
	if c.MaxPendingJobs < 0 {
		return out, errEmailInvalid
	}
	if c.PageBatch {
		emailSupersedeLegacyBrowserJobs(e, box.ID)
		if e.err != nil {
			return out, e.err
		}
	}
	if c.MaxPendingJobs > 0 {
		status, _ := emailOwnerStatus(e)
		newIDs := map[string]bool{}
		for _, v := range c.Members {
			if v.Draft {
				continue
			}
			id := emailID(e.owner, c.MailboxID, v.ProviderMessageID)
			if _, exists := emailGet[app.EmailMail](e, "mail", id); !exists {
				newIDs[id] = true
			}
		}
		if status.BacklogCount+len(newIDs) > c.MaxPendingJobs {
			return out, ErrEmailBacklogFull
		}
	}
	for _, v := range c.Members {
		if v.Draft {
			continue
		}
		id := emailID(e.owner, c.MailboxID, v.ProviderMessageID)
		m, exists := emailGet[app.EmailMail](e, "mail", id)
		if !exists {
			at := v.SourceTime
			if at.IsZero() {
				at = c.ObservedAt
			}
			m = app.EmailMail{ID: id, OwnerID: e.owner, MailboxID: c.MailboxID, ProviderMessageID: v.ProviderMessageID, ProviderSelectionID: v.ProviderSelectionID, ProviderThreadID: v.ProviderThreadID, Direction: v.Direction, Folder: v.Folder, DiscoveryReason: v.Reason, RemoteReadState: v.RemoteReadState, RemoteReadObservedAt: postgresTime(c.ObservedAt), DiscoveredAt: postgresTime(c.ObservedAt), SourceTime: postgresTime(at), ArrivalSequence: emailCounter(e, "arrival", true), CaptureState: "pending", ParseState: "pending", AssignmentState: app.EmailAssignmentPending}
			emailSaveMail(e, m)
			if !c.PageBatch {
				_, err = emailRequest(e, EmailJobRequest{Kind: app.EmailJobCapture, TargetID: id, MailboxID: box.ID, BindingGeneration: box.BindingGeneration})
				if err != nil {
					return out, err
				}
			}
		}
		if exists && m.LocalSendID != "" && m.CaptureID == "" {
			if v.Direction != "sent" {
				return out, errEmailConflict
			}
			m.ProviderSelectionID = v.ProviderSelectionID
			m.ProviderThreadID = v.ProviderThreadID
			m.Folder = v.Folder
			m.RemoteReadState = v.RemoteReadState
			m.RemoteReadObservedAt = postgresTime(c.ObservedAt)
			emailSaveMail(e, m)
			if !c.PageBatch {
				if _, err = emailRequest(e, EmailJobRequest{Kind: app.EmailJobCapture, TargetID: id, MailboxID: box.ID, BindingGeneration: box.BindingGeneration}); err != nil {
					return out, err
				}
			}
		}
		out.Mails = append(out.Mails, m)
		if v.ProviderThreadID != "" {
			threadID := emailID(box.ID, v.ProviderThreadID)
			thread, ok := emailGet[app.EmailProviderThread](e, "thread", threadID)
			if !ok {
				thread = app.EmailProviderThread{ID: threadID, MailboxID: box.ID, ProviderThreadID: v.ProviderThreadID, ProviderSelectionID: v.ProviderSelectionID, Folder: v.Folder, Coverage: "pending"}
				emailPut(e, "thread", threadID, box.ID, "", "", "", emailOrder(thread.LastCheckedAt, threadID), thread)
			}
			if !exists {
				thread.ObservationVersion++
				emailPut(e, "thread", threadID, box.ID, "", "", "", emailOrder(thread.LastCheckedAt, threadID), thread)
				emailTouch(e, "thread:"+box.ID+":"+v.ProviderThreadID)
			}
		}
	}
	if c.ThreadID == "" && emailRecentTrigger(c.Trigger) {
		box.Cursor = c.Cursor
		box.Coverage = c.Coverage
	}
	box.ErrorCode = ""
	box.LastCheckedAt = postgresTime(c.ObservedAt)
	box.UpdatedAt = e.now
	if !c.CompletedBoundary.IsZero() {
		if c.ThreadID != "" || !emailRecentTrigger(c.Trigger) {
			return out, errEmailInvalid
		}
		if c.CompletedBoundary.Before(box.Boundary) || c.CompletedBoundary.After(c.ObservedAt) || c.Coverage != "complete" {
			return out, errEmailInvalid
		}
		box.Boundary = postgresTime(c.CompletedBoundary)
	}
	emailSaveMailbox(e, box)
	out.Run = app.EmailSyncRun{ID: emailID(e.owner, c.CommandKey), MailboxID: box.ID, Trigger: c.Trigger, Cursor: c.Cursor, Coverage: c.Coverage, Discovered: len(out.Mails), Gaps: c.Gaps, CreatedAt: e.now}
	emailPut(e, "sync", out.Run.ID, box.ID, "", "", "", emailOrder(e.now, out.Run.ID), out.Run)
	if c.ThreadID != "" {
		id := emailID(box.ID, c.ThreadID)
		thread, existed := emailGet[app.EmailProviderThread](e, "thread", id)
		oldCoverage, oldCursor, oldGaps := thread.Coverage, thread.Cursor, string(emailJSON(emailUnique(thread.Gaps)))
		thread.ID = id
		thread.MailboxID = box.ID
		thread.ProviderThreadID = c.ThreadID
		if c.ProviderSelectionID != "" {
			thread.ProviderSelectionID = c.ProviderSelectionID
		}
		thread.Folder = c.Folder
		if c.Trigger == app.EmailJobThreadSync {
			thread.ErrorCode = ""
		}
		if !(existed && c.Trigger == "thread_discovery") {
			thread.Cursor = c.Cursor
			thread.Coverage = c.Coverage
			thread.Gaps = emailUnique(c.Gaps)
		}
		changed := !existed || thread.Coverage != oldCoverage || thread.Cursor != oldCursor || string(emailJSON(emailUnique(thread.Gaps))) != oldGaps
		if changed {
			thread.ObservationVersion++
		}
		if c.Trigger != "thread_discovery" {
			thread.LastCheckedAt = e.now
		}
		emailPut(e, "thread", id, box.ID, "", "", "", emailOrder(thread.LastCheckedAt, id), thread)
		if changed {
			emailTouch(e, "thread:"+box.ID+":"+c.ThreadID)
		}
	}
	return out, e.err
}
func emailCapture(e *emailEngine, c EmailCaptureCommand) (app.EmailMail, error) {
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
	kind, target := app.EmailJobCapture, m.ID
	if c.PageBatch {
		if !slices.Contains([]string{"read", "unknown"}, c.ReadState) || (c.ReadState == "read" && c.Capture.State != app.EmailCaptureComplete) {
			return m, errEmailInvalid
		}
		job, ok := emailGet[app.EmailJob](e, "job", c.Lease.JobID)
		if !ok || job.MailboxID != m.MailboxID || job.BindingGeneration != c.BindingGeneration {
			return m, errEmailInvalid
		}
		kind, target = app.EmailJobDiscover, m.MailboxID
	} else if c.ReadState != "" {
		return m, errEmailInvalid
	}
	if err = emailLeaseCheck(e, c.Lease, kind, target); err != nil {
		return m, err
	}
	v := c.Capture
	if v.ID == "" || !emailSafePath(v.ManifestPath) || !emailHashValid(v.ManifestSHA256) || !emailSafePath(v.OriginalPath) || !emailHashValid(v.OriginalSHA256) || !slices.Contains([]string{app.EmailCaptureComplete, app.EmailCapturePartial, app.EmailCaptureFailed}, v.State) {
		return m, errEmailInvalid
	}
	if c.PageBatch && m.CaptureState == app.EmailCaptureComplete {
		// Repeated pages may confirm a previously uncertain remote read, but
		// must not replace a durable source or invalidate local analysis.
		if c.ReadState == "read" && m.RemoteReadState != "read" {
			m.RemoteReadState = "read"
			m.RemoteReadObservedAt = e.now
			emailSaveMail(e, m)
		}
		return m, e.err
	}
	if prior, exists := emailGet[app.EmailCaptureVersion](e, "capture", v.ID); exists {
		if c.PageBatch && m.CaptureID == v.ID {
			// A crash between a partial receipt and the page ack must replay
			// safely too. A reused version ID with different content is invalid.
			v.CreatedAt = prior.CreatedAt
			if string(emailJSON(v)) == string(emailJSON(prior)) {
				return m, e.err
			}
		}
		return m, errEmailConflict
	}
	v.CreatedAt = e.now
	emailPut(e, "capture", v.ID, m.ID, "", "", "", v.ID, v)
	m.CaptureID = v.ID
	m.CaptureState = v.State
	m.InputVersion++
	if c.PageBatch && (c.ReadState == "read" || m.RemoteReadState != "read") {
		m.RemoteReadState = c.ReadState
		m.RemoteReadObservedAt = e.now
	}
	emailSaveMail(e, m)
	emailChangedMail(e, m)
	if v.State == app.EmailCaptureComplete || v.State == app.EmailCapturePartial {
		_, err = emailRequest(e, EmailJobRequest{Kind: app.EmailJobParse, TargetID: m.ID})
		if err != nil {
			return m, err
		}
	}
	if v.State == app.EmailCaptureComplete && !c.PageBatch {
		_, err = emailRequest(e, EmailJobRequest{Kind: app.EmailJobMarkRead, TargetID: m.ID, MailboxID: m.MailboxID, BindingGeneration: c.BindingGeneration})
	}
	return m, err
}
func emailRepresentation(e *emailEngine, c EmailRepresentationCommand) (app.EmailMail, error) {
	v := c.Representation
	m, err := emailMail(e, v.MailID)
	if err != nil {
		return m, err
	}
	if err = emailLeaseCheck(e, c.Lease, app.EmailJobParse, m.ID); err != nil {
		return m, err
	}
	if v.ID == "" || v.CaptureID != m.CaptureID || !slices.Contains([]string{app.EmailParseReady, app.EmailParsePartial, app.EmailParseUnsupported, app.EmailParseFailed}, v.State) || len(v.BodyText) > 4<<20 || len(v.Attachments) > 100 {
		return m, errEmailInvalid
	}
	if _, exists := emailGet[app.EmailRepresentation](e, "representation", v.ID); exists {
		return m, errEmailConflict
	}
	for _, a := range v.Attachments {
		missing := a.Path == "" && a.SHA256 == "" && containsEmail([]string{"missing", "skipped", "unsupported", "failed"}, a.State)
		if (!missing && (!emailSafePath(a.Path) || !emailHashValid(a.SHA256))) || (a.TextPath != "" && !emailSafePath(a.TextPath)) {
			return m, errEmailInvalid
		}
	}
	v.CreatedAt = e.now
	emailPut(e, "representation", v.ID, m.ID, "", "", "", v.ID, v)
	m.RepresentationID = v.ID
	m.Subject = v.Subject
	m.Participants = emailUnique(append(append(append([]string{}, v.From...), v.To...), v.CC...))
	if m.MessageID != "" && m.MessageID != v.MessageID {
		emailTouch(e, "reply:"+m.MessageID)
	}
	m.MessageID = v.MessageID
	m.ReplyReferences = append([]string{}, v.ReplyReferences...)
	if !v.SourceTime.IsZero() {
		m.SourceTime = postgresTime(v.SourceTime)
	}
	m.ParseState = v.State
	m.InputVersion++
	emailSaveMail(e, m)
	emailChangedMail(e, m)
	emailTouch(e, "source:"+m.ID)
	if v.MessageID != "" {
		emailTouch(e, "reply:"+v.MessageID)
	}
	emailTouch(e, "search")
	emailApplyRule(e, &m)
	emailSaveMail(e, m)
	kind := app.EmailJobClassification
	_, err = emailRequest(e, EmailJobRequest{Kind: kind, TargetID: m.ID})
	return m, err
}
func emailContext(e *emailEngine, c EmailContextCommand) (app.EmailMail, error) {
	v := c.Context
	m, err := emailMail(e, v.MailID)
	if err != nil {
		return m, err
	}
	if v.ID == "" || len(v.RelatedMailIDs) > 100 || len(v.UnresolvedReferences) > 100 {
		return m, errEmailInvalid
	}
	if _, exists := emailGet[app.EmailContextVersion](e, "context", v.ID); exists {
		return m, errEmailConflict
	}
	for _, id := range v.RelatedMailIDs {
		if _, err = emailMail(e, id); err != nil {
			return m, err
		}
	}
	if m.ContextID != "" {
		old, ok := emailGet[app.EmailContextVersion](e, "context", m.ContextID)
		if ok && slices.Equal(emailUnique(old.RelatedMailIDs), emailUnique(v.RelatedMailIDs)) && slices.Equal(emailUnique(old.UnresolvedReferences), emailUnique(v.UnresolvedReferences)) && old.Coverage == v.Coverage {
			return m, e.err
		}
	}
	v.CreatedAt = e.now
	emailPut(e, "context", v.ID, m.ID, "", "", "", v.ID, v)
	m.ContextID = v.ID
	m.InputVersion++
	emailSaveMail(e, m)
	emailChangedMail(e, m)
	return m, e.err
}
func emailViewed(e *emailEngine, c EmailViewedCommand) ([]app.EmailViewReceipt, error) {
	if len(c.MailIDs) < 1 || len(c.MailIDs) > 100 {
		return nil, errEmailInvalid
	}
	out := []app.EmailViewReceipt{}
	at := c.ViewedAt
	if at.IsZero() {
		at = e.now
	}
	for _, id := range emailUnique(c.MailIDs) {
		m, err := emailMail(e, id)
		if err != nil {
			return nil, err
		}
		v, ok := emailGet[app.EmailViewReceipt](e, "view", id)
		if !ok {
			v = app.EmailViewReceipt{MailID: id, FirstViewedAt: postgresTime(at)}
			emailPut(e, "view", id, "", "", "", "", id, v)
			if m.ConversationID != "" && (emailEvents(e) || emailEffectiveEntry(m) == "interaction") {
				conv, _ := emailGet[app.EmailConversation](e, "conversation", m.ConversationID)
				if conv.UnseenCount > 0 {
					conv.UnseenCount--
					emailSaveConversation(e, conv)
				}
			}
		}
		out = append(out, v)
	}
	return out, e.err
}

type emailBindingSelection struct {
	MailboxID string
	Version   int64
}

func emailRecentTrigger(trigger string) bool {
	return trigger == "" || trigger == "recent" || trigger == "recent_inbound"
}
