package store

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

// A local send is a management fact backed by the frozen user snapshot and
// provider receipt. It is not a fabricated RFC822 capture or Message-ID.
func emailPublishLocalSend(e *emailEngine, d *EmailDraft) error {
	if d.State != "sent" || d.Receipt == nil || d.Snapshot == nil {
		return nil
	}
	box, ok := emailGet[app.EmailMailbox](e, "mailbox", d.MailboxID)
	if !ok || box.Provider != d.Receipt.Provider {
		return errEmailConflict
	}
	id := emailID(e.owner, "local_send", d.ID, d.SendKey)
	if d.Receipt.ProviderMessageID != "" {
		id = emailID(e.owner, d.MailboxID, d.Receipt.ProviderMessageID)
	}
	m, exists := emailGet[app.EmailMail](e, "mail", id)
	if exists && (m.Direction != "sent" || m.LocalSendID != "" && m.LocalSendID != d.Snapshot.InvocationID) {
		return errEmailConflict
	}
	if m.ConversationID != "" && d.ConversationID != "" && m.ConversationID != d.ConversationID {
		return errEmailConflict
	}
	if m.ConversationID != "" {
		d.ConversationID = m.ConversationID
	}
	conv, found := emailGet[app.EmailConversation](e, "conversation", d.ConversationID)
	if !found {
		conv = app.EmailConversation{ID: emailID(e.owner, "send_conversation", d.ID), OwnerID: e.owner, Title: d.Subject, CreatedAt: e.now}
		d.ConversationID = conv.ID
	}
	oldMembers, oldUnseen := 0, 0
	if m.ConversationID == conv.ID {
		oldMembers++
		if _, seen := emailGet[app.EmailViewReceipt](e, "view", id); !seen && emailEffectiveEntry(m) == "interaction" {
			oldUnseen++
		}
	}
	replaced := d.TimelineMailID != "" && d.TimelineMailID != id
	if replaced {
		previous, ok := emailGet[app.EmailMail](e, "mail", d.TimelineMailID)
		if !ok || previous.LocalSendID != d.Snapshot.InvocationID || previous.ConversationID != conv.ID {
			return errEmailConflict
		}
		oldMembers++
		previousView, previousSeen := emailGet[app.EmailViewReceipt](e, "view", previous.ID)
		if !previousSeen && emailEffectiveEntry(previous) == "interaction" {
			oldUnseen++
		}
		if _, currentSeen := emailGet[app.EmailViewReceipt](e, "view", id); previousSeen && !currentSeen {
			previousView.MailID = id
			emailPut(e, "view", id, "", "", "", "", id, previousView)
		}
		previous.SupersededByMailID = id
		emailSaveMail(e, previous)
	}

	if !exists {
		m = app.EmailMail{ID: id, OwnerID: e.owner, MailboxID: d.MailboxID, Direction: "sent", SourceTime: e.now, DiscoveredAt: e.now, ArrivalSequence: emailCounter(e, "arrival", true), CaptureState: "source_pending", ParseState: app.EmailParseReady, InputVersion: 1, Folder: "sent"}
	}
	m.SendConfirmationSource = d.ConfirmationSource
	m.LocalSendID = d.Snapshot.InvocationID
	m.ReplyMailID = d.ReplyMailID
	m.ProviderMessageID = d.Receipt.ProviderMessageID
	if m.CaptureID == "" {
		m.ProviderThreadID = d.Receipt.ProviderThreadID
	}
	m.ConversationID = d.ConversationID
	m.AssignmentState = app.EmailAssignmentAssigned
	if m.CaptureID == "" {
		m.Participants = emailUnique(append(append([]string{box.Address}, d.To...), d.CC...))
		m.Subject = d.Subject
	}
	if m.Classification == nil || m.Classification.Source != "manual" {
		revision := int64(1)
		if m.Classification != nil {
			revision = m.Classification.Revision + 1
		}
		m.Verification = nil
		m.Classification = &app.EmailClassification{Category: "interaction", EffectiveEntry: "interaction", Source: "manual", State: "ready", Revision: revision, ReasonCode: "user_send", UpdatedAt: e.now}
	}
	if m.RepresentationID == "" {
		rep := app.EmailRepresentation{ID: emailID("send_representation", d.Snapshot.InvocationID), MailID: id, Subject: d.Subject, From: []string{box.Address}, To: d.To, CC: d.CC, BodyText: d.Body, SourceTime: e.now, State: app.EmailParseReady, Coverage: "user_send_snapshot", ParserVersion: "user_send_snapshot_v1", CreatedAt: e.now}
		m.RepresentationID = rep.ID
		emailPut(e, "representation", rep.ID, id, "", rep.State, "", rep.ID, rep)
	}
	// Recompute only the affected contribution: classification transitions and
	// provisional/source deduplication must not double-count unseen members.
	conv.MemberCount += 1 - oldMembers
	if oldMembers != 1 || replaced {
		conv.MembershipVersion++
	}
	newUnseen := 0
	if _, seen := emailGet[app.EmailViewReceipt](e, "view", m.ID); !seen && emailEffectiveEntry(m) == "interaction" {
		newUnseen = 1
	}
	conv.UnseenCount += newUnseen - oldUnseen

	conv.InputVersion++
	conv.UpdatedAt = e.now
	conv.MailboxIDs = emailUnique(append(conv.MailboxIDs, d.MailboxID))
	conv.Participants = emailUnique(append(conv.Participants, m.Participants...))
	emailSaveConversation(e, conv)
	m.InputVersion++
	emailSaveMail(e, m)
	emailTouch(e, "mail:"+m.ID)
	emailTouch(e, "conversation:"+conv.ID)
	emailTouch(e, "search")
	emailCounter(e, "assignment_epoch", true)
	d.TimelineMailID = id
	if m.CaptureID != "" {
		d.SentMailID = id
		at := e.now
		d.ReconciledAt = &at
	}
	if _, err := emailRequest(e, EmailJobRequest{Kind: app.EmailJobMessageSummary, TargetID: m.ID, Rearm: true}); err != nil {
		return err
	}
	if _, err := emailRequest(e, EmailJobRequest{Kind: app.EmailJobConversationSummary, TargetID: conv.ID, Rearm: true}); err != nil {
		return err
	}
	return e.err
}

