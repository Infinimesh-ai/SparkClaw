package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validCapacityCatalog = `{
  "profiles": {
    "test": {
      "executable": true,
      "physical_models": {
        "chat": {"context_tokens": 4096},
        "embedding": {"context_tokens": 2048},
        "guard": {"context_tokens": 2048},
        "ocr": {"context_tokens": 4096}
      },
      "lanes": {
        "fast": {"physical_model":"chat","output_budgets":{"compact_structured":128,"workflow_structured":512,"answer":512,"vision_structured":256}},
        "deep": {"physical_model":"chat","output_budgets":{"workflow_structured":512,"answer":512}},
        "embedding": {"physical_model":"embedding","output_budgets":{}},
        "guard": {"physical_model":"guard","output_budgets":{"guard":64}},
        "ocr": {"physical_model":"ocr","output_budgets":{"ocr_document":1024}}
      }
    }
  }
}`

func TestRepositoryModelCapacityCatalogIsValid(t *testing.T) {
	if err := ValidateModelCapacityCatalog(defaultModelCapacityCatalogPath()); err != nil {
		t.Fatal(err)
	}
}

func TestModelCapacityCatalogRejectsInvalidFacts(t *testing.T) {
	tests := []struct {
		name    string
		old     string
		new     string
		message string
	}{
		{name: "zero physical context", old: `"chat": {"context_tokens": 4096}`, new: `"chat": {"context_tokens": 0}`, message: "context_tokens must be positive"},
		{name: "missing required class", old: `,"answer":512`, new: ``, message: `missing output class "answer"`},
		{name: "zero class", old: `"guard":64`, new: `"guard":0`, message: `output class "guard" must be positive`},
		{name: "budget reaches context", old: `"ocr_document":1024`, new: `"ocr_document":4096`, message: "must be less than context_tokens"},
		{name: "unknown class", old: `"guard":64`, new: `"guard":64,"mystery":1`, message: `unknown output class "mystery"`},
		{name: "unknown lane", old: `"ocr": {"physical_model":"ocr","output_budgets":{"ocr_document":1024}}`, new: `"ocr": {"physical_model":"ocr","output_budgets":{"ocr_document":1024}},"other":{"physical_model":"chat","output_budgets":{}}`, message: `unknown lane "other"`},
		{name: "missing physical target", old: `"physical_model":"embedding"`, new: `"physical_model":"missing"`, message: "references missing physical model"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.json")
			raw := strings.Replace(validCapacityCatalog, test.old, test.new, 1)
			if raw == validCapacityCatalog {
				t.Fatalf("fixture replacement %q did not match", test.old)
			}
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := ValidateModelCapacityCatalog(path); err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("validation error = %v, want %q", err, test.message)
			}
		})
	}
}

func TestLoadRejectsLegacyCapacityJSONAndNonExecutableProfile(t *testing.T) {
	legacyPath := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(legacyPath, []byte(`{"model":{"fast":{"context_tokens":8192}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(legacyPath); err == nil || !strings.Contains(err.Error(), "is not allowed") {
		t.Fatalf("legacy JSON capacity error = %v", err)
	}

	profilePath := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(profilePath, []byte(`{"model":{"capacity_profile":"external-model","capacity_catalog":"`+defaultModelCapacityCatalogPath()+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(profilePath); err == nil || !strings.Contains(err.Error(), "is not executable") {
		t.Fatalf("non-executable profile error = %v", err)
	}
}
