package gateway

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
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
	root, err := executionMemoryWorkspace(service.Root())
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
	local := store.NewMemoryStore().WithTransientContentAdmission(budget.Admit)
	broker, err := s.browserHostBroker()
	if err != nil {
		return execution.Output{}, err
	}
	browser := broker.ForScope(browserhost.Scope{Identity: browserhost.Identity{OwnerID: e.OwnerID, ClientID: e.ClientID, InstallationID: e.InstallationID}, ConversationID: e.ConversationID, TaskID: e.TaskID})
	artifacts := execution.TemporaryArtifacts{Store: artifact.NewStore(storage), Budget: budget}
	runtime, releaseRuntime, err := s.runtime.WithExecutionScope(local, toolhub.ExecutionResources{
		OwnerID: e.OwnerID, WorkspaceRoot: workspace, Artifacts: artifacts, Browser: browser,
		MaxDuration: execution.ExecutionBudget, MaxObservationBytes: 1 << 20,
	})
	if err != nil {
		return execution.Output{}, err
	}
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
		if _, err = local.AddMessage(ctx, app.Message{ID: app.NewID("m"), SessionID: session.ID, Role: m.Role, Content: m.Content, CreatedAt: time.Now().UTC()}); err != nil {
			return execution.Output{}, err
		}
	}
	attachments := []app.MessageAttachment{}
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
		attachments = append(attachments, app.MessageAttachment{Name: manifest.Name, RelPath: name, Bytes: manifest.Size, SHA256: manifest.SHA256, Source: "client_supplied"})
	}
	// A client-provided assistant message is ordinary conversation input. No
	// previous run, approval, tool receipt or trusted ingress is imported.
	result, err := runtime.HandleMessageWithAttachments(ctx, session.ID, e.Messages[len(e.Messages)-1].Content, attachments)
	if err != nil {
		return execution.Output{}, err
	}
	result, err = s.continueExecutionApprovals(ctx, service, e, local, runtime, budget, result)
	if err != nil {
		return execution.Output{}, err
	}
	out := execution.Output{Content: result.Message.Content, Files: map[string][]byte{}}
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
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
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
