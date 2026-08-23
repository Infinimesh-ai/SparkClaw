package credential

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"

// Shared test-fixture aliases. The implementations live once in
// internal/storetest; the local names predate that package and are kept so
// existing call sites stay unchanged.
var (
	mustCredentialListAudit = storetest.MustListAudit
)
