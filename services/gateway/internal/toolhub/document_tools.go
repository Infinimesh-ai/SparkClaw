package toolhub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

type textReplacement struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

func (h *ToolHub) officeReplaceText(ctx context.Context, args map[string]any) (Result, error) {
	return h.replaceDocumentText(ctx, args, "office_version_written")
}

func (h *ToolHub) textReplaceText(ctx context.Context, args map[string]any) (Result, error) {
	return h.replaceDocumentText(ctx, args, "text_version_written")
}

func (h *ToolHub) replaceDocumentText(ctx context.Context, args map[string]any, status string) (Result, error) {
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	replacements, err := replacementArgs(args["replacements"])
	if err != nil {
		return Result{}, err
	}
	expected := intArg(args, "expected_replacements", 0)
	targets := make([]document.LocatorRequest, 0, len(replacements))
	for _, replacement := range replacements {
		targets = append(targets, document.LocatorRequest{
			Kind: document.LocatorExactText, Text: replacement.Find, AllowMultiple: expected > 0,
		})
	}
	result, err := h.editDocumentWorkflow(ctx, document.EditRequest{
		Path: inputPath, OutputPath: outputPath, Operation: "replace_text", Targets: targets, ExpectedMatches: expected,
		Arguments: map[string]any{"replacements": args["replacements"]}, MaxBytes: document.SmallExtractedMaxBytes,
	})
	if err != nil {
		return Result{}, err
	}
	output := documentChangeOutput(result, status)
	output["replacements"] = result.Changed
	return Result{Output: output}, nil
}

func (h *ToolHub) resolveNewOutputPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("output_path is required")
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) && strings.HasPrefix(clean, "..") {
		return "", errors.New("output_path cannot escape workspace")
	}
	abs, err := h.resolvePath(clean)
	if err != nil {
		return "", err
	}
	if strings.Contains(abs, string(os.PathSeparator)+".sparkclaw"+string(os.PathSeparator)+"state") {
		return "", errors.New("output_path cannot target SparkClaw control files")
	}
	return abs, nil
}

func replacementArgs(value any) ([]textReplacement, error) {
	items, ok := arrayItems(value)
	if !ok || len(items) == 0 {
		return nil, errors.New("replacements must be a non-empty array")
	}
	out := make([]textReplacement, 0, len(items))
	for i, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("replacements[%d] must be object", i)
		}
		find := strings.TrimSpace(stringArg(object, "find", ""))
		replace := stringArg(object, "replace", "")
		if find == "" {
			return nil, fmt.Errorf("replacements[%d].find cannot be empty", i)
		}
		out = append(out, textReplacement{Find: find, Replace: replace})
	}
	return out, nil
}

func outputStringArray(value any) []string {
	items, ok := arrayItems(value)
	if !ok {
		return []string{}
	}
	out := []string{}
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// documentAdapterTimeout bounds a single document subprocess so a hung
// node/python process cannot pin the request forever when the caller's
// context carries no deadline of its own.
const documentAdapterTimeout = 60 * time.Second

func runPythonAdapter(ctx context.Context, script string, request map[string]any) (map[string]any, error) {
	return runSubprocessAdapter(ctx, request, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, documentPythonBinary(), "-c", script)
	})
}

func runNodeAdapter(ctx context.Context, script string, request map[string]any) (map[string]any, error) {
	return runSubprocessAdapter(ctx, request, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, documentNodeBinary(), "-e", script)
	})
}

func runSubprocessAdapter(ctx context.Context, request map[string]any, makeCmd func(context.Context) *exec.Cmd) (map[string]any, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, documentAdapterTimeout)
		defer cancel()
	}
	cmd := makeCmd(ctx)
	cmd.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, err
	}
	if errText := stringArg(out, "error", ""); errText != "" {
		return nil, errors.New(errText)
	}
	return out, nil
}

func documentPythonBinary() string {
	return "python3"
}

func documentNodeBinary() string {
	return "node"
}

func fileSize(path string) int {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return int(info.Size())
}
