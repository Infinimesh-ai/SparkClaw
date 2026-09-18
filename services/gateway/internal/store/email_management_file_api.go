package store

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) BindEmailMailbox(ctx context.Context, c EmailBindCommand) (app.EmailMailbox, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBindEmailMailbox, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailMailbox), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailMailbox), errEmailCommandInvalid(ctx, OperationBindEmailMailbox)
	}
	return emailFileRun(s, ctx, OperationBindEmailMailbox, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMailbox, error) { return emailBind(e, c) })
}
func (s *FileStore) PauseEmailMailbox(ctx context.Context, c EmailPauseCommand) (app.EmailMailbox, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPauseEmailMailbox, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailMailbox), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailMailbox), errEmailCommandInvalid(ctx, OperationPauseEmailMailbox)
	}
	return emailFileRun(s, ctx, OperationPauseEmailMailbox, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMailbox, error) { return emailPause(e, c) })
}
func (s *FileStore) GetEmailOwnerStatus(ctx context.Context, ownerID string) (app.EmailOwnerStatus, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationGetEmailOwnerStatus, 1)
	if err != nil {
		return *new(app.EmailOwnerStatus), err
	}
	defer release()
	return s.inner.GetEmailOwnerStatus(ctx, ownerID)
}

func (s *FileStore) ListEmailMailboxes(ctx context.Context, ownerID string) ([]app.EmailMailbox, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationListEmailMailboxes, 1)
	if err != nil {
		return *new([]app.EmailMailbox), err
	}
	defer release()
	return s.inner.ListEmailMailboxes(ctx, ownerID)
}

func (s *FileStore) GetEmailMailbox(ctx context.Context, ownerID, id string) (app.EmailMailbox, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationGetEmailMailbox, 1)
	if err != nil {
		return *new(app.EmailMailbox), false, err
	}
	defer release()
	return s.inner.GetEmailMailbox(ctx, ownerID, id)
}

func (s *FileStore) BeginEmailSync(ctx context.Context, c EmailSyncBeginCommand) (EmailSyncCheckpoint, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBeginEmailSync, fileAdmissionCapacity)
	if err != nil {
		return EmailSyncCheckpoint{}, err
	}
	defer release()
	if c.CommandKey == "" {
		return EmailSyncCheckpoint{}, errEmailCommandInvalid(ctx, OperationBeginEmailSync)
	}
	return emailFileRun(s, ctx, OperationBeginEmailSync, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailSyncCheckpoint, error) { return emailBeginSync(e, c) })
}

func (s *FileStore) CommitEmailSync(ctx context.Context, c EmailSyncCommitCommand) (EmailSyncCommitResult, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationCommitEmailSync, fileAdmissionCapacity)
	if err != nil {
		return EmailSyncCommitResult{}, err
	}
	defer release()
	if c.CommandKey == "" {
		return EmailSyncCommitResult{}, errEmailCommandInvalid(ctx, OperationCommitEmailSync)
	}
	return emailFileRun(s, ctx, OperationCommitEmailSync, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailSyncCommitResult, error) { return emailCommitSync(e, c) })
}

func (s *FileStore) ReportEmailSourceFailure(ctx context.Context, c EmailSourceFailureCommand) (app.EmailMail, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReportEmailSourceFailure, fileAdmissionCapacity)
	if err != nil {
		return app.EmailMail{}, err
	}
	defer release()
	if c.CommandKey == "" {
		return app.EmailMail{}, errEmailCommandInvalid(ctx, OperationReportEmailSourceFailure)
	}
	return emailFileRun(s, ctx, OperationReportEmailSourceFailure, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailReportSourceFailure(e, c) })
}

func (s *FileStore) ListEmailSyncWarnings(ctx context.Context, q EmailQuery) (EmailSyncWarningPage, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationListEmailSyncWarnings, 1)
	if err != nil {
		return EmailSyncWarningPage{}, err
	}
	defer release()
	return s.inner.ListEmailSyncWarnings(ctx, q)
}

