package modelrouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
)

func TestModelCallsMarkServiceOutagesUnavailable(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		unavailable bool
	}{
		{"server error", http.StatusServiceUnavailable, true},
		{"overloaded", http.StatusTooManyRequests, true},
		{"refused credentials", http.StatusUnauthorized, true},
		{"bad request", http.StatusBadRequest, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "failure", test.status)
			}))
			defer server.Close()
			cfg := configtest.MustLoadDefault()
			cfg.Model.Mock = false
			cfg.Model.Fast.BaseURL, cfg.Model.Deep.BaseURL, cfg.Model.Embedding.BaseURL = server.URL, server.URL, server.URL
			router := New(cfg)
			_, chatErr := router.Chat(t.Context(), Task{Operation: modelcapacity.OperationConversationAnswer, Risk: app.RiskRead}, "system", "hello")
			_, embedErr := router.Embed(t.Context(), modelcapacity.OperationIntentQueryEmbedding, []string{"hello"})
			for name, err := range map[string]error{"chat": chatErr, "embedding": embedErr} {
				if err == nil || IsUnavailable(err) != test.unavailable {
					t.Fatalf("%s HTTP %d: err=%v unavailable=%v, want %v", name, test.status, err, IsUnavailable(err), test.unavailable)
				}
			}
		})
	}

	t.Run("unreachable", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		url := server.URL
		server.Close()
		cfg := configtest.MustLoadDefault()
		cfg.Model.Mock = false
		cfg.Model.Fast.BaseURL, cfg.Model.Deep.BaseURL = url, url
		_, err := New(cfg).Chat(t.Context(), Task{Operation: modelcapacity.OperationConversationAnswer, Risk: app.RiskRead}, "system", "hello")
		if !IsUnavailable(err) {
			t.Fatalf("unreachable endpoint err=%v is not marked unavailable", err)
		}
	})

	t.Run("caller cancelled", func(t *testing.T) {
		if IsUnavailable(transportUnavailable(context.Canceled)) {
			t.Fatal("a caller's own cancellation is not a model outage")
		}
	})
}
