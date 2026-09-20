package db

import (
	"testing"

	"github.com/jobscout/jobscout/internal/db/pgtest"
)

func TestNotificationsEnabled_DefaultsTrueAndTogglePersists(t *testing.T) {
	pool := pgtest.Open(t)
	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := GetNotificationsEnabled(t.Context(), pool)
	if err != nil {
		t.Fatalf("GetNotificationsEnabled: %v", err)
	}
	if !got {
		t.Fatal("notifications_enabled must default to true after seed")
	}

	stored, err := SetNotificationsEnabled(t.Context(), pool, false)
	if err != nil {
		t.Fatalf("SetNotificationsEnabled(false): %v", err)
	}
	if stored {
		t.Fatal("SetNotificationsEnabled(false) must return false")
	}
	got, err = GetNotificationsEnabled(t.Context(), pool)
	if err != nil {
		t.Fatalf("GetNotificationsEnabled after off: %v", err)
	}
	if got {
		t.Fatal("notifications_enabled must stay false after toggle off")
	}

	settings, err := GetSearchSettings(t.Context(), pool)
	if err != nil || settings == nil {
		t.Fatalf("GetSearchSettings: %v %#v", err, settings)
	}
	if settings.NotificationsEnabled {
		t.Fatal("GetSearchSettings must include notifications_enabled=false")
	}
	if len(settings.TitleInclude) == 0 {
		t.Fatal("toggle must not clear filter word lists")
	}

	stored, err = SetNotificationsEnabled(t.Context(), pool, true)
	if err != nil {
		t.Fatalf("SetNotificationsEnabled(true): %v", err)
	}
	if !stored {
		t.Fatal("SetNotificationsEnabled(true) must return true")
	}
}

func TestNotificationsEnabled_MissingRowDefaultsTrue(t *testing.T) {
	pool := pgtest.Open(t)
	if err := Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got, err := GetNotificationsEnabled(t.Context(), pool)
	if err != nil {
		t.Fatalf("GetNotificationsEnabled: %v", err)
	}
	if !got {
		t.Fatal("missing search_settings must default notifications to enabled")
	}
}
