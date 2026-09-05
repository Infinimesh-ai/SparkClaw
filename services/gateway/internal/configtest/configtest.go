package configtest

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

// RepositoryCapacityCatalog returns the absolute path of the versioned
// configs/model.profiles.json, found by walking up from the test working
// directory. Tests pass it explicitly instead of relying on a path baked
// into the binary.
func RepositoryCapacityCatalog() string {
	dir, err := os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("resolve working directory: %v", err))
	}
	for {
		candidate := filepath.Join(dir, "configs", "model.profiles.json")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("configs/model.profiles.json not found above the test working directory")
		}
		dir = parent
	}
}

// MustLoadDefault returns a fully resolved default config for tests that
// exercise model-capacity-owned behavior.
func MustLoadDefault() config.Config {
	cfg, err := config.ResolveDefault(RepositoryCapacityCatalog())
	if err != nil {
		panic(fmt.Sprintf("load default test config: %v", err))
	}
	return cfg
}
