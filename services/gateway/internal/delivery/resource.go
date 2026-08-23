package delivery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type artifactStore interface {
	ListArtifactObjects(context.Context, int) ([]app.ArtifactObject, error)
}

type endpointResourceStore interface {
	artifactStore
	GetSession(context.Context, string) (app.Session, bool, error)
}

type ResourceResolver interface {
	Resolve(context.Context, app.MessagePart) (string, error)
}

type EndpointResourceResolver struct {
	store    endpointResourceStore
	endpoint app.MessageEndpoint
}

func NewEndpointResourceResolver(st endpointResourceStore, endpoint app.MessageEndpoint) EndpointResourceResolver {
	return EndpointResourceResolver{store: st, endpoint: endpoint}
}

func (r EndpointResourceResolver) Resolve(ctx context.Context, part app.MessagePart) (string, error) {
	if strings.TrimSpace(part.ArtifactID) != "" {
		return NewStoreResourceResolver(r.store).Resolve(ctx, part)
	}
	if part.Resource != nil && part.Resource.Kind == "workspace_file" {
		if r.store == nil || strings.TrimSpace(r.endpoint.SessionID) == "" {
			return "", errors.New("workspace delivery endpoint has no linked session")
		}
		session, ok, err := r.store.GetSession(ctx, r.endpoint.SessionID)
		if err != nil {
			return "", fmt.Errorf("read workspace delivery session: %w", err)
		}
		if !ok || strings.TrimSpace(session.WorkspaceRoot) == "" {
			return "", errors.New("workspace delivery session is unavailable")
		}
		root, err := filepath.Abs(session.WorkspaceRoot)
		if err != nil {
			return "", errors.New("workspace delivery root is invalid")
		}
		candidate, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(strings.TrimSpace(part.Resource.Ref))))
		if err != nil || candidate == root || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
			return "", errors.New("workspace delivery resource escapes the linked workspace")
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() {
			return "", errors.New("workspace delivery resource is unavailable")
		}
		return candidate, nil
	}
	return NewStoreResourceResolver(r.store).Resolve(ctx, part)
}

type StoreResourceResolver struct {
	store artifactStore
}

func NewStoreResourceResolver(st artifactStore) StoreResourceResolver {
	return StoreResourceResolver{store: st}
}

func (r StoreResourceResolver) Resolve(ctx context.Context, part app.MessagePart) (string, error) {
	if r.store == nil {
		return "", errors.New("artifact resolver is unavailable")
	}
	wanted := strings.TrimSpace(part.ArtifactID)
	if wanted == "" && part.Resource != nil {
		switch part.Resource.Kind {
		case "artifact", "workspace_file":
			wanted = strings.TrimSpace(part.Resource.Ref)
		default:
			return "", fmt.Errorf("resource kind %q is not deliverable", part.Resource.Kind)
		}
	}
	if wanted == "" {
		return "", errors.New("binary delivery part requires an artifact id")
	}
	objects, err := r.store.ListArtifactObjects(ctx, 5000)
	if err != nil {
		return "", errors.New("artifact registry is unavailable")
	}
	for _, object := range objects {
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
