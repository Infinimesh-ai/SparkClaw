package gateway

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"golang.org/x/sys/unix"
)

func (s *Server) executeWorkbenchWorkflow(ctx context.Context, e execution.Envelope, inputs map[string][]byte) (answer execution.Output, execErr error) {
	// This registration spans the detached computation, so device revocation
	// cancels it even after the submit HTTP request has returned.
	ctx, release, err := s.clientConnectionContext(ctx, e.ClientID)
	if err != nil {
		return execution.Output{}, context.Canceled
	}
	defer release()
	service, err := s.executionService()
	if err != nil {
		return execution.Output{}, err
	}
	textOnly, _ := ctx.Value(textOnlyExecutionContextKey{}).(bool)
	var root string
	if textOnly {
		if len(e.InputFiles) != 0 || len(inputs) != 0 || envelopeHasHandles(e) {
			return execution.Output{}, errors.New("text-only execution does not accept files")
		}
		// This workspace holds empty directories only. Tool exposure and explicit
		// invocation are disabled below; message context stays in MemoryStore.
		root, err = os.MkdirTemp("", "sparkclaw-text-execution-")
	} else {
		root, err = executionMemoryWorkspace(service.Root())
	}
	if err != nil {
		return execution.Output{}, err
	}
	defer func() {
		if err := os.RemoveAll(root); err != nil {
			answer = execution.Output{}
			execErr = execution.ErrUnavailable
		}
	}()
	workspace := filepath.Join(root, "workspace")
	storage := s.cfg.Storage
	storage.ArtifactBackend = "filesystem"
	storage.ArtifactDir = filepath.Join(root, "artifacts")
	if err = os.MkdirAll(workspace, 0700); err != nil {
		return execution.Output{}, err
	}
	budget := execution.NewBudget(execution.TaskBytes)
	for _, raw := range inputs {
		if err = budget.Reserve(len(raw)); err != nil {
			return execution.Output{}, err
		}
	}
	workspaceBudget := newExecutionWorkspaceBudget(workspace, budget)
	local := store.NewMemoryStore().WithTransientContentAdmission(budget.Admit)
	var browser browserautomation.Adapter
	if !textOnly {
		broker, err := s.browserHostBroker()
		if err != nil {
			return execution.Output{}, err
		}
		browser = broker.ForScope(browserhost.Scope{Identity: browserhost.Identity{OwnerID: e.OwnerID, ClientID: e.ClientID, InstallationID: e.InstallationID}, ConversationID: e.ConversationID, TaskID: e.TaskID})
	}
	artifacts := execution.TemporaryArtifacts{Store: artifact.NewStore(storage), Budget: budget}
	materializer := newConversationFileMaterializer(workspace, service, e, budget)
	resources := toolhub.ExecutionResources{
		OwnerID: e.OwnerID, WorkspaceRoot: workspace, Artifacts: artifacts, Browser: browser,
		MaxDuration: execution.ExecutionBudget, MaxObservationBytes: 1 << 20, TextOnly: textOnly,
		ReserveWorkspaceBytes: workspaceBudget.reserve, ReleaseWorkspaceBytes: workspaceBudget.release,
	}
	if !textOnly {
		resources.MaterializeConversationFile = materializer.read
	}
	runtime, releaseRuntime, err := s.runtime.WithExecutionScope(local, executionResourcesForContext(ctx, resources))
	if err != nil {
		return execution.Output{}, err
	}
	var clarificationHistory []clarification.Record
	if len(e.Messages) > 1 {
		message := e.Messages[len(e.Messages)-2]
		if message.Role == "assistant" && message.Clarification != nil {
			clarificationHistory = append(clarificationHistory, *message.Clarification)
		}
	}
	snapshot, err := executionClarificationSnapshot(e)
	if err != nil {
		_ = releaseRuntime(ctx)
		return execution.Output{}, err
	}
	source := string(app.MessageSourceWeb)
	contextDigest, err := clarification.ContextDigest(snapshot, source)
	if err != nil {
		_ = releaseRuntime(ctx)
		return execution.Output{}, err
	}
	runtime = runtime.WithClarificationScope(service, clarification.Binding{OwnerID: e.OwnerID, ClientID: e.ClientID, InstallationID: e.InstallationID, ConversationID: e.ConversationID}, e.RequestID, clarificationHistory).
		WithClarificationContext(e.Messages[len(e.Messages)-1].ID, contextDigest).
		WithClarificationSnapshot(snapshot, source)
	defer func() {
		if err := releaseRuntime(ctx); err != nil {
			answer = execution.Output{}
			execErr = execution.ErrUnavailable
		}
	}()
	session, err := local.CreateSessionWithScope(ctx, "Workbench execution", e.OwnerID, workspace, "webchat", false)
	if err != nil {
		return execution.Output{}, err
	}
	for _, m := range e.Messages[:len(e.Messages)-1] {
		historyAttachments := make([]app.MessageAttachment, 0, len(m.Files))
		for _, file := range m.Files {
			path := filepath.Join(workspace, "conversation-files", file.ID, file.Name)
			if _, ok := materializer.byPath[path]; !ok {
				continue
			}
			source := "conversation_attachment"
			if m.Role == "assistant" {
				source = "conversation_result"
			}
			historyAttachments = append(historyAttachments, app.MessageAttachment{ArtifactID: file.ID, Name: file.Name, RelPath: filepath.ToSlash(filepath.Join("conversation-files", file.ID, file.Name)), Bytes: file.Size, SHA256: file.SHA256, Source: source})
		}
		if _, err = local.AddMessage(ctx, app.Message{ID: m.ID, SessionID: session.ID, Role: m.Role, Content: m.Content, Attachments: historyAttachments, FilesUnavailable: m.FilesUnavailable, Clarification: m.Clarification, CreatedAt: time.Now().UTC()}); err != nil {
			return execution.Output{}, err
		}
	}
	attachments := []app.MessageAttachment{}
	current := e.Messages[len(e.Messages)-1]
	for _, file := range current.Files {
		path := filepath.Join(workspace, "conversation-files", file.ID, file.Name)
		if _, ok := materializer.byPath[path]; ok {
			attachments = append(attachments, app.MessageAttachment{ArtifactID: file.ID, Name: file.Name, RelPath: filepath.ToSlash(filepath.Join("conversation-files", file.ID, file.Name)), Bytes: file.Size, SHA256: file.SHA256, Source: "conversation_attachment"})
		}
	}
	inputNames := map[string]bool{}
	for _, manifest := range e.InputFiles {
		// The user-visible name is also the temporary locator. Admission
		// rejects duplicate names, so explicit references never select another input.
		name := manifest.Name
		if strings.ContainsAny(name, "/\\\x00") {
			return execution.Output{}, errors.New("invalid temporary input name")
		}
		if err = os.WriteFile(filepath.Join(workspace, name), inputs[manifest.ID], 0600); err != nil {
			return execution.Output{}, err
		}
		inputNames[name] = true
		attachments = append(attachments, app.MessageAttachment{ArtifactID: manifest.ID, Name: manifest.Name, RelPath: name, Bytes: manifest.Size, SHA256: manifest.SHA256, Source: "client_supplied"})
	}
	// A client-provided assistant message is ordinary conversation input. No
	// previous run, approval, tool receipt or trusted ingress is imported.
	ctx = agent.WithSourceMessageID(ctx, current.ID)
	ctx = agent.WithSourceMessageFilesUnavailable(ctx, current.FilesUnavailable)
	ctx = agent.WithFinalAnswerFormat(ctx, agent.FinalAnswerMarkdown)
	if e.ClientPresentation != nil {
		ctx = agent.WithClientPresentation(ctx, e.RequestID, e.ConversationID, *e.ClientPresentation)
	}
	answerStarted := false
	ctx = agent.WithFinalAnswerStart(ctx, func() {
		if answerStarted {
			execution.ResetAnswer(ctx)
		}
		answerStarted = true
	})
	result, err := runtime.HandleMessageStreamWithAttachments(ctx, session.ID, e.Messages[len(e.Messages)-1].Content, attachments, func(event agent.StreamEvent) error {
		if event.Type == "text_delta" {
			execution.PublishAnswer(ctx, event.Text)
			execution.PublishProgress(ctx, event.Text)
		}
		return nil
	})
	if err != nil {
		return execution.Output{}, err
	}
	result, err = s.continueExecutionApprovals(ctx, service, e, local, runtime, budget, result)
	if err != nil {
		return execution.Output{}, err
	}
	out := execution.Output{Content: result.Message.Content, ClientAction: result.ClientAction, Clarification: result.Clarification, Files: map[string][]byte{}}
	size := len(out.Content)
	entries := 0
	err = filepath.WalkDir(workspace, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if !textOnly && path == filepath.Join(workspace, "conversation-files") {
				return filepath.SkipDir
			}
			return nil
		}
		entries++
		if entries > 1000 || entry.Type()&os.ModeSymlink != 0 {
			return execution.ErrCapacity
		}
		relative, _ := filepath.Rel(workspace, path)
		if inputNames[relative] {
			return nil
		}
		stat, err := entry.Info()
		if err != nil {
			return err
		}
		if !stat.Mode().IsRegular() || stat.Size() > execution.ResultBytes || int64(size)+stat.Size() > execution.ResultBytes {
			return execution.ErrCapacity
		}
		name := entry.Name()
		if _, exists := out.Files[name]; exists {
			return execution.ErrConflict
		}
		if err = workspaceBudget.reserve(path, int(stat.Size())); err != nil {
			return err
		}
		input, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		opened, err := input.Stat()
		if err != nil || !opened.Mode().IsRegular() || opened.Size() != stat.Size() {
			_ = input.Close()
			return execution.ErrUnavailable
		}
		raw, readErr := io.ReadAll(io.LimitReader(input, stat.Size()+1))
		closeErr := input.Close()
		if readErr != nil || closeErr != nil || len(raw) != int(stat.Size()) {
			return execution.ErrUnavailable
		}
		size += len(raw)
		out.Files[name] = raw
		return nil
	})
	return out, err
}

