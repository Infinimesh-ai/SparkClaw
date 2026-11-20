package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// Principal is what a connector hands the Gateway after verifying a device.
type Principal struct {
	contract.Principal
	// ClientID is the Gateway client row that represents this device.
	ClientID   string
	Conditions contract.Conditions
	Profile    string
	Transport  string // lan | iscp | local
}

// Request is one forward request.
type Request struct {
	Principal Principal
	Op        string
	Params    json.RawMessage
	// Conn is the open connection the request arrived on (nil for a
	// single Dispatch).
	Conn *Conn
}

// Handler serves one operation. The returned value is marshalled as the
// response result; a *contract.Error becomes a structured failure.
type Handler func(ctx context.Context, req *Request) (any, error)

// FrameStream is a duplex frame transport supplied by a connector.
type FrameStream interface {
	ReadFrame(ctx context.Context) ([]byte, error)
	WriteFrame(ctx context.Context, frame []byte) error
	Close() error
}

// Service routes link frames to registered handlers.
type Service struct {
	mu       sync.RWMutex
	handlers map[string]Handler
	conns    map[string]map[*Conn]struct{}
	onOpen   []func(*Conn)
	onClose  []func(*Conn)
	nextID   atomic.Uint64
	// MaxConcurrent bounds in-flight requests per connection.
	MaxConcurrent int
	// Backend names this backend in device.describe.
	Backend string
}

// NewService builds an empty service.
func NewService() *Service {
	return &Service{handlers: map[string]Handler{}, conns: map[string]map[*Conn]struct{}{}, MaxConcurrent: 16}
}

// Handle registers the handler for op. The op must be in the contract.
func (s *Service) Handle(op string, h Handler) {
	if _, ok := contract.LookupOperation(op); !ok {
		panic("link: unknown operation " + op)
	}
	s.mu.Lock()
	s.handlers[op] = h
	s.mu.Unlock()
}

// OnOpen registers a callback for every new connection (after hello).
func (s *Service) OnOpen(fn func(*Conn)) { s.mu.Lock(); s.onOpen = append(s.onOpen, fn); s.mu.Unlock() }

// OnClose registers a callback for every closed connection.
func (s *Service) OnClose(fn func(*Conn)) {
	s.mu.Lock()
	s.onClose = append(s.onClose, fn)
	s.mu.Unlock()
}

// Features lists the operations available to a device: the contract rule
// intersected with what the Gateway implements.
func (s *Service) Features(c contract.Conditions, profile string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for _, op := range contract.Available(c, profile) {
		if _, ok := s.handlers[op]; ok {
			out = append(out, op)
		}
	}
	return out
}

func (s *Service) check(p Principal, op string) (Handler, *contract.Error) {
	def, ok := contract.LookupOperation(op)
	if !ok {
		return nil, contract.Errorf(contract.CodeUnsupportedCapability, "operation %s is not part of %s", op, contract.Version)
	}
	if !def.Requires.Satisfied(p.Conditions) {
		return nil, contract.Errorf(contract.CodeUnsupportedCapability, "this device cannot use %s", op)
	}
	if def.DuplexOnly && p.Profile == contract.ProfilePull {
		return nil, contract.Errorf(contract.CodeUnsupportedCapability, "%s needs a duplex connection", op)
	}
	s.mu.RLock()
	h, ok := s.handlers[op]
	s.mu.RUnlock()
	if !ok {
		return nil, contract.Errorf(contract.CodeUnsupportedCapability, "this backend does not provide %s", op)
	}
	return h, nil
}

// Dispatch serves one request without a connection (pull access).
func (s *Service) Dispatch(ctx context.Context, p Principal, f contract.Frame) contract.Frame {
	if f.Type != contract.FrameRequest {
		return contract.Failure(f.ID, contract.Errorf(contract.CodeInvalidRequest, "expected a request"))
	}
	h, cerr := s.check(p, f.Op)
	if cerr != nil {
		return contract.Failure(f.ID, cerr)
	}
	return s.invoke(ctx, h, &Request{Principal: p, Op: f.Op, Params: f.Params}, f.ID)
}

