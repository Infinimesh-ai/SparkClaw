package store

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) BindEmailMailbox(ctx context.Context, c EmailBindCommand) (app.EmailMailbox, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMailbox), errEmailCommandInvalid(ctx, OperationBindEmailMailbox)
	}
	return emailMemoryRun(s, ctx, OperationBindEmailMailbox, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMailbox, error) { return emailBind(e, c) })
}

func (s *MemoryStore) PauseEmailMailbox(ctx context.Context, c EmailPauseCommand) (app.EmailMailbox, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMailbox), errEmailCommandInvalid(ctx, OperationPauseEmailMailbox)
	}
	return emailMemoryRun(s, ctx, OperationPauseEmailMailbox, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMailbox, error) { return emailPause(e, c) })
}

func (s *MemoryStore) GetEmailOwnerStatus(ctx context.Context, ownerID string) (app.EmailOwnerStatus, error) {
	return emailMemoryRun(s, ctx, OperationGetEmailOwnerStatus, ownerID, "", nil, false, func(e *emailEngine) (app.EmailOwnerStatus, error) { return emailOwnerStatus(e) })
}

func (s *MemoryStore) ListEmailMailboxes(ctx context.Context, ownerID string) ([]app.EmailMailbox, error) {
	return emailMemoryRun(s, ctx, OperationListEmailMailboxes, ownerID, "", nil, false, func(e *emailEngine) ([]app.EmailMailbox, error) { return emailMailboxes(e) })
}

func (s *MemoryStore) GetEmailMailbox(ctx context.Context, ownerID, id string) (app.EmailMailbox, bool, error) {
	pair, err := emailMemoryRun(s, ctx, OperationGetEmailMailbox, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailMailbox], error) {
		return emailReadRecord[app.EmailMailbox](e, "mailbox", id)
	})
	return pair.Value, pair.Found, err
}

func (s *MemoryStore) BeginEmailSync(ctx context.Context, c EmailSyncBeginCommand) (EmailSyncCheckpoint, error) {
	if c.CommandKey == "" {
		return EmailSyncCheckpoint{}, errEmailCommandInvalid(ctx, OperationBeginEmailSync)
	}
	return emailMemoryRun(s, ctx, OperationBeginEmailSync, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailSyncCheckpoint, error) { return emailBeginSync(e, c) })
}

func (s *MemoryStore) CommitEmailSync(ctx context.Context, c EmailSyncCommitCommand) (EmailSyncCommitResult, error) {
	if c.CommandKey == "" {
		return EmailSyncCommitResult{}, errEmailCommandInvalid(ctx, OperationCommitEmailSync)
	}
	return emailMemoryRun(s, ctx, OperationCommitEmailSync, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailSyncCommitResult, error) { return emailCommitSync(e, c) })
}

func (s *MemoryStore) ReportEmailSourceFailure(ctx context.Context, c EmailSourceFailureCommand) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return app.EmailMail{}, errEmailCommandInvalid(ctx, OperationReportEmailSourceFailure)
	}
	return emailMemoryRun(s, ctx, OperationReportEmailSourceFailure, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailReportSourceFailure(e, c) })
}

func (s *MemoryStore) ListEmailSyncWarnings(ctx context.Context, q EmailQuery) (EmailSyncWarningPage, error) {
	return emailMemoryRun(s, ctx, OperationListEmailSyncWarnings, q.OwnerID, "", nil, false, func(e *emailEngine) (EmailSyncWarningPage, error) { return emailSyncWarnings(e, q) })
}

func (s *MemoryStore) AcknowledgeEmailSyncWarning(ctx context.Context, c EmailSyncWarningAck) (app.EmailSyncWarning, error) {
	if c.CommandKey == "" {
		return app.EmailSyncWarning{}, errEmailCommandInvalid(ctx, OperationAcknowledgeEmailSyncWarning)
	}
	return emailMemoryRun(s, ctx, OperationAcknowledgeEmailSyncWarning, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailSyncWarning, error) { return emailAcknowledgeSyncWarning(e, c) })
}

func (s *MemoryStore) AdmitEmailDiscovery(ctx context.Context, c EmailDiscoveryCommand) (EmailDiscoveryAdmission, error) {
	if c.CommandKey == "" {
		return *new(EmailDiscoveryAdmission), errEmailCommandInvalid(ctx, OperationAdmitEmailDiscovery)
	}
	return emailMemoryRun(s, ctx, OperationAdmitEmailDiscovery, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailDiscoveryAdmission, error) { return emailAdmit(e, c) })
}

func (s *MemoryStore) RequestEmailJob(ctx context.Context, c EmailJobRequest) (app.EmailJob, error) {
	if c.CommandKey == "" {
		return *new(app.EmailJob), errEmailCommandInvalid(ctx, OperationRequestEmailJob)
	}
	return emailMemoryRun(s, ctx, OperationRequestEmailJob, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailJob, error) { return emailRequest(e, c) })
}