func emailConfirmSentSource(e *emailEngine, d *EmailDraft, sourceID string) error {
	if d.Snapshot == nil || sourceID == "" {
		return errEmailInvalid
	}
	source, ok := emailGet[app.EmailMail](e, "mail", sourceID)
	if !ok {
		return errEmailNotFound
	}
	if source.MailboxID != d.MailboxID || source.Direction != "sent" || source.CaptureID == "" || source.CaptureState != app.EmailCaptureComplete || source.ProviderMessageID == "" || source.SupersededByMailID != "" {
		return errEmailInvalid
	}
	if source.ConversationID != "" && d.ConversationID != "" && source.ConversationID != d.ConversationID {
		return errEmailConflict
	}
	box, ok := emailGet[app.EmailMailbox](e, "mailbox", d.MailboxID)
	if !ok {
		return errEmailNotFound
	}
	d.Receipt = &app.EmailSendResult{Provider: box.Provider, Status: "sent", ProviderMessageID: source.ProviderMessageID, ProviderThreadID: source.ProviderThreadID}
	d.ConfirmationSource = "owner_confirmed_capture"
	d.State = "sent"
	d.ErrorCode = ""
	d.Snapshot.State = "sent"
	d.Snapshot.Receipt = d.Receipt
	emailPut(e, "send_snapshot", emailID(d.ID, d.SendKey), d.ID, "", "sent", "", emailOrder(e.now, d.ID), d.Snapshot)
	if err := emailPublishLocalSend(e, d); err != nil {
		return err
	}
	d.SentMailID = source.ID
	now := e.now
	d.ReconciledAt = &now
	d.Version++
	return e.err
}
