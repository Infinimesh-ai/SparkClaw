// Command link-helper is the desktop's LinkClient process. It speaks
// newline-delimited JSON on stdin/stdout with the Electron main process:
//
//	→ {"id":"1","method":"status"}
//	→ {"id":"2","method":"join","credential":"…","name":"MacBook"}
//	→ {"id":"3","method":"open","describe":{"class":"desktop","conditions":{…}},"reverse":["browser.controller.status",…]}
//	→ {"id":"4","method":"request","op":"sync.changes","params":{…},"timeout_ms":30000}
//	→ {"method":"reverse_response","reverse_id":"s1","result":{…}} | {"…","error":{…}}
//	→ {"id":"5","method":"close"} / "leave" / "forget"
//	← {"id":"4","result":…} | {"id":"4","error":{"code","message","retryable"}}
//	← {"event":"state","path":"lan|iscp|","hello":{…}}
//	← {"event":"frame","frame":{…}}           (pushed events)
//	← {"event":"reverse","reverse_id":"s1","op":"…","params":{…}}
//
// Device state lives in -state DIR (private files).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/devicestate"
	"github.com/Chiiz0/SparkClaw/services/link/linkclient"
)

const ipcVersion = 1

type inbound struct {
	ID         string          `json:"id"`
	Method     string          `json:"method"`
	Credential string          `json:"credential,omitempty"`
	Name       string          `json:"name,omitempty"`
	Describe   json.RawMessage `json:"describe,omitempty"`
	Reverse    []string        `json:"reverse,omitempty"`
	Op         string          `json:"op,omitempty"`
	Params     json.RawMessage `json:"params,omitempty"`
	TimeoutMS  int64           `json:"timeout_ms,omitempty"`
	ReverseID  string          `json:"reverse_id,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      *contract.Error `json:"error,omitempty"`
	Profile    string          `json:"profile,omitempty"`
	Console    string          `json:"console,omitempty"`
	Pin        string          `json:"pin,omitempty"`
}

type helper struct {
	dir string
	out *json.Encoder
	mu  sync.Mutex

	cmu     sync.Mutex
	client  *linkclient.Client
	cancel  context.CancelFunc
	reverse map[string]chan inbound
	nextRev uint64
}

func (h *helper) send(v map[string]any) {
	v["ipc_version"] = ipcVersion
	h.mu.Lock()
	defer h.mu.Unlock()
	_ = h.out.Encode(v)
}

func (h *helper) reply(id string, result any, err error) {
	if err != nil {
		cerr := contract.AsError(err)
		var con *consoleapi.Error
		if errors.As(err, &con) {
			cerr = &contract.Error{Code: con.Code, Message: con.Message}
		}
		h.send(map[string]any{"id": id, "error": cerr})
		return
	}
	h.send(map[string]any{"id": id, "result": result})
}

func main() {
	dir := flag.String("state", "", "private device state directory")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "-state is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	h := &helper{dir: *dir, out: json.NewEncoder(os.Stdout), reverse: map[string]chan inbound{}}
	h.send(map[string]any{"event": "hello", "version": contract.Version})
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 64*1024), contract.MaxFrameBytes+64*1024)
	var wg sync.WaitGroup
	for in.Scan() {
		var msg inbound
		if err := json.Unmarshal(in.Bytes(), &msg); err != nil {
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); h.handle(msg) }()
	}
	// stdin closed: the main process is gone. Finish replies already in
	// flight, then stop.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	h.closeClient()
}

func (h *helper) handle(msg inbound) {
	ctx := context.Background()
	timeout := 60 * time.Second
	if msg.TimeoutMS > 0 {
		timeout = time.Duration(msg.TimeoutMS) * time.Millisecond
	}
	switch msg.Method {
	case "status":
		out := devicestate.Summary(h.dir)
		h.cmu.Lock()
		if h.client != nil {
			out["path"] = h.client.Path()
			out["hello"] = h.client.Hello()
		}
		h.cmu.Unlock()
		h.reply(msg.ID, out, nil)
	case "default_console":
		devicestate.SetDefaultConsole(msg.Console, msg.Pin)
		h.reply(msg.ID, map[string]bool{"ok": true}, nil)
	case "join":
		jctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		name := msg.Name
		if name == "" {
			name, _ = os.Hostname()
		}
		_, err := devicestate.Join(jctx, h.dir, msg.Credential, name, contract.ClassDesktop, "desktop")
		if err != nil {
			h.reply(msg.ID, nil, err)
			return
		}
		h.reply(msg.ID, devicestate.Summary(h.dir), nil)
	case "open":
		h.reply(msg.ID, map[string]bool{"ok": true}, h.open(msg))
	case "request":
		h.cmu.Lock()
		c := h.client
		h.cmu.Unlock()
		if c == nil {
			h.reply(msg.ID, nil, linkclient.ErrBackendUnreachable)
			return
		}
		rctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var params any
		if len(msg.Params) > 0 {
			params = msg.Params
		}
		var out json.RawMessage
		err := c.Request(rctx, msg.Op, params, &out)
		if len(out) == 0 {
			out = json.RawMessage("null")
		}
		h.reply(msg.ID, out, err)
	case "reverse_response":
		h.cmu.Lock()
		ch := h.reverse[msg.ReverseID]
		h.cmu.Unlock()
		if ch != nil {
			ch <- msg
		}
	case "close":
		h.closeClient()
		h.reply(msg.ID, map[string]bool{"ok": true}, nil)
	case "leave":
		h.closeClient()
		lctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		h.reply(msg.ID, map[string]bool{"ok": true}, devicestate.Leave(lctx, h.dir))
	case "forget":
		h.closeClient()
		h.reply(msg.ID, map[string]bool{"ok": true}, devicestate.Forget(h.dir))
	default:
		h.reply(msg.ID, nil, &contract.Error{Code: contract.CodeInvalidRequest, Message: "unknown method " + msg.Method})
	}
}

func (h *helper) closeClient() {
	h.cmu.Lock()
	c, cancel := h.client, h.cancel
	h.client, h.cancel = nil, nil
	h.cmu.Unlock()
	if cancel != nil {
		cancel()
	}
	if c != nil {
		c.Close()
	}
}

func (h *helper) open(msg inbound) error {
	h.closeClient()
	var describe contract.Device
	if len(msg.Describe) > 0 {
		if err := json.Unmarshal(msg.Describe, &describe); err != nil {
			return err
		}
	}
	describe.Class = contract.ClassDesktop
	ctx, cancel := context.WithCancel(context.Background())
	opts, err := devicestate.Options(ctx, h.dir, describe)
	if err != nil {
		cancel()
		return err
	}
	opts.Profile = contract.ProfileDuplex
	opts.Reverse = msg.Reverse
	opts.OnState = func(path string, hello *contract.Hello) {
		h.send(map[string]any{"event": "state", "path": path, "hello": hello})
	}
	opts.OnEvent = func(f contract.Frame) {
		h.send(map[string]any{"event": "frame", "frame": f})
	}
	opts.OnReverse = func(rctx context.Context, op string, params json.RawMessage) (any, error) {
		h.cmu.Lock()
		h.nextRev++
		id := "s" + strconv.FormatUint(h.nextRev, 10)
		ch := make(chan inbound, 1)
		h.reverse[id] = ch
		h.cmu.Unlock()
		defer func() {
			h.cmu.Lock()
			delete(h.reverse, id)
			h.cmu.Unlock()
		}()
		h.send(map[string]any{"event": "reverse", "reverse_id": id, "op": op, "params": params})
		select {
		case resp := <-ch:
			if resp.Error != nil {
				return nil, resp.Error
			}
			return resp.Result, nil
		case <-rctx.Done():
			return nil, rctx.Err()
		}
	}
	c, err := linkclient.New(opts)
	if err != nil {
		cancel()
		return err
	}
	c.Start(ctx)
	h.cmu.Lock()
	h.client, h.cancel = c, cancel
	h.cmu.Unlock()
	return nil
}
