package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type HTTPRunner struct {
	baseURL string
	client  *http.Client
}

func NewHTTPRunner(baseURL string) HTTPRunner {
	return HTTPRunner{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 35 * time.Second},
	}
}

func (r HTTPRunner) Run(ctx context.Context, req Request) (Result, error) {
	req = normalizeRequest(req)
	if r.baseURL == "" {
		return Result{}, errors.New("sandbox runner URL is empty")
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return Result{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/run", bytes.NewReader(raw))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(httpReq)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	var decoded struct {
		Result Result `json:"result"`
		Error  string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Result{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if decoded.Error == "" {
			decoded.Error = fmt.Sprintf("sandbox runner returned HTTP %d", resp.StatusCode)
		}
		return decoded.Result, errors.New(decoded.Error)
	}
	return decoded.Result, nil
}

func Handler(runner Runner) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "sandbox-runner"})
	})
	mux.HandleFunc("POST /run", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		result, err := runner.Run(r.Context(), req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"result": result, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": result})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
