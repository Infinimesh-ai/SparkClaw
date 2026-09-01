package configtest

import (
	"fmt"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

// MustLoadDefault returns a fully resolved default config for tests that
// exercise model-capacity-owned behavior.
func MustLoadDefault() config.Config {
	cfg, err := config.LoadDefault()
	if err != nil {
		panic(fmt.Sprintf("load default test config: %v", err))
	}
	return cfg
}
