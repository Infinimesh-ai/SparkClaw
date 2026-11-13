package mcdintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
)

// Helper uses an installed, build-verified interpreter. It never installs
// packages, starts a listener or accepts a shell command/URL from a caller.
type Helper struct {
	Python         string
	ManifestSHA256 string
}

func (h Helper) Quote(ctx context.Context, e Envelope, x Execution, q QuoteRequest, root string) (json.RawMessage, error) {
	if !filepath.IsAbs(root) {
		return nil, ErrInvalid
	}
	return h.callWithArgs(ctx, "selection.session.quote", map[string]any{"envelope": e, "execution": x, "request": q.Request, "purchase_id": q.PurchaseID, "quote_revision": q.QuoteRevision, "handoff_epoch": q.HandoffEpoch, "source_quote_ref": q.SourceQuoteRef}, "--execution-store", root)
}

func (h Helper) Validate(ctx context.Context, e Envelope) error {
	_, err := h.call(ctx, "session.validate", map[string]any{"envelope": e})
	return err
}
func (h Helper) Read(ctx context.Context, e Envelope, x Execution, op string, args json.RawMessage) (json.RawMessage, error) {
	return h.call(ctx, "selection.session.read", map[string]any{"envelope": e, "execution": x, "tool": op, "arguments": args})
}
func (h Helper) Cart(ctx context.Context, e Envelope, x Execution, op string, args json.RawMessage, root string, authority *CartAuthority) (json.RawMessage, error) {
	if !filepath.IsAbs(root) {
		return nil, ErrInvalid
	}
	return h.callWithArgs(ctx, "selection.session.cart", map[string]any{"envelope": e, "execution": x, "tool": op, "arguments": args, "authority": authority}, "--execution-store", root)
}

type boundedOutput struct{ bytes.Buffer }

func (w *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > 320<<10-w.Len() {
		return 0, ErrUnavailable
	}
	return w.Buffer.Write(p)
}

func (h Helper) call(ctx context.Context, op string, params any) (json.RawMessage, error) {
	return h.callWithArgs(ctx, op, params)
}
func (h Helper) callWithArgs(ctx context.Context, op string, params any, extra ...string) (json.RawMessage, error) {
	if !filepath.IsAbs(h.Python) {
		return nil, ErrUnavailable
	}
	if h.ManifestSHA256 != "" && VerifyRuntime(h.Python, h.ManifestSHA256) != nil {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	raw, err := json.Marshal(map[string]any{"operation": op, "params": params})
	defer clear(raw)
	if err != nil || len(raw) > 64<<10 {
		return nil, ErrInvalid
	}
	argv := append([]string{"-I", "-B", "-m", "sparkclaw_mcd.helper", "--role", "selection"}, extra...)
	cmd := exec.CommandContext(ctx, h.Python, argv...)
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Stderr = io.Discard
	var output boundedOutput
	cmd.Stdout = &output
	defer func() { clear(output.Bytes()) }()
	runErr := cmd.Run()
	var response struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if appstream.DecodeStrict(output.Bytes(), &response) != nil {
		return nil, ErrUnavailable
	}
	if !response.OK && (response.Error == "upstream_session_expired_reconnect_locally" || response.Error == "upstream_wallet_member_mismatch") {
		return nil, ErrCredential
	}
	if runErr != nil || !response.OK || len(response.Result) == 0 {
		return nil, ErrUnavailable
	}
	return response.Result, nil
}
