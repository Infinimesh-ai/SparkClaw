package store

import (
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func emailBrowserKind(kind string) bool {
	return slices.Contains([]string{app.EmailJobCapture, app.EmailJobMarkRead, app.EmailJobDiscover, app.EmailJobThreadSync}, kind)
}
func emailAnalysisKind(kind string) bool {
	return slices.Contains([]string{app.EmailJobClassification, app.EmailJobMessageSummary, app.EmailJobAssignment, app.EmailJobRelationshipCheck, app.EmailJobConversationSummary}, kind)
}
func emailKnownJob(kind string) bool {
	// Source recovery is owner-scoped and touches no browser, so it is neither a
	// browser kind (no mailbox binding) nor an analysis kind (no model inputs).
	return emailBrowserKind(kind) || emailAnalysisKind(kind) || kind == app.EmailJobParse ||
		kind == app.EmailJobPresentation || kind == app.EmailJobSourceRecovery
}
func emailToken() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func emailSaveJob(e *emailEngine, j app.EmailJob) {
	emailPut(e, "job", j.ID, j.MailboxID, j.Kind, j.State, j.Priority, emailOrder(j.NextAttemptAt, j.ID), j)
}

const emailPageBatchSuperseded = "email_page_batch_superseded"

func emailSupersedeLegacyBrowserJobs(e *emailEngine, mailboxID string) {
	for _, kind := range []string{app.EmailJobCapture, app.EmailJobMarkRead, app.EmailJobThreadSync} {
		if emailEvents(e) {
			continue
		}
		query := emailRowsQuery{Kind: "job", Parent: mailboxID, Related: kind, States: []string{app.EmailJobQueued, app.EmailJobRetryWait, app.EmailJobRunning, app.EmailJobPaused}, Limit: 100, ExcludePageBatchSuperseded: true}
		for {
			jobs := emailList[app.EmailJob](e, query)
			for _, job := range jobs {
				if job.ErrorCode == emailPageBatchSuperseded || (job.State == app.EmailJobRunning && e.now.Before(job.LeaseExpiresAt)) {
					continue
				}
				job.State = app.EmailJobPaused
				job.ErrorCode = emailPageBatchSuperseded
				job.LeaseToken = ""
				job.LeaseExpiresAt = time.Time{}
				job.UpdatedAt = e.now
				emailSaveJob(e, job)
			}
			if e.err != nil || len(jobs) < query.Limit {
				break
			}
			last := jobs[len(jobs)-1]
			query.After = emailOrder(last.NextAttemptAt, last.ID)
		}
	}
}
func emailRequest(e *emailEngine, c EmailJobRequest) (app.EmailJob, error) {
	var zero app.EmailJob
	if emailTimelineLegacyKind(c.Kind) && emailTimelineActive(e) {
		return zero, errEmailConflict
	}
	priority := ""
	if (c.ForceAnalysis && (!c.Rearm || !emailAnalysisKind(c.Kind))) ||
		(c.AutomaticPoll && (c.Kind != app.EmailJobDiscover || !c.Rearm || c.RepeatInterval <= 0)) ||
		(c.RearmFailed && (!c.Rearm || c.Kind != app.EmailJobSourceRecovery && c.Kind != app.EmailJobDiscover)) ||
		!emailKnownJob(c.Kind) || c.TargetID == "" || len(c.Dependencies) > 200 || c.RepeatInterval < 0 || c.RepeatInterval > 24*time.Hour {
		return zero, errEmailInvalid
	}
	if emailBrowserKind(c.Kind) {
		if _, err := emailBound(e, c.MailboxID, c.BindingGeneration); err != nil {
			return zero, err
		}
	}
	selected := append([]string{}, c.Dependencies...)
	if emailAnalysisKind(c.Kind) && c.Dependencies == nil {
		if existing, ok := emailGet[app.EmailAnalysisTarget](e, "target", c.Kind+":"+c.TargetID); ok {
			selected = append(selected, existing.SelectedDependencies...)
			if existing.SelectedDependencies == nil {
				// Older snapshots did not distinguish selected and automatic
				// edges. Preserve their known dependencies once on upgrade.
				for ref := range existing.Inputs {
					selected = append(selected, ref)
				}
			}
		}
	}
	if emailEvents(e) && (c.Kind == app.EmailJobClassification || c.Kind == app.EmailJobAssignment) {
		clean := []string{}
		if c.Kind == app.EmailJobAssignment {
			for _, ref := range selected {
				if strings.HasPrefix(ref, "source:") || strings.HasPrefix(ref, "mapping:") || strings.HasPrefix(ref, "members:") {
					clean = append(clean, ref)
				}
			}
		}
		selected = clean
	}
	selected = emailUnique(selected)
	refs := append([]string{}, selected...)
	if c.Kind == app.EmailJobConversationSummary {
		if _, ok := emailGet[app.EmailConversation](e, "conversation", c.TargetID); !ok {
			return zero, errEmailNotFound
		}
		refs = append(refs, "conversation:"+c.TargetID)
		for _, member := range emailList[app.EmailMail](e, emailRowsQuery{Kind: "mail", Related: c.TargetID, Limit: 20}) {
			if member.ReplyMailID != "" {
				refs = append(refs, "mail:"+member.ReplyMailID)
			}
		}
	} else if c.Kind != app.EmailJobDiscover && c.Kind != app.EmailJobThreadSync && c.Kind != app.EmailJobSourceRecovery {
		// Source recovery is owner-scoped: its target is the owner, not a mail.
		m, err := emailMail(e, c.TargetID)
		if err != nil {
			return zero, err
		}
		if c.MailboxID != "" && m.MailboxID != c.MailboxID {
			return zero, errEmailInvalid
		}
		if c.Kind == app.EmailJobCapture && m.DiscoveryReason == app.EmailJobThreadSync {
			priority = app.EmailJobPriorityHistory
		}
		refs = append(refs, "mail:"+m.ID)
		if c.Kind == app.EmailJobAssignment && (m.ConversationID != "" || (!emailEvents(e) && emailEffectiveEntry(m) == "notification")) {
			return zero, errEmailConflict
		}
		if c.Kind == app.EmailJobRelationshipCheck && m.ConversationID == "" {
			return zero, errEmailConflict
		}
		if emailAnalysisKind(c.Kind) && !(emailEvents(e) && c.Kind == app.EmailJobClassification) {
			if m.ReplyMailID != "" {
				refs = append(refs, "mail:"+m.ReplyMailID)
			}
			for _, ref := range app.EmailAnalysisReferences(m.ReplyReferences) {
				refs = append(refs, "reply:"+ref)
			}
			if m.ProviderThreadID != "" {
				refs = append(refs, "thread:"+m.MailboxID+":"+m.ProviderThreadID)
			}
			if m.ContextID != "" && !emailEvents(e) {
				v, _ := emailGet[app.EmailContextVersion](e, "context", m.ContextID)
				for _, id := range v.RelatedMailIDs {
					refs = append(refs, "mail:"+id)
				}
				for _, ref := range v.UnresolvedReferences {
					refs = append(refs, "reply:"+ref)
				}
			}
		}
		if c.Kind == app.EmailJobAssignment || c.Kind == app.EmailJobRelationshipCheck {
			refs = append(refs, "search")
		}
	}
	if emailEvents(e) && (c.Kind == app.EmailJobClassification || c.Kind == app.EmailJobAssignment) {
		clean := []string{"source:" + c.TargetID}
		if c.Kind == app.EmailJobAssignment {
			clean = append(clean, "mapping:"+c.TargetID)
			for _, ref := range refs {
				if strings.HasPrefix(ref, "source:") || strings.HasPrefix(ref, "mapping:") || strings.HasPrefix(ref, "members:") || strings.HasPrefix(ref, "reply:") || strings.HasPrefix(ref, "thread:") {
					clean = append(clean, ref)
				}
			}
		}
		refs = clean
	}
	if emailEvents(e) && emailSummaryKind(c.Kind) {
		selected = []string{}
		refs = emailSourceSummaryRefs(e, c.Kind, c.TargetID)
	}
	refs = emailUnique(refs)
	if len(refs) > 200 {
		return zero, errEmailInvalid
	}
	for _, ref := range refs {
		if err := emailValidateDependency(e, c.Kind, c.TargetID, ref); err != nil {
			return zero, err
		}
	}
	if emailEvents(e) && emailAnalysisKind(c.Kind) {
		refs = append(refs, "policy:"+EmailEventPolicyVersion)
	}
	inputs := map[string]int64{}
	for _, ref := range refs {
		inputs[ref] = emailRef(e, ref)
	}
	fingerprint := emailID(string(emailJSON(inputs)))
	generation := int64(1)
	if emailAnalysisKind(c.Kind) {
		id := c.Kind + ":" + c.TargetID
		t, exists := emailGet[app.EmailAnalysisTarget](e, "target", id)
		if exists {
			generation = t.Generation
			if t.InputFingerprint != fingerprint {
				generation++
			}
		}
		changed := !exists || t.InputFingerprint != fingerprint
		selectionChanged := t.SelectedDependencies == nil || !slices.Equal(t.SelectedDependencies, selected)
		t.SelectedDependencies = selected
		t.Kind = c.Kind
		t.TargetID = c.TargetID
		t.Generation = generation
		t.InputFingerprint = fingerprint
		t.Inputs = inputs
		if changed {
			t.State = app.EmailSummaryPending
		}
		if changed || selectionChanged {
			emailPut(e, "target", id, c.Kind, c.TargetID, t.State, "", id, t)
		}
		if changed {
			if changed && containsEmail([]string{app.EmailJobMessageSummary, app.EmailJobConversationSummary}, c.Kind) {
				emailTouch(e, "summary:"+c.Kind+":"+c.TargetID)
			}
			for _, ref := range refs {
				depID := emailID(ref, id)
				emailPut(e, "dependency", depID, ref, id, "", "", depID, struct {
					Target    string
					Reference string
				}{id, ref})
			}
		}
	}
	id := emailID(c.Kind, c.TargetID, fingerprint, strings.TrimSpace(c.MailboxID), string(emailJSON(c.BindingGeneration)))
	j, exists := emailGet[app.EmailJob](e, "job", id)
	if exists {
		emailPollRequest(e, &j, c)
		// An explicit sync bypasses only the idle polling delay. Preserve
		// active leases, retry backoff, and explicitly scheduled requests.
		if c.Rearm && c.RepeatInterval == 0 && c.NextAttemptAt.IsZero() &&
			(c.Kind == app.EmailJobDiscover || c.Kind == app.EmailJobThreadSync) &&
			j.State == app.EmailJobQueued && j.ErrorCode == "" && j.NextAttemptAt.After(e.now) {
			j.NextAttemptAt = e.now
			j.UpdatedAt = e.now
			j.SyncTrigger, j.SyncActor = c.SyncTrigger, c.SyncActor
			emailSaveJob(e, j)
		}
		if emailAnalysisKind(c.Kind) && j.Generation != generation {
			j.Generation = generation
			j.State = app.EmailJobQueued
			j.Attempt = 0
			j.ErrorCode = ""
			j.LeaseToken = ""
			j.LeaseExpiresAt = time.Time{}
			j.NextAttemptAt = e.now
			j.UpdatedAt = e.now
			emailSaveJob(e, j)
		}
		if emailEvents(e) && emailDisabledAnalysis(j.Kind) {
			return j, e.err
		}
		if c.Rearm && (j.State == app.EmailJobFailed || j.State == app.EmailJobSucceeded || j.State == app.EmailJobPaused) {
			if c.RepeatInterval > 0 && j.State == app.EmailJobFailed && !c.RearmFailed {
				return j, e.err
			}
			var repeatAfter time.Time
			if c.RepeatInterval > 0 && (j.State == app.EmailJobSucceeded || c.Kind == app.EmailJobDiscover && j.State == app.EmailJobFailed) {
				// Round up to Store precision so even a fractional-microsecond
				// interval cannot make the next attempt eligible too early.
				repeatAfter = postgresTime(j.UpdatedAt.Add(c.RepeatInterval + time.Microsecond - time.Nanosecond))
			}
			if emailAnalysisKind(c.Kind) && !(emailEvents(e) && c.Kind == app.EmailJobClassification) {
				t, ok := emailGet[app.EmailAnalysisTarget](e, "target", c.Kind+":"+c.TargetID)
				if ok {
					if !c.ForceAnalysis && j.State == app.EmailJobSucceeded && t.State == app.EmailSummaryCurrent && emailFingerprint(e, t.Inputs) == t.InputFingerprint {
						return j, e.err
					}
					if j.State == app.EmailJobSucceeded {
						t.Generation++
						j.Generation = t.Generation
					}
					t.State = app.EmailSummaryPending
					emailPut(e, "target", t.Kind+":"+t.TargetID, t.Kind, t.TargetID, t.State, "", t.Kind+":"+t.TargetID, t)
				}
			}
			j.State = app.EmailJobQueued
			j.Attempt = 0
			j.ErrorCode = ""
			j.LeaseToken = ""
			j.LeaseExpiresAt = time.Time{}
			j.NextAttemptAt = c.NextAttemptAt
			j.SyncTrigger, j.SyncActor = c.SyncTrigger, c.SyncActor
			if j.NextAttemptAt.IsZero() {
				j.NextAttemptAt = e.now
			}
			if j.NextAttemptAt.Before(repeatAfter) {
				j.NextAttemptAt = repeatAfter
			}
			j.UpdatedAt = e.now
			emailSaveJob(e, j)
		}
		return j, e.err
	}
	at := c.NextAttemptAt
	if at.IsZero() {
		at = e.now
	}
	j = app.EmailJob{Priority: priority, ID: id, OwnerID: e.owner, Kind: c.Kind, TargetID: c.TargetID, MailboxID: c.MailboxID, BindingGeneration: c.BindingGeneration, InputFingerprint: fingerprint, Generation: generation, State: app.EmailJobQueued, MaxAttempts: 5, NextAttemptAt: postgresTime(at), CreatedAt: e.now, UpdatedAt: e.now}
	j.SyncTrigger, j.SyncActor = c.SyncTrigger, c.SyncActor
	emailPollRequest(e, &j, c)
	if emailEvents(e) && emailDisabledAnalysis(j.Kind) {
		j.State = app.EmailJobPaused
		j.ErrorCode = emailEventSuspended
	}
	if emailEvents(e) && emailAnalysisKind(j.Kind) {
		j.MaxAttempts = 2
	}
	emailSaveJob(e, j)
	return j, e.err
}
func emailLeaseCheck(e *emailEngine, l EmailJobLease, kind, target string) error {
	j, ok := emailGet[app.EmailJob](e, "job", l.JobID)
	if !ok {
		return errEmailNotFound
	}
	if emailTimelineLegacyKind(j.Kind) && emailTimelineActive(e) {
		return errEmailConflict
	}
	if emailEvents(e) && emailDisabledAnalysis(j.Kind) {
		return errEmailConflict
	}
	if emailEvents(e) && emailAnalysisKind(j.Kind) {
		t, _ := emailGet[app.EmailAnalysisTarget](e, "target", j.Kind+":"+j.TargetID)
		if _, ok := t.Inputs["policy:"+EmailEventPolicyVersion]; !ok || (emailSummaryKind(j.Kind) && !emailHasSourceSummaryPolicy(t)) {
			return errEmailConflict
		}
	}

	if l.OwnerID != "" && normalizeConnectorOwner(l.OwnerID) != e.owner {
		return errEmailInvalid
	}
	now := e.now
	if l.Now.After(now) {
		now = l.Now
	}
	if j.State != app.EmailJobRunning || j.LeaseToken == "" || j.LeaseToken != l.LeaseToken || !now.Before(j.LeaseExpiresAt) || j.Kind != kind || j.TargetID != target {
		return errEmailConflict
	}
	if emailBrowserKind(j.Kind) {
		_, err := emailBound(e, j.MailboxID, j.BindingGeneration)
		return err
	}
	return e.err
}
func emailClaim(e *emailEngine, c EmailJobClaim) (app.EmailJob, bool, error) {
	now := c.Now
	if now.IsZero() {
		now = e.now
	}
	duration := c.LeaseDuration
	if duration <= 0 {
		duration = time.Minute
	}
	if duration > 10*time.Minute {
		return app.EmailJob{}, false, errEmailInvalid
	}
	for _, kind := range c.Kinds {
		if !emailKnownJob(kind) {
			return app.EmailJob{}, false, errEmailInvalid
		}
	}
	// Filter kind and active mailbox before taking a bounded due window. Taking
	// one window per eligible kind avoids starvation behind browser backlog.
	kinds := c.Kinds
	if len(kinds) == 0 {
		kinds = []string{app.EmailJobCapture, app.EmailJobMarkRead, app.EmailJobDiscover, app.EmailJobThreadSync, app.EmailJobParse, app.EmailJobClassification, app.EmailJobPresentation, app.EmailJobMessageSummary, app.EmailJobAssignment, app.EmailJobRelationshipCheck, app.EmailJobConversationSummary}
	}
	boxes := emailList[app.EmailMailbox](e, emailRowsQuery{Kind: "mailbox", State: "active", Limit: 3})
	busyMailboxes := map[string]bool{}
	for _, kind := range kinds {
		if !emailBrowserKind(kind) {
			continue
		}
		for _, box := range boxes {
			busyMailboxes[box.ID] = emailMailboxBrowserBusy(e, box.ID, now)
		}
		break
	}
	jobs := []app.EmailJob{}
	for _, kind := range kinds {
		if emailEvents(e) && emailDisabledAnalysis(kind) {
			continue
		}
		if emailTimelineLegacyKind(kind) && emailTimelineActive(e) {
			continue
		}
		parents := []string{""}
		if emailBrowserKind(kind) {
			parents = nil
			for _, box := range boxes {
				if !busyMailboxes[box.ID] {
					parents = append(parents, box.ID)
				}
			}
		}
		for _, parent := range parents {
			query := emailRowsQuery{Kind: "job", Parent: parent, Related: kind, States: []string{app.EmailJobRunning, app.EmailJobRetryWait, app.EmailJobQueued, app.EmailJobPaused}, Asc: true, Limit: 100, Due: emailOrder(now, "~"), ExcludeHistory: kind == app.EmailJobCapture, ExcludePageBatchSuperseded: true}
			rows := emailList[app.EmailJob](e, query)
			jobs = append(jobs, rows...)
			if kind == app.EmailJobCapture {
				query.ExcludeHistory = false
				query.HistoryOnly = true
				jobs = append(jobs, emailList[app.EmailJob](e, query)...)
			}
		}
	}
	slices.SortFunc(jobs, func(a, b app.EmailJob) int {
		if a.Priority != b.Priority {
			if a.Priority == app.EmailJobPriorityHistory {
				return 1
			}
			if b.Priority == app.EmailJobPriorityHistory {
				return -1
			}
		}
		return strings.Compare(emailOrder(a.NextAttemptAt, a.ID), emailOrder(b.NextAttemptAt, b.ID))
	})
	{
		for _, j := range jobs {
			if j.State == app.EmailJobPaused && j.ErrorCode == emailPageBatchSuperseded {
				continue
			}
			if len(c.Kinds) > 0 && !slices.Contains(c.Kinds, j.Kind) {
				continue
			}
			if j.State == app.EmailJobRunning && now.Before(j.LeaseExpiresAt) {
				continue
			}
			if emailBrowserKind(j.Kind) {
				box, err := emailBound(e, j.MailboxID, j.BindingGeneration)
				if err != nil {
					current, present := emailGet[app.EmailMailbox](e, "mailbox", j.MailboxID)
					if present && current.Active && current.IntakeEnabled && current.BindingGeneration > j.BindingGeneration {
						_, requestErr := emailRequest(e, EmailJobRequest{Kind: j.Kind, TargetID: j.TargetID, MailboxID: j.MailboxID, BindingGeneration: current.BindingGeneration})
						if requestErr != nil {
							return app.EmailJob{}, false, requestErr
						}
						j.State = app.EmailJobSucceeded
						j.ErrorCode = "binding_replaced"
					} else {
						j.State = app.EmailJobPaused
					}
					j.LeaseToken = ""
					emailPollStop(e, &j)
					emailSaveJob(e, j)
					continue
				}
				// Discovery may probe for recovery; other browser work waits for it.
				if box.ErrorCode == string(app.ToolErrorEmailLoginRequired) && j.Kind != app.EmailJobDiscover {
					continue
				}
			}
			if j.Kind == app.EmailJobCapture || j.Kind == app.EmailJobMarkRead {
				mail, err := emailMail(e, j.TargetID)
				if err != nil {
					return app.EmailJob{}, false, err
				}
				if (j.Kind == app.EmailJobCapture && mail.CaptureState == app.EmailCaptureComplete) || (j.Kind == app.EmailJobMarkRead && mail.RemoteReadState == "read") {
					j.State = app.EmailJobSucceeded
					j.LeaseToken = ""
					j.ErrorCode = "already_committed"
					emailSaveJob(e, j)
					continue
				}
			}
			if j.Kind == app.EmailJobAssignment || j.Kind == app.EmailJobMessageSummary {
				m, ok := emailGet[app.EmailMail](e, "mail", j.TargetID)
				if ok && emailPatternVerification(m) {
					j.State = app.EmailJobSucceeded
					j.ErrorCode = "verification_pattern"
					j.LeaseToken = ""
					j.LeaseExpiresAt = time.Time{}
					emailSaveJob(e, j)
					if j.Kind == app.EmailJobMessageSummary {
						emailSkipAnalysis(e, j.Kind, j.TargetID, "verification_pattern")
					}
					continue
				}
				if ok && m.RepresentationID != "" && m.Classification == nil {
					if _, err := emailRequest(e, EmailJobRequest{Kind: app.EmailJobClassification, TargetID: m.ID}); err != nil {
						return app.EmailJob{}, false, err
					}
					j.State = app.EmailJobSucceeded
					j.ErrorCode = "awaiting_classification"
					j.LeaseToken = ""
					emailSaveJob(e, j)
					continue
				}
			}
			if j.Kind == app.EmailJobAssignment {
				m, _ := emailGet[app.EmailMail](e, "mail", j.TargetID)
				if m.ConversationID != "" || (!emailEvents(e) && emailEffectiveEntry(m) == "notification") {
					j.State = app.EmailJobSucceeded
					j.ErrorCode = "notification_routed"
					j.LeaseToken = ""
					emailSaveJob(e, j)
					continue
				}
			}
			if emailEvents(e) && emailAnalysisKind(j.Kind) {
				t, _ := emailGet[app.EmailAnalysisTarget](e, "target", j.Kind+":"+j.TargetID)
				if _, ok := t.Inputs["policy:"+EmailEventPolicyVersion]; !ok || (emailSummaryKind(j.Kind) && !emailHasSourceSummaryPolicy(t)) {
					if _, err := emailRequest(e, EmailJobRequest{Kind: j.Kind, TargetID: j.TargetID, Dependencies: []string{}}); err != nil {
						return app.EmailJob{}, false, err
					}
					j.State = app.EmailJobSucceeded
					j.ErrorCode = "superseded"
					j.LeaseToken = ""
					emailSaveJob(e, j)
					continue
				}
			}

			if emailAnalysisKind(j.Kind) {
				t, _ := emailGet[app.EmailAnalysisTarget](e, "target", j.Kind+":"+j.TargetID)
				if t.Generation != j.Generation || t.InputFingerprint != j.InputFingerprint || emailFingerprint(e, t.Inputs) != j.InputFingerprint {
					j.State = app.EmailJobSucceeded
					j.ErrorCode = "superseded"
					j.LeaseToken = ""
					emailSaveJob(e, j)
					continue
				}
			}
			if j.Attempt >= j.MaxAttempts {
				j.State = app.EmailJobFailed
				j.ErrorCode = "attempts_exhausted"
				emailFailureProjection(e, j)
				j.LeaseToken = ""
				emailPollFinish(e, &j)
				emailSaveJob(e, j)
				continue
			}
			j.Attempt++
			emailPollClaim(&j)
			if j.Kind == app.EmailJobDiscover {
				j.RoundStartedAt = postgresTime(now)
			}
			j.State = app.EmailJobRunning
			j.LeaseToken = emailToken()
			j.LeaseExpiresAt = postgresTime(now.Add(duration))
			j.NextAttemptAt = j.LeaseExpiresAt
			j.UpdatedAt = e.now
			if j.Kind == app.EmailJobThreadSync {
				thread, found := emailGet[app.EmailProviderThread](e, "thread", j.TargetID)
				if found && thread.ErrorCode != "" {
					thread.ErrorCode = ""
					emailPut(e, "thread", thread.ID, thread.MailboxID, "", "", "", emailOrder(thread.LastCheckedAt, thread.ID), thread)
				}
			}
			emailSaveJob(e, j)
			return j, true, e.err
		}
	}
	return app.EmailJob{}, false, e.err
}

// Claim runs under the owner's Store transaction/lock, making this check and
// the new lease atomic across workers (and across PostgreSQL clients). A live
// lease has a future NextAttemptAt, so it must not use the bounded due-job
// query or only inspect the requested kinds. Local jobs do not occupy a lane.
func emailMailboxBrowserBusy(e *emailEngine, mailboxID string, now time.Time) bool {
	query := emailRowsQuery{Kind: "job", Parent: mailboxID, State: app.EmailJobRunning, Limit: 100}
	for {
		jobs := emailList[app.EmailJob](e, query)
		for _, job := range jobs {
			if emailTimelineLegacyKind(job.Kind) && emailTimelineActive(e) {
				continue
			}
			if emailBrowserKind(job.Kind) && now.Before(job.LeaseExpiresAt) {
				return true
			}
		}
		if e.err != nil || len(jobs) < query.Limit {
			return false
		}
		last := jobs[len(jobs)-1]
		query.After = emailOrder(last.NextAttemptAt, last.ID)
	}
}

func emailRenew(e *emailEngine, c EmailJobRenew) (app.EmailJob, error) {
	j, ok := emailGet[app.EmailJob](e, "job", c.JobID)
	if !ok {
		return j, errEmailNotFound
	}
	if err := emailLeaseCheck(e, c.EmailJobLease, j.Kind, j.TargetID); err != nil {
		return j, err
	}
	duration := c.LeaseDuration
	if duration <= 0 || duration > 10*time.Minute {
		return j, errEmailInvalid
	}
	now := c.Now
	if now.IsZero() {
		now = e.now
	}
	j.LeaseExpiresAt = postgresTime(now.Add(duration))
	j.NextAttemptAt = j.LeaseExpiresAt
	j.UpdatedAt = e.now
	emailSaveJob(e, j)
	return j, e.err
}
func emailFinish(e *emailEngine, c EmailJobFinish) (app.EmailJob, error) {
	j, ok := emailGet[app.EmailJob](e, "job", c.JobID)
	if !ok {
		return j, errEmailNotFound
	}
	if err := emailLeaseCheck(e, c.EmailJobLease, j.Kind, j.TargetID); err != nil {
		return j, err
	}
	j.LeaseToken = ""
	j.LeaseExpiresAt = time.Time{}
	j.ErrorCode = c.ErrorCode
	j.UpdatedAt = e.now
	j.State = app.EmailJobSucceeded
	if j.Kind == app.EmailJobDiscover {
		j.RoundFinishedAt = e.now
	}
	if c.ErrorCode != "" {
		j.State = app.EmailJobFailed
		if !c.RetryAt.IsZero() && j.Attempt < j.MaxAttempts {
			j.State = app.EmailJobRetryWait
			j.NextAttemptAt = postgresTime(c.RetryAt)
		}
	}
	emailFailureProjection(e, j)
	emailPollFinish(e, &j)
	if j.Kind == app.EmailJobMarkRead && j.State == app.EmailJobSucceeded {
		m, err := emailMail(e, j.TargetID)
		if err != nil {
			return j, err
		}
		m.RemoteReadState = "read"
		m.RemoteReadObservedAt = e.now
		emailSaveMail(e, m)
	}
	emailSaveJob(e, j)
	return j, e.err
}

func emailLeaseInputs(e *emailEngine, l EmailJobLease, generation int64, fingerprint string) error {
	j, ok := emailGet[app.EmailJob](e, "job", l.JobID)
	if !ok {
		return errEmailNotFound
	}
	if j.Generation != generation || j.InputFingerprint != fingerprint {
		return errEmailConflict
	}
	return e.err
}

func emailFailureProjection(e *emailEngine, j app.EmailJob) {
	// Login expiry needs user action immediately, before retry exhaustion.
	if emailBrowserKind(j.Kind) && j.ErrorCode == string(app.ToolErrorEmailLoginRequired) {
		box, ok := emailGet[app.EmailMailbox](e, "mailbox", j.MailboxID)
		if ok && box.BindingGeneration == j.BindingGeneration {
			box.ErrorCode = j.ErrorCode
			box.UpdatedAt = e.now
			emailSaveMailbox(e, box)
		}
		return
	}
	if j.State != app.EmailJobFailed {
		return
	}
	if j.Kind == app.EmailJobDiscover || j.Kind == app.EmailJobThreadSync {
		box, ok := emailGet[app.EmailMailbox](e, "mailbox", j.MailboxID)
		if ok && box.BindingGeneration == j.BindingGeneration {
			if box.ErrorCode != string(app.ToolErrorEmailLoginRequired) {
				box.ErrorCode = j.ErrorCode
			}
			box.UpdatedAt = e.now
			emailSaveMailbox(e, box)
			if j.Kind == app.EmailJobThreadSync {
				thread, found := emailGet[app.EmailProviderThread](e, "thread", j.TargetID)
				if found && thread.MailboxID == box.ID {
					thread.ErrorCode = j.ErrorCode
					emailPut(e, "thread", thread.ID, box.ID, "", "", "", emailOrder(thread.LastCheckedAt, thread.ID), thread)
				}
			}
		}
		return
	}

	if j.Kind == app.EmailJobClassification {
		m, ok := emailGet[app.EmailMail](e, "mail", j.TargetID)
		if ok && (m.Classification == nil || (m.Classification.Source != "manual" && m.Classification.Source != "rule")) {
			revision := int64(1)
			if m.Classification != nil {
				revision = m.Classification.Revision + 1
			}
			m.Classification = &app.EmailClassification{Category: "unknown", EffectiveEntry: "interaction", Source: "fallback", State: "failed", Revision: revision, ReasonCode: "classification_failed", Uncertainty: true, UpdatedAt: e.now}
			emailApplyRule(e, &m)
			if err := emailSaveClassification(e, m); err != nil {
				e.err = err
			}
		}
	}
	if emailAnalysisKind(j.Kind) {
		t, ok := emailGet[app.EmailAnalysisTarget](e, "target", j.Kind+":"+j.TargetID)
		if ok && t.Generation == j.Generation && t.InputFingerprint == j.InputFingerprint {
			t.State = app.EmailSummaryFailed
			emailPut(e, "target", t.Kind+":"+t.TargetID, t.Kind, t.TargetID, t.State, "", t.Kind+":"+t.TargetID, t)
			if containsEmail([]string{app.EmailJobMessageSummary, app.EmailJobConversationSummary}, t.Kind) {
				emailTouch(e, "summary:"+t.Kind+":"+t.TargetID)
			}
		}
		return
	}
	if j.Kind == app.EmailJobCapture || j.Kind == app.EmailJobParse {
		m, err := emailMail(e, j.TargetID)
		if err != nil {
			e.err = err
			return
		}
		if j.Kind == app.EmailJobCapture && m.CaptureID == "" {
			m.CaptureState = app.EmailCaptureFailed
		}
		if j.Kind == app.EmailJobParse && m.RepresentationID == "" {
			m.ParseState = app.EmailParseFailed
		}
		emailSaveMail(e, m)
	}
}

func emailValidateDependency(e *emailEngine, kind, targetID, ref string) error {
	if len(ref) > 2048 {
		return errEmailInvalid
	}
	switch {
	case ref == "policy:"+EmailEventPolicyVersion || ref == EmailSourceSummaryPolicy:
	case strings.HasPrefix(ref, "source:"), strings.HasPrefix(ref, "mapping:"):
		_, err := emailMail(e, strings.SplitN(ref, ":", 2)[1])
		return err
	case strings.HasPrefix(ref, "members:"):
		if _, ok := emailGet[app.EmailConversation](e, "conversation", strings.TrimPrefix(ref, "members:")); !ok {
			return errEmailNotFound
		}
	case ref == "search":
		if kind == app.EmailJobMessageSummary || kind == app.EmailJobConversationSummary {
			return errEmailInvalid
		}
	case strings.HasPrefix(ref, "mail:"):
		if _, err := emailMail(e, strings.TrimPrefix(ref, "mail:")); err != nil {
			return err
		}
	case strings.HasPrefix(ref, "conversation:"):
		if _, ok := emailGet[app.EmailConversation](e, "conversation", strings.TrimPrefix(ref, "conversation:")); !ok {
			return errEmailNotFound
		}
	case strings.HasPrefix(ref, "reply:"):
		if strings.TrimPrefix(ref, "reply:") == "" {
			return errEmailInvalid
		}
	case strings.HasPrefix(ref, "thread:"):
		parts := strings.SplitN(ref, ":", 3)
		if len(parts) != 3 {
			return errEmailInvalid
		}
		if _, err := emailMailbox(e, parts[1]); err != nil {
			return err
		}
	case strings.HasPrefix(ref, "summary:"):
		parts := strings.SplitN(ref, ":", 3)
		if len(parts) != 3 || !containsEmail([]string{app.EmailJobMessageSummary, app.EmailJobConversationSummary}, parts[1]) {
			return errEmailInvalid
		}
		if kind == app.EmailJobMessageSummary || (kind == app.EmailJobConversationSummary && parts[1] != app.EmailJobMessageSummary) {
			return errEmailInvalid
		}
		if parts[1] == app.EmailJobMessageSummary {
			if _, err := emailMail(e, parts[2]); err != nil {
				return err
			}
		} else {
			if _, ok := emailGet[app.EmailConversation](e, "conversation", parts[2]); !ok {
				return errEmailNotFound
			}
		}
	default:
		return errEmailInvalid
	}
	return e.err
}
