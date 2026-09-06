package config

import (
	"strings"
	"testing"
)

func TestGatewayAuthWarningsFireOnlyForUnauthenticatedNonLoopbackBinds(t *testing.T) {
	for _, test := range []struct {
		name    string
		gateway GatewayConfig
		warn    bool
	}{
		{name: "loopback without auth", gateway: GatewayConfig{Bind: "127.0.0.1"}},
		{name: "ipv6 loopback without auth", gateway: GatewayConfig{Bind: "::1"}},
		{name: "localhost without auth", gateway: GatewayConfig{Bind: "localhost"}},
		{name: "wildcard with pairing", gateway: GatewayConfig{Bind: "0.0.0.0", PairingRequired: true}},
		{name: "wildcard with token", gateway: GatewayConfig{Bind: "0.0.0.0", APIToken: "token"}},
		{name: "wildcard without auth", gateway: GatewayConfig{Bind: "0.0.0.0"}, warn: true},
		{name: "lan address without auth", gateway: GatewayConfig{Bind: "192.168.1.8"}, warn: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			warnings := gatewayAuthWarnings(test.gateway)
			if test.warn != (len(warnings) == 1) {
				t.Fatalf("warnings = %#v, want warning=%v", warnings, test.warn)
			}
			if test.warn && !strings.Contains(warnings[0], test.gateway.Bind) {
				t.Fatalf("warning does not name the bind: %q", warnings[0])
			}
		})
	}
}
