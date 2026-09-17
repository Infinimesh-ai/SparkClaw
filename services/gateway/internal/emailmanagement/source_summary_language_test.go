package emailmanagement

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestSummaryLanguageSamplesCurrentOwnerPreference(t *testing.T) {
	repo := store.NewMemoryStore()
	profile, err := repo.SaveOwnerProfile(t.Context(), app.OwnerProfile{ID: "email-owner", DisplayName: "Owner", Preferences: map[string]string{app.OwnerPreferenceLanguage: "zh", "tone": "brief"}})
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{repository: repo}
	first, err := service.summaryLanguage(t.Context(), profile.ID)
	if err != nil || first != "zh" {
		t.Fatalf("first language = %q, err = %v", first, err)
	}
	profile.Preferences[app.OwnerPreferenceLanguage] = "en"
	if _, err = repo.SaveOwnerProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	second, err := service.summaryLanguage(t.Context(), profile.ID)
	if err != nil || second != "en" {
		t.Fatalf("second language = %q, err = %v", second, err)
	}
	if first != "zh" {
		t.Fatal("the previously sampled language changed retroactively")
	}
}

func TestSummaryLanguageDefaultsToEnglish(t *testing.T) {
	repo := store.NewMemoryStore()
	if _, err := repo.SaveOwnerProfile(t.Context(), app.OwnerProfile{ID: "email-owner", DisplayName: "Owner", Preferences: map[string]string{app.OwnerPreferenceLanguage: "fr"}}); err != nil {
		t.Fatal(err)
	}
	service := &Service{repository: repo}
	got, err := service.summaryLanguage(t.Context(), "email-owner")
	if err != nil || got != "en" {
		t.Fatalf("language = %q, err = %v", got, err)
	}
}
