package store

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *PostgresStore) BindEmailMailbox(ctx context.Context, c EmailBindCommand) (app.EmailMailbox, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMailbox), errEmailCommandInvalid(ctx, OperationBindEmailMailbox)
	}
	return emailPostgresRun(s, ctx, OperationBindEmailMailbox, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMailbox, error) { return emailBind(e, c) })
}

func (s *PostgresStore) PauseEmailMailbox(ctx context.Context, c EmailPauseCommand) (app.EmailMailbox, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMailbox), errEmailCommandInvalid(ctx, OperationPauseEmailMailbox)
	}
	return emailPostgresRun(s, ctx, OperationPauseEmailMailbox, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMailbox, error) { return emailPause(e, c) })
}

func (s *PostgresStore) GetEmailOwnerStatus(ctx context.Context, ownerID string) (app.EmailOwnerStatus, error) {
	return emailPostgresRun(s, ctx, OperationGetEmailOwnerStatus, ownerID, "", nil, false, func(e *emailEngine) (app.EmailOwnerStatus, error) { return emailOwnerStatus(e) })
}

func (s *PostgresStore) ListEmailMailboxes(ctx context.Context, ownerID string) ([]app.EmailMailbox, error) {
	return emailPostgresRun(s, ctx, OperationListEmailMailboxes, ownerID, "", nil, false, func(e *emailEngine) ([]app.EmailMailbox, error) { return emailMailboxes(e) })
}

func (s *PostgresStore) GetEmailMailbox(ctx context.Context, ownerID, id string) (app.EmailMailbox, bool, error) {
	pair, err := emailPostgresRun(s, ctx, OperationGetEmailMailbox, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailMailbox], error) {
		return emailReadRecord[app.EmailMailbox](e, "mailbox", id)
	})
	return pair.Value, pair.Found, err
}

func (s *PostgresStore) AdmitEmailDiscovery(ctx context.Context, c EmailDiscoveryCommand) (EmailDiscoveryAdmission, error) {
	if c.CommandKey == "" {
		return *new(EmailDiscoveryAdmission), errEmailCommandInvalid(ctx, OperationAdmitEmailDiscovery)
	}
	return emailPostgresRun(s, ctx, OperationAdmitEmailDiscovery, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailDiscoveryAdmission, error) { return emailAdmit(e, c) })
}

func (s *PostgresStore) RequestEmailJob(ctx context.Context, c EmailJobRequest) (app.EmailJob, error) {
	if c.CommandKey == "" {
		return *new(app.EmailJob), errEmailCommandInvalid(ctx, OperationRequestEmailJob)
	}
	return emailPostgresRun(s, ctx, OperationRequestEmailJob, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailJob, error) { return emailRequest(e, c) })
}

func (s *PostgresStore) ClaimEmailJob(ctx context.Context, c EmailJobClaim) (app.EmailJob, bool, error) {
	pair, err := emailPostgresRun(s, ctx, OperationClaimEmailJob, c.OwnerID, "", c, true, func(e *emailEngine) (emailOptional[app.EmailJob], error) { return emailClaimOptional(e, c) })
	return pair.Value, pair.Found, err
}

func (s *PostgresStore) RenewEmailJob(ctx context.Context, c EmailJobRenew) (app.EmailJob, error) {
	return emailPostgresRun(s, ctx, OperationRenewEmailJob, c.OwnerID, "", c, true, func(e *emailEngine) (app.EmailJob, error) { return emailRenew(e, c) })
}

func (s *PostgresStore) FinishEmailJob(ctx context.Context, c EmailJobFinish) (app.EmailJob, error) {
	return emailPostgresRun(s, ctx, OperationFinishEmailJob, c.OwnerID, "", c, true, func(e *emailEngine) (app.EmailJob, error) { return emailFinish(e, c) })
}

func (s *PostgresStore) PublishEmailCapture(ctx context.Context, c EmailCaptureCommand) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationPublishEmailCapture)
	}
	return emailPostgresRun(s, ctx, OperationPublishEmailCapture, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailCapture(e, c) })
}

