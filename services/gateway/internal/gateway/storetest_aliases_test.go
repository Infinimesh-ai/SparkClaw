package gateway

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"

// Shared test-fixture aliases. The implementations live once in
// internal/storetest; the local names predate that package and are kept so
// existing call sites stay unchanged.
var (
	testSaveRun            = storetest.SaveRun
	testGetRun             = storetest.GetRun
	testListRuns           = storetest.ListRuns
	testListModelCalls     = storetest.ListModelCalls
	testSaveToolCall       = storetest.SaveToolCall
	testListToolCalls      = storetest.ListToolCalls
	testSaveEpisodeSummary = storetest.SaveEpisodeSummary
	mustGatewayListAudit   = storetest.MustListAudit
)
