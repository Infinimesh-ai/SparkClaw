package toolhub

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// Every declared tool definition must have an executor, and every executor
// should correspond to a declared definition, so the two tables cannot drift.
func TestToolRegistryMatchesDefinitions(t *testing.T) {
	defs := map[string]bool{}
	for _, def := range defaultDefinitions() {
		defs[def.Name] = true
		if _, ok := toolRegistry[def.Name]; !ok {
			t.Errorf("definition %q has no entry in toolRegistry", def.Name)
		}
	}
	for name := range toolRegistry {
		if !defs[name] {
			t.Errorf("toolRegistry entry %q has no definition in defaultDefinitions", name)
		}
	}
}

func TestMigratedRegistrationsOwnExposureMetadata(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Web.Search.Enabled = true
	cfg.Tools.BrowserAutomation.Enabled = true
	hub := New(cfg, store.NewMemoryStore())

	capabilityCounts := map[string]int{}
	for name, registration := range toolRegistry {
		if len(registration.capabilities) == 0 {
			continue
		}
		def, ok := hub.Definition(name)
		if !ok {
			t.Fatalf("migrated tool %q is not registered", name)
		}
		if len(def.Capabilities) == 0 || def.Capabilities[0].Name == "" {
			t.Fatalf("migrated tool %q has incomplete capabilities: %#v", name, def.Capabilities)
		}
		if def.OutcomeAdapter == "" {
			t.Fatalf("migrated tool %q has no outcome adapter", name)
		}
		if def.Directory.Summary == "" || def.Directory.WhenToUse == "" || len(def.Directory.Effects) == 0 {
			t.Fatalf("migrated tool %q has incomplete directory metadata: %#v", name, def.Directory)
		}
		for _, capability := range def.Capabilities {
			capabilityCounts[capability.Name]++
		}
	}
	for _, capability := range []app.CapabilityID{
		app.CapabilityBrowserSearch,
		app.CapabilityBrowserAutomation,
		app.CapabilityDocumentInformation,
		app.CapabilityDocumentProcessing,
	} {
		if capabilityCounts[string(capability)] == 0 {
			t.Fatalf("workflow capability %q has no registered tools: %#v", capability, capabilityCounts)
		}
	}
	deleteDefinition, ok := hub.Definition("file.delete")
	if !ok || len(deleteDefinition.Capabilities) != 1 || deleteDefinition.Capabilities[0].Name != string(app.CapabilityDocumentProcessing) {
		t.Fatalf("file.delete was not migrated into document.processing: %#v", deleteDefinition)
	}
}
