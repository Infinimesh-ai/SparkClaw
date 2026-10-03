// Isolated native qualification fixture, never a product endpoint.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3browser"
)

func main() {
	if len(os.Args) != 4 {
		panic("usage: broker-fixture private-root certificate key")
	}
	broker, err := r3browser.NewBroker(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer broker.Close()
	identity := r3browser.Identity{OwnerID: "qualification-owner", ClientID: "qualification-client", InstallationID: "qualification-installation"}
	var mu sync.Mutex
	bindings := map[string]r3browser.Binding{}
	mux := http.NewServeMux()
	mux.HandleFunc("/fixture", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>R3 native fixture</title><h1 id="conversation"></h1><button id="increment">Increment</button><div id="counter">Counter: 0</div><label>Name<input id="name"></label><div id="draft"></div><script>document.querySelector('#conversation').textContent='Conversation '+new URL(location.href).searchParams.get('conversation');let n=0;document.querySelector('#increment').onclick=()=>document.querySelector('#counter').textContent='Counter: '+(++n);document.querySelector('#name').oninput=()=>document.querySelector('#draft').textContent='Draft: '+document.querySelector('#name').value;</script>`)
	})
	authorized := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer isolated-r3-native-qualification" {
			http.Error(w, "unauthorized", 401)
			return false
		}
		return true
	}
	mux.HandleFunc("POST /api/r3/hosts/grants", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		if r.Header.Get("X-SparkClaw-Installation") != identity.InstallationID {
			http.Error(w, "installation rejected", 403)
			return
		}
		var input struct{}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil {
			http.Error(w, "strict grant request rejected", 400)
			return
		}
		grant, err := broker.IssueGrant(identity)
		if err != nil {
			http.Error(w, "grant failed", 500)
			return
		}
		json.NewEncoder(w).Encode(grant)
	})
	mux.HandleFunc("GET /api/r3/hosts/connect", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) || r.Header.Get("X-SparkClaw-Installation") != identity.InstallationID {
			return
		}
		broker.ServeHost(r.Context(), w, r, identity)
	})
	mux.HandleFunc("POST /qualify/step", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) {
			return
		}
		var input struct {
			ConversationID string         `json:"conversation_id"`
			TaskID         string         `json:"task_id"`
			Operation      string         `json:"operation"`
			Arguments      map[string]any `json:"arguments"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<10)).Decode(&input) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		mu.Lock()
		binding, ok := bindings[input.ConversationID]
		mu.Unlock()
		var err error
		if !ok {
			binding, err = broker.Acquire(ctx, r3browser.Scope{Identity: identity, ConversationID: input.ConversationID, TaskID: input.TaskID})
			if err == nil {
				mu.Lock()
				bindings[input.ConversationID] = binding
				mu.Unlock()
			}
		}
		var output json.RawMessage
		if err == nil {
			if input.Operation == "acquire" {
				output, _ = json.Marshal(binding)
			} else {
				output, err = broker.Dispatch(ctx, binding, fmt.Sprintf("qualification_%d", time.Now().UnixNano()), input.Operation, input.Arguments)
			}
		}
		if input.Operation == "release" && err == nil {
			mu.Lock()
			delete(bindings, input.ConversationID)
			mu.Unlock()
		}
		if err != nil {
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(output)
	})
	mux.HandleFunc("GET /api/r3/hosts/fences", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r) || r.Header.Get("X-SparkClaw-Installation") != identity.InstallationID {
			http.Error(w, "installation rejected", 403)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"fences": broker.Fences(identity)})
	})
	mux.HandleFunc("GET /qualify/fences", func(w http.ResponseWriter, r *http.Request) {
		if authorized(w, r) {
			json.NewEncoder(w).Encode(broker.Fences(identity))
		}
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"origin": "https://" + listener.Addr().String()})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	if err := server.ServeTLS(listener, os.Args[2], os.Args[3]); err != nil {
		panic(err)
	}
}