func (s *Service) invoke(ctx context.Context, h Handler, req *Request, id string) (out contract.Frame) {
	defer func() {
		if r := recover(); r != nil {
			out = contract.Failure(id, contract.Errorf(contract.CodeInternal, "handler failed"))
		}
	}()
	result, err := h(ctx, req)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return contract.Failure(id, contract.Errorf(contract.CodeCancelled, "request cancelled"))
		}
		return contract.Failure(id, contract.AsError(err))
	}
	resp, err := contract.Success(id, result)
	if err != nil {
		return contract.Failure(id, contract.Errorf(contract.CodeInternal, "encode result: %v", err))
	}
	return resp
}

// Conn is one open device connection.
type Conn struct {
	s         *Service
	stream    FrameStream
	Principal Principal
	Reverse   []string

	ctx     context.Context
	cancel  context.CancelFunc
	writeMu sync.Mutex

	mu       sync.Mutex
	inflight map[string]context.CancelFunc
	reverse  map[string]chan contract.Frame
	closers  []func()
	nextRev  uint64
	closed   bool
}

// Context ends when the connection closes.
func (c *Conn) Context() context.Context { return c.ctx }

// OnClose runs fn when the connection ends (subscription cleanup).
func (c *Conn) OnClose(fn func()) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		fn()
		return
	}
	c.closers = append(c.closers, fn)
	c.mu.Unlock()
}

func (c *Conn) write(f contract.Frame) error {
	raw, err := contract.Encode(f)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	wctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()
	return c.stream.WriteFrame(wctx, raw)
}

// Emit pushes an event to the device.
func (c *Conn) Emit(subscription, topic string, data any) error {
	f, err := contract.Event("e"+strconv.FormatUint(c.s.nextID.Add(1), 10), subscription, topic, data)
	if err != nil {
		return err
	}
	return c.write(f)
}

// ErrReverseUnavailable is returned when the device does not serve the
// reverse operation or the connection ended.
var ErrReverseUnavailable = errors.New("device does not serve this operation")

// Call invokes a reverse operation on the device (design 3.12) and waits
// for its response.
func (c *Conn) Call(ctx context.Context, op string, params any) (json.RawMessage, error) {
	if !slices.Contains(c.Reverse, op) {
		return nil, ErrReverseUnavailable
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrReverseUnavailable
	}
	c.nextRev++
	id := "s" + strconv.FormatUint(c.nextRev, 10)
	ch := make(chan contract.Frame, 1)
	c.reverse[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.reverse, id)
		c.mu.Unlock()
	}()
	f, err := contract.Request(id, op, params)
	if err != nil {
		return nil, err
	}
	if err := c.write(f); err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		if resp.OK != nil && *resp.OK {
			return resp.Result, nil
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return nil, errors.New("reverse call failed")
	case <-ctx.Done():
		_ = c.write(contract.Frame{Type: contract.FrameCancel, ID: id})
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, ErrReverseUnavailable
	}
}

// Close ends the connection.
func (c *Conn) Close() { c.cancel() }

