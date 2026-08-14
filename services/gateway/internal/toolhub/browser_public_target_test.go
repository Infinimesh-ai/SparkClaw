package toolhub

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

func TestSelectInfoPublicBrowserTargetPreservesInfoFinalOrderAndOriginalIdentity(t *testing.T) {
	sources := []websearch.InfoBrowserSource{
		{Index: 4, ID: "linkless", URL: "file:///private", Linkable: false},
		{Index: 7, ID: "unsafe", URL: "https://unsafe.example/", Linkable: true},
		{Index: 9, ID: "official", URL: "https://official.example/app", Linkable: true},
		{Index: 12, ID: "later", URL: "https://later.example/", Linkable: true},
	}
	seen := []string{}
	selection, unsafeCount, ok := selectInfoPublicBrowserTarget(context.Background(), sources,
		func(_ context.Context, rawURL string) (string, []string, error) {
			seen = append(seen, rawURL)
			if rawURL == "https://unsafe.example/" {
				return "", nil, errors.New("private redirect")
			}
			return rawURL, []string{"https://redirect.example/"}, nil
		})
	if !ok || unsafeCount != 1 || selection.Index != 9 || selection.SourceID != "official" || selection.RawURL != "https://official.example/app" ||
		selection.FinalURL != "https://official.example/app" || !slices.Equal(seen, []string{"https://unsafe.example/", "https://official.example/app"}) {
		t.Fatalf("Info final order, linkability, or source identity changed: selection=%#v unsafe=%d seen=%#v", selection, unsafeCount, seen)
	}
}

func TestSelectInfoPublicBrowserTargetDoesNotInspectSourceProse(t *testing.T) {
	sources := []websearch.InfoBrowserSource{
		{Index: 2, ID: "no-url", URL: "", Linkable: false},
		{Index: 3, ID: "structured", URL: "https://structured.example/", Linkable: true},
	}
	seen := []string{}
	selection, unsafeCount, ok := selectInfoPublicBrowserTarget(context.Background(), sources,
		func(_ context.Context, rawURL string) (string, []string, error) {
			seen = append(seen, rawURL)
			return rawURL, nil, nil
		})
	if !ok || unsafeCount != 0 || selection.Index != 3 || selection.SourceID != "structured" || !slices.Equal(seen, []string{"https://structured.example/"}) {
		t.Fatalf("identifier consumed a non-linkable source: selection=%#v unsafe=%d seen=%#v", selection, unsafeCount, seen)
	}
}