func (s *PostgresStore) PublishEmailRepresentation(ctx context.Context, c EmailRepresentationCommand) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationPublishEmailRepresentation)
	}
	return emailPostgresRun(s, ctx, OperationPublishEmailRepresentation, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailRepresentation(e, c) })
}

func (s *PostgresStore) PublishEmailContext(ctx context.Context, c EmailContextCommand) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationPublishEmailContext)
	}
	return emailPostgresRun(s, ctx, OperationPublishEmailContext, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailContext(e, c) })
}

func (s *PostgresStore) ExpandEmailRefresh(ctx context.Context, c EmailRefreshCommand) (EmailRefreshResult, error) {
	if c.CommandKey == "" {
		return *new(EmailRefreshResult), errEmailCommandInvalid(ctx, OperationExpandEmailRefresh)
	}
	return emailPostgresRun(s, ctx, OperationExpandEmailRefresh, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailRefreshResult, error) { return emailExpand(e, c) })
}

func (s *PostgresStore) CommitEmailAssignment(ctx context.Context, c EmailAssignmentCommand) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationCommitEmailAssignment)
	}
	return emailPostgresRun(s, ctx, OperationCommitEmailAssignment, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailAssignment(e, c) })
}

func (s *PostgresStore) PublishEmailConcern(ctx context.Context, c EmailConcernCommand) (app.EmailAssignmentConcern, error) {
	if c.CommandKey == "" {
		return *new(app.EmailAssignmentConcern), errEmailCommandInvalid(ctx, OperationPublishEmailConcern)
	}
	return emailPostgresRun(s, ctx, OperationPublishEmailConcern, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailAssignmentConcern, error) { return emailConcern(e, c) })
}

func (s *PostgresStore) PublishEmailSummary(ctx context.Context, c EmailSummaryCommand) (app.EmailSummary, error) {
	if c.CommandKey == "" {
		return *new(app.EmailSummary), errEmailCommandInvalid(ctx, OperationPublishEmailSummary)
	}
	return emailPostgresRun(s, ctx, OperationPublishEmailSummary, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailSummary, error) { return emailSummary(e, c) })
}

func (s *PostgresStore) MarkEmailMailsViewed(ctx context.Context, c EmailViewedCommand) ([]app.EmailViewReceipt, error) {
	if c.CommandKey == "" {
		return *new([]app.EmailViewReceipt), errEmailCommandInvalid(ctx, OperationMarkEmailMailsViewed)
	}
	return emailPostgresRun(s, ctx, OperationMarkEmailMailsViewed, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) ([]app.EmailViewReceipt, error) { return emailViewed(e, c) })
}

func (s *PostgresStore) ListEmailMails(ctx context.Context, q EmailQuery) (EmailMailPage, error) {
	return emailPostgresRun(s, ctx, OperationListEmailMails, q.OwnerID, "", nil, false, func(e *emailEngine) (EmailMailPage, error) { return emailMails(e, q) })
}

func (s *PostgresStore) GetEmailMail(ctx context.Context, ownerID, id string) (app.EmailMail, bool, error) {
	pair, err := emailPostgresRun(s, ctx, OperationGetEmailMail, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailMail], error) { return emailGetMail(e, id) })
	return pair.Value, pair.Found, err
}

func (s *PostgresStore) GetEmailCapture(ctx context.Context, ownerID, id string) (app.EmailCaptureVersion, bool, error) {
	pair, err := emailPostgresRun(s, ctx, OperationGetEmailCapture, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailCaptureVersion], error) {
		return emailReadRecord[app.EmailCaptureVersion](e, "capture", id)
	})
	return pair.Value, pair.Found, err
}

func (s *PostgresStore) GetEmailRepresentation(ctx context.Context, ownerID, id string) (app.EmailRepresentation, bool, error) {
	pair, err := emailPostgresRun(s, ctx, OperationGetEmailRepresentation, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailRepresentation], error) {
		return emailReadRecord[app.EmailRepresentation](e, "representation", id)
	})
	return pair.Value, pair.Found, err
}