// Serve runs one connection: hello, then requests until the stream ends.
// The connector has already authenticated the device as p.
func (s *Service) Serve(ctx context.Context, p Principal, stream FrameStream) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer stream.Close()
	stop := context.AfterFunc(ctx, func() { _ = stream.Close() })
	defer stop()
	hctx, hcancel := context.WithTimeout(ctx, 30*time.Second)
	raw, err := stream.ReadFrame(hctx)
	hcancel()
	if err != nil {
		return err
	}
	first, err := contract.Decode(raw)
	if err != nil || first.Type != contract.FrameHello || first.Hello == nil {
		f := contract.Failure("hello", contract.Errorf(contract.CodeInvalidRequest, "a connection starts with hello"))
		if b, e := contract.Encode(f); e == nil {
			_ = stream.WriteFrame(ctx, b)
		}
		return errors.New("link: connection did not start with hello")
	}
	h := first.Hello
	if !slices.Contains(h.Versions, contract.Version) {
		f := contract.Failure(first.ID, contract.Errorf(contract.CodeUnsupportedCapability, "this backend speaks %s", contract.Version))
		if b, e := contract.Encode(f); e == nil {
			_ = stream.WriteFrame(ctx, b)
		}
		return errors.New("link: no common version")
	}
	if h.Device != nil {
		if h.Device.Class != "" && h.Device.Class != p.Class {
			f := contract.Failure(first.ID, contract.Errorf(contract.CodeUnauthorized, "device class %s does not match its console record (%s)", h.Device.Class, p.Class))
			if b, e := contract.Encode(f); e == nil {
				_ = stream.WriteFrame(ctx, b)
			}
			return errors.New("link: device class mismatch")
		}
		p.Conditions = h.Device.Conditions
		if p.Name == "" {
			p.Name = h.Device.Name
		}
	}
	// The class bounds what a device may claim: only a desktop has the
	// built-in browser surface; a watch shows file names only.
	switch p.Class {
	case contract.ClassPhone:
		p.Conditions.BrowserSurface = false
	case contract.ClassWatch:
		p.Conditions.BrowserSurface = false
		p.Conditions.Attachments = false
	}
	p.Profile = h.Profile
	if p.Profile != contract.ProfilePull {
		p.Profile = contract.ProfileDuplex
	}
	conn := &Conn{s: s, stream: stream, Principal: p, ctx: ctx, cancel: cancel, inflight: map[string]context.CancelFunc{}, reverse: map[string]chan contract.Frame{}}
	for _, op := range h.Reverse {
		if contract.ReverseAllowed(op, p.Conditions) {
			conn.Reverse = append(conn.Reverse, op)
		}
	}
	pr := p.Principal
	hello := contract.Frame{Type: contract.FrameHello, ID: first.ID, Hello: &contract.Hello{Version: contract.Version, Profile: p.Profile, Features: s.Features(p.Conditions, p.Profile), Principal: &pr, ServerTime: time.Now().UTC().Format(time.RFC3339), Reverse: conn.Reverse}}
	if err := conn.write(hello); err != nil {
		return err
	}
	s.mu.Lock()
	if s.conns[p.DeviceID] == nil {
		s.conns[p.DeviceID] = map[*Conn]struct{}{}
	}
	s.conns[p.DeviceID][conn] = struct{}{}
	opens := append([]func(*Conn){}, s.onOpen...)
	closes := append([]func(*Conn){}, s.onClose...)
	s.mu.Unlock()
	for _, fn := range opens {
		fn(conn)
	}
	defer func() {
		conn.mu.Lock()
		conn.closed = true
		closers := conn.closers
		conn.closers = nil
		for _, c := range conn.inflight {
			c()
		}
		conn.mu.Unlock()
		for _, fn := range closers {
			fn()
		}
		for _, fn := range closes {
			fn(conn)
		}
		s.mu.Lock()
		delete(s.conns[p.DeviceID], conn)
		if len(s.conns[p.DeviceID]) == 0 {
			delete(s.conns, p.DeviceID)
		}
		s.mu.Unlock()
	}()
	limit := s.MaxConcurrent
	if limit <= 0 {
		limit = 16
	}
	sem := make(chan struct{}, limit)
	// Requests beyond the window wait in a bounded backlog.
	backlog := limit * 8
	var wg sync.WaitGroup
	defer wg.Wait()
	for ctx.Err() == nil {
		raw, err := stream.ReadFrame(ctx)
		if err != nil {
			cancel()
			return err
		}
		f, err := contract.Decode(raw)
		if err != nil {
			if f.ID != "" && f.Type == contract.FrameRequest {
				_ = conn.write(contract.Failure(f.ID, contract.AsError(err)))
			}
			continue
		}
		switch f.Type {
		case contract.FrameRequest:
			hd, cerr := s.check(conn.Principal, f.Op)
			if cerr != nil {
				_ = conn.write(contract.Failure(f.ID, cerr))
				continue
			}
			rctx, rcancel := context.WithCancel(ctx)
			conn.mu.Lock()
			if _, dup := conn.inflight[f.ID]; dup {
				conn.mu.Unlock()
				rcancel()
				_ = conn.write(contract.Failure(f.ID, contract.Errorf(contract.CodeConflict, "request id %s is already in flight", f.ID)))
				continue
			}
			if len(conn.inflight) >= limit+backlog {
				// The window and its backlog are full: refuse at once so the
				// read loop keeps serving cancels and reverse replies.
				conn.mu.Unlock()
				rcancel()
				_ = conn.write(contract.Failure(f.ID, &contract.Error{Code: contract.CodeBusy, Message: "too many requests in flight", Retryable: true, NotSent: true}))
				continue
			}
			conn.inflight[f.ID] = rcancel
			conn.mu.Unlock()
			wg.Add(1)
			go func(f contract.Frame) {
				defer wg.Done()
				defer func() {
					conn.mu.Lock()
					delete(conn.inflight, f.ID)
					conn.mu.Unlock()
					rcancel()
				}()
				// A request waits for a slot without holding the read loop;
				// one cancelled while waiting never runs.
				select {
				case sem <- struct{}{}:
				case <-rctx.Done():
					_ = conn.write(contract.Failure(f.ID, &contract.Error{Code: contract.CodeCancelled, Message: "request cancelled", NotSent: true}))
					return
				}
				defer func() { <-sem }()
				resp := s.invoke(rctx, hd, &Request{Principal: conn.Principal, Op: f.Op, Params: f.Params, Conn: conn}, f.ID)
				conn.mu.Lock()
				delete(conn.inflight, f.ID)
				conn.mu.Unlock()
				_ = conn.write(resp)
			}(f)
		case contract.FrameCancel:
			conn.mu.Lock()
			if c := conn.inflight[f.ID]; c != nil {
				c()
			}
			conn.mu.Unlock()
		case contract.FrameResponse:
			conn.mu.Lock()
			ch := conn.reverse[f.ID]
			conn.mu.Unlock()
			if ch != nil {
				select {
				case ch <- f:
				default:
				}
			}
		case contract.FrameHello:
			// A repeated hello (a client that lost the first reply) gets the
			// same answer again; the connection state is unchanged.
			again := hello
			again.ID = f.ID
			_ = conn.write(again)
		case contract.FrameEvent:
			// Devices do not push events.
		}
	}
	return ctx.Err()
}

