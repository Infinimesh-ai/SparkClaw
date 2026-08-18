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

func TestRetiredPatchToolIsNotRegistered(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())
	if _, ok := hub.Definition("code.apply_patch"); ok {
		t.Fatal("retired code.apply_patch tool is still registered")
	}
}

func TestWorkspaceDataAccessToolIsRuntimeOnly(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())
	definition, ok := hub.Definition(app.ToolWorkspaceDataAccess)
	if !ok || definition.Risk != app.RiskRead || definition.RequiresApproval || len(definition.Capabilities) != 0 ||
		len(definition.Directory.Effects) != 1 || definition.Directory.Effects[0] != app.ToolEffectWorkspaceRead {
		t.Fatalf("workspace data confirmation is outside its Runtime-only read boundary: %#v", definition)
	}
}

func TestMigratedRegistrationsOwnExposureMetadata(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Web.Search.Enabled = true
	cfg.Tools.BrowserAutomation.Enabled = true
	configureTestInfoCredentials(&cfg)
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
		app.ToolCapabilityWeatherRender,
		app.ToolCapabilityBrowserListTabs,
		app.ToolCapabilityBrowserFocus,
		app.ToolCapabilityBrowserOpen,
		app.ToolCapabilityBrowserClose,
		app.ToolCapabilityBrowserHealth,
		app.ToolCapabilityBrowserNavigate,
		app.ToolCapabilityBrowserSnapshot,
		app.ToolCapabilityBrowserPageRead,
		app.ToolCapabilityBrowserPublicTarget,
		app.ToolCapabilityBrowserVisualInspect,
		app.ToolCapabilityBrowserWait,
		app.ToolCapabilityBrowserClick,
		app.ToolCapabilityBrowserFormType,
		app.ToolCapabilityBrowserFormSelect,
		app.ToolCapabilityBrowserTransitionValidate,
		app.ToolCapabilityBrowserGoalAssess,
		app.ToolCapabilityDocumentRead,
		app.ToolCapabilityDocumentEdit,
		app.ToolCapabilityScheduleManage,
		app.ToolCapabilityObservationRead,
	} {
		if capabilityCounts[capability] == 0 {
			t.Fatalf("workflow capability %q has no registered tools: %#v", capability, capabilityCounts)
		}
	}
	observationRead, ok := hub.Definition("observation.read")
	if !ok || observationRead.Risk != app.RiskRead || observationRead.RequiresApproval || len(observationRead.Capabilities) != 1 ||
		observationRead.Capabilities[0].Name != app.ToolCapabilityObservationRead {
		t.Fatalf("observation.read is outside its read-only capability boundary: %#v", observationRead)
	}
	weather, ok := hub.Definition("media.render_weather_card")
	if !ok || weather.OutcomeAdapter != app.OutcomeAdapterWeatherCard || len(weather.Directory.OutputKinds) != 1 || weather.Directory.OutputKinds[0] != app.OutputKindImage {
		t.Fatalf("weather card registration is outside its typed workflow boundary: %#v", weather)
	}
	lookup, ok := hub.Definition("weather.lookup")
	if !ok || lookup.OutcomeAdapter != app.OutcomeAdapterWeatherPayload || len(lookup.Capabilities) != 1 ||
		lookup.Capabilities[0].Name != app.ToolCapabilityInfoQuestion ||
		lookup.Capabilities[0].Qualifiers[app.CapabilityQualifierProvider] != app.CapabilityProviderInfo {
		t.Fatalf("dedicated weather lookup did not reuse the existing Info capability: %#v", lookup)
	}
	if _, ok := hub.Definition("info.query"); ok {
		t.Fatal("legacy direct Info query remains registered")
	}
	publicTarget, ok := hub.Definition("browser.identify_public_target")
	if !ok || publicTarget.RequiresApproval || publicTarget.Risk != app.RiskRead ||
		len(publicTarget.Capabilities) != 1 || publicTarget.Capabilities[0].Name != app.ToolCapabilityBrowserPublicTarget ||
		publicTarget.OutcomeAdapter != app.OutcomeAdapterBrowserPublicTarget {
		t.Fatalf("public target identifier is outside its read-only Workflow boundary: %#v", publicTarget)
	}
	if err := hub.Validate("browser.identify_public_target", map[string]any{}); err != nil {
		t.Fatalf("public target identifier rejected its no-model-argument schema: %v", err)
	}
	visual, ok := hub.Definition("browser.visual_inspect")
	if !ok || visual.RequiresApproval || visual.Risk != app.RiskRead || len(visual.Capabilities) != 1 ||
		visual.Capabilities[0].Name != app.ToolCapabilityBrowserVisualInspect || visual.OutcomeAdapter != app.OutcomeAdapterBrowserVisual {
		t.Fatalf("visual inspection is outside its generation-bound read scope: %#v", visual)
	}
	pageRead, ok := hub.Definition("browser.read")
	if !ok || !definitionHasCapability(pageRead, app.ToolCapabilityBrowserPageRead) {
		t.Fatalf("browser.read did not enter the page-read capability boundary: %#v", pageRead)
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

func definitionHasCapability(definition app.ToolDefinition, name string) bool {
	for _, descriptor := range definition.Capabilities {
		if descriptor.Name == name {
			return true
		}
	}
	return false
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

func TestXLSXDirectoryMetadataDistinguishesAllSixOperations(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())
	defer hub.Close()
	tests := []struct {
		tool       string
		operation  string
		wantUse    string
		wantReject string
	}{
		{tool: "office.replace_text", operation: "replace_text", wantUse: "explicit old and new text", wantReject: "values are typed"},
		{tool: "xlsx.update_cell", operation: "update_cell", wantUse: "exactly one evidence-located cell", wantReject: "multiple cells"},
		{tool: "xlsx.update_row", operation: "update_row", wantUse: "multiple leading cells", wantReject: "update only one explicit cell"},
		{tool: "xlsx.insert_row", operation: "insert_row", wantUse: "before or after", wantReject: "end-of-sheet append"},
		{tool: "xlsx.append_row", operation: "append_row", wantUse: "final structured row", wantReject: "before or after row anchor"},
		{tool: "xlsx.delete_row", operation: "delete_row", wantUse: "complete evidence-bound row", wantReject: "clear one cell"},
	}
	for _, test := range tests {
		t.Run(test.operation, func(t *testing.T) {
			definition, ok := hub.Definition(test.tool)
			if !ok {
				t.Fatalf("XLSX editor %s is not registered", test.tool)
			}
			if !strings.Contains(definition.Directory.WhenToUse, test.wantUse) || !strings.Contains(definition.Directory.WhenNotToUse, test.wantReject) {
				t.Fatalf("XLSX %s directory boundary is incomplete: %#v", test.operation, definition.Directory)
			}
			found := false
			for _, capability := range definition.Capabilities {
				if capability.Name == app.ToolCapabilityDocumentEdit && capability.Qualifiers[app.CapabilityQualifierFormat] == app.DocumentFormatXLSX &&
					capability.Qualifiers[app.CapabilityQualifierOperation] == test.operation {
					found = true
				}
			}
			if !found {
				t.Fatalf("XLSX %s boundary is not attached to its exact capability: %#v", test.operation, definition.Capabilities)
			}
		})
	}
}

func TestOfficeReplaceDirectoryMetadataRetainsCrossFormatBoundary(t *testing.T) {
	directory := toolRegistry["office.replace_text"].directory
	for _, want := range []string{"structured text blocks", "text-valued cells", "whole-slide rewriting"} {
		if !strings.Contains(directory.WhenToUse+" "+directory.WhenNotToUse, want) {
			t.Fatalf("office.replace_text lost cross-format boundary %q: %#v", want, directory)
		}
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
