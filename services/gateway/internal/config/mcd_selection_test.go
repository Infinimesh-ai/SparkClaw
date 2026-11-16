package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadMCDQualifiedEatTypes(t *testing.T) {
	for _, test := range []struct {
		name, config string
		want         []string
	}{
		{"omitted", `{}`, []string{"locker-in"}},
		{"enabled with defaults", `{"enabled":true,"cart_enabled":true,"purchase_enabled":true}`, []string{"locker-in"}},
		{"null backfill", `{"qualified_eat_types":null}`, []string{"locker-in"}},
		{"newly qualified method", `{"qualified_eat_types":["locker-in"," take-in-store "]}`, []string{"locker-in", "take-in-store"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"mcd_selection":`+test.config+`}`), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil || !slices.Equal(cfg.MCDSelection.QualifiedEatTypes, test.want) {
				t.Fatalf("qualified pickup methods = %v, error = %v; want %v", cfg.MCDSelection.QualifiedEatTypes, err, test.want)
			}
		})
	}
	defaults := Default()
	defaults.MCDSelection.QualifiedEatTypes[0] = "changed"
	if !slices.Equal(Default().MCDSelection.QualifiedEatTypes, []string{"locker-in"}) {
		t.Fatal("default pickup methods share mutable storage")
	}
}

func TestLoadRejectsInvalidMCDQualifiedEatTypes(t *testing.T) {
	for _, value := range []string{`[]`, `[""]`, `[" "]`, `["Locker-In"]`, `["take_in_store"]`, `["locker-in"," locker-in "]`, `["../locker-in"]`} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"mcd_selection":{"qualified_eat_types":`+value+`}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "mcd_selection.qualified_eat_types") {
				t.Fatalf("invalid qualified pickup methods accepted: %v", err)
			}
		})
	}
}
