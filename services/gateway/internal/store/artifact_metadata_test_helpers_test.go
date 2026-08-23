package store

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustSaveArtifactObject(t testing.TB, repository ArtifactMetadataRepository, object app.ArtifactObject) app.ArtifactObject {
	t.Helper()
	stored, err := repository.SaveArtifactObject(t.Context(), object)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func mustListArtifactObjects(t testing.TB, repository ArtifactMetadataRepository, limit int) []app.ArtifactObject {
	t.Helper()
	objects, err := repository.ListArtifactObjects(t.Context(), limit)
	if err != nil {
		t.Fatal(err)
	}
	return objects
}

func mustFindArtifactObjectByURI(t testing.TB, repository ArtifactMetadataRepository, uri, sessionID, runID string) (app.ArtifactObject, bool) {
	t.Helper()
	object, found, err := repository.FindArtifactObjectByURI(t.Context(), uri, sessionID, runID)
	if err != nil {
		t.Fatal(err)
	}
	return object, found
}
