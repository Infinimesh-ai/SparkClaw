package toolhub

import (
	"strings"
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
	for _, capability := range []string{
		app.ToolCapabilityWebDiscovery,
		app.ToolCapabilityInfoQuestion,
		app.ToolCapabilityWeatherStructure,
		app.ToolCapabilityWeatherRender,
		app.ToolCapabilityBrowserListTabs,
		app.ToolCapabilityBrowserFocus,
		app.ToolCapabilityBrowserOpen,
		app.ToolCapabilityBrowserClose,
		app.ToolCapabilityBrowserHealth,
		app.ToolCapabilityBrowserNavigate,
		app.ToolCapabilityBrowserSnapshot,
		app.ToolCapabilityBrowserWait,
		app.ToolCapabilityBrowserClick,
		app.ToolCapabilityBrowserTransitionValidate,
		app.ToolCapabilityBrowserGoalAssess,
		app.ToolCapabilityDocumentRead,
		app.ToolCapabilityDocumentEdit,
		app.ToolCapabilityScheduleManage,
	} {
		if capabilityCounts[capability] == 0 {
			t.Fatalf("workflow capability %q has no registered tools: %#v", capability, capabilityCounts)
		}
	}
	weather, ok := hub.Definition("media.render_weather_card")
	if !ok || weather.OutcomeAdapter != app.OutcomeAdapterWeatherCard || len(weather.Directory.OutputKinds) != 1 || weather.Directory.OutputKinds[0] != app.OutputKindImage {
		t.Fatalf("weather card registration is outside its typed workflow boundary: %#v", weather)
	}
	imageInspect, ok := hub.Definition("images.inspect")
	if !ok || imageInspect.OutcomeAdapter != app.OutcomeAdapterWorkspaceRead || len(imageInspect.Capabilities) != 1 ||
		imageInspect.Capabilities[0].Name != app.ToolCapabilityDocumentRead ||
		imageInspect.Capabilities[0].Qualifiers[app.CapabilityQualifierFormat] != app.DocumentFormatImage {
		t.Fatalf("image inspection is outside the document.read format boundary: %#v", imageInspect)
	}
	deleteDefinition, ok := hub.Definition("file.delete")
	if !ok || len(deleteDefinition.Capabilities) != 1 || deleteDefinition.Capabilities[0].Name == app.ToolCapabilityDocumentEdit {
		t.Fatalf("file.delete entered document.edit r1: %#v", deleteDefinition)
	}
	for _, name := range []string{"browser.read", "browser.type", "browser.select"} {
		definition, ok := hub.Definition(name)
		if !ok {
			t.Fatalf("legacy tool %q is unavailable", name)
		}
		for _, descriptor := range definition.Capabilities {
			if descriptor.Name == app.ToolCapabilityWebDiscovery || descriptor.Name == app.ToolCapabilityBrowserListTabs ||
				descriptor.Name == app.ToolCapabilityBrowserFocus || descriptor.Name == app.ToolCapabilityBrowserOpen {
				t.Fatalf("legacy tool %q entered a current browser r1 scope: %#v", name, definition.Capabilities)
			}
		}
	}
}

func TestDOCXParagraphDirectoryMetadataDistinguishesRevisionFromInsertion(t *testing.T) {
	replace := toolRegistry["docx.replace_paragraph"].directory
	insert := toolRegistry["docx.insert_paragraph"].directory

	if !strings.Contains(replace.WhenToUse, "existing paragraph") ||
		!strings.Contains(replace.WhenToUse, "improve") ||
		!strings.Contains(replace.WhenNotToUse, "new paragraph") {
		t.Fatalf("DOCX replacement metadata does not describe existing-content revision: %#v", replace)
	}
	if !strings.Contains(insert.WhenToUse, "explicitly requests") ||
		!strings.Contains(insert.WhenToUse, "new paragraph") ||
		!strings.Contains(insert.WhenNotToUse, "existing paragraph") {
		t.Fatalf("DOCX insertion metadata does not require an additive request: %#v", insert)
	}
}

func TestReminderToolsExposeDiscoveryBeforeScheduleMutation(t *testing.T) {
	want := map[string]app.RouteOperation{
		"reminders.create": app.RouteOperationCreate,
		"reminders.list":   app.RouteOperationRead,
		"reminders.update": app.RouteOperationEdit,
		"reminders.cancel": app.RouteOperationDelete,
	}
	for name, operation := range want {
		registration := toolRegistry[name]
		if name == "reminders.list" {
			if len(registration.capabilities) != 3 {
				t.Fatalf("schedule discovery must serve read, edit, and delete stages: %#v", registration.capabilities)
			}
			for _, candidate := range []app.RouteOperation{app.RouteOperationRead, app.RouteOperationEdit, app.RouteOperationDelete} {
				found := false
				for _, descriptor := range registration.capabilities {
					if descriptor.Qualifiers[app.CapabilityQualifierOperation] == string(candidate) {
						found = true
					}
				}
				if !found {
					t.Fatalf("reminders.list is missing %s discovery capability: %#v", candidate, registration.capabilities)
				}
			}
			continue
		}
		if len(registration.capabilities) != 1 || registration.capabilities[0].Name != app.ToolCapabilityScheduleManage ||
			registration.capabilities[0].Qualifiers[app.CapabilityQualifierOperation] != string(operation) {
			t.Fatalf("%s is not isolated to schedule %s: %#v", name, operation, registration.capabilities)
		}
	}
}
