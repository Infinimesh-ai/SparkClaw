package toolhub

import "testing"

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
