package delivery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type artifactStore interface {
	ListArtifactObjects(int) []app.ArtifactObject
}

type ResourceResolver interface {
	Resolve(context.Context, app.MessagePart) (string, error)
}

type StoreResourceResolver struct {
	store artifactStore
}

func NewStoreResourceResolver(st artifactStore) StoreResourceResolver {
	return StoreResourceResolver{store: st}
}

func (r StoreResourceResolver) Resolve(_ context.Context, part app.MessagePart) (string, error) {
	if r.store == nil {
		return "", errors.New("artifact resolver is unavailable")
	}
	wanted := strings.TrimSpace(part.ArtifactID)
	if wanted == "" && part.Resource != nil && part.Resource.Kind == "artifact" {
		wanted = strings.TrimSpace(part.Resource.Ref)
	}
	if wanted == "" {
		return "", errors.New("binary delivery part requires an artifact id")
	}
	for _, object := range r.store.ListArtifactObjects(5000) {
		if object.ID != wanted && object.URI != wanted && object.Key != wanted {
			continue
		}
		path := strings.TrimSpace(object.Path)
		if path == "" {
			return "", fmt.Errorf("artifact %q is not available as a local delivery resource", wanted)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("artifact %q delivery resource is unavailable", wanted)
		}
		return path, nil
	}
	return "", fmt.Errorf("artifact %q was not found", wanted)
}

type PreparedPart struct {
	Part app.MessagePart
	Path string
}

// PrepareParts resolves every binary resource before a provider performs its
// first external send, preventing a late invalid part from being dropped or
// turning a capability error into a partial delivery.
func PrepareParts(ctx context.Context, content app.MessageContent, resolver ResourceResolver) ([]PreparedPart, error) {
	prepared := make([]PreparedPart, 0, len(content.Parts))
	for _, part := range content.Parts {
		item := PreparedPart{Part: part}
		if part.Kind != app.MessagePartText {
			if resolver == nil {
				return nil, errors.New("binary resource resolver is unavailable")
			}
			path, err := resolver.Resolve(ctx, part)
			if err != nil {
				return nil, fmt.Errorf("prepare part %q: %w", part.ID, err)
			}
			item.Path = path
		}
		prepared = append(prepared, item)
	}
	return prepared, nil
}
