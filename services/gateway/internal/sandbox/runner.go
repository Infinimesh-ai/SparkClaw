package sandbox

import (
	"context"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type Request struct {
	Command       string `json:"command"`
	WorkspaceRoot string `json:"workspace_root"`
	TimeoutMS     int    `json:"timeout_ms"`
	Image         string `json:"image,omitempty"`
	Network       string `json:"network,omitempty"`
}

type Result struct {
	Status  string `json:"status"`
	Backend string `json:"backend"`
	Network string `json:"network"`
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
}

type Runner interface {
	Run(ctx context.Context, req Request) (Result, error)
}

func NewRunner(cfg config.Config) Runner {
	if !cfg.Sandbox.Enabled {
		return DisabledRunner{}
	}
	switch cfg.Sandbox.Backend {
	case "remote", "http", "sandbox-runner":
		return NewHTTPRunner(cfg.Sandbox.RunnerURL)
	default:
		return LocalDockerRunner{}
	}
}

type DisabledRunner struct{}

func (DisabledRunner) Run(context.Context, Request) (Result, error) {
	return Result{}, errors.New("sandbox is disabled")
}

func normalizeRequest(req Request) Request {
	if req.TimeoutMS <= 0 || req.TimeoutMS > 30000 {
		req.TimeoutMS = 10000
	}
	if req.Image == "" {
		req.Image = "alpine:3.22"
	}
	if req.Network == "" || req.Network == "none_by_default" {
		req.Network = "none"
	}
	return req
}

func trimOutput(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n[truncated]"
}
