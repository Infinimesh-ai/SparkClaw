package config

import (
	"os"
	"path/filepath"
	"testing"
)

// repositoryCapacityCatalog is the absolute path of the versioned catalog,
// resolved from the package directory that go test runs in.
func repositoryCapacityCatalog() string {
	path, err := filepath.Abs(repositoryPath("configs", "model.profiles.json"))
	if err != nil {
		panic(err)
	}
	return path
}

// TestMain points Load at the repository catalog explicitly: the binary no
// longer carries a build-machine path, so a bare Load("") would otherwise
// look for configs/model.profiles.json under the package directory. Tests
// that need a different catalog still override the variable with t.Setenv.
func TestMain(m *testing.M) {
	if os.Getenv("SPARKCLAW_MODEL_CAPACITY_CATALOG") == "" {
		os.Setenv("SPARKCLAW_MODEL_CAPACITY_CATALOG", repositoryCapacityCatalog())
	}
	os.Exit(m.Run())
}
