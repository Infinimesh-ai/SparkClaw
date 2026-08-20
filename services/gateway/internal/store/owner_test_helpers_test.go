package store

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustGetOwnerProfile(t testing.TB, repository OwnerRepository) app.OwnerProfile {
	t.Helper()
	profile, err := repository.GetOwnerProfile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func mustUpdateOwnerProfile(t testing.TB, repository OwnerRepository, profile app.OwnerProfile) app.OwnerProfile {
	t.Helper()
	updated, err := repository.UpdateOwnerProfile(context.Background(), profile)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func mustSaveOwnerProfile(t testing.TB, repository OwnerRepository, profile app.OwnerProfile) app.OwnerProfile {
	t.Helper()
	saved, err := repository.SaveOwnerProfile(context.Background(), profile)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func mustGetOwnerProfileByID(t testing.TB, repository OwnerRepository, id string) (app.OwnerProfile, bool) {
	t.Helper()
	profile, found, err := repository.GetOwnerProfileByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return profile, found
}

func mustFindOwnerProfileByExternalRef(t testing.TB, repository OwnerRepository, source, externalRef string) (app.OwnerProfile, bool) {
	t.Helper()
	profile, found, err := repository.FindOwnerProfileByExternalRef(context.Background(), source, externalRef)
	if err != nil {
		t.Fatal(err)
	}
	return profile, found
}

func mustListOwnerProfiles(t testing.TB, repository OwnerRepository) []app.OwnerProfile {
	t.Helper()
	profiles, err := repository.ListOwnerProfiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return profiles
}
