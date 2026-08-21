package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const defaultRecoveryProbeInterval = 30 * time.Second

type RuntimeOptions struct {
	Backend          BackendKind
	Timeouts         OperationTimeouts
	File             FileStoreOptions
	PostgresDSN      string
	RecoveryInterval time.Duration
}

type repositorySet struct {
	iscpOnboarding      ISCPOnboardingRepository
	owner               OwnerRepository
	client              ClientRepository
	credential          CredentialRepository
	connector           ConnectorRepository
	session             SessionRepository
	conversation        ConversationRepository
	run                 RunRepository
	document            DocumentRepository
	approval            ApprovalRepository
	audit               AuditRepository
	evaluation          EvaluationRepository
	artifactMetadata    ArtifactMetadataRepository
	browserState        BrowserStateRepository
	memory              MemoryRepository
	schedule            ScheduleRepository
	passiveNotification PassiveNotificationRepository
	deliveryRecord      DeliveryRecordRepository
	externalChat        ExternalChatRepository
	mcp                 MCPRepository
}

type Runtime struct {
	repositories   repositorySet
	supervisor     *Supervisor
	probe          func(context.Context) error
	probeTimeout   time.Duration
	recoveryEvery  time.Duration
	closeBackend   func() error
	probeGate      chan struct{}
	recoveryMu     sync.Mutex
	recoveryCancel context.CancelFunc
	recoveryDone   chan struct{}
	closing        bool
	closeOnce      sync.Once
	closeErr       error
}

func NewRuntime(ctx context.Context, opts RuntimeOptions) (*Runtime, error) {
	backend := opts.Backend
	if backend == "" {
		backend = BackendFile
	}
	supervisor := newSupervisor(backend, nil)
	timeouts := normalizeOperationTimeouts(opts.Timeouts)
	timeouts.supervisor = supervisor
	recoveryEvery := opts.RecoveryInterval
	if recoveryEvery <= 0 {
		recoveryEvery = defaultRecoveryProbeInterval
	}
	runtime := &Runtime{
		supervisor: supervisor, probeTimeout: timeouts.Transaction,
		recoveryEvery: recoveryEvery, probeGate: make(chan struct{}, 1),
	}
	runtime.probeGate <- struct{}{}

	switch backend {
	case BackendMemory:
		memory := NewMemoryStoreWithOptions(timeouts)
		runtime.repositories = newRepositorySet(memory, memory, memory, memory, memory, memory, memory, memory, memory, memory, memory, memory, memory, memory, memory, memory, memory, memory, memory, memory)
		runtime.probe = func(context.Context) error { return probeMemoryStore(memory) }
		runtime.closeBackend = func() error { return nil }
	case BackendFile:
		fileOpts := opts.File
		fileOpts.ReadTimeout = timeouts.Read
		fileOpts.WriteTimeout = timeouts.Write
		fileOpts.TransactionTimeout = timeouts.Transaction
		file, err := NewFileStoreWithOptions(fileOpts)
		if err != nil {
			return nil, err
		}
		file.timeouts.supervisor = supervisor
		file.inner.operationTimeouts.supervisor = supervisor
		runtime.repositories = newRepositorySet(file, file, file, file, file, file, file, file, file, file, file, file, file, file, file, file, file, file, file, file)
		runtime.probe = func(ctx context.Context) error { return probeFileStore(ctx, file) }
		runtime.closeBackend = func() error { return nil }
	case BackendPostgres:
		postgres, err := NewPostgresStoreWithOptions(ctx, opts.PostgresDSN, timeouts)
		if err != nil {
			return nil, err
		}
		runtime.repositories = newRepositorySet(postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres, postgres)
		runtime.probe = func(ctx context.Context) error { return probePostgresStore(ctx, postgres) }
		runtime.closeBackend = func() error {
			postgres.Close()
			return nil
		}
	default:
		return nil, fmt.Errorf("unsupported state backend %q", backend)
	}

	if err := runtime.Probe(ctx); err != nil {
		_ = runtime.closeBackend()
		return nil, fmt.Errorf("probe %s store: %w", backend, err)
	}
	return runtime, nil
}

func newRepositorySet(
	iscpOnboarding ISCPOnboardingRepository,
	owner OwnerRepository,
	client ClientRepository,
	credential CredentialRepository,
	connector ConnectorRepository,
	session SessionRepository,
	conversation ConversationRepository,
	run RunRepository,
	document DocumentRepository,
	approval ApprovalRepository,
	audit AuditRepository,
	evaluation EvaluationRepository,
	artifactMetadata ArtifactMetadataRepository,
	browserState BrowserStateRepository,
	memory MemoryRepository,
	schedule ScheduleRepository,
	passiveNotification PassiveNotificationRepository,
	deliveryRecord DeliveryRecordRepository,
	externalChat ExternalChatRepository,
	mcp MCPRepository,
) repositorySet {
	return repositorySet{
		iscpOnboarding: iscpOnboarding, owner: owner, client: client,
		credential: credential, connector: connector, session: session,
		conversation: conversation, run: run, document: document,
		approval: approval, audit: audit, evaluation: evaluation,
		artifactMetadata: artifactMetadata, browserState: browserState, memory: memory,
		schedule: schedule, passiveNotification: passiveNotification, deliveryRecord: deliveryRecord,
		externalChat: externalChat, mcp: mcp,
	}
}

