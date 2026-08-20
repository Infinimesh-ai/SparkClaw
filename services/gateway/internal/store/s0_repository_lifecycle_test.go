package store

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type s0LifecycleCase struct {
	mutate    func(*testing.T, Store)
	auditType string
	eventType string
}

var s0RepositoryLifecycleCases = map[string]s0LifecycleCase{
	"OwnerRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.SaveOwnerProfile(app.OwnerProfile{ID: "owner-lifecycle", DisplayName: "Lifecycle"})
		},
		auditType: "owner_profile.updated", eventType: "owner_profile.updated",
	},
	"ClientRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.SaveClient(app.Client{ID: "client-lifecycle", Name: "Lifecycle"})
		},
		auditType: "client.saved", eventType: "client.saved",
	},
	"CredentialRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.SaveCredentialSecret(app.CredentialSecret{Ref: "credential-lifecycle", Kind: "token", Value: "secret"})
		},
		auditType: "credential_secret.saved",
	},
	"SessionRepository": {
		mutate:    func(_ *testing.T, st Store) { st.CreateSession("Lifecycle") },
		auditType: "session.created", eventType: "session.created",
	},
	"ConversationRepository": {
		mutate: func(_ *testing.T, st Store) {
			session := st.CreateSession("Lifecycle conversation")
			st.AddMessage(app.Message{ID: "message-lifecycle", SessionID: session.ID, Role: "user", Content: "hello"})
		},
		eventType: "message.created",
	},
	"RunRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.SaveRun(app.AgentRun{ID: "run-lifecycle", SessionID: "session-lifecycle", State: "completed"})
		},
		eventType: "run.completed",
	},
	"DocumentRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.SaveDocumentRecord(app.DocumentRecord{ID: "document-lifecycle", SessionID: "session-lifecycle", LastActivityAt: time.Now().UTC()})
		},
		auditType: "document.saved", eventType: "document.saved",
	},
	"ApprovalRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.SaveApproval(app.Approval{ID: "approval-lifecycle", Status: "pending", Summary: "Lifecycle"})
		},
		auditType: "approval.pending", eventType: "approval.pending",
	},
	"ScheduleRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.SaveReminder(app.Reminder{ID: "reminder-lifecycle", Status: "pending", DueTime: time.Now().UTC()})
		},
		auditType: "reminder.pending", eventType: "reminder.pending",
	},
	"ConnectorRepository": {
		mutate: func(t *testing.T, st Store) {
			if _, err := st.UpdateConnectorSetting(app.ConnectorSetting{OwnerID: "owner-lifecycle", Channel: "telegram", Enabled: true}, 0); err != nil {
				t.Fatal(err)
			}
		},
		auditType: "connector.enabled", eventType: "connector.enabled",
	},
	"PassiveNotificationRepository": {
		mutate: func(t *testing.T, st Store) {
			notification := testPassiveNotification("passive-lifecycle", "endpoint-lifecycle", "delivery-lifecycle", "fingerprint-lifecycle")
			if _, inserted, err := st.CreatePassiveNotification(notification); err != nil || !inserted {
				t.Fatalf("create passive notification: inserted=%v err=%v", inserted, err)
			}
		},
		auditType: "notification.received",
	},
	"ExternalChatRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.SaveExternalChatSession(app.ExternalChatSession{ID: "external-lifecycle", BindingID: "binding-lifecycle", Channel: "telegram", Status: "active"})
		},
		auditType: "external_chat_session.active", eventType: "external_chat_session.active",
	},
	"DeliveryRecordRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.SaveMessageReceive(app.MessageReceiveRecord{ID: "receive-lifecycle", SourceEndpointID: "endpoint-lifecycle", NativeMessageID: "native-lifecycle", Status: "received"})
		},
		auditType: "message.receive.received",
	},
	"BrowserStateRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.SaveBrowserAuthRecord(app.BrowserAuthRecord{ID: "browser-lifecycle", OwnerID: "owner-lifecycle", BrowserProfileID: "profile-lifecycle", SiteOrigin: "https://example.com"})
		},
		auditType: "browser_auth.record_saved", eventType: "browser_auth.record_saved",
	},
	"MemoryRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.AddMemoryCandidate(app.MemoryCandidate{ID: "memory-lifecycle", SessionID: "session-lifecycle", RunID: "run-lifecycle", Status: "pending"})
		},
		auditType: "memory_candidate.created", eventType: "memory_candidate.created",
	},
	"AuditRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.AddAudit(app.AuditEvent{ID: "audit-lifecycle", Type: "s0.audit.supplied", Time: time.Now().UTC()})
		},
		auditType: "s0.audit.supplied",
	},
	"EvaluationRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.SaveEvalRun(app.EvalRun{ID: "evaluation-lifecycle", Status: "passed"})
		},
		auditType: "eval.passed", eventType: "eval.passed",
	},
	"ArtifactMetadataRepository": {
		mutate: func(_ *testing.T, st Store) {
			st.SaveArtifactObject(app.ArtifactObject{ID: "artifact-lifecycle", URI: "artifact://s0/lifecycle", Key: "lifecycle"})
		},
		auditType: "artifact.saved", eventType: "artifact.saved",
	},
}

func TestS0BackendNeutralRepositoryLifecycleEvidence(t *testing.T) {
	if len(s0RepositoryLifecycleCases) != len(s0RepositoryMethods)-2 {
		t.Fatalf("repository lifecycle cases = %d, want %d", len(s0RepositoryLifecycleCases), len(s0RepositoryMethods)-2)
	}
	for repository, lifecycle := range s0RepositoryLifecycleCases {
		t.Run(repository, func(t *testing.T) {
			for _, backend := range newS0RepositoryBackends(t) {
				t.Run(backend.name, func(t *testing.T) {
					beforeAudits := len(backend.store.ListAudit(""))
					beforeEvents := len(backend.store.EventsAfter("", ""))
					lifecycle.mutate(t, backend.store)
					if lifecycle.auditType != "" {
						audits := backend.store.ListAudit("")
						if len(audits) <= beforeAudits || !hasAuditType(audits, lifecycle.auditType) {
							t.Fatalf("%s did not append audit %q: %#v", repository, lifecycle.auditType, audits)
						}
					}
					if lifecycle.eventType != "" {
						events := backend.store.EventsAfter("", "")
						if len(events) <= beforeEvents || !hasEventType(events, lifecycle.eventType) {
							t.Fatalf("%s did not append event %q: %#v", repository, lifecycle.eventType, events)
						}
					}
				})
			}
		})
	}
}
