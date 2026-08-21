package store

// testBackend keeps cross-repository characterization fixtures convenient
// without restoring a production-wide Store contract.
type testBackend interface {
	ISCPOnboardingRepository
	OwnerRepository
	ClientRepository
	CredentialRepository
	ConnectorRepository
	SessionRepository
	ConversationRepository
	RunRepository
	DocumentRepository
	ApprovalRepository
	AuditRepository
	EvaluationRepository
	ArtifactMetadataRepository
	BrowserStateRepository
	MemoryRepository
	ScheduleRepository
	PassiveNotificationRepository
	DeliveryRecordRepository
	ExternalChatRepository
	MCPRepository
}

var (
	_ testBackend = (*MemoryStore)(nil)
	_ testBackend = (*FileStore)(nil)
	_ testBackend = (*PostgresStore)(nil)
)
