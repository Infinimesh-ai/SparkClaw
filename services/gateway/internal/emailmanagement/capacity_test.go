package emailmanagement

import (
	"errors"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestCapacityStateThresholds(t *testing.T) {
	const gib = int64(1) << 30
	for name, expect := range map[string]struct {
		total, free int64
		state       string
		used        int
	}{
		"plenty of room":          {100 * gib, 50 * gib, "ok", 50},
		"just under the warning":  {100 * gib, 16 * gib, "ok", 84},
		"crosses used percentage": {100 * gib, 14 * gib, "warning", 86},
		"crosses critical":        {100 * gib, 4 * gib, "critical", 96},
		// A large volume can be proportionally fine but absolutely nearly full.
		"small absolute headroom": {10000 * gib, gib, "critical", 99},
		"warn on absolute free":   {1000 * gib, 3 * gib / 2, "critical", 99},
		"unusable readings":       {0, 0, "unknown", 0},
		"free exceeds total":      {gib, 2 * gib, "unknown", 0},
	} {
		t.Run(name, func(t *testing.T) {
			got := capacityState(expect.total, expect.free)
			if got.State != expect.state || got.UsedPercent != expect.used {
				t.Fatalf("state=%s used=%d want state=%s used=%d", got.State, got.UsedPercent, expect.state, expect.used)
			}
		})
	}
}

// A probe failure must degrade to "unknown", never fail the status projection.
func TestCapacityProbeFailureIsNonFatal(t *testing.T) {
	s, _, _ := newFixtureService(t, store.NewMemoryStore())
	original := statfs
	t.Cleanup(func() { statfs = original })
	statfs = func(string) (int64, int64, error) { return 0, 0, errProbe }
	if got := s.capacity(); got.State != "unknown" {
		t.Fatalf("state=%s", got.State)
	}
	view, err := s.Status(t.Context(), "email-owner")
	if err != nil {
		t.Fatal(err)
	}
	if view.Capacity != nil {
		t.Fatalf("unknown capacity must be omitted: %+v", view.Capacity)
	}
}

var errProbe = errors.New("probe unavailable")
