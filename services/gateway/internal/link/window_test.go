package link

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// chanStream is an in-memory FrameStream: in carries device frames, out
// receives the backend's.
type chanStream struct {
	in, out chan []byte
	done    chan struct{}
}

func (c *chanStream) ReadFrame(ctx context.Context) ([]byte, error) {
	select {
	case b := <-c.in:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, errors.New("closed")
	}
}

func (c *chanStream) WriteFrame(ctx context.Context, b []byte) error {
	select {
	case c.out <- b:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *chanStream) Close() error {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	return nil
}

func TestFullRequestWindowKeepsServingCancels(t *testing.T) {
	svc := NewService()
	svc.MaxConcurrent = 1
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	svc.Handle(contract.OpRunStatus, func(ctx context.Context, _ *Request) (any, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return map[string]string{"state": "idle"}, nil
	})
	st := &chanStream{in: make(chan []byte, 64), out: make(chan []byte, 64), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = svc.Serve(ctx, Principal{Principal: contract.Principal{OwnerID: "o", DeviceID: "d", Class: contract.ClassPhone}}, st) }()
	send := func(f contract.Frame) {
		raw, _ := contract.Encode(f)
		st.in <- raw
	}
	read := func() contract.Frame {
		t.Helper()
		select {
		case raw := <-st.out:
			f, err := contract.Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			return f
		case <-time.After(5 * time.Second):
			t.Fatal("no frame from the backend")
		}
		return contract.Frame{}
	}
	send(contract.Frame{Type: contract.FrameHello, ID: "hello", Hello: &contract.Hello{Versions: []string{contract.Version}, Device: &contract.Device{Class: contract.ClassPhone}}})
	if f := read(); f.Type != contract.FrameHello {
		t.Fatalf("hello reply %+v", f)
	}
	// r1 holds the only slot; r2 waits in the backlog.
	req1, _ := contract.Request("r1", contract.OpRunStatus, nil)
	send(req1)
	<-started
	req2, _ := contract.Request("r2", contract.OpRunStatus, nil)
	send(req2)
	// The read loop still takes frames: cancelling the waiting request
	// answers it at once, without it ever running.
	send(contract.Frame{Type: contract.FrameCancel, ID: "r2"})
	f := read()
	if f.ID != "r2" || f.Error == nil || f.Error.Code != contract.CodeCancelled || !f.Error.NotSent {
		t.Fatalf("cancelled request %+v %+v", f, f.Error)
	}
	close(release)
	if f := read(); f.ID != "r1" || f.OK == nil || !*f.OK {
		t.Fatalf("first request %+v", f)
	}
	// Beyond the window and its backlog a request is refused as not sent.
	block := make(chan struct{})
	svc.Handle(contract.OpRunStatus, func(ctx context.Context, _ *Request) (any, error) { <-block; return nil, nil })
	defer close(block)
	for i := 0; i < 1+8; i++ {
		req, _ := contract.Request("w"+string(rune('a'+i)), contract.OpRunStatus, nil)
		send(req)
	}
	req, _ := contract.Request("over", contract.OpRunStatus, nil)
	send(req)
	f = read()
	if f.ID != "over" || f.Error == nil || f.Error.Code != contract.CodeBusy || !f.Error.NotSent {
		t.Fatalf("over-window request %+v %+v", f, f.Error)
	}
}
