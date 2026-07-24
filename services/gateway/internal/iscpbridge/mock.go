package iscpbridge

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

const mockRequestLimit = 1 << 20

func NewMockHandler(gateway *GatewayClient, clientToken string) (http.Handler, error) {
	if gateway == nil {
		return nil, errors.New("Gateway client is required")
	}
	clientToken = strings.TrimSpace(clientToken)
	if clientToken == "" {
		return nil, errors.New("mock Bridge client token is required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if !mockLocalRequest(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		writeMockJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "sparkclaw-iscp-bridge-mock"})
	})
	mux.HandleFunc("POST /v1/requests", func(w http.ResponseWriter, r *http.Request) {
		if !mockLocalRequest(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if len(got) != len(clientToken) || subtle.ConstantTimeCompare([]byte(got), []byte(clientToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, mockRequestLimit)
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request Request
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		response, err := gateway.Dispatch(r.Context(), request)
		if err != nil && response.Type == "" {
			http.Error(w, "Gateway unavailable", http.StatusBadGateway)
			return
		}
		writeMockJSON(w, HTTPStatus(response), response)
	})
	return mux, nil
}

func ValidateMockListenAddress(address string) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return errors.New("mock listen address must include a port")
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("mock Bridge must listen on loopback")
	}
	return nil
}

func mockLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]")
	}
	ip := net.ParseIP(host)
	return host == "" || host == "localhost" || (ip != nil && ip.IsLoopback())
}

func writeMockJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
