package store

import (
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func prepareArtifactObject(object app.ArtifactObject, now time.Time) app.ArtifactObject {
	if strings.TrimSpace(object.ID) == "" {
		object.ID = app.NewID("obj")
	}
	if object.CreatedAt.IsZero() {
		object.CreatedAt = now
	}
	return normalizeArtifactObject(object)
}

func normalizeArtifactObject(object app.ArtifactObject) app.ArtifactObject {
	object.CreatedAt = postgresTime(object.CreatedAt)
	return object
}
