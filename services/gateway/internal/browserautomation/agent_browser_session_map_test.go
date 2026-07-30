package browserautomation

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func sessionMapAdapterFixture(t *testing.T) *AgentBrowserAdapter {
	t.Helper()
	adapter, ok := NewAdapter(config.Config{}).(*AgentBrowserAdapter)
	if !ok {
		t.Fatal("NewAdapter did not return an AgentBrowserAdapter")
	}
	return adapter
}

func hiddenSessionKey(owner string) agentBrowserSessionKey {
	return agentBrowserSessionKey{profile: owner + "\x00default", presentation: "hidden"}
}

func TestResolveSessionEntryKeepsDistinctProfilesWarm(t *testing.T) {
	adapter := sessionMapAdapterFixture(t)
	first, victims := adapter.resolveSessionEntry(hiddenSessionKey("owner-a"))
	if first == nil || len(victims) != 0 {
		t.Fatalf("first resolve = %#v victims=%d", first, len(victims))
	}
	second, victims := adapter.resolveSessionEntry(hiddenSessionKey("owner-b"))
	if second == nil || second == first || len(victims) != 0 {
		t.Fatalf("alternating owner evicted a warm session: victims=%d", len(victims))
	}
	again, victims := adapter.resolveSessionEntry(hiddenSessionKey("owner-a"))
	if again != first || len(victims) != 0 || len(adapter.entries) != 2 {
		t.Fatalf("returning owner did not reuse its warm entry: same=%t victims=%d entries=%d",
			again == first, len(victims), len(adapter.entries))
	}
}

func TestResolveSessionEntryEvictsSameProfileOtherPresentation(t *testing.T) {
	adapter := sessionMapAdapterFixture(t)
	hiddenKey := agentBrowserSessionKey{profile: "owner-a\x00default", presentation: "hidden"}
	visibleKey := agentBrowserSessionKey{profile: "owner-a\x00default", presentation: "visible"}
	hidden, _ := adapter.resolveSessionEntry(hiddenKey)
	visible, victims := adapter.resolveSessionEntry(visibleKey)
	if len(victims) != 1 || victims[0] != hidden {
		t.Fatalf("presentation switch must detach the profile-flock holder: victims=%#v", victims)
	}
	if len(adapter.entries) != 1 || adapter.entries[visibleKey] != visible {
		t.Fatalf("entry map after presentation switch: %#v", adapter.entries)
	}
}

func TestResolveSessionEntryEvictsEntriesIdleBeyondDaemonBound(t *testing.T) {
	adapter := sessionMapAdapterFixture(t)
	idleKey := hiddenSessionKey("owner-idle")
	idle, _ := adapter.resolveSessionEntry(idleKey)
	adapter.entries[idleKey].lastUsed = time.Now().Add(-time.Duration(config.DefaultBrowserDaemonIdleTimeoutMS+60000) * time.Millisecond)
	_, victims := adapter.resolveSessionEntry(hiddenSessionKey("owner-active"))
	if len(victims) != 1 || victims[0] != idle {
		t.Fatalf("idle entry beyond the daemon bound was not evicted: victims=%#v", victims)
	}
}

func TestResolveSessionEntryVisibleIdleBoundIsGenerous(t *testing.T) {
	adapter := sessionMapAdapterFixture(t)
	visibleKey := agentBrowserSessionKey{profile: "owner-a\x00default", presentation: "visible"}
	if _, victims := adapter.resolveSessionEntry(visibleKey); len(victims) != 0 {
		t.Fatalf("unexpected victims: %#v", victims)
	}
	// Idle past the hidden bound but well inside the scaled visible bound.
	adapter.entries[visibleKey].lastUsed = time.Now().Add(-time.Duration(config.DefaultBrowserDaemonIdleTimeoutMS+60000) * time.Millisecond)
	if _, victims := adapter.resolveSessionEntry(hiddenSessionKey("owner-b")); len(victims) != 0 {
		t.Fatalf("visible session was evicted on the hidden idle bound: %#v", victims)
	}
}

func TestResolveSessionEntryCapsWarmSessionsByLeastRecentUse(t *testing.T) {
	adapter := sessionMapAdapterFixture(t)
	owners := []string{"owner-1", "owner-2", "owner-3", "owner-4"}
	entries := map[string]*agentBrowserSessionEntry{}
	for index, owner := range owners {
		entry, victims := adapter.resolveSessionEntry(hiddenSessionKey(owner))
		if len(victims) != 0 {
			t.Fatalf("victims before exceeding the cache limit: %#v", victims)
		}
		entries[owner] = entry
		adapter.entries[hiddenSessionKey(owner)].lastUsed = time.Now().Add(time.Duration(index-10) * time.Minute)
	}
	_, victims := adapter.resolveSessionEntry(hiddenSessionKey("owner-5"))
	if len(victims) != 1 || victims[0] != entries["owner-1"] {
		t.Fatalf("cache overflow must evict the least recently used entry: victims=%#v", victims)
	}
	if len(adapter.entries) != agentBrowserSessionCacheLimit {
		t.Fatalf("entry map size after overflow = %d, want %d", len(adapter.entries), agentBrowserSessionCacheLimit)
	}
}

func TestCloseSessionEntryToleratesUninitializedEntries(t *testing.T) {
	adapter := sessionMapAdapterFixture(t)
	entry, _ := adapter.resolveSessionEntry(hiddenSessionKey("owner-a"))
	closeSessionEntries([]*agentBrowserSessionEntry{entry})
	if entry.session != nil {
		t.Fatalf("closing an uninitialized entry set a session: %#v", entry.session)
	}
}
