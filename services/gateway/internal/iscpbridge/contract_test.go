package iscpbridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublishedSchemaContainsAllRequestTypes(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	schemaPath := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../../../packages/protocol/iscp-bridge.v1.schema.json"))
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatal("published Bridge schema is not valid JSON")
	}
	for requestType := range supportedRequestTypes {
		if !strings.Contains(string(raw), `"`+requestType+`"`) {
			t.Fatalf("published Bridge schema is missing %q", requestType)
		}
	}
}

func TestProductionConfigRejectsFileIdentityKey(t *testing.T) {
	config := Config{
		Profile: ProfileProduction, IdentityDirectory: "/identity", IdentityKeyBackend: IdentityKeyBackendFile,
		EnrollmentFile: "/enrollment", Gateway: GatewayConfig{TokenFile: "/token"},
	}
	if err := config.normalizeAndValidate(); err == nil {
		t.Fatal("production config accepted file identity key backend")
	}
}

func TestMockHandlerRequiresClientToken(t *testing.T) {
	if _, err := NewMockHandler(&GatewayClient{}, ""); err == nil {
		t.Fatal("mock handler accepted an empty client token")
	}
	for _, address := range []string{"0.0.0.0:18792", "192.0.2.1:18792"} {
		if err := ValidateMockListenAddress(address); err == nil {
			t.Fatalf("mock accepted non-loopback address %q", address)
		}
	}
}
