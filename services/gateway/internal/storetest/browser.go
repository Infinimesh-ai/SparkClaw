package storetest

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func MustSaveBrowserLoginBlock(t testing.TB, repository store.BrowserStateRepository, block app.BrowserLoginBlock) app.BrowserLoginBlock {
	t.Helper()
	stored, err := repository.SaveBrowserLoginBlock(t.Context(), block)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func MustGetBrowserLoginBlock(t testing.TB, repository store.BrowserStateRepository, id string) (app.BrowserLoginBlock, bool) {
	t.Helper()
	block, found, err := repository.GetBrowserLoginBlock(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return block, found
}

func MustFindActiveBrowserLoginBlock(t testing.TB, repository store.BrowserStateRepository, sessionID string) (app.BrowserLoginBlock, bool) {
	t.Helper()
	block, found, err := repository.FindActiveBrowserLoginBlock(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return block, found
}

func MustListBrowserLoginBlocks(t testing.TB, repository store.BrowserStateRepository, sessionID, status string) []app.BrowserLoginBlock {
	t.Helper()
	blocks, err := repository.ListBrowserLoginBlocks(t.Context(), sessionID, status)
	if err != nil {
		t.Fatal(err)
	}
	return blocks
}
