package weixin

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"

// Shared test-fixture aliases. The implementations live once in
// internal/storetest; the local names predate that package and are kept so
// existing call sites stay unchanged.
var (
	testSaveRun              = storetest.SaveRun
	testListRuns             = storetest.ListRuns
	testSaveToolCall         = storetest.SaveToolCall
	testGetToolCall          = storetest.GetToolCall
	testListToolCalls        = storetest.ListToolCalls
	testSaveEpisodeSummary   = storetest.SaveEpisodeSummary
	testListEpisodeSummaries = storetest.ListEpisodeSummaries
)