func (r *Runtime) ISCPOnboardingRepository() ISCPOnboardingRepository {
	return r.repositories.iscpOnboarding
}
func (r *Runtime) OwnerRepository() OwnerRepository               { return r.repositories.owner }
func (r *Runtime) ClientRepository() ClientRepository             { return r.repositories.client }
func (r *Runtime) CredentialRepository() CredentialRepository     { return r.repositories.credential }
func (r *Runtime) ConnectorRepository() ConnectorRepository       { return r.repositories.connector }
func (r *Runtime) SessionRepository() SessionRepository           { return r.repositories.session }
func (r *Runtime) ConversationRepository() ConversationRepository { return r.repositories.conversation }
func (r *Runtime) RunRepository() RunRepository                   { return r.repositories.run }
func (r *Runtime) DocumentRepository() DocumentRepository         { return r.repositories.document }
func (r *Runtime) ApprovalRepository() ApprovalRepository         { return r.repositories.approval }
func (r *Runtime) AuditRepository() AuditRepository               { return r.repositories.audit }
func (r *Runtime) EvaluationRepository() EvaluationRepository     { return r.repositories.evaluation }
func (r *Runtime) ArtifactMetadataRepository() ArtifactMetadataRepository {
	return r.repositories.artifactMetadata
}
func (r *Runtime) BrowserStateRepository() BrowserStateRepository { return r.repositories.browserState }
func (r *Runtime) MemoryRepository() MemoryRepository             { return r.repositories.memory }
func (r *Runtime) ScheduleRepository() ScheduleRepository         { return r.repositories.schedule }
func (r *Runtime) PassiveNotificationRepository() PassiveNotificationRepository {
	return r.repositories.passiveNotification
}
func (r *Runtime) DeliveryRecordRepository() DeliveryRecordRepository {
	return r.repositories.deliveryRecord
}
func (r *Runtime) ExternalChatRepository() ExternalChatRepository { return r.repositories.externalChat }
func (r *Runtime) MCPRepository() MCPRepository                   { return r.repositories.mcp }

func (r *Runtime) Status() RuntimeStatus { return r.supervisor.Status() }

func (r *Runtime) Metrics() []OperationMetric { return r.supervisor.Metrics() }

func (r *Runtime) Probe(ctx context.Context) error {
	probeCtx, cancel := boundedContext(ctx, r.probeTimeout)
	defer cancel()
	if err := r.acquireProbe(probeCtx); err != nil {
		return err
	}
	defer r.releaseProbe()
	if status := r.supervisor.Status(); status.State == RuntimeStateClosing || status.State == RuntimeStateClosed {
		return ErrRuntimeClosing
	}
	err := r.probe(probeCtx)
	r.supervisor.recordProbe(err)
	return err
}

func (r *Runtime) StartRecovery(ctx context.Context) {
	r.recoveryMu.Lock()
	if r.closing || r.recoveryCancel != nil {
		r.recoveryMu.Unlock()
		return
	}
	recoveryCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	r.recoveryCancel = cancel
	r.recoveryDone = done
	r.recoveryMu.Unlock()
	go r.runRecovery(recoveryCtx, done)
}

func (r *Runtime) runRecovery(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(r.recoveryEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !r.Status().Ready {
				_ = r.Probe(ctx)
			}
		}
	}
}

func (r *Runtime) Close(ctx context.Context) error {
	r.closeOnce.Do(func() {
		closeCtx, cancel := boundedContext(ctx, r.probeTimeout)
		defer cancel()
		r.supervisor.startClose()
		r.recoveryMu.Lock()
		r.closing = true
		r.recoveryMu.Unlock()
		r.stopRecovery(closeCtx)
		probeErr := r.acquireProbe(closeCtx)
		waitErr := r.supervisor.wait(closeCtx)
		closeErr := r.closeBackend()
		if probeErr == nil {
			r.releaseProbe()
		}
		r.closeErr = errors.Join(probeErr, waitErr, closeErr)
		r.supervisor.finishClose()
	})
	return r.closeErr
}

func (r *Runtime) acquireProbe(ctx context.Context) error {
	select {
	case <-r.probeGate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) releaseProbe() {
	r.probeGate <- struct{}{}
}

func (r *Runtime) stopRecovery(ctx context.Context) {
	r.recoveryMu.Lock()
	cancel := r.recoveryCancel
	done := r.recoveryDone
	r.recoveryMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func boundedContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, exists := parent.Deadline(); exists && time.Until(deadline) <= timeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}
