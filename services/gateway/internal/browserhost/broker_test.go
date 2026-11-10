package browserhost

import (
	"path/filepath"
	"testing"
)

var testIdentity = Identity{OwnerID: "owner", ClientID: "client", InstallationID: "installation"}

func newTestBroker(t *testing.T) *Broker {
	t.Helper()
	b, err := NewBroker(filepath.Join(t.TempDir(), "control"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}
func TestBackendControlsFailClosed(t *testing.T) {
	bad := []struct {
		op   string
		args map[string]any
	}{{"fill", map[string]any{"ref": "e1", "snapshot_id": "s", "value": 13}}, {"click", map[string]any{"ref": "e1"}}, {"wait", map[string]any{"milliseconds": 6000}}, {"navigate", map[string]any{"url": "https://user:secret@example.test"}}}
	for _, input := range bad {
		if validateOperation(input.op, input.args) == nil {
			t.Fatal(input)
		}
	}
}