func executionMemoryWorkspace(controlRoot string) (string, error) {
	// Content-bearing tools need a real filesystem. Require a memory-backed
	// filesystem rather than silently placing plaintext in /tmp or a backup root.
	var stat unix.Statfs_t
	if err := unix.Statfs("/dev/shm", &stat); err != nil || stat.Type != 0x01021994 {
		return "", errors.New("workbench memory-backed tool workspace unavailable")
	}
	return os.MkdirTemp("/dev/shm", executionWorkspacePrefix(controlRoot))
}

func executionWorkspacePrefix(controlRoot string) string {
	return "sparkclaw-execution-" + execution.Digest([]byte(controlRoot))[:16] + "-"
}
func executionMemorySweep(controlRoot string) error {
	entries, err := os.ReadDir("/dev/shm")
	if err != nil {
		return err
	}
	prefix := executionWorkspacePrefix(controlRoot)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
			return execution.ErrUnavailable
		}
		if err = os.RemoveAll(filepath.Join("/dev/shm", entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Continue only the original temporary run after an installed client explicitly
// approves its immutable arguments. Client context never supplies authority.
func (s *Server) continueExecutionApprovals(ctx context.Context, service *execution.Service, e execution.Envelope, local *store.MemoryStore, runtime agent.Runtime, budget *execution.Budget, result agent.Result) (agent.Result, error) {
	for round := 0; result.Run.State == "approval_pending"; round++ {
		if round >= 32 {
			return agent.Result{}, execution.ErrCapacity
		}
		pending, err := local.ListApprovals(ctx, app.ApprovalStatusPending)
		if err != nil {
			return agent.Result{}, err
		}
		count := 0
		for _, approval := range pending {
			if approval.RunID != result.Run.ID {
				continue
			}
			count++
			public := map[string]any{}
			for key, value := range approval.Arguments {
				if !strings.HasPrefix(key, "_") {
					public[key] = value
				}
			}
			row, err := execution.NewPendingApproval(approval.ID, approval.Tool, approval.Summary, public)
			if err != nil {
				return agent.Result{}, err
			}
			if err = budget.Admit(row); err != nil {
				return agent.Result{}, err
			}
			decision, err := service.AwaitApproval(ctx, e, row)
			if err != nil {
				return agent.Result{}, err
			}
			if err = ctx.Err(); err != nil {
				return agent.Result{}, err
			}
			status := app.ApprovalStatusApproved
			if decision == "reject" {
				status = app.ApprovalStatusRejected
			}
			resolved, err := local.ResolveApproval(ctx, approval.ID, status, "")
			if err != nil {
				return agent.Result{}, err
			}
			if status == app.ApprovalStatusRejected {
				call, found, err := local.GetToolCall(ctx, approval.ToolCallID)
				if err != nil || !found {
					return agent.Result{}, execution.ErrUnavailable
				}
				now := time.Now().UTC()
				call.Status = app.ToolCallStatusRejected
				call.Error = "owner rejected approval"
				call.CompletedAt = &now
				if _, err = local.SaveToolCall(ctx, call); err != nil {
					return agent.Result{}, err
				}
				// Reject terminates this temporary workflow; remaining calls never run.
				result.Run.State = "blocked"
				result.Message.Content = "Action rejected. The requested operation was not executed."
				return result, nil
			}
			executed, err := runtime.ExecuteApprovedToolCall(ctx, resolved)
			if err != nil {
				return agent.Result{}, err
			}
			if executed.Status.Failed() {
				// A tool failure may follow a partial external write. Do not
				// claim completion or replay this temporary computation.
				return agent.Result{}, execution.ErrUnavailable
			}
		}
		if count == 0 {
			return agent.Result{}, execution.ErrUnavailable
		}
		resumed, ok, err := runtime.ResumeRunAfterApproval(ctx, result.Run.SessionID, result.Run.ID)
		if err != nil {
			return agent.Result{}, err
		}
		if ok {
			result = resumed
		} else {
			if err = runtime.CompleteRunIfApprovalsResolved(ctx, result.Run.ID); err != nil {
				return agent.Result{}, err
			}
			run, found, err := local.GetRun(ctx, result.Run.ID)
			if err != nil || !found || run.State == "approval_pending" {
				return agent.Result{}, execution.ErrUnavailable
			}
			result.Run = run
			result.Message.Content = "Approved operation completed."
		}
	}
	return result, nil
}

func envelopeHasHandles(e execution.Envelope) bool {
	for _, message := range e.Messages {
		if len(message.Files) > 0 {
			return true
		}
	}
	return false
}
