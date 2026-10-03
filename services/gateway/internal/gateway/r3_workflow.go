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

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3browser"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3execution"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"golang.org/x/sys/unix"
)

func (s *Server) executeR3Workflow(ctx context.Context, e r3execution.Envelope, inputs map[string][]byte) (answer r3execution.Output, execErr error) {
	// This registration spans the detached computation, so device revocation
	// cancels it even after the submit HTTP request has returned.
	ctx, release, err := s.clientConnectionContext(ctx, e.ClientID)
	if err != nil {
		return r3execution.Output{}, context.Canceled
	}
	defer release()
	service, err := s.r3ExecutionService()
	if err != nil {
		return r3execution.Output{}, err
	}
	root, err := r3MemoryWorkspace(service.Root())
	if err != nil {
		return r3execution.Output{}, err
	}
	defer func() {
		if err := os.RemoveAll(root); err != nil {
			answer = r3execution.Output{}
			execErr = r3execution.ErrUnavailable
		}
	}()
	cfg := s.cfg
	cfg.Workspaces.DefaultRoot = filepath.Join(root, "workspace")
	cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
	cfg.Storage.ArtifactBackend = "filesystem"
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	cfg.Storage.TraceDir = ""
	cfg.Storage.LogDir = ""
	cfg.State.Path = ""
	cfg.State.DSN = ""
	cfg.Security.ToolPolicyPath = ""
	cfg.Memory.Enabled = false
	cfg.Tools.Reminders.Enabled = false
	cfg.Tools.Notifications.Channels = nil
	cfg.MCPServers = nil
	cfg.Runtime.RunMaxDurationSeconds = int(r3execution.ExecutionBudget / time.Second)
	cfg.Runtime.RunMaxObservationBytes = min(cfg.Runtime.RunMaxObservationBytes, 1<<20)
	cfg.Security.DeniedTools = append(append([]string{}, cfg.Security.DeniedTools...), "shell.run", "notify.send", "memory.save", "memory.delete", "reminders.create", "reminders.update", "reminders.cancel")
	if err = os.MkdirAll(cfg.Workspaces.DefaultRoot, 0700); err != nil {
		return r3execution.Output{}, err
	}
	budget := r3execution.NewBudget(r3execution.TaskBytes)
	for _, raw := range inputs {
		if err = budget.Reserve(len(raw)); err != nil {
			return r3execution.Output{}, err
		}
	}
	local := store.NewMemoryStore().WithTransientContentAdmission(budget.Admit)
	tools := toolhub.New(cfg, local)
	defer tools.Close()
	// BrowserHostAdapter is installed here after the Broker tranche is merged.
	if err = s.bindR3Browser(tools, e); err != nil {
		return r3execution.Output{}, err
	}
	artifacts := r3execution.TemporaryArtifacts{Store: artifact.NewStore(cfg.Storage), Budget: budget}
	tools.WithArtifactStore(artifacts)
	runtime := s.runtime.WithTransientRepositories(local, tools, policy.New(cfg), artifacts)
	session, err := local.CreateSessionWithScope(ctx, "R3 temporary execution", e.OwnerID, cfg.Workspaces.DefaultRoot, "webchat", false)
	if err != nil {
		return r3execution.Output{}, err
	}
	for _, m := range e.Messages[:len(e.Messages)-1] {
		if _, err = local.AddMessage(ctx, app.Message{ID: app.NewID("m"), SessionID: session.ID, Role: m.Role, Content: m.Content, CreatedAt: time.Now().UTC()}); err != nil {
			return r3execution.Output{}, err
		}
	}
	attachments := []app.MessageAttachment{}
	inputNames := map[string]bool{}
	for _, manifest := range e.InputFiles {
		name := manifest.ID + "-" + manifest.Name
		if strings.ContainsAny(name, "/\\\x00") {
			return r3execution.Output{}, errors.New("invalid temporary input name")
		}
		if err = os.WriteFile(filepath.Join(cfg.Workspaces.DefaultRoot, name), inputs[manifest.ID], 0600); err != nil {
			return r3execution.Output{}, err
		}
		inputNames[name] = true
		attachments = append(attachments, app.MessageAttachment{Name: manifest.Name, RelPath: name, Bytes: manifest.Size, SHA256: manifest.SHA256, Source: "client_supplied"})
	}
	// A client-provided assistant message is ordinary conversation input. No
	// previous run, approval, tool receipt or trusted ingress is imported.
	result, err := runtime.HandleMessageWithAttachments(ctx, session.ID, e.Messages[len(e.Messages)-1].Content, attachments)
	if err != nil {
		return r3execution.Output{}, err
	}
	out := r3execution.Output{Content: result.Message.Content, Files: map[string][]byte{}}
	size := len(out.Content)
	entries := 0
	err = filepath.WalkDir(cfg.Workspaces.DefaultRoot, func(path string, entry fs.DirEntry, walkErr error) error {
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
			return r3execution.ErrCapacity
		}
		relative, _ := filepath.Rel(cfg.Workspaces.DefaultRoot, path)
		if inputNames[relative] {
			return nil
		}
		stat, err := entry.Info()
		if err != nil {
			return err
		}
		if !stat.Mode().IsRegular() || stat.Size() > r3execution.ResultBytes || int64(size)+stat.Size() > r3execution.ResultBytes {
			return r3execution.ErrCapacity
		}
		name := entry.Name()
		if _, exists := out.Files[name]; exists {
			return r3execution.ErrConflict
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

func r3MemoryWorkspace(controlRoot string) (string, error) {
	// Content-bearing tools need a real filesystem. Require a memory-backed
	// filesystem rather than silently placing plaintext in /tmp or a backup root.
	var stat unix.Statfs_t
	if err := unix.Statfs("/dev/shm", &stat); err != nil || stat.Type != 0x01021994 {
		return "", errors.New("R3 memory-backed tool workspace unavailable")
	}
	return os.MkdirTemp("/dev/shm", r3WorkspacePrefix(controlRoot))
}

func (s *Server) bindR3Browser(tools *toolhub.ToolHub, e r3execution.Envelope) error {
	broker, err := s.r3HostBroker()
	if err != nil {
		return err
	}
	tools.WithBrowserAutomationAdapter(broker.ForScope(r3browser.Scope{Identity: r3browser.Identity{OwnerID: e.OwnerID, ClientID: e.ClientID, InstallationID: e.InstallationID}, ConversationID: e.ConversationID, TaskID: e.TaskID}))
	return nil
}

func r3WorkspacePrefix(controlRoot string) string {
	return "sparkclaw-r3-" + r3execution.Digest([]byte(controlRoot))[:16] + "-"
}
func r3MemorySweep(controlRoot string) error {
	entries, err := os.ReadDir("/dev/shm")
	if err != nil {
		return err
	}
	prefix := r3WorkspacePrefix(controlRoot)
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
			return r3execution.ErrUnavailable
		}
		if err = os.RemoveAll(filepath.Join("/dev/shm", entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
