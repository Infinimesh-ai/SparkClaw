package toolhub

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/jingsiscope"
)

// Every exposable static tool must declare only effects the JingSi scope
// mapping knows, so a JingSi grant can expose it by granting the matching
// data_scope/network_scope tokens. A tool that fails this test is hidden from
// every JingSi execution; that is the intended fail-closed default, but it
// must be a deliberate registry decision rather than a forgotten mapping.
func TestJingSiScopeMappingCoversEveryExposableStaticTool(t *testing.T) {
	for _, definition := range defaultDefinitions() {
		registration, ok := toolRegistry[definition.Name]
		if !ok || len(registration.capabilities) == 0 {
			continue
		}
		if len(registration.directory.Effects) == 0 {
			t.Errorf("%s: exposable tool declares no effect and would never be exposed to JingSi", definition.Name)
		}
		for _, effect := range registration.directory.Effects {
			if _, _, known := jingsiscope.EffectRequirement(effect); !known {
				t.Errorf("%s: effect %q has no JingSi scope mapping", definition.Name, effect)
			}
		}
	}
}