func (s *PostgresStore) GetEmailContext(ctx context.Context, ownerID, id string) (app.EmailContextVersion, bool, error) {
	pair, err := emailPostgresRun(s, ctx, OperationGetEmailContext, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailContextVersion], error) {
		return emailReadRecord[app.EmailContextVersion](e, "context", id)
	})
	return pair.Value, pair.Found, err
}

func (s *PostgresStore) GetEmailConversation(ctx context.Context, ownerID, id string) (app.EmailConversation, bool, error) {
	pair, err := emailPostgresRun(s, ctx, OperationGetEmailConversation, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailConversation], error) { return emailGetConversation(e, id) })
	return pair.Value, pair.Found, err
}

func (s *PostgresStore) ReconcileEmailCommand(ctx context.Context, ownerID, id string) (EmailCommandReceipt, bool, error) {
	pair, err := emailPostgresRun(s, ctx, OperationReconcileEmailCommand, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[EmailCommandReceipt], error) {
		return emailReadRecord[EmailCommandReceipt](e, "command", id)
	})
	return pair.Value, pair.Found, err
}

func (s *PostgresStore) ListEmailConversations(ctx context.Context, q EmailQuery) (EmailConversationPage, error) {
	return emailPostgresRun(s, ctx, OperationListEmailConversations, q.OwnerID, "", nil, false, func(e *emailEngine) (EmailConversationPage, error) { return emailConversations(e, q) })
}

func (s *PostgresStore) FindEmailCandidates(ctx context.Context, q EmailCandidateQuery) (EmailCandidateSet, error) {
	return emailPostgresRun(s, ctx, OperationFindEmailCandidates, q.OwnerID, "", nil, false, func(e *emailEngine) (EmailCandidateSet, error) { return emailCandidates(e, q) })
}

func (s *PostgresStore) GetEmailAnalysisTarget(ctx context.Context, ownerID, kind, id string) (app.EmailAnalysisTarget, bool, error) {
	pair, err := emailPostgresRun(s, ctx, OperationGetEmailAnalysisTarget, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailAnalysisTarget], error) {
		return emailGetTarget(e, kind, id)
	})
	return pair.Value, pair.Found, err
}

func (s *PostgresStore) ListEmailConcerns(ctx context.Context, q EmailQuery) ([]app.EmailAssignmentConcern, error) {
	return emailPostgresRun(s, ctx, OperationListEmailConcerns, q.OwnerID, "", nil, false, func(e *emailEngine) ([]app.EmailAssignmentConcern, error) { return emailConcerns(e, q) })
}

func (s *PostgresStore) ListEmailJobs(ctx context.Context, q EmailQuery) ([]app.EmailJob, error) {
	return emailPostgresRun(s, ctx, OperationListEmailJobs, q.OwnerID, "", nil, false, func(e *emailEngine) ([]app.EmailJob, error) { return emailQueryRows[app.EmailJob](e, q, "job") })
}

func (s *PostgresStore) ListEmailThreads(ctx context.Context, q EmailQuery) ([]app.EmailProviderThread, error) {
	return emailPostgresRun(s, ctx, OperationListEmailThreads, q.OwnerID, "", nil, false, func(e *emailEngine) ([]app.EmailProviderThread, error) {
		return emailQueryRows[app.EmailProviderThread](e, q, "thread")
	})
}

func (s *PostgresStore) ListEmailSyncRuns(ctx context.Context, q EmailQuery) ([]app.EmailSyncRun, error) {
	return emailPostgresRun(s, ctx, OperationListEmailSyncRuns, q.OwnerID, "", nil, false, func(e *emailEngine) ([]app.EmailSyncRun, error) {
		return emailQueryRows[app.EmailSyncRun](e, q, "sync")
	})
}

func (s *PostgresStore) GetEmailThread(ctx context.Context, ownerID, id string) (app.EmailProviderThread, bool, error) {
	pair, err := emailPostgresRun(s, ctx, OperationGetEmailThread, ownerID, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailProviderThread], error) {
		return emailReadRecord[app.EmailProviderThread](e, "thread", id)
	})
	return pair.Value, pair.Found, err
}