// Revoke ends every connection of a device after telling it why.
func (s *Service) Revoke(deviceID string) {
	s.mu.RLock()
	var conns []*Conn
	for c := range s.conns[deviceID] {
		conns = append(conns, c)
	}
	s.mu.RUnlock()
	for _, c := range conns {
		_ = c.Emit("", contract.TopicDevice, map[string]string{"state": "revoked"})
		c.Close()
	}
}

// Connections returns the open connections of a device (all devices when
// deviceID is empty).
func (s *Service) Connections(deviceID string) []*Conn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Conn
	for id, set := range s.conns {
		if deviceID != "" && id != deviceID {
			continue
		}
		for c := range set {
			out = append(out, c)
		}
	}
	return out
}

// Broadcast emits an event on every open duplex connection of the owner.
func (s *Service) Broadcast(ownerID, topic string, data any) {
	for _, c := range s.Connections("") {
		if c.Principal.OwnerID != ownerID || c.Principal.Profile == contract.ProfilePull {
			continue
		}
		_ = c.Emit("", topic, data)
	}
}

// DecodeParams is a convenience for handlers.
func DecodeParams[T any](req *Request) (T, error) {
	var v T
	if err := contract.DecodeParams(req.Params, &v); err != nil {
		return v, err
	}
	return v, nil
}

// Errorf builds a contract error.
func Errorf(code, format string, args ...any) error {
	return &contract.Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
