package storetest

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func MustSaveArtifactObject(t testing.TB, repository store.ArtifactMetadataRepository, object app.ArtifactObject) app.ArtifactObject {
	t.Helper()
	stored, err := repository.SaveArtifactObject(t.Context(), object)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func MustListArtifactObjects(t testing.TB, repository store.ArtifactMetadataRepository, limit int) []app.ArtifactObject {
	t.Helper()
	objects, err := repository.ListArtifactObjects(t.Context(), limit)
	if err != nil {
		t.Fatal(err)
	}
	return objects
}

func MustFindArtifactObjectByURI(t testing.TB, repository store.ArtifactMetadataRepository, uri, sessionID, runID string) (app.ArtifactObject, bool) {
	t.Helper()
	object, found, err := repository.FindArtifactObjectByURI(t.Context(), uri, sessionID, runID)
	if err != nil {
		t.Fatal(err)
	}
	return object, found
}
