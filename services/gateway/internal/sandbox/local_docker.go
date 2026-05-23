package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type LocalDockerRunner struct {
	HostWorkspaceRoot      string
	ContainerWorkspaceRoot string
}

func (r LocalDockerRunner) Run(ctx context.Context, req Request) (Result, error) {
	req = normalizeRequest(req)
	if req.Command == "" {
		return Result{}, errors.New("command cannot be empty")
	}
	if req.WorkspaceRoot == "" {
		return Result{}, errors.New("workspace root cannot be empty")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return Result{}, errors.New("docker sandbox backend unavailable: docker CLI not found")
	}
	workspaceRoot := r.workspaceRootForDocker(req.WorkspaceRoot)
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(
		execCtx,
		"docker",
		"run",
		"--rm",
		"--network",
		req.Network,
		"-v",
		workspaceRoot+":/workspace:rw",
		"-w",
		"/workspace",
		req.Image,
		"sh",
		"-lc",
		req.Command,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := trimOutput(stdout.String(), 16000)
	errorOutput := trimOutput(stderr.String(), 8000)
	result := Result{
		Status:  "completed",
		Backend: "local-docker",
		Network: req.Network,
		Stdout:  output,
		Stderr:  errorOutput,
	}
	if execCtx.Err() == context.DeadlineExceeded {
		result.Status = "timed_out"
		return result, errors.New("sandbox command timed out")
	}
	if err != nil {
		result.Status = "failed"
		return result, fmt.Errorf("sandbox command failed: %w: %s", err, errorOutput)
	}
	return result, nil
}

func (r LocalDockerRunner) workspaceRootForDocker(containerPath string) string {
	containerPath = filepath.Clean(containerPath)
	containerRoot := strings.TrimSpace(r.ContainerWorkspaceRoot)
	hostRoot := strings.TrimSpace(r.HostWorkspaceRoot)
	if containerRoot == "" || hostRoot == "" {
		return containerPath
	}
	containerRoot = filepath.Clean(containerRoot)
	hostRoot = filepath.Clean(hostRoot)
	if containerPath == containerRoot {
		return hostRoot
	}
	prefix := containerRoot + string(filepath.Separator)
	if strings.HasPrefix(containerPath, prefix) {
		return filepath.Join(hostRoot, strings.TrimPrefix(containerPath, prefix))
	}
	return containerPath
}