func (s *FileStore) AcknowledgeEmailSyncWarning(ctx context.Context, c EmailSyncWarningAck) (app.EmailSyncWarning, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationAcknowledgeEmailSyncWarning, fileAdmissionCapacity)
	if err != nil {
		return app.EmailSyncWarning{}, err
	}
	defer release()
	if c.CommandKey == "" {
		return app.EmailSyncWarning{}, errEmailCommandInvalid(ctx, OperationAcknowledgeEmailSyncWarning)
	}
	return emailFileRun(s, ctx, OperationAcknowledgeEmailSyncWarning, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailSyncWarning, error) { return emailAcknowledgeSyncWarning(e, c) })
}

func (s *FileStore) AdmitEmailDiscovery(ctx context.Context, c EmailDiscoveryCommand) (EmailDiscoveryAdmission, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationAdmitEmailDiscovery, fileAdmissionCapacity)
	if err != nil {
		return *new(EmailDiscoveryAdmission), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(EmailDiscoveryAdmission), errEmailCommandInvalid(ctx, OperationAdmitEmailDiscovery)
	}
	return emailFileRun(s, ctx, OperationAdmitEmailDiscovery, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailDiscoveryAdmission, error) { return emailAdmit(e, c) })
}
func (s *FileStore) RequestEmailJob(ctx context.Context, c EmailJobRequest) (app.EmailJob, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRequestEmailJob, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailJob), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailJob), errEmailCommandInvalid(ctx, OperationRequestEmailJob)
	}
	return emailFileRun(s, ctx, OperationRequestEmailJob, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailJob, error) { return emailRequest(e, c) })
}
func (s *FileStore) ClaimEmailJob(ctx context.Context, c EmailJobClaim) (app.EmailJob, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationClaimEmailJob, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailJob), false, err
	}
	defer release()
	pair, err := emailFileRun(s, ctx, OperationClaimEmailJob, c.OwnerID, "", c, true, func(e *emailEngine) (emailOptional[app.EmailJob], error) { return emailClaimOptional(e, c) })
	return pair.Value, pair.Found, err
}
func (s *FileStore) RenewEmailJob(ctx context.Context, c EmailJobRenew) (app.EmailJob, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRenewEmailJob, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailJob), err
	}
	defer release()
	return emailFileRun(s, ctx, OperationRenewEmailJob, c.OwnerID, "", c, true, func(e *emailEngine) (app.EmailJob, error) { return emailRenew(e, c) })
}
func (s *FileStore) FinishEmailJob(ctx context.Context, c EmailJobFinish) (app.EmailJob, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationFinishEmailJob, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailJob), err
	}
	defer release()
	return emailFileRun(s, ctx, OperationFinishEmailJob, c.OwnerID, "", c, true, func(e *emailEngine) (app.EmailJob, error) { return emailFinish(e, c) })
}
func (s *FileStore) PublishEmailCapture(ctx context.Context, c EmailCaptureCommand) (app.EmailMail, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPublishEmailCapture, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailMail), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationPublishEmailCapture)
	}
	return emailFileRun(s, ctx, OperationPublishEmailCapture, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailCapture(e, c) })
}
func (s *FileStore) PublishEmailRepresentation(ctx context.Context, c EmailRepresentationCommand) (app.EmailMail, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPublishEmailRepresentation, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailMail), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationPublishEmailRepresentation)
	}
	return emailFileRun(s, ctx, OperationPublishEmailRepresentation, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailRepresentation(e, c) })
}
func (s *FileStore) PublishEmailRenderPreview(ctx context.Context, c EmailRenderPreviewCommand) (app.EmailRenderPreview, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPublishEmailRenderPreview, fileAdmissionCapacity)
	if err != nil {
		return app.EmailRenderPreview{}, err
	}
	defer release()
	if c.CommandKey == "" {
		return app.EmailRenderPreview{}, errEmailCommandInvalid(ctx, OperationPublishEmailRenderPreview)
	}
	return emailFileRun(s, ctx, OperationPublishEmailRenderPreview, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailRenderPreview, error) { return emailPublishRenderPreview(e, c) })
}
func (s *FileStore) PublishEmailContext(ctx context.Context, c EmailContextCommand) (app.EmailMail, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPublishEmailContext, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailMail), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationPublishEmailContext)
	}
	return emailFileRun(s, ctx, OperationPublishEmailContext, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailContext(e, c) })
}
func (s *FileStore) ExpandEmailRefresh(ctx context.Context, c EmailRefreshCommand) (EmailRefreshResult, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExpandEmailRefresh, fileAdmissionCapacity)
	if err != nil {
		return *new(EmailRefreshResult), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(EmailRefreshResult), errEmailCommandInvalid(ctx, OperationExpandEmailRefresh)
	}
	return emailFileRun(s, ctx, OperationExpandEmailRefresh, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailRefreshResult, error) { return emailExpand(e, c) })
}
func (s *FileStore) CommitEmailAssignment(ctx context.Context, c EmailAssignmentCommand) (app.EmailMail, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationCommitEmailAssignment, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailMail), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationCommitEmailAssignment)
	}
	return emailFileRun(s, ctx, OperationCommitEmailAssignment, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailAssignment(e, c) })
}
func (s *FileStore) PublishEmailConcern(ctx context.Context, c EmailConcernCommand) (app.EmailAssignmentConcern, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPublishEmailConcern, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailAssignmentConcern), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailAssignmentConcern), errEmailCommandInvalid(ctx, OperationPublishEmailConcern)
	}
	return emailFileRun(s, ctx, OperationPublishEmailConcern, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailAssignmentConcern, error) { return emailConcern(e, c) })
}
func (s *FileStore) PublishEmailSummary(ctx context.Context, c EmailSummaryCommand) (app.EmailSummary, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPublishEmailSummary, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailSummary), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailSummary), errEmailCommandInvalid(ctx, OperationPublishEmailSummary)
	}
	return emailFileRun(s, ctx, OperationPublishEmailSummary, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailSummary, error) { return emailSummary(e, c) })
}
func (s *FileStore) MarkEmailMailsViewed(ctx context.Context, c EmailViewedCommand) ([]app.EmailViewReceipt, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMarkEmailMailsViewed, fileAdmissionCapacity)
	if err != nil {
		return *new([]app.EmailViewReceipt), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new([]app.EmailViewReceipt), errEmailCommandInvalid(ctx, OperationMarkEmailMailsViewed)
	}
	return emailFileRun(s, ctx, OperationMarkEmailMailsViewed, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) ([]app.EmailViewReceipt, error) { return emailViewed(e, c) })
}
func (s *FileStore) ListEmailMails(ctx context.Context, q EmailQuery) (EmailMailPage, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationListEmailMails, 1)
	if err != nil {
		return *new(EmailMailPage), err
	}
	defer release()
	return s.inner.ListEmailMails(ctx, q)
}