func (s *MemoryStore) ClaimEmailJob(ctx context.Context, c EmailJobClaim) (app.EmailJob, bool, error) {
	pair, err := emailMemoryRun(s, ctx, OperationClaimEmailJob, c.OwnerID, "", c, true, func(e *emailEngine) (emailOptional[app.EmailJob], error) { return emailClaimOptional(e, c) })
	return pair.Value, pair.Found, err
}

func (s *MemoryStore) RenewEmailJob(ctx context.Context, c EmailJobRenew) (app.EmailJob, error) {
	return emailMemoryRun(s, ctx, OperationRenewEmailJob, c.OwnerID, "", c, true, func(e *emailEngine) (app.EmailJob, error) { return emailRenew(e, c) })
}

func (s *MemoryStore) FinishEmailJob(ctx context.Context, c EmailJobFinish) (app.EmailJob, error) {
	return emailMemoryRun(s, ctx, OperationFinishEmailJob, c.OwnerID, "", c, true, func(e *emailEngine) (app.EmailJob, error) { return emailFinish(e, c) })
}

func (s *MemoryStore) PublishEmailCapture(ctx context.Context, c EmailCaptureCommand) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationPublishEmailCapture)
	}
	return emailMemoryRun(s, ctx, OperationPublishEmailCapture, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailCapture(e, c) })
}

func (s *MemoryStore) PublishEmailRepresentation(ctx context.Context, c EmailRepresentationCommand) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationPublishEmailRepresentation)
	}
	return emailMemoryRun(s, ctx, OperationPublishEmailRepresentation, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailRepresentation(e, c) })
}

func (s *MemoryStore) PublishEmailContext(ctx context.Context, c EmailContextCommand) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationPublishEmailContext)
	}
	return emailMemoryRun(s, ctx, OperationPublishEmailContext, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailContext(e, c) })
}

func (s *MemoryStore) ExpandEmailRefresh(ctx context.Context, c EmailRefreshCommand) (EmailRefreshResult, error) {
	if c.CommandKey == "" {
		return *new(EmailRefreshResult), errEmailCommandInvalid(ctx, OperationExpandEmailRefresh)
	}
	return emailMemoryRun(s, ctx, OperationExpandEmailRefresh, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailRefreshResult, error) { return emailExpand(e, c) })
}

func (s *MemoryStore) CommitEmailAssignment(ctx context.Context, c EmailAssignmentCommand) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationCommitEmailAssignment)
	}
	return emailMemoryRun(s, ctx, OperationCommitEmailAssignment, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailAssignment(e, c) })
}

func (s *MemoryStore) PublishEmailConcern(ctx context.Context, c EmailConcernCommand) (app.EmailAssignmentConcern, error) {
	if c.CommandKey == "" {
		return *new(app.EmailAssignmentConcern), errEmailCommandInvalid(ctx, OperationPublishEmailConcern)
	}
	return emailMemoryRun(s, ctx, OperationPublishEmailConcern, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailAssignmentConcern, error) { return emailConcern(e, c) })
}

func (s *MemoryStore) PublishEmailSummary(ctx context.Context, c EmailSummaryCommand) (app.EmailSummary, error) {
	if c.CommandKey == "" {
		return *new(app.EmailSummary), errEmailCommandInvalid(ctx, OperationPublishEmailSummary)
	}
	return emailMemoryRun(s, ctx, OperationPublishEmailSummary, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailSummary, error) { return emailSummary(e, c) })
}

func (s *MemoryStore) MarkEmailMailsViewed(ctx context.Context, c EmailViewedCommand) ([]app.EmailViewReceipt, error) {
	if c.CommandKey == "" {
		return *new([]app.EmailViewReceipt), errEmailCommandInvalid(ctx, OperationMarkEmailMailsViewed)
	}
	return emailMemoryRun(s, ctx, OperationMarkEmailMailsViewed, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) ([]app.EmailViewReceipt, error) { return emailViewed(e, c) })
}

func (s *MemoryStore) ListEmailMails(ctx context.Context, q EmailQuery) (EmailMailPage, error) {
	return emailMemoryRun(s, ctx, OperationListEmailMails, q.OwnerID, "", nil, false, func(e *emailEngine) (EmailMailPage, error) { return emailMails(e, q) })
}

func (s *MemoryStore) GetEmailMail(ctx context.Context, ownerID, id string) (app.EmailMail, bool, error) {
	pair, err := emailMemoryRun(s, ctx, OperationGetEmailMail, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailMail], error) { return emailGetMail(e, id) })
	return pair.Value, pair.Found, err
}

