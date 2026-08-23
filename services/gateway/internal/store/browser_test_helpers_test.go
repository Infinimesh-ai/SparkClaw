package store

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustSaveBrowserAuthRecord(t testing.TB, repository BrowserStateRepository, record app.BrowserAuthRecord) app.BrowserAuthRecord {
	t.Helper()
	stored, err := repository.SaveBrowserAuthRecord(t.Context(), record)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func mustGetBrowserAuthRecord(t testing.TB, repository BrowserStateRepository, id string) (app.BrowserAuthRecord, bool) {
	t.Helper()
	record, found, err := repository.GetBrowserAuthRecord(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return record, found
}

func mustFindBrowserAuthRecord(t testing.TB, repository BrowserStateRepository, ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (app.BrowserAuthRecord, bool) {
	t.Helper()
	record, found, err := repository.FindBrowserAuthRecord(t.Context(), ownerID, browserProfileID, siteOrigin, siteRealm, accountHint)
	if err != nil {
		t.Fatal(err)
	}
	return record, found
}

func mustSaveBrowserLoginBlock(t testing.TB, repository BrowserStateRepository, block app.BrowserLoginBlock) app.BrowserLoginBlock {
	t.Helper()
	stored, err := repository.SaveBrowserLoginBlock(t.Context(), block)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func mustGetBrowserLoginBlock(t testing.TB, repository BrowserStateRepository, id string) (app.BrowserLoginBlock, bool) {
	t.Helper()
	block, found, err := repository.GetBrowserLoginBlock(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return block, found
}

func mustFindActiveBrowserLoginBlock(t testing.TB, repository BrowserStateRepository, sessionID string) (app.BrowserLoginBlock, bool) {
	t.Helper()
	block, found, err := repository.FindActiveBrowserLoginBlock(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return block, found
}

func mustListBrowserLoginBlocks(t testing.TB, repository BrowserStateRepository, sessionID, status string) []app.BrowserLoginBlock {
	t.Helper()
	blocks, err := repository.ListBrowserLoginBlocks(t.Context(), sessionID, status)
	if err != nil {
		t.Fatal(err)
	}
	return blocks
}
