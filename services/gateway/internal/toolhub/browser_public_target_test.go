package toolhub

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestSelectInfoPublicBrowserTargetPreservesProviderOrderAndSkipsUnsafe(t *testing.T) {
	results := []any{
		map[string]any{"url": "https://unsafe.example/", "snippet": "https://ignored.example/"},
		map[string]any{"url": "https://official.example/app"},
		map[string]any{"url": "https://later.example/"},
	}
	seen := []string{}
	selection, unsafeCount, ok := selectInfoPublicBrowserTarget(context.Background(), results,
		func(_ context.Context, rawURL string) (string, []string, error) {
			seen = append(seen, rawURL)
			if rawURL == "https://unsafe.example/" {
				return "", nil, errors.New("private redirect")
			}
			return rawURL, []string{"https://redirect.example/"}, nil
		})
	if !ok || unsafeCount != 1 || selection.Index != 1 || selection.RawURL != "https://official.example/app" ||
		selection.FinalURL != "https://official.example/app" || !slices.Equal(seen, []string{"https://unsafe.example/", "https://official.example/app"}) {
		t.Fatalf("Info result order or unsafe skipping changed: selection=%#v unsafe=%d seen=%#v", selection, unsafeCount, seen)
	}
}

func TestSelectInfoPublicBrowserTargetConsumesOnlyStructuredURLField(t *testing.T) {
	results := []any{
		map[string]any{"snippet": "official URL: https://prose.example/"},
		"https://string.example/",
		map[string]any{"url": "https://structured.example/"},
	}
	seen := []string{}
	selection, unsafeCount, ok := selectInfoPublicBrowserTarget(context.Background(), results,
		func(_ context.Context, rawURL string) (string, []string, error) {
			seen = append(seen, rawURL)
			return rawURL, nil, nil
		})
	if !ok || unsafeCount != 0 || selection.Index != 2 || !slices.Equal(seen, []string{"https://structured.example/"}) {
		t.Fatalf("identifier consumed prose or an unstructured result: selection=%#v unsafe=%d seen=%#v", selection, unsafeCount, seen)
	}
}