func (s *MemoryStore) GetEmailCapture(ctx context.Context, ownerID, id string) (app.EmailCaptureVersion, bool, error) {
	pair, err := emailMemoryRun(s, ctx, OperationGetEmailCapture, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailCaptureVersion], error) {
		return emailReadRecord[app.EmailCaptureVersion](e, "capture", id)
	})
	return pair.Value, pair.Found, err
}

func (s *MemoryStore) GetEmailRepresentation(ctx context.Context, ownerID, id string) (app.EmailRepresentation, bool, error) {
	pair, err := emailMemoryRun(s, ctx, OperationGetEmailRepresentation, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailRepresentation], error) {
		return emailReadRecord[app.EmailRepresentation](e, "representation", id)
	})
	return pair.Value, pair.Found, err
}

func (s *MemoryStore) GetEmailContext(ctx context.Context, ownerID, id string) (app.EmailContextVersion, bool, error) {
	pair, err := emailMemoryRun(s, ctx, OperationGetEmailContext, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailContextVersion], error) {
		return emailReadRecord[app.EmailContextVersion](e, "context", id)
	})
	return pair.Value, pair.Found, err
}

func (s *MemoryStore) GetEmailConversation(ctx context.Context, ownerID, id string) (app.EmailConversation, bool, error) {
	pair, err := emailMemoryRun(s, ctx, OperationGetEmailConversation, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailConversation], error) { return emailGetConversation(e, id) })
	return pair.Value, pair.Found, err
}

func (s *MemoryStore) ReconcileEmailCommand(ctx context.Context, ownerID, id string) (EmailCommandReceipt, bool, error) {
	pair, err := emailMemoryRun(s, ctx, OperationReconcileEmailCommand, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[EmailCommandReceipt], error) {
		return emailReadRecord[EmailCommandReceipt](e, "command", id)
	})
	return pair.Value, pair.Found, err
}

func (s *MemoryStore) ListEmailConversations(ctx context.Context, q EmailQuery) (EmailConversationPage, error) {
	return emailMemoryRun(s, ctx, OperationListEmailConversations, q.OwnerID, "", nil, false, func(e *emailEngine) (EmailConversationPage, error) { return emailConversations(e, q) })
}

func (s *MemoryStore) FindEmailCandidates(ctx context.Context, q EmailCandidateQuery) (EmailCandidateSet, error) {
	return emailMemoryRun(s, ctx, OperationFindEmailCandidates, q.OwnerID, "", nil, false, func(e *emailEngine) (EmailCandidateSet, error) { return emailCandidates(e, q) })
}

func (s *MemoryStore) GetEmailAnalysisTarget(ctx context.Context, ownerID, kind, id string) (app.EmailAnalysisTarget, bool, error) {
	pair, err := emailMemoryRun(s, ctx, OperationGetEmailAnalysisTarget, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailAnalysisTarget], error) {
		return emailGetTarget(e, kind, id)
	})
	return pair.Value, pair.Found, err
}

func (s *MemoryStore) ListEmailConcerns(ctx context.Context, q EmailQuery) ([]app.EmailAssignmentConcern, error) {
	return emailMemoryRun(s, ctx, OperationListEmailConcerns, q.OwnerID, "", nil, false, func(e *emailEngine) ([]app.EmailAssignmentConcern, error) { return emailConcerns(e, q) })
}

func (s *MemoryStore) ListEmailJobs(ctx context.Context, q EmailQuery) ([]app.EmailJob, error) {
	return emailMemoryRun(s, ctx, OperationListEmailJobs, q.OwnerID, "", nil, false, func(e *emailEngine) ([]app.EmailJob, error) { return emailQueryRows[app.EmailJob](e, q, "job") })
}

func (s *MemoryStore) ListEmailThreads(ctx context.Context, q EmailQuery) ([]app.EmailProviderThread, error) {
	return emailMemoryRun(s, ctx, OperationListEmailThreads, q.OwnerID, "", nil, false, func(e *emailEngine) ([]app.EmailProviderThread, error) {
		return emailQueryRows[app.EmailProviderThread](e, q, "thread")
	})
}

func (s *MemoryStore) ListEmailSyncRuns(ctx context.Context, q EmailQuery) ([]app.EmailSyncRun, error) {
	return emailMemoryRun(s, ctx, OperationListEmailSyncRuns, q.OwnerID, "", nil, false, func(e *emailEngine) ([]app.EmailSyncRun, error) {
		return emailQueryRows[app.EmailSyncRun](e, q, "sync")
	})
}

func (s *MemoryStore) GetEmailThread(ctx context.Context, ownerID, id string) (app.EmailProviderThread, bool, error) {
	pair, err := emailMemoryRun(s, ctx, OperationGetEmailThread, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailProviderThread], error) {
		return emailReadRecord[app.EmailProviderThread](e, "thread", id)
	})
	return pair.Value, pair.Found, err
}
