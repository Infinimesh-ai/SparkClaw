package toolhub

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/remindertarget"
)

// ExecutionResources are supplied by the authenticated admission adapter, never
// deserialized from model/client context. Browser is already bound to the admitted
// owner, installation, conversation and task by the host broker.
// The caller owns the workspace and artifact lifetime; the hub owns Browser.
type ExecutionResources struct {
	OwnerID             string
	WorkspaceRoot       string
	Artifacts           artifact.Store
	Browser             browserautomation.Adapter
	MaxDuration         time.Duration
	MaxObservationBytes int
	// TextOnly is a trusted admission restriction. It removes every tool from
	// exposure and rejects invocation, including dynamic/provider tools.
	TextOnly bool
}

type executionResource string

const (
	resourceWorkbenchMemory    executionResource = "workbench memory repository"
	resourceWorkbenchSchedules executionResource = "workbench schedule repository"
	resourceSandboxWorkspace   executionResource = "sandbox workspace binding"
	resourceAcquisitionBrowser executionResource = "authorized acquisition browser"
	resourceExternalLedger     executionResource = "external connector retention ledger"
	resourceToolExecution      executionResource = "tool execution unavailable in the text-only profile"
)

// WithExecutionScope derives a content-isolated hub from the active services.
// Providers and registry remain service-owned. Content-bearing caches, document
// enrichers, browser windows, repositories and artifacts are execution-owned.
func (h *ToolHub) WithExecutionScope(st Repository, resources ExecutionResources) (*ToolHub, error) {
	if h == nil || st == nil || resources.Artifacts == nil || strings.TrimSpace(resources.OwnerID) == "" || !filepath.IsAbs(resources.WorkspaceRoot) {
		return nil, errors.New("execution scope requires owner, repository, artifacts and an absolute workspace")
	}
	resources.WorkspaceRoot = filepath.Clean(resources.WorkspaceRoot)
	scoped := *h
	scoped.store = st
	scoped.resources = &resources
	scoped.cfg.Workspaces.DefaultRoot = resources.WorkspaceRoot
	scoped.cfg.Workspaces.Allowlist = []string{resources.WorkspaceRoot}
	scoped.cfg.Memory.Enabled = false
	scoped.cfg.Storage.TraceDir = ""
	scoped.cfg.Storage.LogDir = ""
	if resources.MaxDuration > 0 {
		scoped.cfg.Runtime.RunMaxDurationSeconds = int(resources.MaxDuration / time.Second)
	}
	if resources.MaxObservationBytes > 0 {
		scoped.cfg.Runtime.RunMaxObservationBytes = min(scoped.cfg.Runtime.RunMaxObservationBytes, resources.MaxObservationBytes)
	}
	scoped.artifacts = resources.Artifacts
	scoped.reminders = remindertarget.NewResolver(st)
	scoped.browser = resources.Browser
	scoped.managedBrowserWindows = newManagedBrowserWindowRegistry()
	scoped.lifecycle = &toolHubLifecycle{}
	scoped.ownsProviders = false
	scoped.ocrRuntime = newDocumentOCRRuntime(h.cfg.Adapters.DocumentOCR, h.ocr, nil)
	scoped.documents = newDocumentPipeline(&scoped)
	// These services have no admitted durable repository/browser binding in this
	// scope. The registry's resource requirement keeps them out of exposure and
	// rejects explicit invocation rather than returning an empty/ephemeral result.
	scoped.aiChatExporter = nil
	scoped.connectorGate = nil
	return &scoped, nil
}

func (h *ToolHub) unavailableResource(name string) executionResource {
	if h.resources == nil {
		return ""
	}
	if h.resources.TextOnly {
		return resourceToolExecution
	}
	if _, dynamic := h.registry.origins[name]; dynamic {
		return resourceExternalLedger
	}
	return toolRegistry[name].resource
}

func (h *ToolHub) requireResource(name string) error {
	h.registry.mu.RLock()
	resource := h.unavailableResource(name)
	h.registry.mu.RUnlock()
	if resource == "" {
		return nil
	}
	return &app.CodedToolError{Code: app.ToolErrorResourceUnavailable, Err: fmt.Errorf("tool %q requires %s", name, resource)}
}

func (h *ToolHub) validateExecutionSession(ctx context.Context, sessionID string) error {
	if h.resources == nil {
		return nil
	}
	session, found, err := h.store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if !found || session.OwnerID != h.resources.OwnerID || filepath.Clean(session.WorkspaceRoot) != h.resources.WorkspaceRoot {
		return errors.New("session does not belong to the admitted execution resources")
	}
	return nil
}
