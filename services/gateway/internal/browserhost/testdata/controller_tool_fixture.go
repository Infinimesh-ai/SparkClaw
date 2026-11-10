// Qualification-only JSON bridge: real scoped public tools and Controller
// client, with a local test transport. This is not an ISCP/Relay qualification.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type frame struct {
	Kind         string          `json:"kind"`
	ID           string          `json:"id"`
	Tool         string          `json:"tool,omitempty"`
	Conversation string          `json:"conversation_id,omitempty"`
	Task         string          `json:"task_id,omitempty"`
	Arguments    map[string]any  `json:"arguments,omitempty"`
	Status       int             `json:"status,omitempty"`
	Body         json.RawMessage `json:"body,omitempty"`
}

type bridge struct {
	mu       sync.Mutex
	writer   sync.Mutex
	pending  map[string]chan frame
	sequence atomic.Uint64
	done     chan struct{}
}

var identity = browserhost.Identity{OwnerID: "semantic-owner", ClientID: "semantic-client", InstallationID: "semantic-installation"}

func (b *bridge) write(value any) {
	b.writer.Lock()
	defer b.writer.Unlock()
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		panic(err)
	}
}
func (b *bridge) SessionID() string             { return "semantic_session" }
func (b *bridge) AuthorizationRevision() uint64 { return 1 }
func (b *bridge) Done() <-chan struct{}         { return b.done }
func (b *bridge) Command(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("legacy reverse commands forbidden in Controller fixture")
}
func (b *bridge) ReverseSession(got browserhost.Identity) (browserhost.ReverseSession, error) {
	if got != identity {
		return nil, browserhost.ErrFence
	}
	return b, nil
}
func (b *bridge) ControllerCall(ctx context.Context, operation string, body json.RawMessage) (json.RawMessage, error) {
	id := fmt.Sprintf("reverse_%d", b.sequence.Add(1))
	result := make(chan frame, 1)
	b.mu.Lock()
	b.pending[id] = result
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.pending, id); b.mu.Unlock() }()
	b.write(map[string]any{"kind": "reverse", "id": id, "operation": operation, "body": body})
	select {
	case response := <-result:
		if response.Status != 200 || !json.Valid(response.Body) {
			return nil, fmt.Errorf("local fixture transport response status %d", response.Status)
		}
		return response.Body, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.done:
		return nil, errors.New("local fixture transport closed")
	}
}

func main() {
	if len(os.Args) != 2 || !filepath.IsAbs(os.Args[1]) || !strings.HasPrefix(filepath.Base(filepath.Dir(os.Args[1])), "sx-controller-semantics-") {
		panic("private sx-controller-semantics-* broker directory required")
	}
	broker, err := browserhost.NewBroker(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer broker.Close()
	peer := &bridge{pending: map[string]chan frame{}, done: make(chan struct{})}
	broker.SetReverseRouter(peer)
	broker.EnableClientController(config.Default())
	commands := make(chan frame, 4)
	go func() {
		defer close(commands)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 65536), 4<<20)
		for scanner.Scan() {
			var input frame
			if json.Unmarshal(scanner.Bytes(), &input) != nil || input.ID == "" {
				panic("invalid qualification frame")
			}
			if input.Kind == "reverse_result" {
				peer.mu.Lock()
				pending := peer.pending[input.ID]
				peer.mu.Unlock()
				if pending != nil {
					select {
					case pending <- input:
					default:
					}
				}
			} else {
				commands <- input
			}
		}
		if err := scanner.Err(); err != nil {
			panic(err)
		}
	}()
	adapters := map[string]browserautomation.Adapter{}
	peer.write(map[string]any{"kind": "ready", "scope": "public_tools_local_test_transport_not_ISCP", "identity": identity})
	for input := range commands {
		if input.Kind == "shutdown" {
			for _, adapter := range adapters {
				_ = adapter.Close()
			}
			peer.write(map[string]any{"kind": "result", "id": input.ID, "shutdown": true, "fences": broker.Fences(identity)})
			close(peer.done)
			return
		}
		if input.Kind != "tool" || !strings.HasPrefix(input.Conversation, "semantic-") || !strings.HasPrefix(input.Task, "semantic-") || !strings.HasPrefix(input.Tool, "browser.") {
			panic("invalid public tool qualification input")
		}
		key := input.Conversation + "/" + input.Task
		adapter := adapters[key]
		if adapter == nil {
			adapter = broker.ForScope(browserhost.Scope{Identity: identity, ConversationID: input.Conversation, TaskID: input.Task})
			adapters[key] = adapter
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		var result any
		var err error
		if input.Tool == "browser.status" {
			result, err = adapter.Health(ctx, input.Arguments)
		} else if input.Tool == "browser.read" {
			target, _ := input.Arguments["url"].(string)
			result, err = adapter.ReadPage(ctx, target, input.Arguments)
		} else {
			result, err = adapter.Call(ctx, input.Tool, input.Arguments)
		}
		cancel()
		output := map[string]any{"kind": "result", "id": input.ID, "tool": input.Tool, "result": result}
		if err != nil {
			output["error"] = err.Error()
		}
		peer.write(output)
	}
	close(peer.done)
}
