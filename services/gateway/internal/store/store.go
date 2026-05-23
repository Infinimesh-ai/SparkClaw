package store

import (
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type Store interface {
	CreateSession(title string) app.Session
	ListSessions() []app.Session
	GetSession(id string) (app.Session, bool)
	SaveClient(client app.Client)
	GetClient(id string) (app.Client, bool)
	ListClients() []app.Client
	RevokeClient(id string) (app.Client, error)
	FindClientByTokenHash(tokenHash string) (app.Client, bool)
	TouchClient(id string)
	GetOwnerProfile() app.OwnerProfile
	UpdateOwnerProfile(profile app.OwnerProfile) app.OwnerProfile
	SavePairingCode(code app.PairingCode)
	GetPairingCode(id string) (app.PairingCode, bool)
	ClaimPairingCode(id, clientID string) (app.PairingCode, error)
	AddMessage(message app.Message) app.Message
	ListMessages(sessionID string) []app.Message
	SaveRunFeedback(feedback app.RunFeedback) app.RunFeedback
	ListRunFeedback(runID string) []app.RunFeedback
	SaveRun(run app.AgentRun)
	GetRun(id string) (app.AgentRun, bool)
	ListRuns(sessionID string) []app.AgentRun
	SaveModelCall(call app.ModelCall)
	ListModelCalls(sessionID, runID string) []app.ModelCall
	SaveToolCall(call app.ToolCall)
	GetToolCall(id string) (app.ToolCall, bool)
	ListToolCalls(sessionID string) []app.ToolCall
	SaveApproval(approval app.Approval)
	ResolveApproval(id, status, note string) (app.Approval, error)
	ListApprovals(status string) []app.Approval
	AddMemoryCandidate(candidate app.MemoryCandidate) app.MemoryCandidate
	ResolveMemoryCandidate(id, status string) (app.MemoryCandidate, *app.Memory, error)
	ListMemoryCandidates(status string) []app.MemoryCandidate
	SearchMemories(query string) []app.Memory
	UpdateMemory(id, kind, content string) (app.Memory, error)
	DeleteMemory(id string) (app.Memory, error)
	PruneMemories(cutoff time.Time) []app.Memory
	AddAudit(event app.AuditEvent)
	ListAudit(sessionID string) []app.AuditEvent
	EventsAfter(sessionID, after string) []app.Event
	SaveEvalRun(run app.EvalRun)
	GetEvalRun(id string) (app.EvalRun, bool)
	ListEvalRuns() []app.EvalRun
	SaveArtifactObject(object app.ArtifactObject)
	ListArtifactObjects(limit int) []app.ArtifactObject
	SaveEpisodeSummary(summary app.EpisodeSummary)
	ListEpisodeSummaries(sessionID string) []app.EpisodeSummary
}

type DocumentStore interface {
	ReplaceDocumentChunks(root string, documents []app.Document, chunks []app.DocumentChunk) (app.DocumentIndexSummary, error)
	SearchDocumentChunks(query string, embedding []float32, maxResults int) ([]app.DocumentChunkHit, error)
}
