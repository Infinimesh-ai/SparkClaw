package websearch

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestInfinimeshInfoLiveSmoke(t *testing.T) {
	if !liveSmokeEnabled(os.Getenv("SPARKCLAW_INFINIMESH_INFO_LIVE_SMOKE")) {
		t.Skip("Infinimesh Info live smoke is not enabled")
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	info := cfg.Plugins.Entries.InfinimeshInfo.Config
	if !info.Configured() {
		t.Skip("Infinimesh Info live smoke credentials are not configured")
	}
	info.TokenBatchSize = 2
	info.MaxAttempts = 2
	info.MaxSources = 3
	info.RequestTimeoutSeconds = 30
	adapter, err := NewInfinimeshInfoAdapter(info, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	result, err := adapter.Search(ctx, Request{
		Query:      "SparkClaw official public information",
		MaxResults: 3,
		Freshness:  "medium",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != InfoResultSchemaVersion || result.Provider != "infinimesh-info" || !result.Untrusted || strings.TrimSpace(result.Aggregate.Summary) == "" {
		t.Fatal("Infinimesh Info live smoke returned no mapped summary")
	}
}

func liveSmokeEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