func (s *FileStore) GetEmailMail(ctx context.Context, ownerID, id string) (app.EmailMail, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationGetEmailMail, 1)
	if err != nil {
		return *new(app.EmailMail), false, err
	}
	defer release()
	return s.inner.GetEmailMail(ctx, ownerID, id)
}

func (s *FileStore) GetEmailCapture(ctx context.Context, ownerID, id string) (app.EmailCaptureVersion, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationGetEmailCapture, 1)
	if err != nil {
		return *new(app.EmailCaptureVersion), false, err
	}
	defer release()
	return s.inner.GetEmailCapture(ctx, ownerID, id)
}

func (s *FileStore) GetEmailRepresentation(ctx context.Context, ownerID, id string) (app.EmailRepresentation, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationGetEmailRepresentation, 1)
	if err != nil {
		return *new(app.EmailRepresentation), false, err
	}
	defer release()
	return s.inner.GetEmailRepresentation(ctx, ownerID, id)
}

func (s *FileStore) GetEmailRenderPreview(ctx context.Context, ownerID, representationID, sanitizerVersion string) (app.EmailRenderPreview, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationGetEmailRenderPreview, 1)
	if err != nil {
		return app.EmailRenderPreview{}, false, err
	}
	defer release()
	return s.inner.GetEmailRenderPreview(ctx, ownerID, representationID, sanitizerVersion)
}

