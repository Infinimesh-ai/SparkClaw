package toolhub

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
)

// The service owns one live OCR provider. Execution scopes borrow it but never
// share OCR content caches or close the provider when their work completes.
type documentOCRProvider struct {
	mu         sync.Mutex
	adapter    documentocr.Adapter
	generation uint64
	nextID     uint64
	calls      map[uint64]context.CancelCauseFunc
	closed     bool
}

func newDocumentOCRProvider(adapter documentocr.Adapter) *documentOCRProvider {
	return &documentOCRProvider{adapter: adapter, generation: 1, calls: map[uint64]context.CancelCauseFunc{}}
}

func (p *documentOCRProvider) Enabled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && p.adapter != nil && p.adapter.Enabled()
}

func (p *documentOCRProvider) replace(adapter documentocr.Adapter) {
	p.mu.Lock()
	previous := p.adapter
	for _, cancel := range p.calls {
		cancel(errors.New("document OCR provider changed"))
	}
	p.adapter = adapter
	p.generation++
	p.mu.Unlock()
	if previous != nil {
		if err := previous.Close(); err != nil {
			slog.Warn("replaced OCR provider did not close cleanly", "error", err)
		}
	}
}

type documentOCRLease struct {
	adapter    documentocr.Adapter
	generation uint64
	ctx        context.Context
	finish     func()
}

func (p *documentOCRProvider) begin(ctx context.Context) documentOCRLease {
	p.mu.Lock()
	defer p.mu.Unlock()
	callCtx, cancel := context.WithCancelCause(ctx)
	p.nextID++
	id := p.nextID
	p.calls[id] = cancel
	if p.closed {
		cancel(errors.New("document OCR provider closed"))
	}
	return documentOCRLease{adapter: p.adapter, generation: p.generation, ctx: callCtx, finish: func() {
		p.mu.Lock()
		delete(p.calls, id)
		p.mu.Unlock()
		cancel(nil)
	}}
}

func (p *documentOCRProvider) Parse(ctx context.Context, request documentocr.Request) (documentocr.Result, error) {
	lease := p.begin(ctx)
	defer lease.finish()
	return lease.parse(request)
}

func (lease documentOCRLease) parse(request documentocr.Request) (documentocr.Result, error) {
	if err := context.Cause(lease.ctx); err != nil {
		return documentocr.Result{}, err
	}
	if lease.adapter == nil {
		return documentocr.Result{}, errors.New("document OCR provider unavailable")
	}
	result, err := lease.adapter.Parse(lease.ctx, request)
	if cause := context.Cause(lease.ctx); cause != nil {
		return documentocr.Result{}, cause
	}
	return result, err
}

func (p *documentOCRProvider) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	for _, cancel := range p.calls {
		cancel(errors.New("document OCR provider closed"))
	}
	adapter := p.adapter
	p.mu.Unlock()
	if adapter != nil {
		return adapter.Close()
	}
	return nil
}
