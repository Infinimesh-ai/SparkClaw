package toolhub

import (
	"context"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/sandbox"
)

func (h *ToolHub) shellExecSandboxed(ctx context.Context, args map[string]any) (Result, error) {
	command := stringArg(args, "command", "")
	if command == "" {
		return Result{}, errors.New("command cannot be empty")
	}
	result, err := h.runner.Run(ctx, sandbox.Request{
		Command:       command,
		WorkspaceRoot: h.cfg.Workspaces.DefaultRoot,
		TimeoutMS:     intArg(args, "timeout_ms", 10000),
		Image:         h.cfg.Sandbox.Image,
		Network:       h.cfg.Sandbox.Network,
	})
	if err != nil {
		return Result{Output: result}, err
	}
	return Result{Output: result}, nil
}