func (s *FileStore) GetEmailContext(ctx context.Context, ownerID, id string) (app.EmailContextVersion, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationGetEmailContext, 1)
	if err != nil {
		return *new(app.EmailContextVersion), false, err
	}
	defer release()
	return s.inner.GetEmailContext(ctx, ownerID, id)
}

func (s *FileStore) GetEmailConversation(ctx context.Context, ownerID, id string) (app.EmailConversation, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationGetEmailConversation, 1)
	if err != nil {
		return *new(app.EmailConversation), false, err
	}
	defer release()
	return s.inner.GetEmailConversation(ctx, ownerID, id)
}

func (s *FileStore) ReconcileEmailCommand(ctx context.Context, ownerID, id string) (EmailCommandReceipt, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReconcileEmailCommand, 1)
	if err != nil {
		return *new(EmailCommandReceipt), false, err
	}
	defer release()
	return s.inner.ReconcileEmailCommand(ctx, ownerID, id)
}

func (s *FileStore) ListEmailConversations(ctx context.Context, q EmailQuery) (EmailConversationPage, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationListEmailConversations, 1)
	if err != nil {
		return *new(EmailConversationPage), err
	}
	defer release()
	return s.inner.ListEmailConversations(ctx, q)
}

func (s *FileStore) FindEmailCandidates(ctx context.Context, q EmailCandidateQuery) (EmailCandidateSet, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationFindEmailCandidates, 1)
	if err != nil {
		return *new(EmailCandidateSet), err
	}
	defer release()
	return s.inner.FindEmailCandidates(ctx, q)
}

func (s *FileStore) GetEmailAnalysisTarget(ctx context.Context, ownerID, kind, id string) (app.EmailAnalysisTarget, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationGetEmailAnalysisTarget, 1)
	if err != nil {
		return *new(app.EmailAnalysisTarget), false, err
	}
	defer release()
	return s.inner.GetEmailAnalysisTarget(ctx, ownerID, kind, id)
}

func (s *FileStore) ListEmailConcerns(ctx context.Context, q EmailQuery) ([]app.EmailAssignmentConcern, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationListEmailConcerns, 1)
	if err != nil {
		return *new([]app.EmailAssignmentConcern), err
	}
	defer release()
	return s.inner.ListEmailConcerns(ctx, q)
}

func (s *FileStore) ListEmailJobs(ctx context.Context, q EmailQuery) ([]app.EmailJob, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationListEmailJobs, 1)
	if err != nil {
		return *new([]app.EmailJob), err
	}
	defer release()
	return s.inner.ListEmailJobs(ctx, q)
}

func (s *FileStore) ListEmailThreads(ctx context.Context, q EmailQuery) ([]app.EmailProviderThread, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationListEmailThreads, 1)
	if err != nil {
		return *new([]app.EmailProviderThread), err
	}
	defer release()
	return s.inner.ListEmailThreads(ctx, q)
}

func (s *FileStore) ListEmailSyncRuns(ctx context.Context, q EmailQuery) ([]app.EmailSyncRun, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationListEmailSyncRuns, 1)
	if err != nil {
		return *new([]app.EmailSyncRun), err
	}
	defer release()
	return s.inner.ListEmailSyncRuns(ctx, q)
}

func (s *FileStore) GetEmailThread(ctx context.Context, ownerID, id string) (app.EmailProviderThread, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationGetEmailThread, 1)
	if err != nil {
		return *new(app.EmailProviderThread), false, err
	}
	defer release()
	return s.inner.GetEmailThread(ctx, ownerID, id)
}
